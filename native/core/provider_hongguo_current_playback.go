package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const hongguoCurrentRegisterURL = "https://log5-applog-lf.fqnovel.com/service/2/device_register/?tt_data=a&cronet_version=8d40f833_2026-03-03&ttnet_version=4.2.243.31-douyin&use_store_region_cookie=1"

func hongguoPlaybackRegistrationBody(now time.Time) ([]byte, error) {
	random := make([]byte, 56)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	uuid := func(raw []byte) string {
		raw[6] = raw[6]&15 | 64
		raw[8] = raw[8]&63 | 128
		return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	}
	header := map[string]any{
		"display_name": "红果免费短剧", "update_version_code": 73932, "manifest_version_code": 73932,
		"app_version_minor": "7.3.9.32", "aid": 8662, "channel": "update_64", "package": "com.phoenix.read",
		"app_version": "7.3.9.32", "version_code": 73932, "sdk_version": "3.7.0-rc.34-fanqie-xiaoshuo",
		"sdk_target_version": 29, "git_hash": "3a88f4e", "os": "Android", "os_version": "16", "os_api": 36,
		"device_model": "25053RT47C", "device_brand": "Redmi", "device_manufacturer": "Redmi",
		"cpu_abi": "arm64-v8a", "release_build": "b5e187f", "density_dpi": 520, "display_density": "mdpi",
		"resolution": "1280x2772", "language": "zh", "timezone": 8, "access": "wifi", "not_request_sender": 0,
		"carrier": "CHINA MOBILE", "mcc_mnc": "46000", "rom": "eng.root.20231116.160340",
		"rom_version": "W528JS release-keys", "cdid": uuid(random[:16]),
		"sig_hash": "56a962410c494bbaf0b58dba20cae56f", "openudid": hex.EncodeToString(random[48:]),
		"clientudid": uuid(random[16:32]), "ipv6_list": []string{}, "region": "CN", "tz_name": "Asia/Shanghai",
		"tz_offset": 28800, "sim_region": "cn", "sim_serial_number": []string{}, "oaid_may_support": false,
		"req_id": uuid(random[32:48]), "device_platform": "android",
		"custom":                map[string]any{"account_region_src": "fallback", "account_region": "cn", "host_bit": 64, "dragon_device_type": "phone"},
		"pre_installed_channel": "", "apk_first_install_time": now.UnixMilli(), "is_system_app": 0,
		"sdk_flavor": "china", "guest_mode": 0,
	}
	plain, err := json.Marshal(map[string]any{"magic_tag": "ss_app_log", "header": header, "_gen_time": now.UnixMilli()})
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(plain); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	inner := sha512.Sum512(seed)
	derived := sha512.Sum512(append(inner[:], hongguoURLConstants...))
	digest := sha512.Sum512(compressed.Bytes())
	content := append(digest[:], compressed.Bytes()...)
	padding := aes.BlockSize - len(content)%aes.BlockSize
	content = append(content, bytes.Repeat([]byte{byte(padding)}, padding)...)
	block, err := aes.NewCipher(derived[:16])
	if err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(block, derived[16:32]).CryptBlocks(content, content)
	return append(append([]byte{116, 99, 5, 16, 0, 0}, seed...), content...), nil
}

func (d *Downloader) registerHongguoPlaybackDevice(ctx context.Context) (hongguoRegistration, error) {
	now := time.Now()
	body, err := hongguoPlaybackRegistrationBody(now)
	if err != nil {
		return hongguoRegistration{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hongguoCurrentRegisterURL, bytes.NewReader(body))
	if err != nil {
		return hongguoRegistration{}, err
	}
	request.Header.Set("User-Agent", "com.phoenix.read/73932 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3)")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/octet-stream;tt-data=a")
	request.Header.Set("Log-Encode-Type", "gzip")
	request.Header.Set("X-SS-DP", "8662")
	request.Header.Set("Sdk-Version", "2")
	request.Header.Set("X-SS-STUB", hgMd5HexUpper(body))
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, nil)
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
		return hongguoRegistration{}, errors.New("红果播放设备登记响应无效")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return hongguoRegistration{}, errors.New("红果播放设备登记数据格式无效")
	}
	identity := hongguoRegistration{Version: 1, DeviceID: mapString(result, "device_id_str", "device_id"), InstallID: mapString(result, "install_id_str", "install_id")}
	if !identity.valid() {
		return hongguoRegistration{}, errors.New("红果播放设备登记未返回有效编号")
	}
	return identity, nil
}

func (d *Downloader) hongguoCurrentPlayerPostAt(ctx context.Context, videoID, base string) (map[string]any, error) {
	identity, err := d.registerHongguoPlaybackDevice(ctx)
	if err != nil {
		return nil, err
	}
	if base == "" {
		client := d.hongguoClient()
		client.mu.Lock()
		base = client.baseURL
		client.mu.Unlock()
	}
	payload := hongguoMultiPayload{
		BizParam:        hongguoMultiBizParam{DeviceLevel: 3, NeedAllVideoDefinition: true, VideoPlatform: 1024},
		MixedVideoIDMap: map[string][]string{"1004": {videoID}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, automaticReadOnlyKey{}, true)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+hongguoMultiPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	response, err := d.doPreparedCatalogRequest(request, 20*time.Second, func(prepared *http.Request) {
		address, headers := hgDrpySignedVideoRequestWithDevice(body, time.Now(), strings.TrimRight(base, "/")+hongguoMultiPath, identity.InstallID, identity.DeviceID)
		prepared.URL.RawQuery = address[strings.Index(address, "?")+1:]
		for name, value := range headers {
			prepared.Header.Set(name, value)
		}
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Retry-After") != "" {
		return nil, d.catalogResponseError(request, response, content)
	}
	if len(content) == 0 {
		return nil, errHongguoEmptyPlayback
	}
	if len(content) > providerMaxBodyBytes {
		return nil, errors.New("红果播放响应过大")
	}
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, errors.New("红果播放响应格式无效")
	}
	if code := mapString(result, "code", "status_code"); code != "" && code != "0" {
		return nil, fmt.Errorf("红果播放接口暂不可用（%s）", truncate(code, 20))
	}
	entry := nestedMap(nestedMap(result, "data"), videoID)
	if len(entry) == 0 {
		return nil, errors.New("红果播放接口未返回所选分集")
	}
	return entry, nil
}
