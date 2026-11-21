// SPDX-License-Identifier: GPL-3.0-only

package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/bits"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type hongguoV5Identity struct {
	hongguoRegistration
	CDID string
}

const (
	hgV5License     = "646637794"
	hgV5MagicHex    = "2d4b4fca49750d433fb5ae2c226dcc56"
	hgV5SignKeyHex  = "b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d"
	hgV5VersionName = "7.4.3.32"
	hgV5Channel     = "vivo_8662_64"
	hgV5DeviceType  = "PGT-AN10"
	hgV5OSVersion   = "12"
	hgV5PlayUA      = "com.phoenix.read/73932 (Linux; U; Android 12; zh_CN_#Hans; PGT-AN10; Build/V417IR;tt-ok/3.12.13.20)"
	hgV5Aid         = "8662"
	hgV5AppName     = "novelread"
	hgV5VersionCode = "74332"
	hgV5Endpoint    = "https://api5-normal-sinfonlinea.fqnovel.com/novel/player/multi_video_model/player/v1"
	hgV5TabEndpoint = "https://api5-normal-sinfonlineb.fqnovel.com/reading/bookapi/video_tab/multi_video_model/v1/"
)

func hgV5MustHex(value string) []byte {
	raw, err := hex.DecodeString(value)
	if err != nil {
		panic("红果 v5 常量不是合法 hex: " + err.Error())
	}
	return raw
}

var (
	hgV5Magic   = hgV5MustHex(hgV5MagicHex)
	hgV5SignKey = hgV5MustHex(hgV5SignKeyHex)
	hgV5SBox    = hgV5MustHex(
		"53290fe9e51f316f90f74a7e034d367784ab49237cd0135cdc632c322fd3be0e2624a417ecd9151a7ff64f60730a87f9445ec6ada2f865d22b696c4188f24c0d" +
			"f37b75510b939f6bc2fe6883e68bc8c722181ba0bdb08c9871b50c9a16b46eea8eed20ba4b8a14e0fcf1468dbbef05a93e5a9d7dcf06898635c1fd99f5de287a" +
			"80c594daaab19b6279fbd75070cb52eb2555c456e39e7839853874e2f0013ac0b64742d8ca10e4b79c955de8b843ce4ed464a6a72ed508091c92e7b3eedd1e1d" +
			"d12afac321618fa3d65f9167c982f43d66a53c6a1997585b346dbf9672ff37cdbc073b2dae11453304df00815476b2a13fccdb30b957a8acafe1274002485912" +
			"38922563e36406d37a24100b7902a028044e218453c2a399199c9d13dc40c55ba67e502273850ebdc1a2e2188b8f2d9e3cb909b8c46a2a9f4a6dd93007eafa65" +
			"b7b3a881aa908cc74f0ad76bbbb58617c37012ca05a469fd44e62eecf1e4272ce860c0ae5eadf99b20d8f0234174006f3f110d7657d01645d4eff8f71471cbfe" +
			"1cb2f52648727bf3c9de55153e031b4d61757db63dbf7782eeafe9018a8891362fb14759ac32bab03b349aeb5a377c5156be4c8dfbe13a684bda39cf836697c8" +
			"94545fe0f2491acea9fc6252f4ff1e0fe72bd2df6e67a7f635e5bcc6d1a5dd951d89960887a18eb40c785cdb1f80ed42299333ab98cdd67f316cd54346585dcc" +
			"d759fb722789b08807cc0fda293f7e5e1819bb0353517020b31d3d00374952bea2c421e862040a9ec892a05b266b7a0d0cc316f483b81af39715657ffe32ad8d" +
			"e0c13077646035236ee5363e5f900e9a7cf850a81cd9960b6df73317226f99eaa6fff15cec34acb98246639b918ab2851466a7cdfc4ad1b52c2ae95a58d3734e" +
			"47ddd5b7560193f644ef9c408b7dee39b4d6aabf4de761e3252d4c7ba3843187745d55e6a5db05d42b45caed43c0c5d0b64bfa9d94a4a9d8f2f0e1a108dcfd28" +
			"8fd2bc1fc7bd675412138169cb09249875ba3b68cfb11180afae104fc9789571c6dfce02413886796ae21e3c488cc21b9f423a066c2eabf98e2fdeeb5776e4f5" +
			"d09a515ba58cbbab3e37b09fd4f6d9244df80512e84cd7956be301cf64f1429d58577cc241f3a2c655144f81aaf95a4e108da894b38970561ae9002619fbbac9" +
			"1fa922ed202bfc62cb08e0de2a7f3631435f8b47c8a3c563b4db4071ce74c46da6fa696a9e282d1e825d663b93bfd56c48dce1884bb10fccea063929f746443f" +
			"d830a4232e53da21a1f2d1bdfebc3a02c10a1b8ad31d04fd616590d272ac737e98eb33af60528716bee78ec0ee8699031177277d9cff32542c67caf5f4459168" +
			"1335ae0e178f3409a7803db83892dde5b9b7f0c750e24aef077badd678a09b850b84ec1597e4963cb5e675df1c0d6fb618496ecd76c35c798325590c2fb27a5e",
	)
)

type hgV5Aes struct {
	box   []byte
	con2  [4]int
	order [16]int
	keys  [12][4]byte
}

func hgV5NewAes(key []byte, khronos uint32) *hgV5Aes {
	word := int(khronos & 3)
	box := hgV5SBox[word*256 : (word+1)*256]
	conTables := [4][4]int{
		{2, 0, 3, 1}, {1, 3, 2, 0}, {3, 2, 1, 0}, {2, 3, 1, 0},
	}
	orderTables := [4][16]int{
		{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
		{0, 9, 14, 11, 4, 13, 2, 3, 8, 1, 6, 15, 12, 5, 10, 7},
		{0, 9, 14, 3, 4, 13, 2, 15, 8, 1, 6, 11, 12, 5, 10, 7},
		{0, 9, 14, 15, 4, 13, 2, 3, 8, 1, 6, 11, 12, 5, 10, 7},
	}
	initValues := [4]uint32{0xbbb5de72, 0xd4638d5b, 0x92a5f73b, 0x89f8a1c9}
	init := initValues[word]
	var initial [16]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(initial[i*4:], init)
	}
	mk := make([]byte, 16)
	for i := 0; i < 16; i++ {
		mk[i] = initial[i] ^ key[i]
	}
	expanded := make([]byte, 48)
	copy(expanded, mk)
	rounds := uint(8)
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if i%4 == 0 {
			t := byte((init >> (rounds & 24)) ^ uint32(box[b]))
			b, c, d, a = box[c], box[d], box[a], t
		}
		rounds += 2
		expanded[at+4] = a ^ expanded[at-12]
		expanded[at+5] = b ^ expanded[at-11]
		expanded[at+6] = c ^ expanded[at-10]
		expanded[at+7] = d ^ expanded[at-9]
	}
	cipher := &hgV5Aes{box: box, con2: conTables[word]}
	copy(cipher.order[:], orderTables[word][:])
	for i := 0; i < 12; i++ {
		copy(cipher.keys[i][:], expanded[i*4:i*4+4])
	}
	return cipher
}

func (c *hgV5Aes) addCon(rows *[4][4]byte, keys [4][4]byte) {
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			rows[i][j] ^= keys[i][c.con2[j]]
		}
	}
}

func (c *hgV5Aes) block(value []byte) []byte {
	var rows [4][4]byte
	for i := 0; i < 4; i++ {
		copy(rows[i][:], value[i*4:i*4+4])
	}
	firstKeys, roundKeys := [4][4]byte{}, [4][4]byte{}
	copy(firstKeys[:], c.keys[0:4])
	c.addCon(&rows, firstKeys)
	for round := 1; round <= 2; round++ {
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rows[i][j] = c.box[rows[i][j]]
			}
		}
		old := rows
		for i := 0; i < 4; i++ {
			rows[i] = old[c.con2[i]]
		}
		flat := make([]byte, 16)
		for i := 0; i < 4; i++ {
			copy(flat[i*4:], rows[i][:])
		}
		shifted := make([]byte, 16)
		for i := 0; i < 16; i++ {
			shifted[i] = flat[c.order[i]]
		}
		for i := 0; i < 4; i++ {
			copy(rows[i][:], shifted[i*4:i*4+4])
		}
		if round == 1 {
			for i := 0; i < 4; i++ {
				oldRow := rows[i]
				rows[i] = [4]byte{oldRow[c.con2[0]], oldRow[c.con2[1]], oldRow[c.con2[2]], oldRow[c.con2[3]]}
			}
			for i := 0; i < 4; i++ {
				t := rows[0][i] ^ rows[1][i] ^ rows[2][i] ^ rows[3][i]
				u := rows[0][i]
				rows[0][i] ^= t ^ hgDrpyXt(rows[0][i]^rows[1][i])
				rows[1][i] ^= t ^ hgDrpyXt(rows[1][i]^rows[2][i])
				rows[2][i] ^= t ^ hgDrpyXt(rows[2][i]^rows[3][i])
				rows[3][i] ^= t ^ hgDrpyXt(rows[3][i]^u)
			}
		}
		copy(roundKeys[:], c.keys[round*4:round*4+4])
		c.addCon(&rows, roundKeys)
	}
	copy(firstKeys[:], c.keys[4:8])
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			rows[i][j] ^= firstKeys[i][j]
		}
	}
	out := make([]byte, 16)
	for i := 0; i < 4; i++ {
		copy(out[i*4:], rows[i][:])
	}
	return out
}

func (c *hgV5Aes) encrypt(data, iv []byte) []byte {
	plaintext := make([]byte, 32)
	wire := [8]int{6, 5, 3, 4, 2, 7, 1, 0}
	compact := [8]int{1, 7, 3, 6, 2, 0, 4, 5}
	for i := 0; i < 31; i++ {
		for j := 0; j < 8; j++ {
			bit := (data[i*8+j] >> uint(wire[j])) & 1
			plaintext[i] |= bit << uint(compact[j])
		}
	}
	plaintext[31] = 1
	blocks := make([]byte, 0, 32)
	previous := make([]byte, 16)
	copy(previous, iv)
	for at := 0; at < len(plaintext); at += 16 {
		xored := make([]byte, 16)
		for i := 0; i < 16; i++ {
			xored[i] = plaintext[at+i] ^ previous[i]
		}
		value := c.block(xored)
		blocks = append(blocks, value...)
		previous = value
	}
	key := blocks
	out := make([]byte, len(data))
	copy(out, data)
	for i := 0; i < 31; i++ {
		for j := 0; j < 8; j++ {
			at := i*8 + j
			if at >= len(out) {
				continue
			}
			mask := byte(^(1 << uint(wire[j])))
			bit := ((key[i] >> uint(compact[j])) & 1) << uint(wire[j])
			out[at] = (out[at] & mask) | bit
		}
	}
	result := make([]byte, 0, 1+len(out))
	result = append(result, key[len(key)-1])
	result = append(result, out...)
	return result
}

func hgV5HashF13(querySm3, bodyMd5 []byte, ts uint32) []byte {
	tsBytes := hgDrpyLe32(ts)
	iv := hgDrpyGetIv(hgDrpyGetIv(hgDrpyGetIv(0x20230928, querySm3), bodyMd5), tsBytes[:])
	if iv&15 != 12 {
		return nil
	}
	const ivV1 uint32 = 0x9d4a97c2
	count := ivV1 + ts
	countV1 := (count + 1) & 255
	countV2 := count
	tt02 := [64]uint32{
		0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8,
		0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288,
		0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e,
		0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c,
		0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3,
		0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8,
		0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14,
		0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586,
	}
	var tt [64]uint32
	for i, v := range tt02 {
		tt[i] = hgDrpyRor32(v, countV1)
	}
	shift := (countV2 + 2) & 7
	seed := [4]uint32{0xfa, 0x45, 0x61, 0xd7}
	pad := make([]byte, 4)
	for i, v := range seed {
		pad[i] = byte(((v | (v << 8)) >> shift) & 255)
	}
	data := make([]byte, 0, 48)
	data = append(data, querySm3...)
	data = append(data, bodyMd5...)
	data = append(data, tsBytes[:]...)
	data = append(data, pad...)
	data = append(data, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0xa0)
	di := make([]uint32, 0, 240)
	for i := 0; i+4 <= len(data); i += 4 {
		di = append(di, binary.BigEndian.Uint32(data[i:]))
	}
	for i := 0; i < 112; i++ {
		x, y := di[i+1], di[i+14]
		r1 := bits.RotateLeft32(x, 14) ^ bits.RotateLeft32(x, 25) ^ (x >> 3)
		r2 := bits.RotateLeft32(y, 13) ^ bits.RotateLeft32(y, 15) ^ (y >> 10)
		di = append(di, di[i]+di[i+9]+r1+r2)
	}
	initial := []uint32{
		0x7aba4fc8, 0x67166507, 0x6403fa00, 0x340f512f,
		984304912, 3005047866, 2874125293, 2152413264,
	}
	for i := range initial {
		initial[i] = hgDrpyRor32(initial[i], count)
	}
	chosen := [11]int{96, 1, 3, 4, 0, 6, 5, 2, 0, 7, 7}
	d := make([]uint32, 8)
	copy(d, initial)
	for i := 0; i < chosen[0]; i++ {
		idx := ivV1 + uint32(i)
		n1 := ((d[chosen[3]] ^ d[chosen[4]]) & d[chosen[1]]) ^ d[chosen[3]]
		n2 := bits.RotateLeft32(d[chosen[1]], 26) ^ bits.RotateLeft32(d[chosen[1]], 21) ^ bits.RotateLeft32(d[chosen[1]], 7)
		n4 := di[idx&127] + n1 + n2 + tt[idx&63] + d[chosen[5]]
		n5 := bits.RotateLeft32(d[chosen[2]], 30) ^ bits.RotateLeft32(d[chosen[2]], 19) ^ bits.RotateLeft32(d[chosen[2]], 10)
		n6 := (d[chosen[2]] & d[chosen[6]]) | ((d[chosen[2]] | d[chosen[6]]) & d[chosen[7]])
		old := d[chosen[9]]
		d = append([]uint32{d[7]}, d[:7]...)
		d[chosen[10]] = n5 + n6 + n4
		d[chosen[8]] = old + n4
	}
	ret := make([]byte, 0, 32)
	for i := 0; i < 8; i++ {
		be := hgDrpyBe32(d[i] + initial[i])
		ret = append(ret, be[:]...)
	}
	folded := make([]byte, 16)
	for i := 0; i < 16; i++ {
		folded[i] = ret[i] ^ ret[i+16]
	}
	check := hgDrpySumMd5(folded) | 0x20
	out := make([]byte, 20)
	copy(out, folded)
	le := hgDrpyLe32(check)
	copy(out[16:], le[:])
	return out
}

func hgV5SeedIndex(i int) int { return 3 - (i & 3) }

func hgV5Medusa(fullURL string, body []byte, ts uint32, nonce string, started int64, who map[string]string) []byte {
	query := fullURL
	if at := strings.IndexByte(query, '?'); at >= 0 {
		query = query[at+1:]
	}
	querySm3 := hgDrpySM3([]byte(query))
	bodyMd5 := md5.Sum(body)
	f13 := hgV5HashF13(querySm3[:], bodyMd5[:], ts)
	flags := make([]byte, 8)
	version := who["version_name"]
	deviceID := who["device_id"]
	osVersion := who["os_version"]
	deviceType := who["device_type"]

	deviceInfo := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: int64(1), Type: "sint"},
		{Tag: 2, Value: int64(2), Type: "sint"},
		{Tag: 3, Value: "8662", Type: "string"},
		{Tag: 4, Value: deviceID, Type: "string"},
		{Tag: 5, Value: nonce, Type: "string"},
		{Tag: 6, Value: "!noperm!", Type: "string"},
		{Tag: 12, Value: "Asia/Shanghai,8", Type: "string"},
		{Tag: 13, Value: "zh_CN", Type: "string"},
		{Tag: 22, Value: osVersion, Type: "string"},
		{Tag: 25, Value: started, Type: "sint"},
		{Tag: 30, Value: deviceType, Type: "string"},
		{Tag: 31, Value: "HONOR", Type: "string"},
		{Tag: 32, Value: deviceType, Type: "string"},
		{Tag: 34, Value: "HONOR", Type: "string"},
	})
	env := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 6, Value: "v04.10.03.04-bugfix", Type: "string"},
		{Tag: 8, Value: who["channel"], Type: "string"},
		{Tag: 12, Value: deviceInfo, Type: "bytes"},
		{Tag: 14, Value: version, Type: "string"},
	})
	nested15 := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: int64(1), Type: "sint"},
		{Tag: 2, Value: int64(1), Type: "sint"},
		{Tag: 3, Value: int64(1), Type: "sint"},
		{Tag: 5, Value: int64(1), Type: "sint"},
	})
	message := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: hgV5Magic, Type: "bytes"},
		{Tag: 2, Value: int64(5), Type: "sint"},
		{Tag: 3, Value: int64(hgDrpyRandomU32()), Type: "sint"},
		{Tag: 4, Value: "8662", Type: "string"},
		{Tag: 5, Value: deviceID, Type: "string"},
		{Tag: 6, Value: hgV5License, Type: "string"},
		{Tag: 7, Value: version, Type: "string"},
		{Tag: 8, Value: "v04.10.03-ml-android", Type: "string"},
		{Tag: 9, Value: int64(0x040a0300), Type: "sint"},
		{Tag: 10, Value: flags, Type: "bytes"},
		{Tag: 12, Value: int64(ts), Type: "sint"},
		{Tag: 13, Value: f13, Type: "bytes"},
		{Tag: 14, Value: querySm3[:6], Type: "bytes"},
		{Tag: 15, Value: nested15, Type: "bytes"},
		{Tag: 17, Value: int64(ts), Type: "sint"},
		{Tag: 20, Value: "none", Type: "string"},
		{Tag: 21, Value: int64(377), Type: "sint"},
		{Tag: 23, Value: env, Type: "bytes"},
		{Tag: 24, Value: `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`, Type: "string"},
	})

	key := hgV5SignKey
	nonceRand := hgDrpyRandomU32()
	keyLE := hgDrpyLe32(nonceRand)
	hashInput := make([]byte, 0, len(key)*2+4)
	hashInput = append(hashInput, key...)
	hashInput = append(hashInput, keyLE[:]...)
	hashInput = append(hashInput, key...)
	hash := hgDrpySM3(hashInput)

	high := (nonceRand >> 16) & 255
	seedVal := ^(((high << 11) | (nonceRand >> 24)) ^ (high >> 5) ^ high)
	seed := hgDrpyLe32(seedVal)

	inner := hgDrpyXmxor(message, hash[:])
	material := make([]byte, 0, len(flags)+len(inner))
	material = append(material, flags...)
	material = append(material, inner...)
	transformed := hgV5ReverseBytes(material)
	for i := range transformed {
		transformed[i] ^= seed[hgV5SeedIndex(i)]
	}

	check := uint32(0x18000001) |
		((uint32(querySm3[0]) & 63) << 14) |
		((uint32(f13[0]) & 63) << 8)
	checkLE := hgDrpyLe32(check)
	randLE := hgDrpyLe32(hgDrpyRandomU32())
	packed := make([]byte, 0, 1+4+4+len(transformed)+2)
	packed = append(packed, 0xca)
	packed = append(packed, randLE[:]...)
	packed = append(packed, checkLE[:]...)
	packed = append(packed, transformed...)
	packed = append(packed, byte(nonceRand>>16), byte(nonceRand>>24))

	encKey := md5.Sum(key[:16])
	encIV := md5.Sum(key[16:32])
	encrypted := hgV5NewAes(encKey[:], ts).encrypt(packed, encIV[:])

	prefix := make([]byte, 0, 8)
	prefixLE := hgDrpyLe32(5)
	prefix = append(prefix, prefixLE[:]...)
	prefix = append(prefix, hgV5Magic...)
	tsLE := hgDrpyLe32(ts)
	for i := range prefix {
		prefix[i] ^= tsLE[i&3]
	}
	result := make([]byte, 0, len(prefix)+4+len(encrypted))
	result = append(result, prefix...)
	result = append(result, byte(nonceRand), byte(nonceRand>>8), 0, 1)
	result = append(result, encrypted...)
	return result
}

func hgV5ReverseBytes(in []byte) []byte {
	out := make([]byte, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

func hgV5Helios(ts uint32, random uint32) string {
	data := make([]byte, 0, 8)
	stamp := hgDrpyLe32(random)
	data = append(data, stamp[:]...)
	data = append(data, '8', '6', '6', '2')
	digest := md5.Sum(data)
	ascii := make([]byte, 32)
	const hexDigits = "0123456789abcdef"
	for index, value := range digest {
		ascii[index*2] = hexDigits[value>>4]
		ascii[index*2+1] = hexDigits[value&0x0f]
	}
	words := make([]uint64, 4)
	for index := 0; index < 4; index++ {
		words[index] = binary.LittleEndian.Uint64(ascii[index*8:])
	}
	table := []uint64{words[0]}
	b0 := words[0]
	b8 := words[1]
	queue := append([]uint64(nil), words[2:]...)
	for index := int64(0); index < 34; index++ {
		x8 := bits.RotateLeft64(b8, 64-8) + b0
		x8 ^= uint64(index)
		queue = append(queue, x8)
		x8 ^= bits.RotateLeft64(b0, 64-61)
		table = append(table, x8)
		b0 = x8
		b8 = queue[0]
		queue = queue[1:]
	}
	text := []byte(strconv.FormatUint(uint64(ts), 10) + "-" + hgV5License + "-8662")
	padLength := (len(text)/16 + 1) * 16
	if padLength < len(text)+1 {
		padLength = len(text) + 1
	}
	pad := make([]byte, padLength)
	for index := range pad {
		pad[index] = byte(16 - len(text)%16)
	}
	copy(pad, text)
	output := make([]byte, 0, padLength)
	for at := 0; at < padLength; at += 16 {
		a := binary.LittleEndian.Uint64(pad[at:])
		b := binary.LittleEndian.Uint64(pad[at+8:])
		for index := 0; index < 34; index++ {
			b = table[index] ^ (a + bits.RotateLeft64(b, 64-8))
			a = b ^ bits.RotateLeft64(a, 64-61)
		}
		block := make([]byte, 16)
		binary.LittleEndian.PutUint64(block, a)
		binary.LittleEndian.PutUint64(block[8:], b)
		output = append(output, block...)
	}
	result := make([]byte, 0, 4+len(output))
	result = append(result, stamp[:]...)
	result = append(result, output...)
	return base64.StdEncoding.EncodeToString(result)
}

func hgV5Gzip(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func hgV5SignHeader(fullURL string, body []byte, ts uint32, nonce string, started int64, who map[string]string) map[string]string {
	headers := map[string]string{
		"x-khronos": strconv.FormatUint(uint64(ts), 10),
		"x-helios":  hgV5Helios(ts, hgDrpyRandomU32()),
		"x-medusa":  base64.StdEncoding.EncodeToString(hgV5Medusa(fullURL, body, ts, nonce, started, who)),
	}
	sum := md5.Sum(body)
	headers["x-ss-stub"] = strings.ToUpper(hex.EncodeToString(sum[:]))
	return headers
}

func hgV5Query(identity hongguoV5Identity, tabVideoID string, ticket int64) [][2]string {
	values := [][2]string{
		{"aid", hgV5Aid},
		{"app_name", hgV5AppName},
		{"version_code", hgV5VersionCode},
		{"version_name", hgV5VersionName},
		{"channel", hgV5Channel},
		{"device_platform", "android"},
		{"device_type", hgV5DeviceType},
		{"os_version", hgV5OSVersion},
		{"update_version_code", hgV5VersionCode},
		{"iid", identity.InstallID},
		{"cdid", identity.CDID},
		{"device_id", identity.DeviceID},
	}
	if tabVideoID != "" {
		values = append(values, [][2]string{
			{"video_id", tabVideoID}, {"video_id_type", "0"}, {"device_level", "1"},
			{"need_all_video_definition", "1"}, {"need_mp4_align", "0"}, {"use_os_player", "0"},
			{"video_platform", "1024"}, {"source", "0"}, {"from_video_id", ""},
		}...)
	}
	return append(values, [2]string{"_rticket", strconv.FormatInt(ticket, 10)})
}

func hgV5IdentityMap(identity hongguoV5Identity) map[string]string {
	return map[string]string{
		"device_id":    identity.DeviceID,
		"iid":          identity.InstallID,
		"cdid":         identity.CDID,
		"version_name": hgV5VersionName,
		"channel":      hgV5Channel,
		"os_version":   hgV5OSVersion,
		"device_type":  hgV5DeviceType,
	}
}

func hgV5Payload(videoID string) ([]byte, error) {
	raw := `{"biz_param":{"detail_page_version":0,"device_level":1,"disable_digg_stat":false,"disable_video_relate_book":false,"need_all_video_definition":true,"need_mp4_align":false,"use_os_player":false,"use_server_dns":false,"video_platform":1024},"mixed_video_id_map":{"1004":["` +
		videoID + `"]},"dr_scene":"player"}`
	return hgV5Gzip([]byte(raw))
}

func (downloader *Downloader) hongguoV5PlayerPost(ctx context.Context, videoID string, registration hongguoRegistration) (map[string]any, error) {
	if !hongguoNumericID.MatchString(videoID) || !registration.valid() {
		return nil, errors.New("红果新版播放请求缺少有效分集或访客身份")
	}
	identity := hongguoV5Identity{hongguoRegistration: registration, CDID: uuidLike()}
	body, err := hgV5Payload(videoID)
	if err != nil {
		return nil, errors.New("v5 请求体压缩失败")
	}
	model, err := downloader.hongguoV5Request(ctx, videoID, identity, http.MethodPost, hgV5Endpoint, body)
	if errors.Is(err, errHongguoEmptyPlayback) && ctx.Err() == nil {
		return downloader.hongguoV5Request(ctx, videoID, identity, http.MethodGet, hgV5TabEndpoint, nil)
	}
	return model, err
}

func (downloader *Downloader) hongguoV5Request(ctx context.Context, videoID string, identity hongguoV5Identity, method, endpoint string, body []byte) (map[string]any, error) {
	bodyMD5 := md5.Sum(body)
	now := time.Now().UnixMilli()
	ts := uint32(now / 1000)
	tabVideoID := ""
	if method == http.MethodGet {
		tabVideoID = videoID
	}

	var ticket int64
	var query string
	for offset := int64(0); offset < 1024; offset++ {
		ticket = now + offset
		candidate := hgDrpyPythonURLEncode(hgV5Query(identity, tabVideoID, ticket))
		qsm3 := hgDrpySM3([]byte(candidate))
		tsLE := hgDrpyLe32(ts)
		iv := hgDrpyGetIv(hgDrpyGetIv(hgDrpyGetIv(0x20230928, qsm3[:]), bodyMD5[:]), tsLE[:])
		if iv&15 == 12 {
			query = candidate
			break
		}
	}
	if query == "" {
		return nil, errors.New("v5 取流无法生成合法签名票据")
	}

	fullURL := endpoint + "?" + query
	ctx = context.WithValue(ctx, automaticReadOnlyKey{}, true)
	request, err := http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	nonce, started := hgDrpyLoadProcess()
	for name, value := range hgV5SignHeader(fullURL, body, ts, nonce, started, hgV5IdentityMap(identity)) {
		request.Header.Set(name, value)
	}
	request.Header.Set("User-Agent", hgV5PlayUA)
	request.Header.Set("X-SS-Req-Ticket", strconv.FormatInt(ticket, 10))
	if method == http.MethodGet {
		request.Header.Set("Accept", "application/json")
		request.Header.Del("X-SS-Stub")
	} else {
		request.Header.Set("Accept", "application/json, text/plain, */*")
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		request.Header.Set("Content-Encoding", "gzip")
		request.Header.Set("Sdk-Version", "2")
		request.Header.Set("X-XS-From-Web", "0")
	}
	response, err := downloader.doPreparedCatalogRequest(request, 20*time.Second, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Retry-After") != "" || catalogResponseBlockReason(response, data) != "" {
		return nil, downloader.catalogResponseError(request, response, data)
	}
	if len(data) == 0 {
		return nil, errHongguoEmptyPlayback
	}
	if len(data) > providerMaxBodyBytes {
		return nil, errors.New("红果播放响应过大")
	}
	var model map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&model); err != nil {
		return nil, errors.New("红果播放响应格式无效")
	}
	if code := mapString(model, "code", "status_code"); code != "" && code != "0" {
		return nil, errors.New("红果新版播放接口未提供播放权限或暂不可用")
	}
	dataModel := nestedMap(model, "data")
	entry := nestedMap(dataModel, videoID)
	if len(entry) == 0 && method == http.MethodGet {
		entry = nestedMap(nestedMap(dataModel, "video_model_data"), videoID)
	}
	if len(entry) == 0 {
		entry = nestedMap(nestedMap(dataModel, "1004"), videoID)
	}
	if len(entry) == 0 {
		return nil, errors.New("红果新版播放接口未返回所选分集")
	}
	return entry, nil
}
