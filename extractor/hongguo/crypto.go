package hongguo

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

var spadeConstants = []byte{
	0x4d, 0xd4, 0xc2, 0xe6, 0xb8, 0x31, 0x62, 0x09, 0x0e, 0x52, 0xb3, 0xc7, 0xa6, 0x73, 0x3b, 0xa4,
	0x1c, 0xb2, 0x46, 0x2b, 0x82, 0x9a, 0xb5, 0x8a, 0x19, 0x6b, 0x39, 0xdb, 0x57, 0x17, 0x75, 0x24,
	0xf4, 0x9b, 0xaf, 0x7f, 0x08, 0xe8, 0xd6, 0x8d, 0x26, 0xa7, 0x2e, 0x37, 0xc1, 0xa9, 0x5a, 0x2f,
	0x1f, 0x05, 0xa5, 0x18, 0x92, 0xae, 0xf2, 0x94, 0x97, 0x32, 0xb6, 0x2a, 0x38, 0xaa, 0xdd, 0x58,
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if n := len(s) % 4; n != 0 {
		s += strings.Repeat("=", 4-n)
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

func deriveContentKey(spade string) (string, error) {
	raw, err := decodeBase64(spade)
	if err != nil {
		return "", fmt.Errorf("decode spade_a: %w", err)
	}
	if len(raw) < 3 {
		return "", fmt.Errorf("spade_a is too short")
	}
	v8 := len(raw) - int(raw[0]^raw[1]^raw[2]) + 47
	if v8 <= 0 || v8 > len(raw)*2 {
		return "", fmt.Errorf("spade_a computed length %d is invalid", v8)
	}
	if 1+v8 > len(raw) {
		v8 = len(raw) - 1
	}
	if v8 < 33 {
		return "", fmt.Errorf("spade_a payload is too short")
	}
	v13 := append([]byte(nil), raw[1:1+v8]...)
	va, vb := byte(85), byte(246)
	for i := range v13 {
		var previous byte
		if i&1 != 0 {
			previous, va = va, v13[i]
		} else {
			previous, vb = vb, v13[i]
		}
		v13[i] = byte(-21 - int(bitsOnes(i)) + int(previous^v13[i]))
	}
	key, err := hex.DecodeString(string(v13[1:33]))
	if err != nil || len(key) != 16 {
		return "", fmt.Errorf("invalid content key in spade_a")
	}
	return hex.EncodeToString(key), nil
}

func bitsOnes(v int) byte {
	var n byte
	for v != 0 {
		n += byte(v & 1)
		v >>= 1
	}
	return n
}

func decryptSpadeURL(encoded string, seed []byte) (string, error) {
	raw, err := decodeBase64(encoded)
	if err != nil {
		return "", err
	}
	if len(raw) < 5 || raw[0] != 0xa8 || raw[2] != 1 || raw[3] != 0 {
		return "", fmt.Errorf("invalid encrypted URL header")
	}
	ciphertext := raw[4:]
	ciphertext = ciphertext[:len(ciphertext)/aes.BlockSize*aes.BlockSize]
	if len(ciphertext) == 0 {
		return "", fmt.Errorf("empty encrypted URL")
	}
	h1 := sha512.Sum512(seed)
	material := append(append([]byte(nil), h1[:]...), spadeConstants...)
	h2 := sha512.Sum512(material)
	block, err := aes.NewCipher(h2[:16])
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, h2[16:32]).CryptBlocks(plain, ciphertext)
	if pad := int(plain[len(plain)-1]); pad >= 1 && pad <= aes.BlockSize && pad <= len(plain) {
		plain = plain[:len(plain)-pad]
	}
	return strings.TrimRight(string(plain), "\x00"), nil
}
