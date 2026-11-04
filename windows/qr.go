package main

// A small QR code encoder (byte mode, error correction level M, versions 1 to 9), so the
// pairing link can be scanned from the console instead of typed. Standard library only.
// It follows the QR Code specification (ISO/IEC 18004); the structure mirrors the widely
// used reference design by Project Nayuki.

import (
	"errors"
	"strings"
)

type qrCode struct {
	size     int
	modules  [][]bool // true = dark
	function [][]bool // true = finder, timing, alignment, format or version module
}

// Error correction level M, versions 1..9: total codewords, error correction codewords per
// block, and number of blocks.
var (
	qrTotalCodewords = [10]int{0, 26, 44, 70, 100, 134, 172, 196, 242, 292}
	qrECCPerBlock    = [10]int{0, 10, 16, 26, 18, 24, 16, 18, 22, 22}
	qrNumBlocks      = [10]int{0, 1, 1, 1, 2, 2, 4, 4, 4, 5}
	qrAlignment      = [10][]int{nil, nil, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34}, {6, 22, 38}, {6, 24, 42}, {6, 26, 46}}
)

func encodeQR(text string) (*qrCode, error) {
	data := []byte(text)
	version := 0
	for v := 1; v <= 9; v++ {
		capacity := qrTotalCodewords[v] - qrECCPerBlock[v]*qrNumBlocks[v]
		if 4+8+8*len(data) <= capacity*8 {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, errors.New("text too long for a QR code")
	}
	dataLen := qrTotalCodewords[version] - qrECCPerBlock[version]*qrNumBlocks[version]

	// Byte mode, 8-bit length, data, terminator, pad to a byte, then pad codewords.
	var bits []bool
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (v>>uint(i))&1 == 1)
		}
	}
	put(0x4, 4)
	put(len(data), 8)
	for _, b := range data {
		put(int(b), 8)
	}
	for i := 0; i < 4 && len(bits) < dataLen*8; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	codewords := make([]byte, 0, dataLen)
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			if bits[i+j] {
				b |= 1 << uint(7-j)
			}
		}
		codewords = append(codewords, b)
	}
	for pad := byte(0xEC); len(codewords) < dataLen; pad ^= 0xEC ^ 0x11 {
		codewords = append(codewords, pad)
	}

	all := qrAddECCAndInterleave(codewords, version)

	q := &qrCode{size: version*4 + 17}
	q.modules = make([][]bool, q.size)
	q.function = make([][]bool, q.size)
	for i := range q.modules {
		q.modules[i] = make([]bool, q.size)
		q.function[i] = make([]bool, q.size)
	}
	q.drawFunctionPatterns(version)
	q.drawCodewords(all)

	best, bestPenalty := 0, -1
	for m := 0; m < 8; m++ {
		q.applyMask(m)
		q.drawFormatBits(m)
		if p := q.penalty(); bestPenalty < 0 || p < bestPenalty {
			best, bestPenalty = m, p
		}
		q.applyMask(m) // XOR again to undo
	}
	q.applyMask(best)
	q.drawFormatBits(best)
	return q, nil
}

func qrAddECCAndInterleave(data []byte, version int) []byte {
	numBlocks := qrNumBlocks[version]
	ecLen := qrECCPerBlock[version]
	raw := qrTotalCodewords[version]
	numShort := numBlocks - raw%numBlocks
	shortLen := raw / numBlocks // codewords (data + ecc) in a short block
	divisor := rsDivisor(ecLen)

	var blocksData, blocksECC [][]byte
	k := 0
	for i := 0; i < numBlocks; i++ {
		n := shortLen - ecLen
		if i >= numShort {
			n++
		}
		d := data[k : k+n]
		k += n
		blocksData = append(blocksData, d)
		blocksECC = append(blocksECC, rsRemainder(d, divisor))
	}
	var out []byte
	for i := 0; i <= shortLen-ecLen; i++ {
		for _, d := range blocksData {
			if i < len(d) {
				out = append(out, d[i])
			}
		}
	}
	for i := 0; i < ecLen; i++ {
		for _, e := range blocksECC {
			out = append(out, e[i])
		}
	}
	return out
}

func gfMul(x, y byte) byte {
	var z byte
	for i := 7; i >= 0; i-- {
		hi := z >> 7
		z <<= 1
		if hi == 1 {
			z ^= 0x1D // x^8 + x^4 + x^3 + x^2 + 1, reduced
		}
		if (y>>uint(i))&1 == 1 {
			z ^= x
		}
	}
	return z
}

func rsDivisor(degree int) []byte {
	result := make([]byte, degree)
	result[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := 0; j < degree; j++ {
			result[j] = gfMul(result[j], root)
			if j+1 < degree {
				result[j] ^= result[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return result
}

func rsRemainder(data, divisor []byte) []byte {
	result := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i, d := range divisor {
			result[i] ^= gfMul(d, factor)
		}
	}
	return result
}

func (q *qrCode) set(x, y int, dark bool) {
	q.modules[y][x] = dark
	q.function[y][x] = true
}

func (q *qrCode) drawFunctionPatterns(version int) {
	for i := 0; i < q.size; i++ {
		q.set(6, i, i%2 == 0)
		q.set(i, 6, i%2 == 0)
	}
	for _, c := range [][2]int{{3, 3}, {q.size - 4, 3}, {3, q.size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := c[0]+dx, c[1]+dy
				if x < 0 || y < 0 || x >= q.size || y >= q.size {
					continue
				}
				d := max(abs(dx), abs(dy))
				q.set(x, y, d != 2 && d != 4)
			}
		}
	}
	pos := qrAlignment[version]
	for i, a := range pos {
		for j, b := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == len(pos)-1) || (i == len(pos)-1 && j == 0) {
				continue // overlaps a finder pattern
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					q.set(a+dx, b+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	q.drawFormatBits(0) // reserve the area; redrawn once the mask is chosen
	if version >= 7 {
		rem := version
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := version<<12 | rem
		for i := 0; i < 18; i++ {
			dark := (bits>>uint(i))&1 == 1
			a, b := q.size-11+i%3, i/3
			q.set(a, b, dark)
			q.set(b, a, dark)
		}
	}
}

func (q *qrCode) drawFormatBits(mask int) {
	data := 0<<3 | mask // error correction level M = 0b00
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>uint(i))&1 == 1 }
	for i := 0; i <= 5; i++ {
		q.set(8, i, bit(i))
	}
	q.set(8, 7, bit(6))
	q.set(8, 8, bit(7))
	q.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		q.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		q.set(q.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		q.set(8, q.size-15+i, bit(i))
	}
	q.set(8, q.size-8, true) // the dark module
}

func (q *qrCode) drawCodewords(data []byte) {
	i := 0
	for right := q.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < q.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = q.size - 1 - vert
				}
				if !q.function[y][x] && i < len(data)*8 {
					q.modules[y][x] = (data[i>>3]>>uint(7-i&7))&1 == 1
					i++
				}
			}
		}
	}
}

func (q *qrCode) applyMask(mask int) {
	for y := 0; y < q.size; y++ {
		for x := 0; x < q.size; x++ {
			if q.function[y][x] {
				continue
			}
			var invert bool
			switch mask {
			case 0:
				invert = (x+y)%2 == 0
			case 1:
				invert = y%2 == 0
			case 2:
				invert = x%3 == 0
			case 3:
				invert = (x+y)%3 == 0
			case 4:
				invert = (x/3+y/2)%2 == 0
			case 5:
				invert = x*y%2+x*y%3 == 0
			case 6:
				invert = (x*y%2+x*y%3)%2 == 0
			case 7:
				invert = ((x+y)%2+x*y%3)%2 == 0
			}
			if invert {
				q.modules[y][x] = !q.modules[y][x]
			}
		}
	}
}

// penalty scores a masked symbol; lower is easier for cameras to read.
func (q *qrCode) penalty() int {
	n := q.size
	at := func(x, y int, row bool) bool {
		if row {
			return q.modules[y][x]
		}
		return q.modules[x][y]
	}
	score := 0
	for _, row := range []bool{true, false} {
		for a := 0; a < n; a++ {
			run := 1
			for b := 1; b < n; b++ {
				if at(b, a, row) == at(b-1, a, row) {
					run++
					if run == 5 {
						score += 3
					} else if run > 5 {
						score++
					}
				} else {
					run = 1
				}
			}
			// finder-like 1:1:3:1:1 with four light modules on one side
			for b := 0; b+7 <= n; b++ {
				p := [7]bool{}
				for k := 0; k < 7; k++ {
					p[k] = at(b+k, a, row)
				}
				if p != [7]bool{true, false, true, true, true, false, true} {
					continue
				}
				light := func(from, to int) bool {
					for k := from; k < to; k++ {
						if k >= 0 && k < n && at(k, a, row) {
							return false
						}
					}
					return true
				}
				if light(b-4, b) || light(b+7, b+11) {
					score += 40
				}
			}
		}
	}
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if q.modules[y][x] {
				dark++
			}
			if x+1 < n && y+1 < n {
				c := q.modules[y][x]
				if c == q.modules[y][x+1] && c == q.modules[y+1][x] && c == q.modules[y+1][x+1] {
					score += 3
				}
			}
		}
	}
	total := n * n
	k := (abs(dark*20-total*10) + total - 1) / total
	return score + (k-1)*10
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// terminal draws the code with half-block characters, two module rows per text line.
// Light modules are drawn as blocks, so on a dark console the code reads as dark on light.
func (q *qrCode) terminal() string {
	const quiet = 2
	n := q.size + 2*quiet
	light := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		if x < 0 || y < 0 || x >= q.size || y >= q.size {
			return true
		}
		return !q.modules[y][x]
	}
	var sb strings.Builder
	for y := 0; y < n; y += 2 {
		sb.WriteString("    ")
		for x := 0; x < n; x++ {
			top, bottom := light(x, y), y+1 >= n || light(x, y+1)
			switch {
			case top && bottom:
				sb.WriteString("█")
			case top:
				sb.WriteString("▀")
			case bottom:
				sb.WriteString("▄")
			default:
				sb.WriteString(" ")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
