package hongguo

import (
	"encoding/binary"
	"math/bits"
)

func hashF13(query, body, ts []byte, k uint32) []byte {
	iv := hashIV(0x20230928, query)
	iv = hashIV(iv, body)
	iv = hashIV(iv, ts)
	iv0 := ((iv & 15) * 171) >> 9
	branch := (iv & 15) - ((iv0 * 3) & 255)
	switch branch {
	case 0:
		return hashBranch0(iv0, k, query, body, ts)
	case 1:
		return hashBranch1(iv0, k, query, body, ts)
	default:
		return hashBranch2(iv, k, query, body, ts)
	}
}
func hashIV(iv uint32, data []byte) uint32 {
	for i, b := range data {
		if i&1 == 0 {
			iv = (iv >> 4) ^ iv ^ (iv << 6) ^ uint32(b)
		} else {
			iv = ^((iv >> 7) ^ iv ^ (uint32(b) | (iv << 12)))
		}
	}
	return iv
}
func hashSum(b []byte) uint32 {
	s := uint32(0x20220420)
	for i := 0; i < 12; i++ {
		var t uint32
		if i&1 == 0 {
			t = (s >> 3) ^ s
			s = uint32(b[i]) ^ (s << 7)
		} else {
			t = (s >> 5) ^ s
			s = uint32(b[i]) | (s << 11)
			s = ^s
		}
		s ^= t
	}
	return (s | 4) ^ 0x1000000
}
func hashFinish(b []byte) []byte { return append(b, uint32LE(hashSum(b))...) }
func xorHalves(b []byte) []byte {
	o := make([]byte, 16)
	for i := range o {
		o[i] = b[i] ^ b[i+16]
	}
	return hashFinish(o)
}

var h0t1 = []uint32{0xc4a78580, 0xb3c0fd39, 0xc58c5686, 0xc9aa3ba7, 0xf5a7adf2, 0x963c2ed1}
var h0t2 = []uint32{0xebb64faf, 0x07aadcc2, 0xcf3187bf, 0xe01138ff, 0x6d0bfcff, 0x5a30a3be, 0xb41ad638, 0x34180eb8, 0xf233eb6f, 0xb1a584cc, 0xccc30dc7, 0x47d1db51, 0xd55653de, 0x70a84fa1, 0x57473c12, 0xf76f0288, 0x2c077f0a, 0xda0dcad0, 0xfbb86f6c, 0xfdc4cf00, 0x688a020d, 0xe676c6a6, 0x8cd6338b, 0x1a3c8d0e, 0xcce8b06b, 0x6ad0ed0b, 0xa0522717, 0xdc71ac83, 0x2285db71, 0xd5b4dda6, 0x736f8650, 0x6560306c, 0x617ce2a6, 0xe423417e, 0x0a40e143, 0x544e4032, 0x88dffb2a, 0x716c1ae0, 0x4c467a88, 0x05b23bb3, 0xe1d0b866, 0xbaa3dcb8, 0xae3374d3, 0xc3381a50, 0x1702f75b, 0xfe6da368, 0xf0b4cf48, 0x4e0ffbb8, 0x72aad10d, 0x26c53a3d, 0xf2bce0f6, 0xb4557581, 0x4a257fdd, 0x8c3182a2, 0xab0b3b86, 0x3d5dfb14, 0x4f103634, 0xd37b52d7, 0x444eff16, 0xeb0a33d1, 0x6ca86f6e, 0x00284ba7, 0x08387cfa, 0x5fb37586}

func hashBranch0(iv0, k uint32, q, b, t []byte) []byte {
	base := h0t1[iv0]
	c1 := byte(base + k + 1)
	c2 := base + k
	tt := make([]uint32, 64)
	for i := range tt {
		tt[i] = bits.RotateLeft32(h0t2[i], -int(c1))
	}
	n := (c2 + 2) & 7
	seed := []byte{0xfa, 0x45, 0x61, 0xd7}
	pad := make([]byte, 4)
	for i, x := range seed {
		pad[i] = byte(uint16(x) * 257 >> n)
	}
	data := append(append(append(append([]byte{}, q...), b...), t...), pad...)
	data = append(data, 0, 0, 0, 0, 0, 0, 1, 0xa0)
	di := make([]uint32, 128)
	for i := 0; i < len(data)/4; i++ {
		di[i] = binary.BigEndian.Uint32(data[i*4:])
	}
	d0 := di[0]
	for i := 0; i < 112; i++ {
		d1, d14 := di[i+1], di[i+14]
		d0 += di[i+9] + (bits.RotateLeft32(d1, 14) ^ bits.RotateLeft32(d1, 25) ^ (d1 >> 3)) + (bits.RotateLeft32(d14, 13) ^ bits.RotateLeft32(d14, 15) ^ (d14 >> 10))
		di[i+16] = d0
		d0 = d1
	}
	init := []uint32{bits.RotateLeft32(0x7aba4fc8, -int(byte(c2))), bits.RotateLeft32(0x67166507, -int(byte(c2))), bits.RotateLeft32(0x6403fa00, -int(byte(c2))), bits.RotateLeft32(0x340f512f, -int(byte(c2))), bits.RotateLeft32(984304912, -int(byte(c2))), bits.RotateLeft32(3005047866, -int(byte(c2))), bits.RotateLeft32(2874125293, -int(byte(c2))), bits.RotateLeft32(2152413264, -int(byte(c2)))}
	args := [][]int{{101, 5, 7, 6, 3, 2, 1, 0, 5, 4, 3}, {96, 0, 6, 7, 5, 3, 2, 1, 5, 4, 4}, {96, 7, 6, 2, 1, 4, 0, 5, 4, 3, 5}, {99, 3, 6, 2, 4, 5, 1, 0, 0, 7, 6}, {96, 0, 5, 6, 7, 3, 1, 2, 5, 4, 4}, {100, 2, 0, 3, 5, 4, 6, 7, 2, 1, 5}}[iv0]
	d := hashBranch0Rounds(init, base, di, tt, args)
	out := make([]byte, 32)
	for i := range d {
		binary.BigEndian.PutUint32(out[i*4:], d[i]+init[i])
	}
	return xorHalves(out)
}
func hashBranch0Rounds(src []uint32, base uint32, di, tt []uint32, a []int) []uint32 {
	d := append([]uint32{}, src...)
	for i := 0; i < a[0]; i++ {
		n0 := di[(int(base)+i)&127]
		n1 := ((d[a[3]] ^ d[a[4]]) & d[a[1]]) ^ d[a[3]]
		n2 := bits.RotateLeft32(d[a[1]], 26) ^ bits.RotateLeft32(d[a[1]], 21) ^ bits.RotateLeft32(d[a[1]], 7)
		n4 := n0 + n1 + n2 + tt[(int(base)+i)&63] + d[a[5]]
		n5 := bits.RotateLeft32(d[a[2]], 30) ^ bits.RotateLeft32(d[a[2]], 19) ^ bits.RotateLeft32(d[a[2]], 10)
		n6 := (d[a[2]] & d[a[6]]) | ((d[a[2]] | d[a[6]]) & d[a[7]])
		o := d[a[9]]
		d = []uint32{d[7], d[0], d[1], d[2], d[3], d[4], d[5], d[6]}
		d[a[10]] = n5 + n6 + n4
		d[a[8]] = o + n4
	}
	return d
}

func hashBranch1(iv0, k uint32, q, b, t []byte) []byte {
	bases := []uint32{0x808a9c79, 0xf079807e, 0xbadf79c5, 0xa785d3ff, 0x82d8438c}
	base := bases[iv0]
	c := bits.RotateLeft64(uint64(k), -int(byte(base+k)))
	orders := []byte{5, 7, 1, 2, 4, 0, 6, 3, 0, 5, 2, 4, 1, 3, 7, 6, 5, 7, 2, 4, 1, 6, 3, 0, 3, 0, 2, 4, 6, 7, 1, 5, 4, 5, 0, 3, 6, 2, 1, 7}
	order := orders[iv0*8 : iv0*8+8]
	consts := []uint64{0x87aeea5dab37cd6b, 0x7ff48becb4f54087, 0xb0724c06706bbd5d, 0x1fe5dfb1143e328d, 0x1a2331d00af4f1f2, 0xcaff7131bb1e71ba, 0x33385e1042752218, 0xff01ed65d4a441fb, 0x0adb1ec8828c80e8, 0x62475d12f4e06fe7, 0xbd0b238da4fe72}
	rot := int((base + k + 1) & 63)
	tt := make([]uint64, 11)
	for i := range tt {
		tt[i] = bits.RotateLeft64(consts[i], -rot)
	}
	data := append(append(append([]byte{}, q...), b...), t...)
	data = append(data, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	src := make([]uint64, 8)
	for i := range src {
		src[i] = binary.BigEndian.Uint64(data[i*8:])
	}
	sx := []uint64{c, c, c, c, c, c, c, c}
	table := branchOneTable[iv0<<14:]
	swap := hashSwap(src, sx, tt, table, order, nil)
	zeros := make([]uint64, 8)
	zeros[7] = 416
	sx = []uint64{swap[4], swap[3], swap[2], swap[1], swap[0], swap[7], swap[6], swap[5]}
	swap = hashSwap(zeros, sx, tt, table, order, swap)
	out := make([]byte, 32)
	n := 0
	for i := 7; i >= 0; i-- {
		var x [8]byte
		binary.LittleEndian.PutUint64(x[:], swap[i])
		out[n], out[n+1], out[n+2], out[n+3] = x[1], x[3], x[5], x[6]
		n += 4
	}
	return xorHalves(out)
}
func hashTable(table []byte, x uint64) uint64 { return binary.LittleEndian.Uint64(table[int(x):]) }
func hashR00(r [8]uint64, tt uint64, tab []byte) uint64 {
	x := hashTable(tab, 8*(r[0]>>56)) ^ tt
	x ^= hashTable(tab, ((r[1]>>45)&2040)+2048)
	x ^= hashTable(tab, ((r[2]>>37)&2040)+4096)
	x ^= hashTable(tab, ((r[3]>>29)&2040)+6144)
	x ^= hashTable(tab, ((r[4]>>21)&2040)+8192)
	x ^= hashTable(tab, ((r[5]>>13)&2040)+10240)
	x ^= hashTable(tab, ((r[6]>>8)&255)*8+12288)
	x ^= hashTable(tab, (r[7]&255)*8+14336)
	return x
}
func rotate8(a []uint64, n int) [8]uint64 {
	var r [8]uint64
	for i := 0; i < 8; i++ {
		r[i] = a[(i+n)&7]
	}
	return r
}
func hashSwap(src, sx, tt []uint64, tab []byte, order []byte, typ []uint64) []uint64 {
	da := make([]uint64, 8)
	ha := append([]uint64{}, sx...)
	for i := range da {
		da[i] = src[order[i]] ^ sx[i]
	}
	for round := 0; round < 10; round++ {
		rr := make([]uint64, 8)
		for i := range rr {
			rr[i] = hashR00(rotate8(ha, i), 0, tab)
		}
		rr[7] ^= tt[round+1]
		old := append([]uint64{}, da...)
		for i := range da {
			da[i] = hashR00(rotate8(old, i), rr[i], tab)
		}
		ha = rr
	}
	if typ == nil {
		src[0] = da[0] ^ sx[0] ^ src[0]
		for i := 1; i < 8; i++ {
			src[i] = da[8-i] ^ sx[i] ^ src[i]
		}
	} else {
		for i := 0; i < 6; i++ {
			src[i] = da[7-i] ^ typ[i+1]
		}
		src[6] = da[1] ^ typ[7] ^ src[7]
		src[7] = da[0] ^ typ[0]
	}
	return src
}

var mdSV = []uint32{0xa7aefe20, 0x7149f1d6, 0x47e4ca07, 0xe9b58f67, 0x93b924de, 0xc614d0f5, 0x38afe0ef, 0xb2bbad73, 0xe24444c3, 0x9d3aec9b, 0xdf7b37e4, 0xd8b16d40, 0xf8ac31b8, 0x76b9a90b, 0x31d833ee, 0x953fce64, 0x353595a4, 0x4609c13b, 0x36925008, 0x8c6d0925, 0x5df5c177, 0x1cfbf52b, 0x8a4fa7f0, 0x114ca35e, 0x8193f984, 0x7a7a8733, 0x316ab4d5, 0x3c20cfc9, 0xa6d84453, 0x3a18500c, 0x798ec47a, 0x97a76b28, 0x66c4ff96, 0x51716443, 0x0dd2fc3b, 0xb5696da7, 0xbbeb3ac5, 0x5c53d204, 0xd32608ce, 0x7279b9ec, 0xf4188ecf, 0xf7d793db, 0x332cc491, 0xab76ae15, 0x9bebe727, 0x18a01384, 0x5be9f8a7, 0x5f90a754, 0x39b663c0, 0x36673c83, 0x7c92f514, 0x9d7d94d7, 0xe2e8d9aa, 0x5f7e9ea9, 0x7abd4551, 0x569e05da, 0x40a25632, 0x3df5a9a5, 0xbab37d80, 0x454286dc, 0x3f5d4e78, 0x03d7b75d, 0xb1fe4af7, 0xa5ab26a3}
var branch2Orders = []byte{15, 7, 4, 0, 9, 8, 3, 10, 6, 11, 5, 13, 14, 1, 12, 2, 15, 5, 8, 12, 0, 9, 2, 1, 3, 7, 14, 6, 11, 10, 13, 4, 6, 5, 0, 7, 12, 0, 10, 4, 8, 15, 1, 11, 13, 9, 2, 14, 6, 11, 2, 5, 4, 3, 8, 1, 1, 7, 10, 0, 13, 12, 9, 14, 13, 7, 14, 15, 11, 2, 8, 3, 12, 5, 9, 1, 0, 4, 6, 10, 13, 9, 2, 6, 15, 11, 10, 4, 8, 7, 0, 12, 5, 3, 1, 14, 12, 9, 15, 7, 6, 15, 3, 14, 2, 13, 4, 5, 1, 11, 10, 0, 12, 5, 10, 9, 14, 8, 2, 4, 4, 7, 3, 15, 1, 6, 11, 0, 11, 15, 4, 8, 2, 10, 7, 0, 9, 13, 6, 1, 14, 3, 5, 12, 11, 6, 10, 5, 8, 2, 12, 3, 7, 15, 14, 9, 13, 0, 1, 4, 9, 6, 8, 15, 5, 8, 0, 4, 10, 11, 3, 13, 1, 2, 12, 14, 9, 13, 12, 6, 4, 7, 10, 3, 3, 15, 0, 8, 1, 5, 2, 14, 1, 0, 13, 15, 9, 10, 11, 14, 4, 2, 8, 7, 3, 6, 12, 5, 1, 8, 10, 12, 15, 9, 5, 6, 11, 0, 3, 4, 2, 14, 7, 13, 4, 8, 15, 0, 12, 15, 14, 13, 10, 1, 6, 2, 7, 9, 5, 3, 4, 2, 5, 8, 13, 11, 10, 6, 6, 0, 14, 15, 7, 12, 9, 3, 10, 8, 4, 15, 0, 11, 1, 6, 13, 12, 7, 9, 3, 14, 5, 2, 10, 7, 11, 5, 15, 0, 2, 14, 1, 8, 3, 13, 12, 6, 9, 4, 13, 7, 15, 8, 5, 15, 6, 4, 11, 10, 14, 12, 9, 0, 2, 3, 13, 12, 2, 7, 4, 1, 11, 14, 14, 8, 6, 15, 9, 5, 0, 3}

func hashBranch2(iv, k uint32, q, b, t []byte) []byte {
	n := ((iv & 15) - 2) * 86
	iv0 := ((n >> 15) & 255) + ((n >> 8) & 255)
	base := []uint32{0x8980f29b, 0xeb549c7f, 0xb08726db, 0xd40cb5e6, 0xe8f559e4}[iv0]
	c1 := byte(base + k + 1)
	c2 := base + k
	sh := (c2 + 5) & 7
	seed := []byte{0x84, 0x96, 0x77, 0x9d, 0xd4, 0x15, 0x0b, 0xf8}
	pad := make([]byte, 8)
	for i, x := range seed {
		pad[i] = byte(uint16(x) * 257 >> sh)
	}
	data := append(append(append(append([]byte{}, q...), b...), t...), pad...)
	data = append(data, 0xa0, 1, 0, 0)
	return customMD5(data, c2, branch2Orders[iv0*64:iv0*64+64], c1)
}
func customMD5(msg []byte, c uint32, ord []byte, c1 byte) []byte {
	sv := make([]uint32, 64)
	for i := range sv {
		sv[i] = bits.RotateLeft32(mdSV[i], -int(c1))
	}
	cc := byte(c)
	start := []uint32{bits.RotateLeft32(0x79e0f2fb, -int(cc)), bits.RotateLeft32(0xc8b52570, -int(cc)), bits.RotateLeft32(0xebc2f8cd, -int(cc)), bits.RotateLeft32(0x7c104d93, -int(cc))}
	ec := byte(c + 6)
	end := []uint32{bits.RotateLeft32(0x19be4866, -int(ec)), bits.RotateLeft32(0xe85986b4, -int(ec)), bits.RotateLeft32(0xe19b326e, -int(ec)), bits.RotateLeft32(0x71d1d7d4, -int(ec))}
	m := make([]uint32, 16)
	for i := range m {
		m[i] = binary.LittleEndian.Uint32(msg[i*4:])
	}
	a, b, cx, d := start[0], start[1], start[2], start[3]
	shifts := [][]int{{7, 12, 17, 22}, {5, 9, 14, 20}, {4, 11, 16, 23}, {6, 10, 15, 21}}
	for r := 0; r < 4; r++ {
		for i := 0; i < 16; i++ {
			var f uint32
			switch r {
			case 0:
				f = (b & cx) | (^b & d)
			case 1:
				f = (b & d) | (cx &^ d)
			case 2:
				f = b ^ cx ^ d
			case 3:
				f = cx ^ (b | ^d)
			}
			nv := b + bits.RotateLeft32(a+f+m[ord[r*16+i]]+sv[r*16+i], shifts[r][i&3])
			a, d, cx, b = d, cx, b, nv
		}
	}
	out := make([]byte, 16)
	vals := []uint32{start[0] + a ^ end[0], start[1] + b ^ end[1], start[2] + cx ^ end[2], start[3] + d ^ end[3]}
	for i, x := range vals {
		binary.LittleEndian.PutUint32(out[i*4:], x)
	}
	return hashFinish(out)
}
