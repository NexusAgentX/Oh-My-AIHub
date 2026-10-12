package supplier

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func testICO(width, height int, bodies ...[]byte) []byte {
	raw := make([]byte, 6+16*len(bodies))
	raw[2] = 1
	binary.LittleEndian.PutUint16(raw[4:], uint16(len(bodies)))
	for i, body := range bodies {
		e := raw[6+i*16:]
		e[0], e[1] = byte(width), byte(height)
		binary.LittleEndian.PutUint32(e[8:], uint32(len(body)))
		binary.LittleEndian.PutUint32(e[12:], uint32(len(raw)))
		raw = append(raw, body...)
	}
	return raw
}

// A 2x2 bottom-up DIB with distinct top/bottom rows and a transparent
// top-right pixel. No provider asset or network dependency in this fixture.
func testDIB(bits int, alpha bool) []byte {
	palette := 0
	if bits <= 8 {
		palette = 1 << bits
	}
	stride := (2*bits + 31) / 32 * 4
	raw := make([]byte, 40+palette*4+stride*2+8)
	binary.LittleEndian.PutUint32(raw, 40)
	binary.LittleEndian.PutUint32(raw[4:], 2)
	binary.LittleEndian.PutUint32(raw[8:], 4)
	binary.LittleEndian.PutUint16(raw[12:], 1)
	binary.LittleEndian.PutUint16(raw[14:], uint16(bits))
	if palette > 0 {
		raw[40+4+2] = 255
	}
	for y := 0; y < 2; y++ {
		row := raw[40+palette*4+y*stride:]
		for x := 0; x < 2; x++ {
			if bits <= 8 {
				if y == 1 {
					row[x*bits/8] |= 1 << uint(8-bits-x*bits%8)
				}
			} else {
				if y == 1 {
					row[x*bits/8+2] = 255
				}
				if bits == 32 && alpha {
					row[x*4+3] = 128
					if y == 1 && x == 1 {
						row[x*4+3] = 0
					}
				}
			}
		}
	}
	raw[len(raw)-4] = 0x40
	return raw
}
func TestBitmapICO(t *testing.T) {
	for _, bits := range []int{1, 4, 8, 24, 32} {
		for _, alpha := range []bool{false, true} {
			img, err := decodeICO(testICO(2, 2, testDIB(bits, alpha)))
			if err != nil {
				t.Fatalf("bits=%d alpha=%v: %v", bits, alpha, err)
			}
			got := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
			wantAlpha := uint8(255)
			if bits == 32 && alpha {
				wantAlpha = 128
			}
			if got != (color.NRGBA{R: 255, A: wantAlpha}) {
				t.Fatalf("top-left bits=%d alpha=%v: %+v", bits, alpha, got)
			}
			if _, _, _, a := img.At(1, 0).RGBA(); a != 0 {
				t.Fatal("mask/alpha was lost")
			}
			if r, _, _, _ := img.At(0, 1).RGBA(); r != 0 {
				t.Fatal("bottom-up orientation was lost")
			}
		}
	}
}
func TestICOEntriesAndBounds(t *testing.T) {
	var pngBody bytes.Buffer
	_ = png.Encode(&pngBody, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	for _, raw := range [][]byte{testICO(2, 2, pngBody.Bytes()), testICO(2, 2, []byte("bad"), testDIB(32, true))} {
		if _, err := rasterIcon(raw); err != nil {
			t.Fatal(err)
		}
	}
	valid := testICO(2, 2, testDIB(24, false))
	for n := 0; n < len(valid); n++ {
		if _, err := rasterIcon(valid[:n]); err == nil {
			t.Fatalf("accepted truncation at %d", n)
		}
	}
	for _, at := range []int{4, 14, 18, 22, 26, 30, 34, 36, 38, 54} {
		raw := bytes.Clone(valid)
		raw[at] = 255
		raw[at+1] = 255
		if _, err := rasterIcon(raw); err == nil {
			t.Fatalf("accepted malformed header at %d", at)
		}
	}
	// A 32-bit alpha image can omit its mask; an all-zero-alpha legacy image cannot.
	for _, alpha := range []bool{false, true} {
		body := testDIB(32, alpha)
		body = body[:len(body)-8]
		_, err := rasterIcon(testICO(2, 2, body))
		if (err == nil) != alpha {
			t.Fatalf("missing mask alpha=%v: %v", alpha, err)
		}
	}
}
