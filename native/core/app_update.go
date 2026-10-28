package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const updateRepository = "Null993/guoapp"

type nativeUpdateInput struct {
	Platform string   `json:"platform"`
	ABIs     []string `json:"abis"`
	Token    string   `json:"token"`
}

type appReleaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type appRelease struct {
	Tag        string            `json:"tag_name"`
	Notes      string            `json:"body"`
	URL        string            `json:"html_url"`
	Draft      bool              `json:"draft"`
	Prerelease bool              `json:"prerelease"`
	Assets     []appReleaseAsset `json:"assets"`
}

type nativeUpdateState struct {
	Repository        string           `json:"repository"`
	Version           string           `json:"version,omitempty"`
	Notes             string           `json:"notes,omitempty"`
	URL               string           `json:"url,omitempty"`
	Status            string           `json:"status"`
	Message           string           `json:"message,omitempty"`
	Asset             *appReleaseAsset `json:"asset,omitempty"`
	Received          int64            `json:"received"`
	Path              string           `json:"path,omitempty"`
	SHA256            string           `json:"sha256,omitempty"`
	RollbackAvailable bool             `json:"rollbackAvailable"`
	checksum          *appReleaseAsset
	cancel            context.CancelFunc
	job               string
}

var appVersionPattern = regexp.MustCompile(`^(?:app-)?v?(\d+)\.(\d+)\.(\d+)(?:[+-](\d+))?$`)
var updateDigestPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func parseAppReleaseVersion(text string) ([4]int, bool) {
	var version [4]int
	matches := appVersionPattern.FindStringSubmatch(text)
	if matches == nil {
		return version, false
	}
	for i := 0; i < 4; i++ {
		if matches[i+1] != "" {
			n, err := strconv.Atoi(matches[i+1])
			if err != nil || n < 0 {
				return version, false
			}
			version[i] = n
		}
	}
	return version, true
}

func compareAppVersion(left, right [4]int) int {
	for i := range left {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

func appVersionText(v [4]int) string { return fmt.Sprintf("%d.%d.%d+%d", v[0], v[1], v[2], v[3]) }

func validUpdateURL(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "api.github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	}
	return false
}

func validReleaseAsset(asset appReleaseAsset) bool {
	u, err := url.Parse(asset.URL)
	return err == nil && validUpdateURL(asset.URL) && u.Hostname() == "github.com" && strings.HasPrefix(u.Path, "/"+updateRepository+"/releases/download/") && asset.Size > 0 && asset.Size <= 1<<30 && filepath.Base(asset.Name) == asset.Name && !strings.ContainsAny(asset.Name, "/\\:")
}

func selectAppRelease(releases []appRelease, platform string, abis []string, all bool) (nativeUpdateState, error) {
	result := nativeUpdateState{Repository: updateRepository, Status: "current"}
	var latest *appRelease
	var version [4]int
	for i := range releases {
		r := &releases[i]
		v, ok := parseAppReleaseVersion(r.Tag)
		if !ok || r.Draft || r.Prerelease {
			continue
		}
		if latest == nil || compareAppVersion(v, version) > 0 {
			latest = r
			version = v
		}
	}
	if latest == nil {
		return result, errors.New("GitHub 尚无可识别的稳定 Release")
	}
	result.Version, result.Notes, result.URL = appVersionText(version), latest.Notes, latest.URL
	if !strings.HasPrefix(result.URL, "https://github.com/"+updateRepository+"/releases/") {
		return result, errors.New("Release 地址不属于更新仓库")
	}
	current, _ := parseAppReleaseVersion(nativeAppVersion + "+" + strconv.Itoa(nativeAppBuild))
	comparison := compareAppVersion(version, current)
	if comparison < 0 {
		result.Status = "local_newer"
		result.Message = "本地版本较新，无需降级"
		return result, nil
	}
	if comparison == 0 {
		result.Message = "当前已是最新稳定版本"
		return result, nil
	}
	slug := "hongguojian"
	if all {
		slug = "zhenguojian"
	}
	suffixes := []string{}
	switch platform {
	case "windows":
		suffixes = []string{"windows-x64.zip"}
	case "android":
		for _, abi := range abis {
			if abi == "arm64-v8a" || abi == "armeabi-v7a" || abi == "x86_64" {
				suffixes = append(suffixes, abi+".apk")
			}
		}
	default:
		result.Status = "unsupported"
		result.Message = "当前平台暂不支持应用内更新"
		return result, nil
	}
	for _, suffix := range suffixes {
		for _, asset := range latest.Assets {
			if asset.Name == slug+"-"+result.Version+"-"+suffix && validReleaseAsset(asset) {
				a := asset
				result.Asset = &a
				break
			}
		}
		if result.Asset != nil {
			break
		}
	}
	if result.Asset == nil {
		result.Status = "no_asset"
		result.Message = "发现新版本，但尚未发布当前版本范围及平台的安装包"
		return result, nil
	}
	for _, asset := range latest.Assets {
		if asset.Name == "SHA256SUMS.txt" && validReleaseAsset(asset) && asset.Size <= 1<<20 {
			a := asset
			result.checksum = &a
		}
	}
	result.SHA256 = strings.TrimPrefix(result.Asset.Digest, "sha256:")
	if result.Asset.Digest == "" || !strings.HasPrefix(result.Asset.Digest, "sha256:") || !updateDigestPattern.MatchString(result.SHA256) {
		result.SHA256 = ""
	}
	if result.SHA256 == "" && result.checksum == nil {
		result.Status = "no_checksum"
		result.Message = "Release 缺少 SHA256 校验信息，暂不能安全安装"
		return result, nil
	}
	result.Status = "available"
	result.Message = "发现新版本，可下载并安装"
	return result, nil
}

func (d *Downloader) appUpdateHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 20 * time.Second
	var round http.RoundTripper = transport
	if d.proxyRouter != nil {
		transport.Proxy = d.proxyRouter.proxy
		round = newAutoNetworkTransport(transport, d)
	} else if d.client != nil && d.client.Transport != nil {
		round = d.client.Transport
	}
	return &http.Client{Transport: round, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !validUpdateURL(r.URL.String()) {
			return errors.New("更新文件重定向地址无效")
		}
		return nil
	}}
}

func updateRequest(ctx context.Context, client *http.Client, address, etag string) (*http.Response, error) {
	if !validUpdateURL(address) {
		return nil, errors.New("更新地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "guoapp/"+nativeAppVersion)
	request.Header.Set("Accept", "application/vnd.github+json")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 && response.StatusCode != 304 {
		response.Body.Close()
		if response.StatusCode == 403 || response.StatusCode == 429 {
			return nil, errors.New("GitHub 拒绝请求或触发限流，请稍后重试")
		}
		return nil, fmt.Errorf("GitHub 更新请求 HTTP %d", response.StatusCode)
	}
	return response, nil
}

func (e *nativeEngine) checkAppUpdate(ctx context.Context, input nativeUpdateInput) (nativeUpdateState, error) {
	if input.Platform != "windows" && input.Platform != "android" {
		return nativeUpdateState{}, errors.New("当前平台暂不支持更新")
	}
	if input.Platform != runtime.GOOS {
		return nativeUpdateState{}, errors.New("更新平台与当前设备不一致")
	}
	path := filepath.Join(e.directory, "updates", "releases-cache.json")
	var cached struct {
		ETag string          `json:"etag"`
		Body json.RawMessage `json:"body"`
	}
	if raw, err := os.ReadFile(path); err == nil && len(raw) <= 3<<20 {
		json.Unmarshal(raw, &cached)
	}
	client := e.downloader.appUpdateHTTPClient()
	defer client.CloseIdleConnections()
	response, err := updateRequest(ctx, client, "https://api.github.com/repos/"+updateRepository+"/releases?per_page=30", cached.ETag)
	if err != nil {
		return nativeUpdateState{}, err
	}
	defer response.Body.Close()
	body := []byte(cached.Body)
	if response.StatusCode == 200 {
		body, err = io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		if err != nil {
			return nativeUpdateState{}, err
		}
	}
	if len(body) == 0 || len(body) > 2<<20 {
		return nativeUpdateState{}, errors.New("GitHub Release 响应无效或过大")
	}
	var releases []appRelease
	if json.Unmarshal(body, &releases) != nil {
		return nativeUpdateState{}, errors.New("GitHub Release 格式异常")
	}
	state, err := selectAppRelease(releases, input.Platform, input.ABIs, buildAllSources == "true")
	if err != nil {
		return state, err
	}
	if response.StatusCode == 200 {
		cached.ETag = response.Header.Get("ETag")
		cached.Body = body
		data, _ := json.Marshal(cached)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
			writeNativeCacheFile(path, data)
		}
	}
	return state, nil
}

func parseUpdateChecksums(body []byte, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && updateDigestPattern.MatchString(fields[0]) {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", errors.New("SHA256SUMS.txt 未包含当前安装包的有效校验值")
}

func (e *nativeEngine) downloadAppUpdate(ctx context.Context, job string, state nativeUpdateState) {
	path, checksum, err := e.fetchAppUpdate(ctx, job, state)
	e.updateMu.Lock()
	defer e.updateMu.Unlock()
	if e.update.job != job {
		return
	}
	e.update.cancel = nil
	if err != nil {
		e.update.Status = "failed"
		e.update.Message = publicError(err).Error()
		if errors.Is(err, context.Canceled) {
			e.update.Status = "cancelled"
			e.update.Message = "下载已取消，可重新下载"
		}
		return
	}
	e.update.Path, e.update.SHA256, e.update.Status, e.update.Message = path, checksum, "ready", "下载完成，SHA256 校验通过"
}

type updateProgressWriter struct {
	engine *nativeEngine
	job    string
}

func (w updateProgressWriter) Write(b []byte) (int, error) {
	w.engine.updateMu.Lock()
	defer w.engine.updateMu.Unlock()
	if w.engine.update.job == w.job {
		w.engine.update.Received += int64(len(b))
	}
	return len(b), nil
}

func (e *nativeEngine) fetchAppUpdate(ctx context.Context, job string, state nativeUpdateState) (string, string, error) {
	if state.Asset == nil || !validReleaseAsset(*state.Asset) {
		return "", "", errors.New("没有可下载的安装包")
	}
	client := e.downloader.appUpdateHTTPClient()
	defer client.CloseIdleConnections()
	checksum := state.SHA256
	if checksum == "" {
		if state.checksum == nil {
			return "", "", errors.New("缺少安装包校验值")
		}
		response, err := updateRequest(ctx, client, state.checksum.URL, "")
		if err != nil {
			return "", "", err
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		response.Body.Close()
		if err != nil {
			return "", "", err
		}
		if len(body) > 1<<20 {
			return "", "", errors.New("校验文件过大")
		}
		checksum, err = parseUpdateChecksums(body, state.Asset.Name)
		if err != nil {
			return "", "", err
		}
	}
	directory := filepath.Join(e.directory, "updates", job)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", "", err
	}
	path := filepath.Join(directory, state.Asset.Name)
	partial := path + ".part"
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", err
	}
	defer func() { file.Close(); os.Remove(partial) }()
	response, err := updateRequest(ctx, client, state.Asset.URL, "")
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", "", errors.New("安装包下载响应异常")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash, updateProgressWriter{e, job}), io.LimitReader(response.Body, state.Asset.Size+1))
	if err != nil {
		return "", "", err
	}
	if n != state.Asset.Size {
		return "", "", errors.New("安装包大小不匹配，请重新下载")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), checksum) {
		return "", "", errors.New("安装包 SHA256 校验失败，已拒绝安装")
	}
	if err := file.Sync(); err != nil {
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	if err := os.Rename(partial, path); err != nil {
		return "", "", err
	}
	return path, strings.ToLower(checksum), nil
}

func (e *nativeEngine) nativeAppUpdate(ctx context.Context, command string, input nativeUpdateInput) (nativeUpdateState, error) {
	if command == "check" {
		e.updateMu.Lock()
		busy := e.update.cancel != nil
		e.updateMu.Unlock()
		if busy {
			return nativeUpdateState{}, errors.New("请先等待下载完成或取消下载")
		}
		state, err := e.checkAppUpdate(ctx, input)
		if err != nil {
			return state, err
		}
		e.updateMu.Lock()
		defer e.updateMu.Unlock()
		if e.update.cancel != nil {
			return e.update, errors.New("安装包正在下载")
		}
		e.update = state
		e.update.RollbackAvailable = e.appRollbackAvailable()
		return e.update, nil
	}
	e.updateMu.Lock()
	defer e.updateMu.Unlock()
	e.update.Repository = updateRepository
	e.update.RollbackAvailable = e.appRollbackAvailable()
	switch command {
	case "status":
		return e.update, nil
	case "cancel":
		if e.update.cancel != nil {
			e.update.cancel()
		}
		return e.update, nil
	case "download":
		if e.update.cancel != nil {
			return e.update, nil
		}
		if e.update.Asset == nil || (e.update.Status != "available" && e.update.Status != "failed" && e.update.Status != "cancelled") {
			return e.update, errors.New("请先检查更新")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		e.update.job = rand.Text()
		e.update.cancel = cancel
		e.update.Received = 0
		e.update.Status = "downloading"
		e.update.Message = "正在从 GitHub 下载"
		state := e.update
		go func() { defer cancel(); e.downloadAppUpdate(ctx, state.job, state) }()
		return e.update, nil
	case "install":
		if e.update.Status != "ready" || e.update.Path == "" {
			return e.update, errors.New("请先完成下载与校验")
		}
		if err := verifyUpdateArchive(e.update.Path, e.update.SHA256); err != nil {
			return e.update, err
		}
		if runtime.GOOS == "windows" {
			err := e.launchAppInstaller(e.update, false)
			return e.update, err
		}
		return e.update, nil
	case "rollback":
		return e.update, e.launchAppInstaller(e.update, true)
	case "confirmLaunch":
		return e.update, e.confirmAppLaunch(input.Token)
	}
	return e.update, errors.New("未知更新操作")
}
