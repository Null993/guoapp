package core

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type appInstallPlan struct {
	Target          string   `json:"target"`
	Stage           string   `json:"stage"`
	Backup          string   `json:"backup"`
	Executable      string   `json:"executable"`
	PID             int      `json:"pid"`
	Token           string   `json:"token"`
	Ready           string   `json:"ready"`
	Result          string   `json:"result"`
	History         string   `json:"history"`
	Version         string   `json:"version"`
	PreviousVersion string   `json:"previousVersion"`
	Files           []string `json:"files"`
	OldFiles        []string `json:"oldFiles"`
	Timeout         int      `json:"timeout"`
}

func safeUpdateRelative(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(name, ":") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || part == "" || strings.TrimRight(part, " .") != part {
			return false
		}
		if strings.ContainsAny(part, "<>\"|?*\x00\r\n") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || regexp.MustCompile(`^(COM|LPT)[1-9]$`).MatchString(base) {
			return false
		}
	}
	return true
}

func verifyUpdateArchive(path, expected string) error {
	if !updateDigestPattern.MatchString(expected) {
		return errors.New("缺少安装包校验值")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) {
		return errors.New("安装包已变化，请重新下载")
	}
	return nil
}

func unpackAppUpdate(path, directory, executable string) ([]string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	if len(archive.File) > 20000 {
		return nil, errors.New("更新包文件数量异常")
	}
	seen := map[string]bool{}
	var files []string
	var size uint64
	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		lower := strings.ToLower(name)
		if !safeUpdateRelative(name) || entry.Mode()&os.ModeType != 0 || seen[lower] {
			return nil, errors.New("更新包包含不安全或重复的路径")
		}
		seen[lower] = true
		size += entry.UncompressedSize64
		if size > 2<<30 {
			return nil, errors.New("更新包解压后过大")
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		source, err := entry.Open()
		if err != nil {
			return nil, err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			source.Close()
			return nil, err
		}
		_, copyErr := io.Copy(output, io.LimitReader(source, int64(entry.UncompressedSize64)+1))
		source.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files = append(files, name)
	}
	for _, name := range []string{executable, "flutter_windows.dll", "duanju_core.dll", "data/app.so", "data/icudtl.dat"} {
		if !seen[strings.ToLower(name)] {
			return nil, errors.New("更新包缺少必要程序文件：" + name)
		}
	}
	return files, nil
}

func installedAppFiles(directory string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == directory {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("安装目录包含链接，请使用普通目录后重试")
		}
		if entry.IsDir() {
			if relative != "data" && !strings.HasPrefix(relative, "data/") {
				return filepath.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(relative)
		if strings.HasPrefix(relative, "data/") || !strings.Contains(relative, "/") && (strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".exe")) {
			files = append(files, relative)
		}
		return nil
	})
	return files, err
}

func (e *nativeEngine) appRollbackAvailable() bool {
	var plan appInstallPlan
	raw, err := os.ReadFile(filepath.Join(e.directory, "updates", "history.json"))
	if err != nil || json.Unmarshal(raw, &plan) != nil || plan.Backup == "" || len(plan.OldFiles) == 0 {
		return false
	}
	for _, name := range plan.OldFiles {
		if !safeUpdateRelative(name) {
			return false
		}
		if info, err := os.Stat(filepath.Join(plan.Backup, filepath.FromSlash(name))); err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func (e *nativeEngine) confirmAppLaunch(token string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9]{20,64}$`).MatchString(token) {
		return errors.New("更新启动令牌无效")
	}
	directory := filepath.Join(e.directory, "updates", token)
	var plan appInstallPlan
	raw, err := os.ReadFile(filepath.Join(directory, "install-plan.json"))
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, &plan) != nil || plan.Token != token || plan.Version != nativeAppVersion+"+"+strconv.Itoa(nativeAppBuild) {
		return errors.New("更新启动版本校验失败")
	}
	return writeNativeCacheFile(filepath.Join(directory, "ready.json"), []byte(`{"ready":true}`))
}
