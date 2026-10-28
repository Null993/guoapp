package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const hongguoRegisterURL = "https://log.snssdk.com/service/2/device_register/"

type hongguoRegistration struct {
	Version   int    `json:"version"`
	DeviceID  string `json:"deviceId"`
	InstallID string `json:"installId"`
}

func (identity hongguoRegistration) valid() bool {
	return identity.Version == 1 && hongguoNumericID.MatchString(identity.DeviceID) && strings.Trim(identity.DeviceID, "0") != "" && hongguoNumericID.MatchString(identity.InstallID) && strings.Trim(identity.InstallID, "0") != ""
}

func loadHongguoRegistration(directory string) (*hongguoRegistration, error) {
	if directory == "" {
		return nil, nil
	}
	file, err := os.Open(filepath.Join(directory, "hongguo-device.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("红果设备注册信息无法读取，请检查应用数据目录权限")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	var identity hongguoRegistration
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &identity) != nil || !identity.valid() {
		return nil, errors.New("红果设备注册信息损坏，请恢复应用数据备份后重试")
	}
	return &identity, nil
}

func (d *Downloader) ensureHongguoRegistration(ctx context.Context) (hongguoRegistration, error) {
	if err := ctx.Err(); err != nil {
		return hongguoRegistration{}, err
	}
	client := d.hongguoClient()
	client.mu.Lock()
	if client.registrationGate == nil {
		client.registrationGate = make(chan struct{}, 1)
	}
	gate := client.registrationGate
	client.mu.Unlock()
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return hongguoRegistration{}, ctx.Err()
	}
	defer func() { <-gate }()
	if err := ctx.Err(); err != nil {
		return hongguoRegistration{}, err
	}
	client.mu.Lock()
	identity, saved, loadErr := client.registration, client.registrationSaved, client.registrationError
	client.mu.Unlock()
	if loadErr != nil {
		return hongguoRegistration{}, loadErr
	}
	if identity != nil && saved {
		return *identity, nil
	}
	var fresh hongguoRegistration
	if identity != nil {
		fresh = *identity
	} else {
		var err error
		fresh, err = d.registerHongguoIdentity(ctx)
		if err != nil {
			return hongguoRegistration{}, err
		}
		client.mu.Lock()
		client.registration = &fresh
		client.mu.Unlock()
	}
	if d.cfg.dataDir != "" {
		data, err := json.Marshal(fresh)
		if err != nil {
			return hongguoRegistration{}, err
		}
		if err := writeNativeCacheFile(filepath.Join(d.cfg.dataDir, "hongguo-device.json"), data); err != nil {
			return hongguoRegistration{}, errors.New("红果设备注册信息无法保存，请检查应用数据目录后重试；本次身份已保留")
		}
	}
	client.mu.Lock()
	client.registration = &fresh
	client.registrationSaved = true
	client.state.DeviceID, client.state.InstallID = fresh.DeviceID, fresh.InstallID
	client.mu.Unlock()
	return fresh, nil
}

func (d *Downloader) registerHongguoIdentity(ctx context.Context) (hongguoRegistration, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return hongguoRegistration{}, errors.New("无法准备红果设备注册请求")
	}
	token := hex.EncodeToString(random)
	deviceUUID := token[:8] + "-" + token[8:12] + "-" + token[12:16] + "-" + token[16:20] + "-" + token[20:]
	header := map[string]any{
		"aid": 8662, "app_name": "novelread", "display_name": "红果免费短剧", "package": "com.phoenix.read",
		"app_version": "7.3.5.32", "version_code": 73532, "manifest_version_code": 73532, "update_version_code": 73532,
		"channel": "update_64", "os": "Android", "device_platform": "android", "os_version": "16", "os_api": 36,
		"device_model": "25053RT47C", "device_brand": "Redmi", "device_manufacturer": "Xiaomi", "cpu_abi": "arm64-v8a",
		"resolution": "2772x1280", "density_dpi": 520, "language": "zh", "app_language": "zh", "access": "wifi",
		"timezone": 8, "tz_name": "Asia/Shanghai", "tz_offset": 28800, "region": "CN", "sim_region": "cn",
		"openudid": token[:16], "clientudid": deviceUUID, "cdid": deviceUUID,
	}
	payload, err := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": time.Now().UnixMilli()})
	if err != nil {
		return hongguoRegistration{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hongguoRegisterURL, bytes.NewReader(payload))
	if err != nil {
		return hongguoRegistration{}, err
	}
	request.Header.Set("User-Agent", hongguoAppUserAgent)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
		query := url.Values{"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {"73532"}, "version_name": {"7.3.5.32"}, "_rticket": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}
		prepared.URL.RawQuery = query.Encode()
	})
	if err != nil {
		return hongguoRegistration{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return hongguoRegistration{}, err
	}
	if response.StatusCode != http.StatusOK {
		return hongguoRegistration{}, d.catalogResponseError(request, response, content)
	}
	if len(content) == 0 || len(content) > 64<<10 {
		return hongguoRegistration{}, errors.New("红果设备注册未返回有效数据")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if decoder.Decode(&result) != nil {
		return hongguoRegistration{}, errors.New("红果设备注册响应格式异常")
	}
	identity := hongguoRegistration{Version: 1, DeviceID: mapString(result, "device_id_str", "device_id"), InstallID: mapString(result, "install_id_str", "install_id")}
	if !identity.valid() {
		return hongguoRegistration{}, fmt.Errorf("红果设备注册未返回有效编号：%s", truncate(firstNonEmpty(mapString(result, "message", "msg"), "请稍后重试"), 120))
	}
	return identity, nil
}
