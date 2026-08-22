package hongguo

// Native signing implementation translated from the locally authorized
// liushen/flurl sources. The application signature is intentionally kept in
// this package so release binaries do not need Python or GUOZI_SRC.

import (
	"crypto/md5"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emmansun/gmsm/sm3"
	"google.golang.org/protobuf/encoding/protowire"
)

const hongguoUserAgent = "com.phoenix.read/71332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"

//go:embed data/branch_one.bin data/aes_sbox.bin
var signingData embed.FS

var branchOneTable []byte
var aesSBox []byte

func init() {
	branchOneTable, _ = signingData.ReadFile("data/branch_one.bin")
	aesSBox, _ = signingData.ReadFile("data/aes_sbox.bin")
}

type signerDevice struct {
	DeviceID, InstallID string
}

type signRandom struct {
	Gorgon uint16
	Helios uint32
	Hash   uint32
	XM     uint32
	Proto  uint32
	Launch uint32
	PID    uint32
}

func randomU32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return binary.LittleEndian.Uint32(b[:])
	}
	return uint32(time.Now().UnixNano())
}

func newSignRandom() signRandom {
	return signRandom{
		Gorgon: uint16(randomU32()), Helios: randomU32(), Proto: randomU32(),
		Hash: randomU32(), XM: randomU32(),
		Launch: 100 + randomU32()%21, PID: 10001 + randomU32()%2000,
	}
}

type orderedParams struct {
	Order  []string
	Values map[string]string
}

func parseOrderedQuery(raw string) orderedParams {
	p := orderedParams{Values: map[string]string{}}
	for _, part := range strings.Split(raw, "&") {
		kv := strings.SplitN(part, "=", 2)
		k, _ := url.QueryUnescape(kv[0])
		v := ""
		if len(kv) == 2 {
			v, _ = url.QueryUnescape(kv[1])
		}
		if _, exists := p.Values[k]; !exists {
			p.Order = append(p.Order, k)
		}
		p.Values[k] = v
	}
	return p
}

func (p *orderedParams) Set(k, v string) {
	if _, exists := p.Values[k]; !exists {
		p.Order = append(p.Order, k)
	}
	p.Values[k] = v
}

func (p orderedParams) Encode() string {
	parts := make([]string, 0, len(p.Order))
	for _, k := range p.Order {
		parts = append(parts, queryEscape(k)+"="+queryEscape(p.Values[k]))
	}
	return strings.Join(parts, "&")
}

func queryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(s), "+", "%20"), "%2A", "*")
}

func signRequest(base string, params orderedParams, body []byte, dev signerDevice, now time.Time, sr signRandom) (string, map[string]string, error) {
	q := orderedParams{Order: append([]string(nil), params.Order...), Values: map[string]string{}}
	for k, v := range params.Values {
		q.Values[k] = v
	}
	q.Set("ts", strconv.FormatInt(now.Unix(), 10))
	q.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
	query := q.Encode()
	signedURL := base + "?" + query
	khronos := uint32(now.Unix())

	hashBody := pythonJSONForHash(body)
	gorgon := encryptGorgon(hashBody, query, khronos, sr.Gorgon)
	helios := encryptHelios(khronos, sr.Helios)
	medusa, err := generateMedusa(signedURL, query, hashBody, dev, khronos, now.Unix(), sr)
	if err != nil {
		return "", nil, err
	}
	bodyMD5 := md5.Sum(body)
	headers := map[string]string{
		"User-Agent":                hongguoUserAgent,
		"Accept":                    "application/json; charset=utf-8,application/x-protobuf",
		"Content-Type":              "application/json; charset=UTF-8",
		"x-xs-from-web":             "0",
		"x-ss-req-ticket":           strconv.FormatInt(now.UnixMilli(), 10),
		"x-tt-request-tag":          "t=0;n=0",
		"sdk-version":               "2",
		"passport-sdk-version":      "50561",
		"x-vc-bdturing-sdk-version": "3.7.2.cn",
		"x-ladon":                   base64.StdEncoding.EncodeToString(uint32BE(khronos)),
		"x-khronos":                 strconv.FormatUint(uint64(khronos), 10),
		"x-argus":                   base64.StdEncoding.EncodeToString(uint32LE(khronos)),
		"x-gorgon":                  gorgon,
		"x-helios":                  helios,
		"x-medusa":                  medusa,
		"x-ss-stub":                 strings.ToUpper(hex.EncodeToString(bodyMD5[:])),
	}
	return signedURL, headers, nil
}

func encryptGorgon(body []byte, query string, khronos uint32, xgRand uint16) string {
	qmd5 := md5.Sum([]byte(query))
	data := append([]byte(nil), qmd5[:4]...)
	if len(body) != 0 {
		// Hash the logical JSON representation. For the fqnovel request this is
		// compact and equal to the wire bytes, but keeping this conversion here
		// mirrors the Python dictionary-signing boundary.
		bmd5 := md5.Sum(pythonJSONForHash(body))
		data = append(data, bmd5[:4]...)
	} else {
		data = append(data, 0, 0, 0, 0)
	}
	data = append(data, make([]byte, 4)...)
	data = append(data, uint32LE(67503104)...)
	data = append(data, uint32BE(khronos)...)
	key := []byte{0x4a, 0x40, 0x16, byte(xgRand >> 8), 0x47, 0x6c, 0x01, byte(xgRand)}
	out := rc4Gorgon(data, key)
	for i := range out {
		a := out[i]
		out[i] = a>>4 | a<<4
		a = out[0]
		if i+1 < len(out) {
			a = out[i+1]
		}
		a ^= out[i]
		out[i] = ^(bits.Reverse8(a) ^ 20)
	}
	ret := []byte{0x84, 0x04}
	ret = append(ret, byte(xgRand), byte(xgRand>>8), 0x40, 0x01)
	ret = append(ret, out...)
	return hex.EncodeToString(ret)
}

func pythonJSONForHash(body []byte) []byte {
	// The reference receives the decoded dictionary and serializes it according
	// to Content-Type. For this endpoint that yields the same compact JSON bytes
	// used as the POST body. Keep the helper explicit because Gorgon/Medusa hash
	// the logical JSON while x-ss-stub hashes the transmitted body.
	var obj any
	if json.Unmarshal(body, &obj) != nil {
		return body
	}
	if root, ok := obj.(map[string]any); ok {
		if biz, ok := root["biz_param"].(map[string]any); ok {
			keys := []string{"detail_page_version", "device_level", "disable_digg_stat", "need_all_video_definition", "need_mp4_align", "use_os_player", "use_server_dns", "video_platform"}
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				if v, exists := biz[k]; exists {
					kb, _ := json.Marshal(k)
					parts = append(parts, string(kb)+":"+pythonJSONValue(v))
				}
			}
			mixed := pythonJSONValue(root["mixed_video_id_map"])
			return []byte(`{"biz_param":` + "{" + strings.Join(parts, ",") + `},"mixed_video_id_map":` + mixed + `}`)
		}
	}
	return []byte(pythonJSONValue(obj))
}
func pythonJSONValue(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		// Go loses source insertion order after unmarshal. The request object's
		// field order is fixed and supplied explicitly by vectorBody/modelBody;
		// sorting is only a fallback for unknown maps.
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			kb, _ := json.Marshal(k)
			parts = append(parts, string(kb)+":"+pythonJSONValue(x[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = pythonJSONValue(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case string:
		b, _ := json.Marshal(x)
		return string(b)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func rc4Gorgon(data, key []byte) []byte {
	s := make([]byte, 256)
	for i := range s {
		s[i] = byte(i)
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + int(s[i]) + int(key[i%len(key)])) & 255
		s[i] = s[j]
	}
	i, jj := 0, 0
	out := make([]byte, len(data))
	for k, v := range data {
		i++
		x := s[i]
		jj += int(x)
		y := s[jj&255]
		s[i] = y
		out[k] = v ^ s[(int(y)+int(y))&255]
	}
	return out
}

func encryptHelios(khronos, r uint32) string {
	data := append(uint32LE(r), []byte("8662")...)
	h := md5.Sum(data)
	hexTable := []byte("0123456789abcdef")
	keysRaw := make([]byte, 32)
	for i, b := range h {
		keysRaw[2*i], keysRaw[2*i+1] = hexTable[b>>4], hexTable[b&15]
	}
	keys := make([]uint64, 4)
	for i := range keys {
		keys[i] = binary.LittleEndian.Uint64(keysRaw[i*8:])
	}
	hashTable := []uint64{keys[0]}
	b0, b8 := keys[0], keys[1]
	queue := append([]uint64(nil), keys[2:]...)
	for i := 0; i < 0x22; i++ {
		x := bits.RotateLeft64(b8, -8) + b0
		x ^= uint64(i)
		queue = append(queue, x)
		x ^= bits.RotateLeft64(b0, -61)
		hashTable = append(hashTable, x)
		b0, b8, queue = x, queue[0], queue[1:]
	}
	in := pkcs7([]byte(fmt.Sprintf("%d-1588093228-8662", khronos)), 16)
	out := append([]byte(nil), uint32LE(r)...)
	for i := 0; i < len(in); i += 16 {
		d0 := binary.LittleEndian.Uint64(in[i:])
		d1 := binary.LittleEndian.Uint64(in[i+8:])
		for j := 0; j < 0x22; j++ {
			d1 = hashTable[j] ^ (d0 + bits.RotateLeft64(d1, -8))
			d0 = d1 ^ bits.RotateLeft64(d0, -61)
		}
		var b [16]byte
		binary.LittleEndian.PutUint64(b[:8], d0)
		binary.LittleEndian.PutUint64(b[8:], d1)
		out = append(out, b[:]...)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// Tiny protobuf encoder. All reference messages use scalar, string, bytes and
// nested-message fields; protowire avoids generated code while producing the
// exact wire format expected by the signing algorithm.
type protoBuilder []byte

func (p protoBuilder) sint32(n protowire.Number, v int32) protoBuilder {
	p = protowire.AppendTag(p, n, protowire.VarintType)
	return protowire.AppendVarint(p, protowire.EncodeZigZag(int64(v)))
}
func (p protoBuilder) sint64(n protowire.Number, v int64) protoBuilder {
	p = protowire.AppendTag(p, n, protowire.VarintType)
	return protowire.AppendVarint(p, protowire.EncodeZigZag(v))
}
func (p protoBuilder) str(n protowire.Number, v string) protoBuilder {
	if v == "" {
		return p
	}
	p = protowire.AppendTag(p, n, protowire.BytesType)
	return protowire.AppendString(p, v)
}
func (p protoBuilder) bytes(n protowire.Number, v []byte) protoBuilder {
	if len(v) == 0 {
		return p
	}
	p = protowire.AppendTag(p, n, protowire.BytesType)
	return protowire.AppendBytes(p, v)
}
func (p protoBuilder) msg(n protowire.Number, v []byte) protoBuilder { return p.bytes(n, v) }
func (p protoBuilder) f32(n protowire.Number, v float32) protoBuilder {
	if v == 0 {
		return p
	}
	p = protowire.AppendTag(p, n, protowire.Fixed32Type)
	return protowire.AppendFixed32(p, mathFloat32bits(v))
}

func generateMedusa(signedURL, query string, body []byte, dev signerDevice, khronos uint32, reportTime int64, sr signRandom) (string, error) {
	qsm3 := sm3.Sum([]byte(strings.SplitN(signedURL, "?", 2)[1]))
	var bodyMD5 [16]byte
	if len(body) > 0 {
		bodyMD5 = md5.Sum(body)
	}
	ts := uint32LE(khronos)
	qbody := hashF13(qsm3[:], bodyMD5[:], ts, khronos)
	qbHash := sm3.Sum(append(append(append([]byte(nil), []byte(query)...), bodyMD5[:]...), []byte("none")...))

	device := protoBuilder{}.
		sint32(1, 1).sint32(2, 2).str(3, "8662").str(4, dev.DeviceID).
		str(5, "Ai6svO3PyrwDOUSmO6ZcResxu").str(6, "!noperm!").
		sint32(7, -888888).sint32(8, -888888).sint32(9, 3).sint32(10, -888888).
		str(11, "!notset!").str(12, "Asia/Shanghai,8").str(13, "zh_CN").sint32(14, 4).
		f32(16, 255.24993896484375).f32(17, 35.58599090576172).f32(18, 3.467449188232422).
		f32(19, 3.467449188232422).f32(20, 255.1754913330078).f32(21, 42.17544174194336).
		str(22, "16").sint32(23, 41).sint32(24, 36).
		sint64(25, 1728388016635).sint64(26, 1728388016635).sint64(27, 1728388016635).sint64(28, 1728388016637).
		sint32(29, -1).str(30, "25053RT47C").str(31, "Redmi").str(32, "25053RT47C").
		str(33, "25053RT47C").str(34, "Xiaomi").str(35, "Redmi").str(36, "Redmi").sint32(38, 31)
	report := protoBuilder{}.sint64(1, reportTime).sint32(2, -2).sint32(4, 200)
	env := protoBuilder{}.sint32(1, int32(sr.Launch)).sint32(2, 146331399).sint32(3, 146331396).
		sint32(5, 7).str(6, "v04.06.04.03-bugfix").sint32(7, int32(sr.PID)).msg(12, device).msg(13, report).str(14, "7.1.3.32")
	req := protoBuilder{}.sint32(1, 111).sint32(2, 10).sint32(3, 694367).sint32(5, 586952199)
	proto := protoBuilder{}.bytes(1, []byte{0xf7, 0xe8, 0x5f, 0xfa, 0xd7, 0xd7, 0xdc, 0x3b, 0xd6, 0x2a, 0xc8, 0x70, 0x57, 0xcf, 0x61, 0x18}).
		sint32(2, 3).sint32(3, int32(sr.Proto)).str(4, "8662").str(5, dev.DeviceID).str(6, "1588093228").
		str(7, "7.1.3.32").str(8, "v04.06.04-ml-android").sint32(9, 67503104).bytes(10, uint64LE(320)).
		sint32(12, int32(khronos)).bytes(13, qbody).bytes(14, qsm3[:6]).msg(15, req).
		str(16, "AXYQOS6n2m60x1fVZHIrH3iol").sint32(17, int32(khronos)).bytes(19, qbHash[:]).str(20, "none").
		sint32(21, 312).msg(23, env).str(24, `{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`)

	key, seed := medusaKeyHash([]byte{0x8e, 0xbd, 0xfa, 0x38, 0x06, 0xec, 0xc5, 0xce, 0xe7, 0x94, 0x23, 0xe6, 0x02, 0x9e, 0xd8, 0x25, 0x40, 0xbc, 0x22, 0x18, 0xbb, 0x7e, 0xae, 0xf7, 0x1c, 0xb6, 0x91, 0xf7, 0xaa, 0x8a, 0xa2, 0xf5}, sr.Hash)
	data := medusaXMXor(proto, key)
	data = append(uint64LE(320), data...)
	reverseBytes(data)
	for i := range data {
		data[i] ^= seed[^i&3]
	}
	check := uint32(0x18000001) | uint32(qsm3[0]&63)<<14 | uint32(qbody[0]&63)<<8
	payload := append([]byte{0x35}, uint32LE(sr.XM)...)
	payload = append(payload, uint32LE(check)...)
	payload = append(payload, data...)
	payload = append(payload, uint32LE(sr.Hash)[2:]...)
	aesOut := newAESV3([]byte{0xf1, 0x59, 0x33, 0x76, 0x76, 0x6e, 0xa9, 0x8d, 0x34, 0xf3, 0x1b, 0x05, 0x7a, 0x9d, 0x5b, 0xe4}, khronos).encrypt(payload, []byte{0x1f, 0xe1, 0x09, 0xa4, 0x12, 0x52, 0x83, 0xf4, 0x18, 0xde, 0x9e, 0x05, 0x1a, 0x96, 0x9e, 0x12})
	version := []byte{0x03, 0, 0, 0, 0xf7, 0xe8, 0x5f, 0xfa, 0xd7, 0xd7, 0xdc, 0x3b, 0xd6, 0x2a, 0xc8, 0x70, 0x57, 0xcf, 0x61, 0x18}
	for i := 0; i < len(version); i += 4 {
		binary.LittleEndian.PutUint32(version[i:], binary.LittleEndian.Uint32(version[i:])^khronos)
	}
	out := append(version, uint32LE(sr.Hash)[:2]...)
	out = append(out, 0, 1)
	out = append(out, aesOut...)
	return base64.StdEncoding.EncodeToString(out), nil
}

func medusaKeyHash(key []byte, r uint32) ([]byte, []byte) {
	b := make([]byte, 68)
	copy(b, key)
	binary.LittleEndian.PutUint32(b[32:], r)
	copy(b[36:], key)
	h := sm3.Sum(b)
	d1 := (r >> 16) & 255
	d2 := (d1 << 11) | (r >> 24)
	d2 ^= (d1 >> 5) ^ d1
	d2 = ^d2
	return h[:], uint32LE(d2)
}
func medusaXMXor(data, key []byte) []byte {
	o := make([]byte, len(data))
	for i, v := range data {
		idx := (i * 4) & 28
		d0, d1 := key[idx], key[idx+1]
		d2 := byte(bits.RotateLeft8(v, 4)+d0) ^ d1
		d2 = ^d2
		d2 = bits.RotateLeft8(d2, 3)
		d2 += d1
		d2 ^= d0
		o[len(data)-1-i] = ^d2
	}
	last := o[len(o)-1] ^ o[len(o)-2]
	d0 := o[0]
	o[0] = byte(int(o[0]) + int(^last))
	o[1] += o[0] ^ o[len(o)-1] ^ 254
	o[2] += byte(int(last)-int(d0)) ^ bits.RotateLeft8(o[1], 3) ^ 2
	for i := 0; i < len(o)-4; i++ {
		temp := bits.RotateLeft8(o[i+2], 3) ^ o[i+1] ^ byte(i+3)
		o[i+3] += ^temp
	}
	o[len(o)-1] ^= o[len(o)-2]
	sum := 0
	for i := 0; i < len(o)-1; i++ {
		sum += int(o[i+1])
	}
	o[0] = (o[0] ^ o[1]) + byte(sum)
	return o
}

func pkcs7(b []byte, n int) []byte { p := n - len(b)%n; return append(b, bytesRepeat(byte(p), p)...) }
func bytesRepeat(b byte, n int) []byte {
	o := make([]byte, n)
	for i := range o {
		o[i] = b
	}
	return o
}
func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}
func uint32LE(v uint32) []byte         { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func uint32BE(v uint32) []byte         { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func uint64LE(v uint64) []byte         { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }
func mathFloat32bits(f float32) uint32 { return math.Float32bits(f) }
