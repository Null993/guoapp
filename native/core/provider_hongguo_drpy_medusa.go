package core

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"math"
	"math/bits"
	"sync"
	"time"
)

var (
	hgDrpyMedusaSignKey = []byte{
		0x8e, 0xbd, 0xfa, 0x38, 0x06, 0xec, 0xc5, 0xce, 0xe7, 0x94, 0x23, 0xe6, 0x02, 0x9e, 0xd8, 0x25,
		0x40, 0xbc, 0x22, 0x18, 0xbb, 0x7e, 0xae, 0x71, 0xcb, 0x69, 0x1f, 0x7a, 0xa8, 0xaa, 0x2f, 0x35,
	}
	hgDrpyMedusaAesKey = []byte{0xf1, 0x59, 0x33, 0x76, 0x76, 0x6e, 0xa9, 0x8d, 0x34, 0xf3, 0x1b, 0x05, 0x7a, 0x9d, 0x5b, 0xe4}
	hgDrpyMedusaAesIV  = []byte{0x1f, 0xe1, 0x09, 0xa4, 0x12, 0x52, 0x83, 0xf4, 0x18, 0xde, 0x9e, 0x05, 0x1a, 0x96, 0x9e, 0x12}
	hgDrpyMedusaMagic  = []byte{0x40, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	hgDrpyMedusaVer    = []byte{0x03, 0x00, 0x00, 0x00, 0xf7, 0xe8, 0x5f, 0xfa, 0xd7, 0xd7, 0xdc, 0x3b, 0xd6, 0x2a, 0xc8, 0x70, 0x57, 0xcf, 0x61, 0x18}
)

var (
	hgDrpyProcessOnce    sync.Once
	hgDrpyProcessNonce   string
	hgDrpyProcessStarted int64
)

func hgDrpyLoadProcess() (string, int64) {
	hgDrpyProcessOnce.Do(func() {
		nonce := make([]byte, 18)
		if _, err := rand.Read(nonce); err != nil {
			nonce = []byte("drpy-process-nonce!")
		}
		hgDrpyProcessNonce = base64.RawURLEncoding.EncodeToString(nonce)
		hgDrpyProcessStarted = time.Now().UnixMilli()
	})
	return hgDrpyProcessNonce, hgDrpyProcessStarted
}

type hgDrpyProtoField struct {
	Tag   int
	Value any
	Type  string
}

func hgDrpyVarintAppend(out []byte, value uint64) []byte {
	for value > 127 {
		out = append(out, byte((value&127)|128))
		value >>= 7
	}
	return append(out, byte(value))
}

func hgDrpyZigzag(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

func hgDrpyProto(fields []hgDrpyProtoField) []byte {
	var out []byte
	for _, field := range fields {
		value := field.Value
		switch field.Type {
		case "string":
			text, _ := value.(string)
			if text == "" {
				continue
			}
			body := []byte(text)
			out = hgDrpyVarintAppend(out, uint64(field.Tag)<<3|2)
			out = hgDrpyVarintAppend(out, uint64(len(body)))
			out = append(out, body...)
		case "bytes", "message":
			body, _ := value.([]byte)
			if body == nil {
				continue
			}
			out = hgDrpyVarintAppend(out, uint64(field.Tag)<<3|2)
			out = hgDrpyVarintAppend(out, uint64(len(body)))
			out = append(out, body...)
		case "float":
			number, _ := value.(float64)
			body := make([]byte, 4)
			binary.LittleEndian.PutUint32(body, math.Float32bits(float32(number)))
			out = hgDrpyVarintAppend(out, uint64(field.Tag)<<3|5)
			out = append(out, body...)
		case "sint":
			number, _ := value.(int64)
			out = hgDrpyVarintAppend(out, uint64(field.Tag)<<3)
			out = hgDrpyVarintAppend(out, hgDrpyZigzag(number))
		default:
			number, _ := value.(int64)
			out = hgDrpyVarintAppend(out, uint64(field.Tag)<<3)
			out = hgDrpyVarintAppend(out, uint64(number))
		}
	}
	return out
}

func hgDrpyDeviceProto(deviceID, processNonce string, processStarted int64) []byte {
	return hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: int64(1), Type: "sint"},
		{Tag: 2, Value: int64(2), Type: "sint"},
		{Tag: 3, Value: "8662", Type: "string"},
		{Tag: 4, Value: deviceID, Type: "string"},
		{Tag: 5, Value: processNonce, Type: "string"},
		{Tag: 6, Value: "!noperm!", Type: "string"},
		{Tag: 7, Value: int64(-888888), Type: "sint"},
		{Tag: 8, Value: int64(-888888), Type: "sint"},
		{Tag: 9, Value: int64(3), Type: "sint"},
		{Tag: 10, Value: int64(-888888), Type: "sint"},
		{Tag: 11, Value: "!notset!", Type: "string"},
		{Tag: 12, Value: "Asia/Shanghai,8", Type: "string"},
		{Tag: 13, Value: "zh_CN", Type: "string"},
		{Tag: 14, Value: int64(4), Type: "sint"},
		{Tag: 16, Value: float64(0), Type: "float"},
		{Tag: 17, Value: float64(0), Type: "float"},
		{Tag: 18, Value: float64(0), Type: "float"},
		{Tag: 19, Value: float64(0), Type: "float"},
		{Tag: 20, Value: float64(0), Type: "float"},
		{Tag: 21, Value: float64(0), Type: "float"},
		{Tag: 22, Value: "16", Type: "string"},
		{Tag: 23, Value: int64(41), Type: "sint"},
		{Tag: 24, Value: int64(36), Type: "sint"},
		{Tag: 25, Value: processStarted, Type: "sint"},
		{Tag: 26, Value: processStarted, Type: "sint"},
		{Tag: 27, Value: processStarted, Type: "sint"},
		{Tag: 28, Value: processStarted, Type: "sint"},
		{Tag: 29, Value: int64(-1), Type: "sint"},
		{Tag: 30, Value: "25053RT47C", Type: "string"},
		{Tag: 31, Value: "Redmi", Type: "string"},
		{Tag: 32, Value: "25053RT47C", Type: "string"},
		{Tag: 33, Value: "25053RT47C", Type: "string"},
		{Tag: 34, Value: "Xiaomi", Type: "string"},
		{Tag: 35, Value: "Redmi", Type: "string"},
		{Tag: 36, Value: "Redmi", Type: "string"},
		{Tag: 38, Value: int64(31), Type: "sint"},
	})
}

func hgDrpyXmxor(data, key []byte) []byte {
	encoded := make([]byte, len(data))
	length := len(data)
	for index := 0; index < length; index++ {
		at := (index * 4) & 28
		d0 := key[at]
		d1 := key[at+1]
		d2 := uint32(bits.RotateLeft8(data[index], 4)) + uint32(d0)
		d2 = ^d2 ^ uint32(d1)
		d2 = uint32(bits.RotateLeft8(byte(d2), 3))
		d2 = (d2 + uint32(d1)) & 255
		d2 = (d2 ^ uint32(d0)) & 255
		encoded[length-index-1] = ^byte(d2)
	}
	if length < 2 {
		return encoded
	}
	last := encoded[length-1] ^ encoded[length-2]
	first := encoded[0]
	encoded[0] = ^last + first
	encoded[1] = (encoded[0] ^ encoded[length-1] ^ 254) + encoded[1]
	rotated := bits.RotateLeft8(encoded[1], 3)
	encoded[2] = encoded[2] + ((last - first) ^ rotated ^ 2)
	for index := 0; index < length-4; index++ {
		rotated := bits.RotateLeft8(encoded[index+2], 3)
		temp := rotated ^ encoded[index+1] ^ byte(index+3)
		encoded[index+3] = ^temp + encoded[index+3]
	}
	encoded[length-1] ^= encoded[length-2]
	sum := byte(0)
	for index := 0; index < length-1; index++ {
		sum += encoded[index+1]
	}
	encoded[0] = (encoded[0] ^ encoded[1]) + sum
	return encoded
}

var hgDrpyMedusaBuildSignKey = []byte{
	0x8e, 0xbd, 0xfa, 0x38, 0x06, 0xec, 0xc5, 0xce, 0xe7, 0x94, 0x23, 0xe6, 0x02, 0x9e, 0xd8, 0x25,
	0x40, 0xbc, 0x22, 0x18, 0xbb, 0x7e, 0xae, 0xf7, 0x1c, 0xb6, 0x91, 0xf7, 0xaa, 0x8a, 0xa2, 0xf5,
}

func hgDrpyKeyHash(random uint32) ([]byte, []byte) {
	return hgDrpyKeyHashWith(hgDrpyMedusaSignKey, random)
}

func hgDrpyKeyHashWith(signKey []byte, random uint32) ([]byte, []byte) {
	input := make([]byte, 0, len(signKey)*2+4)
	input = append(input, signKey...)
	stamp := hgDrpyLe32(random)
	input = append(input, stamp[:]...)
	input = append(input, signKey...)
	digest := hgDrpySM3(input)
	d1 := (random >> 16) & 255
	d2 := ((d1 << 11) | (random >> 24)) ^ (d1 >> 5) ^ d1
	d2 = ^d2
	seed := hgDrpyLe32(d2)
	return digest[:], seed[:]
}

type hgDrpyAesV3 struct {
	wordSize int
	box      []byte
	con2     [4]int
	order    [16]int
	keys     [12][4]byte
}

func hgDrpyNewAesV3(key []byte, khronos uint32) *hgDrpyAesV3 {
	wordSize := int(khronos & 3)
	sbox := hgDrpyLoadSBox()
	box := sbox[wordSize*256 : (wordSize+1)*256]
	conTables := [4][4]int{
		{1, 0, 2, 3},
		{2, 0, 3, 1},
		{0, 1, 3, 2},
		{1, 0, 2, 3},
	}
	orderTables := [4][16]int{
		{0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3},
		{0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11},
		{0, 9, 14, 7, 4, 13, 2, 11, 8, 1, 6, 3, 12, 5, 10, 15},
		{0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3},
	}
	initValues := [4]uint32{0xca025ddc, 0x823dc546, 0xc9420583, 0xc298225f}
	init := initValues[wordSize]
	var initial [16]byte
	for index := 0; index < 4; index++ {
		binary.LittleEndian.PutUint32(initial[index*4:], init)
	}
	mk := make([]byte, 16)
	for index := 0; index < 16; index++ {
		mk[index] = initial[index] ^ key[index]
	}
	expanded := make([]byte, 48)
	copy(expanded, mk)
	rounds := 8
	for index := 4; index < 12; index++ {
		at := 4 * (index - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if index%4 == 0 {
			t := byte((init >> (rounds & 24)) ^ uint32(box[b]))
			b = box[c]
			c = box[d]
			d = box[a]
			a = t
		}
		rounds += 2
		expanded[at+4] = a ^ expanded[at-12]
		expanded[at+5] = b ^ expanded[at-11]
		expanded[at+6] = c ^ expanded[at-10]
		expanded[at+7] = d ^ expanded[at-9]
	}
	cipher := &hgDrpyAesV3{wordSize: wordSize, box: box, con2: conTables[wordSize]}
	copy(cipher.order[:], orderTables[wordSize][:])
	for index := 0; index < 12; index++ {
		copy(cipher.keys[index][:], expanded[index*4:index*4+4])
	}
	return cipher
}

func (cipher *hgDrpyAesV3) matrix(value []byte) [4][4]byte {
	var rows [4][4]byte
	for index := 0; index < 4; index++ {
		copy(rows[index][:], value[index*4:index*4+4])
	}
	return rows
}

func (cipher *hgDrpyAesV3) flat(rows [4][4]byte) []byte {
	out := make([]byte, 16)
	for index := 0; index < 4; index++ {
		copy(out[index*4:], rows[index][:])
	}
	return out
}

func (cipher *hgDrpyAesV3) add(rows *[4][4]byte, keys [4][4]byte) {
	for index := 0; index < 4; index++ {
		for position := 0; position < 4; position++ {
			rows[index][position] ^= keys[index][position]
		}
	}
}

func (cipher *hgDrpyAesV3) addCon(rows *[4][4]byte, keys [4][4]byte) {
	for index := 0; index < 4; index++ {
		for position := 0; position < 4; position++ {
			rows[index][position] ^= keys[index][cipher.con2[position]]
		}
	}
}

func (cipher *hgDrpyAesV3) sub(rows *[4][4]byte) {
	for index := 0; index < 4; index++ {
		for position := 0; position < 4; position++ {
			rows[index][position] = cipher.box[rows[index][position]]
		}
	}
	old := *rows
	for index := 0; index < 4; index++ {
		rows[index] = old[cipher.con2[index]]
	}
}

func (cipher *hgDrpyAesV3) shift(rows *[4][4]byte) {
	flat := cipher.flat(*rows)
	for index := 0; index < 16; index++ {
		rows[index/4][index%4] = flat[cipher.order[index]]
	}
}

func (cipher *hgDrpyAesV3) shiftCon(rows *[4][4]byte) {
	old := *rows
	for index := 0; index < 4; index++ {
		for position := 0; position < 4; position++ {
			rows[index][position] = old[index][cipher.con2[position]]
		}
	}
}

func hgDrpyXt(value byte) byte {
	return ((value << 1) ^ ((value&128)>>7)*0x1b) & 255
}

func hgDrpyAesMix(rows *[4][4]byte) {
	for index := 0; index < 4; index++ {
		t := rows[0][index] ^ rows[1][index] ^ rows[2][index] ^ rows[3][index]
		u := rows[0][index]
		rows[0][index] ^= t ^ hgDrpyXt(rows[0][index]^rows[1][index])
		rows[1][index] ^= t ^ hgDrpyXt(rows[1][index]^rows[2][index])
		rows[2][index] ^= t ^ hgDrpyXt(rows[2][index]^rows[3][index])
		rows[3][index] ^= t ^ hgDrpyXt(rows[3][index]^u)
	}
}

func (cipher *hgDrpyAesV3) block(value []byte) []byte {
	rows := cipher.matrix(value)
	var whitening [4][4]byte
	copy(whitening[:], cipher.keys[0:4])
	cipher.addCon(&rows, whitening)
	for round := 1; round < 3; round++ {
		cipher.sub(&rows)
		cipher.shift(&rows)
		if round == 1 {
			cipher.shiftCon(&rows)
			hgDrpyAesMix(&rows)
		}
		var roundKeys [4][4]byte
		copy(roundKeys[:], cipher.keys[round*4:round*4+4])
		cipher.addCon(&rows, roundKeys)
	}
	var finalKeys [4][4]byte
	copy(finalKeys[:], cipher.keys[4:8])
	cipher.add(&rows, finalKeys)
	return cipher.flat(rows)
}

func (cipher *hgDrpyAesV3) encrypt(data, iv []byte) []byte {
	plaintext := make([]byte, 32)
	plaintext[31] = 1
	for index := 0; index < 31; index++ {
		at := index * 8
		var source [8]byte
		for position := 0; position < 8; position++ {
			if at+position < len(data) {
				source[position] = data[at+position]
			}
		}
		n0 := (source[0] >> 4) & 2
		n1 := n0 | (source[1] & 64)
		n2 := n1 | ((source[2] >> 2) & 1)
		n3 := n2 | ((source[3] << 3) & 128)
		n4 := n3 | ((source[4] >> 1) & 4)
		n5 := n4 | ((source[5] << 3) & 16)
		n6 := n5 | ((source[6] << 5) & 32)
		plaintext[index] = n6 | ((source[7] >> 4) & 8)
	}
	blocks := make([]byte, 0, 32)
	previous := make([]byte, 16)
	copy(previous, iv)
	for at := 0; at < len(plaintext); at += 16 {
		xored := make([]byte, 16)
		for index := 0; index < 16; index++ {
			xored[index] = plaintext[at+index] ^ previous[index]
		}
		value := cipher.block(xored)
		blocks = append(blocks, value...)
		previous = value
	}
	key := blocks
	out := make([]byte, len(data))
	copy(out, data)
	for index := 0; index < 31; index++ {
		at := index * 8
		k := key[index]
		if at < len(out) {
			out[at] &= 0xdf
			out[at] |= (k << 4) & 32
		}
		if at+1 < len(out) {
			out[at+1] &= 0xbf
			out[at+1] |= k & 64
		}
		if at+2 < len(out) {
			out[at+2] &= 0xfb
			out[at+2] |= (k << 2) & 4
		}
		if at+3 < len(out) {
			out[at+3] &= 0xef
			out[at+3] |= (k >> 3) & 16
		}
		if at+4 < len(out) {
			out[at+4] &= 0xf7
			out[at+4] |= (k + k) & 8
		}
		if at+5 < len(out) {
			out[at+5] &= 0xfd
			out[at+5] |= (k >> 3) & 2
		}
		if at+6 < len(out) {
			out[at+6] &= 0xfe
			out[at+6] |= (k >> 5) & 1
		}
		if at+7 < len(out) {
			out[at+7] &= 0x7f
			out[at+7] |= (k << 4) & 128
		}
	}
	result := make([]byte, 0, 1+len(out))
	result = append(result, key[len(key)-1])
	result = append(result, out...)
	return result
}

type hgDrpyMedusaRand struct {
	MessageRand uint32
	EnvLaunch   uint32
	EnvPid      uint32
	KeyHashRand uint32
	XmRand      uint32
}

func hgDrpyBuildMedusa(url string, body []byte, khronos uint32, deviceID, versionName, processNonce string, nowSec, processStartedMs int64, rands *hgDrpyMedusaRand) []byte {
	if rands == nil {
		rands = &hgDrpyMedusaRand{
			MessageRand: hgDrpyRandomU32(),
			EnvLaunch:   100 + hgDrpyRandomU32()%21,
			EnvPid:      10001 + hgDrpyRandomU32()%2000,
			KeyHashRand: hgDrpyRandomU32(),
			XmRand:      hgDrpyRandomU32(),
		}
	}
	query := ""
	if index := indexByte(url, '?'); index >= 0 && index < len(url)-1 {
		query = url[index+1:]
	}
	bodyMd5 := md5.Sum(body)
	querySm3 := hgDrpySM3([]byte(query))
	ts := hgDrpyLe32(khronos)
	queryBodyTs := hgDrpyHashF13(querySm3[:], bodyMd5[:], ts[:], khronos)
	nested := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: int64(111), Type: "sint"},
		{Tag: 2, Value: int64(10), Type: "sint"},
		{Tag: 3, Value: int64(694367), Type: "sint"},
		{Tag: 5, Value: int64(586952199), Type: "sint"},
	})
	messageRand := rands.MessageRand
	envLaunch := rands.EnvLaunch
	envPid := rands.EnvPid
	env := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: int64(envLaunch), Type: "sint"},
		{Tag: 2, Value: int64(146331399), Type: "sint"},
		{Tag: 3, Value: int64(146331396), Type: "sint"},
		{Tag: 5, Value: int64(7), Type: "sint"},
		{Tag: 6, Value: "v04.06.04.03-bugfix", Type: "string"},
		{Tag: 7, Value: int64(envPid), Type: "sint"},
		{Tag: 12, Value: hgDrpyDeviceProto(deviceID, processNonce, processStartedMs), Type: "message"},
		{Tag: 13, Value: hgDrpyProto([]hgDrpyProtoField{
			{Tag: 1, Value: nowSec, Type: "sint"},
			{Tag: 2, Value: int64(-2), Type: "sint"},
			{Tag: 4, Value: int64(200), Type: "sint"},
		}), Type: "message"},
		{Tag: 14, Value: versionName, Type: "string"},
	})
	queryMaterial := make([]byte, 0, len(query)+len(bodyMd5)+4)
	queryMaterial = append(queryMaterial, []byte(query)...)
	queryMaterial = append(queryMaterial, bodyMd5[:]...)
	queryMaterial = append(queryMaterial, []byte("none")...)
	queryHash := hgDrpySM3(queryMaterial)
	message := hgDrpyProto([]hgDrpyProtoField{
		{Tag: 1, Value: []byte{0xf7, 0xe8, 0x5f, 0xfa, 0xd7, 0xd7, 0xdc, 0x3b, 0xd6, 0x2a, 0xc8, 0x70, 0x57, 0xcf, 0x61, 0x18}, Type: "bytes"},
		{Tag: 2, Value: int64(3), Type: "sint"},
		{Tag: 3, Value: int64(messageRand), Type: "sint"},
		{Tag: 4, Value: "8662", Type: "string"},
		{Tag: 5, Value: deviceID, Type: "string"},
		{Tag: 6, Value: "1588093228", Type: "string"},
		{Tag: 7, Value: versionName, Type: "string"},
		{Tag: 8, Value: "v04.06.04-ml-android", Type: "string"},
		{Tag: 9, Value: int64(67503104), Type: "sint"},
		{Tag: 10, Value: hgDrpyMedusaMagic, Type: "bytes"},
		{Tag: 12, Value: int64(khronos), Type: "sint"},
		{Tag: 13, Value: queryBodyTs, Type: "bytes"},
		{Tag: 14, Value: querySm3[:6], Type: "bytes"},
		{Tag: 15, Value: nested, Type: "message"},
		{Tag: 16, Value: processNonce, Type: "string"},
		{Tag: 17, Value: int64(khronos), Type: "sint"},
		{Tag: 19, Value: queryHash[:], Type: "bytes"},
		{Tag: 20, Value: "none", Type: "string"},
		{Tag: 21, Value: int64(312), Type: "sint"},
		{Tag: 23, Value: env, Type: "message"},
		{Tag: 24, Value: `{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`, Type: "string"},
	})
	random := rands.KeyHashRand
	hash, seed := hgDrpyKeyHashWith(hgDrpyMedusaBuildSignKey, random)
	transformed := hgDrpyXmxor(message, hash)
	reversed := make([]byte, 0, 8+len(transformed))
	reversed = append(reversed, hgDrpyMedusaMagic...)
	reversed = append(reversed, transformed...)
	hgDrpyReverseInPlace(reversed)
	for index := range reversed {
		reversed[index] ^= seed[(^index)&3]
	}
	check := ((uint32(querySm3[0])&63)<<14 | 0x18000001 | ((uint32(queryBodyTs[0]) & 63) << 8))
	xmRand := rands.XmRand
	checkStamp := hgDrpyLe32(check)
	xmStamp := hgDrpyLe32(xmRand)
	packed := make([]byte, 0, 1+4+4+len(reversed)+2)
	packed = append(packed, 0x35)
	packed = append(packed, xmStamp[:]...)
	packed = append(packed, checkStamp[:]...)
	packed = append(packed, reversed...)
	packed = append(packed, byte(random>>16), byte(random>>24))
	encrypted := hgDrpyNewAesV3(hgDrpyMedusaAesKey, khronos).encrypt(packed, hgDrpyMedusaAesIV)
	prefix := make([]byte, 20)
	for index := 0; index < 5; index++ {
		word := binary.LittleEndian.Uint32(hgDrpyMedusaVer[index*4:]) ^ khronos
		binary.LittleEndian.PutUint32(prefix[index*4:], word)
	}
	result := make([]byte, 0, 20+4+len(encrypted))
	result = append(result, prefix...)
	result = append(result, byte(random&255), byte((random>>8)&255), 0, 1)
	result = append(result, encrypted...)
	return result
}

func hgDrpyRandomU32() uint32 {
	var buffer [4]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint32(buffer[:])
}

func indexByte(value string, target byte) int {
	for index := 0; index < len(value); index++ {
		if value[index] == target {
			return index
		}
	}
	return -1
}

func hgDrpyReverseInPlace(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}
