package core

import (
	"crypto/md5"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"strings"
	"time"
)

const hgDrpyVideoAPI = "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/"

const hgDrpyVideoUA = "com.phoenix.read/71332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"

var hgDrpyDeviceParams = [][2]string{
	{"ac", "wifi"},
	{"channel", "update_64"},
	{"aid", "8662"},
	{"app_name", "novelread"},
	{"version_code", "71332"},
	{"version_name", "7.1.3.32"},
	{"device_platform", "android"},
	{"os", "android"},
	{"ssmix", "a"},
	{"device_type", "25053RT47C"},
	{"device_brand", "Redmi"},
	{"language", "zh"},
	{"os_api", "36"},
	{"os_version", "16"},
	{"manifest_version_code", "71332"},
	{"resolution", "1280*2772"},
	{"dpi", "520"},
	{"update_version_code", "71332"},
	{"host_abi", "arm64-v8a"},
	{"dragon_device_type", "phone"},
	{"pv_player", "71332"},
	{"compliance_status", "0"},
	{"need_personal_recommend", "1"},
	{"player_so_load", "1"},
	{"is_android_pad_screen", "0"},
}

func hgDrpyPythonURLEncode(values [][2]string) string {
	var builder strings.Builder
	for index, pair := range values {
		if index > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(hgDrpyQuote(pair[0]))
		builder.WriteByte('=')
		builder.WriteString(hgDrpyQuote(pair[1]))
	}
	return builder.String()
}

func hgDrpyQuote(value string) string {
	const upperDigits = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		current := value[index]
		switch {
		case current >= 'A' && current <= 'Z',
			current >= 'a' && current <= 'z',
			current >= '0' && current <= '9',
			current == '-' || current == '_' || current == '.' || current == '~' || current == '*':
			builder.WriteByte(current)
		default:
			builder.WriteByte('%')
			builder.WriteByte(upperDigits[current>>4])
			builder.WriteByte(upperDigits[current&0x0f])
		}
	}
	return builder.String()
}

const hongguoBranchAttempts = 128

func hgDrpyBranchOf(fullURL string, body []byte, khronos uint32) int {
	query := ""
	if index := strings.IndexByte(fullURL, '?'); index >= 0 && index < len(fullURL)-1 {
		query = fullURL[index+1:]
	}
	var bodyMd5 [16]byte
	if len(body) > 0 {
		bodyMd5 = md5.Sum(body)
	}
	querySm3 := hgDrpySM3([]byte(query))
	stamp := hgDrpyLe32(khronos)
	iv := hgDrpyGetIv(hgDrpyGetIv(hgDrpyGetIv(0x20230928, querySm3[:]), bodyMd5[:]), stamp[:])
	low := iv & 15
	return int(low - ((low*171)>>9)*3)
}

func hgDrpyMd5Hex(value []byte) string {
	digest := md5.Sum(value)
	const upperDigits = "0123456789ABCDEF"
	encoded := make([]byte, len(digest)*2)
	for index, current := range digest {
		encoded[index*2] = upperDigits[current>>4]
		encoded[index*2+1] = upperDigits[current&0x0f]
	}
	return string(encoded)
}

func hgDrpySignedVideoRequestWithDevice(payload []byte, now time.Time, api, iid, deviceID string) (string, map[string]string) {
	khronos := uint32(now.Unix())
	ticketBase := now.UnixMilli()
	var query, fullURL string
	requestTicket := ticketBase
	for offset := int64(0); offset < hongguoBranchAttempts; offset++ {
		requestTicket = ticketBase + offset
		values := make([][2]string, 0, len(hgDrpyDeviceParams)+4)
		values = append(values, hgDrpyDeviceParams...)
		values = append(values,
			[2]string{"iid", iid},
			[2]string{"device_id", deviceID},
			[2]string{"ts", strconv.FormatUint(uint64(khronos), 10)},
			[2]string{"_rticket", strconv.FormatInt(requestTicket, 10)},
		)
		query = hgDrpyPythonURLEncode(values)
		fullURL = api + "?" + query
		if hgDrpyBranchOf(fullURL, payload, khronos) == 0 {
			break
		}
	}
	random := uint32(0)
	var randomBuffer [2]byte
	if _, err := crand.Read(randomBuffer[:]); err == nil {
		random = uint32(binary.LittleEndian.Uint16(randomBuffer[:]))
	}
	bodyDigest := hgDrpyMd5Hex(payload)
	ladonStamp := hgDrpyBe32(khronos)
	argusStamp := hgDrpyLe32(khronos)
	nonce, started := hgDrpyLoadProcess()
	headers := map[string]string{
		"User-Agent":                hgDrpyVideoUA,
		"Accept":                    "application/json; charset=utf-8,application/x-protobuf",
		"Content-Type":              "application/json; charset=UTF-8",
		"x-xs-from-web":             "0",
		"x-ss-req-ticket":           strconv.FormatInt(requestTicket, 10),
		"x-tt-request-tag":          "t=0;n=0",
		"sdk-version":               "2",
		"passport-sdk-version":      "50561",
		"x-vc-bdturing-sdk-version": "3.7.2.cn",
		"x-ss-stub":                 bodyDigest,
		"x-gorgon":                  hgDrpyXGorgon(query, payload, khronos, random),
		"x-khronos":                 strconv.FormatUint(uint64(khronos), 10),
		"x-ladon":                   base64.StdEncoding.EncodeToString(ladonStamp[:]),
		"x-argus":                   base64.StdEncoding.EncodeToString(argusStamp[:]),
		"x-helios":                  hgDrpyHelios(khronos, hgDrpyRandomU32()),
		"x-medusa":                  base64.StdEncoding.EncodeToString(hgDrpyBuildMedusa(fullURL, payload, khronos, deviceID, "7.1.3.32", nonce, started/1000, started, nil)),
		"x-tt-dt":                   "",
	}
	return fullURL, headers
}
