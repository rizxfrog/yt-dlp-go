package hongguo

// AES_V3 is the small application-specific block transform used to wrap the
// Medusa signature. This is not standard AES despite its source name.
type aesV3 struct {
	word      int
	sbox, inv []byte
	keys      [][]byte
	con, con2 []int
	order     []int
}

func newAESV3(key []byte, k uint32) *aesV3 {
	w := int(k & 3)
	s := aesSBox[w*256:]
	inv := make([]byte, 256)
	for i := 0; i < 256; i++ {
		inv[s[i]] = byte(i)
	}
	cs := [][]int{{1, 0, 2, 3}, {1, 3, 0, 2}, {0, 1, 3, 2}, {1, 0, 2, 3}}
	c2 := [][]int{{1, 0, 2, 3}, {2, 0, 3, 1}, {0, 1, 3, 2}, {1, 0, 2, 3}}
	orders := [][]int{{0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3}, {0, 9, 14, 15, 4, 13, 2, 7, 8, 1, 6, 3, 12, 5, 10, 11}, {0, 9, 14, 7, 4, 13, 2, 11, 8, 1, 6, 3, 12, 5, 10, 15}, {0, 9, 14, 11, 4, 13, 2, 7, 8, 1, 6, 15, 12, 5, 10, 3}}
	a := &aesV3{word: w, sbox: s[:256], inv: inv, con: cs[w], con2: c2[w], order: orders[w]}
	a.expand(key)
	return a
}
func (a *aesV3) expand(key []byte) {
	init := []uint32{0xca025ddc, 0x823dc546, 0xc9420583, 0xc298225f}[a.word]
	mk := make([]byte, 48)
	for i := 0; i < 4; i++ {
		putLE(mk[i*4:], init)
	}
	for i := 0; i < 16; i++ {
		mk[i] ^= key[i]
	}
	rounds := 8
	for i := 4; i < 12; i++ {
		idx := 4 * (i - 1)
		k0, k1, k2, k3 := mk[idx], mk[idx+1], mk[idx+2], mk[idx+3]
		if i&3 == 0 {
			k00 := byte(init>>uint(rounds&24)) ^ a.sbox[k1]
			k1, k2, k3, k0 = a.sbox[k2], a.sbox[k3], a.sbox[k0], k00
		}
		rounds += 2
		mk[idx+4] = k0 ^ mk[idx-12]
		mk[idx+5] = k1 ^ mk[idx-11]
		mk[idx+6] = k2 ^ mk[idx-10]
		mk[idx+7] = k3 ^ mk[idx-9]
	}
	for i := 0; i < 12; i++ {
		a.keys = append(a.keys, mk[i*4:i*4+4])
	}
}
func putLE(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
func (a *aesV3) encrypt(data, iv []byte) []byte {
	plain := a.sumData(data)
	prev := iv
	key := []byte{}
	for i := 0; i < len(plain); i += 16 {
		x := xor16(plain[i:i+16], prev)
		blk := a.block(x)
		key = append(key, blk...)
		prev = blk
	}
	out := a.mix(data, key)
	return append(key[len(key)-1:], out...)
}
func (a *aesV3) sumData(d []byte) []byte {
	k := make([]byte, 32)
	for i := 0; i < 31; i++ {
		x := i * 8
		k[i] = ((d[x] >> 4) & 2) | (d[x+1] & 64) | ((d[x+2] >> 2) & 1) | ((d[x+3] << 3) & 128) | ((d[x+4] >> 1) & 4) | ((d[x+5] << 3) & 16) | ((d[x+6] << 5) & 32) | ((d[x+7] >> 4) & 8)
	}
	k[31] = 1
	return k
}
func (a *aesV3) mix(d, key []byte) []byte {
	o := append([]byte{}, d...)
	for i := 0; i < 31; i++ {
		k, x := key[i], i*8
		o[x] = (o[x] &^ 32) | ((k << 4) & 32)
		o[x+1] = (o[x+1] &^ 64) | (k & 64)
		o[x+2] = (o[x+2] &^ 4) | ((k * 4) & 4)
		o[x+3] = (o[x+3] &^ 16) | ((k >> 3) & 16)
		o[x+4] = (o[x+4] &^ 8) | ((k + k) & 8)
		o[x+5] = (o[x+5] &^ 2) | ((k >> 3) & 2)
		o[x+6] = (o[x+6] &^ 1) | ((k >> 5) & 1)
		o[x+7] = (o[x+7] & 127) | ((k << 4) & 128)
	}
	return o
}
func (a *aesV3) block(p []byte) []byte {
	s := matrix(p)
	addCon(s, a.keys[:4], a.con2)
	for r := 1; r < 3; r++ {
		for i := range s {
			for j := range s[i] {
				s[i][j] = a.sbox[s[i][j]]
			}
		}
		s = [][4]byte{s[a.con2[0]], s[a.con2[1]], s[a.con2[2]], s[a.con2[3]]}
		bs := unmatrix(s)
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				s[i][j] = bs[a.order[i*4+j]]
			}
		}
		if r == 1 {
			for i := range s {
				s[i] = [4]byte{s[i][a.con2[0]], s[i][a.con2[1]], s[i][a.con2[2]], s[i][a.con2[3]]}
			}
			mixCols(s)
		}
		addCon(s, a.keys[r*4:], a.con2)
	}
	add(s, a.keys[4:])
	return unmatrix(s)
}
func matrix(b []byte) [][4]byte {
	o := make([][4]byte, 4)
	for i := range o {
		copy(o[i][:], b[i*4:])
	}
	return o
}
func unmatrix(s [][4]byte) []byte {
	o := make([]byte, 16)
	for i := range s {
		copy(o[i*4:], s[i][:])
	}
	return o
}
func add(s [][4]byte, k [][]byte) {
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			s[i][j] ^= k[i][j]
		}
	}
}
func addCon(s [][4]byte, k [][]byte, c []int) {
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			s[i][j] ^= k[i][c[j]]
		}
	}
}
func xtime(x byte) byte {
	if x&128 != 0 {
		return (x << 1) ^ 0x1b
	}
	return x << 1
}
func mixCols(s [][4]byte) {
	for i := 0; i < 4; i++ {
		t := s[0][i] ^ s[1][i] ^ s[2][i] ^ s[3][i]
		u := s[0][i]
		s[0][i] ^= t ^ xtime(s[0][i]^s[1][i])
		s[1][i] ^= t ^ xtime(s[1][i]^s[2][i])
		s[2][i] ^= t ^ xtime(s[2][i]^s[3][i])
		s[3][i] ^= t ^ xtime(s[3][i]^u)
	}
}
func xor16(a, b []byte) []byte {
	o := make([]byte, 16)
	for i := range o {
		o[i] = a[i] ^ b[i]
	}
	return o
}
