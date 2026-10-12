package supplier

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

// ICO entries contain either PNG or an uncompressed DIB followed by an AND
// transparency mask. See https://devblogs.microsoft.com/oldnewthing/20101019-00/?p=12503
// Keep all dimensions and offsets bounded before allocating or indexing.
func decodeICO(raw []byte) (image.Image, error) {
	if len(raw) < 6 {
		return nil, ErrIconFormat
	}
	count := int(binary.LittleEndian.Uint16(raw[4:6]))
	if count == 0 || count > 64 || count > (len(raw)-6)/16 {
		return nil, ErrIconFormat
	}
	var best image.Image
	for i := 0; i < count; i++ {
		entry := raw[6+i*16 : 6+(i+1)*16]
		size, offset := uint64(binary.LittleEndian.Uint32(entry[8:12])), uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if size == 0 || offset < uint64(6+count*16) || offset+size > uint64(len(raw)) {
			continue
		}
		w, h := int(entry[0]), int(entry[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		body := raw[offset : offset+size]
		var img image.Image
		var err error
		if bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
			img, err = decodeRaster(body)
		} else {
			img, err = decodeIconDIB(body, w, h)
		}
		if err != nil || img.Bounds().Dx() != w || img.Bounds().Dy() != h {
			continue
		}
		if best == nil || w*h > best.Bounds().Dx()*best.Bounds().Dy() {
			best = img
		}
	}
	if best == nil {
		return nil, ErrIconFormat
	}
	return best, nil
}

func decodeIconDIB(raw []byte, width, height int) (image.Image, error) {
	if len(raw) < 40 {
		return nil, ErrIconFormat
	}
	header := int(binary.LittleEndian.Uint32(raw[:4]))
	// BITMAPINFOHEADER and its RGB extensions; compressed/bitfield layouts are
	// deliberately rejected instead of guessing their channel masks.
	if (header != 40 && header != 108 && header != 124) || header > len(raw) ||
		int32(binary.LittleEndian.Uint32(raw[4:8])) != int32(width) ||
		int32(binary.LittleEndian.Uint32(raw[8:12])) != int32(height*2) ||
		binary.LittleEndian.Uint16(raw[12:14]) != 1 || binary.LittleEndian.Uint32(raw[16:20]) != 0 {
		return nil, ErrIconFormat
	}
	bits := int(binary.LittleEndian.Uint16(raw[14:16]))
	if bits != 1 && bits != 4 && bits != 8 && bits != 24 && bits != 32 {
		return nil, ErrIconFormat
	}
	colors := uint64(binary.LittleEndian.Uint32(raw[32:36]))
	if bits <= 8 {
		if colors == 0 {
			colors = 1 << bits
		}
		if colors > 1<<bits {
			return nil, ErrIconFormat
		}
	}
	pixelOffset := uint64(header) + colors*4
	stride := (width*bits + 31) / 32 * 4
	maskStride := (width + 31) / 32 * 4
	maskOffset := pixelOffset + uint64(stride*height)
	if maskOffset > uint64(len(raw)) {
		return nil, ErrIconFormat
	}
	hasMask := maskOffset+uint64(maskStride*height) <= uint64(len(raw))
	if !hasMask && (bits != 32 || maskOffset != uint64(len(raw))) {
		return nil, ErrIconFormat
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	hasAlpha := false
	for y := 0; y < height; y++ {
		row := raw[int(pixelOffset)+(height-1-y)*stride:]
		for x := 0; x < width; x++ {
			c := color.NRGBA{A: 255}
			if bits <= 8 {
				index := int(row[x*bits/8]>>uint(8-bits-(x*bits%8))) & ((1 << bits) - 1)
				if uint64(index) >= colors {
					return nil, ErrIconFormat
				}
				p := raw[header+index*4:]
				c.R, c.G, c.B = p[2], p[1], p[0]
			} else {
				p := row[x*(bits/8):]
				c.R, c.G, c.B = p[2], p[1], p[0]
				if bits == 32 {
					c.A = p[3]
					hasAlpha = hasAlpha || c.A != 0
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// Older 32-bit icons leave alpha all zero and use the AND mask instead.
	// Alpha-bearing icons use their alpha channel, not a possibly stale mask.
	if bits != 32 || !hasAlpha {
		if !hasMask {
			return nil, ErrIconFormat
		}
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				alpha := uint8(255)
				if raw[int(maskOffset)+(height-1-y)*maskStride+x/8]&(0x80>>uint(x%8)) != 0 {
					alpha = 0
				}
				img.Pix[y*img.Stride+x*4+3] = alpha
			}
		}
	}
	return img, nil
}
