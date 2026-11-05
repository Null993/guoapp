package core

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"math/bits"
	"strconv"
)

func hgDrpyHelios(khronos uint32, random uint32) string {
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
	text := []byte(strconv.FormatUint(uint64(khronos), 10) + "-1588093228-8662")
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
