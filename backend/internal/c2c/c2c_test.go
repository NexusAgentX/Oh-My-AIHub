package c2c

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestFiatAmountFenRoundsUpWithoutFloatingPoint(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		quantity money.Amount
		price    int64
		want     int64
	}{
		{quantity: money.FromNano(1), price: 1, want: 1},
		{quantity: money.FromNano(money.Scale), price: 100, want: 100},
		{quantity: money.FromNano(money.Scale + 1), price: 100, want: 101},
		{quantity: money.FromNano(250_000_000), price: 101, want: 26},
	} {
		got, err := FiatAmountFen(test.quantity, test.price)
		if err != nil || got != test.want {
			t.Fatalf("FiatAmountFen(%d, %d) = %d, %v; want %d", test.quantity, test.price, got, err, test.want)
		}
	}
	if _, err := FiatAmountFen(0, 100); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero quantity error = %v", err)
	}
}

func TestKeyringUsesRecordAndPurposeSeparatedAAD(t *testing.T) {
	t.Parallel()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	keyring, err := ParseKeyring("v1="+key, "v1")
	if err != nil {
		t.Fatalf("parse keyring: %v", err)
	}
	encrypted, err := keyring.Encrypt("record-1", "payment_method", []byte("private contact"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := keyring.Decrypt("record-1", "payment_method", encrypted)
	if err != nil || string(plaintext) != "private contact" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	if _, err := keyring.Decrypt("record-2", "payment_method", encrypted); err == nil {
		t.Fatal("record AAD substitution unexpectedly decrypted")
	}
	if _, err := keyring.Decrypt("record-1", "evidence:payment", encrypted); err == nil {
		t.Fatal("purpose AAD substitution unexpectedly decrypted")
	}
}

func TestSanitizeImageReencodesJPEGAndRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()
	original := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			original.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 30), B: 80, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, original, nil); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	jpegBytes := encoded.Bytes()
	exifPayload := []byte("Exif\x00\x00sensitive-location")
	app1 := make([]byte, 4+len(exifPayload))
	app1[0], app1[1] = 0xff, 0xe1
	binary.BigEndian.PutUint16(app1[2:4], uint16(len(exifPayload)+2))
	copy(app1[4:], exifPayload)
	withEXIF := append(append(append([]byte{}, jpegBytes[:2]...), app1...), jpegBytes[2:]...)

	clean, err := SanitizeImage(bytes.NewReader(withEXIF))
	if err != nil {
		t.Fatalf("sanitize JPEG: %v", err)
	}
	if clean.MIME != "image/jpeg" || clean.Width != 8 || clean.Height != 6 || bytes.Contains(clean.Bytes, []byte("sensitive-location")) {
		t.Fatalf("sanitized image = mime %s, %dx%d, metadata present %v", clean.MIME, clean.Width, clean.Height, bytes.Contains(clean.Bytes, []byte("sensitive-location")))
	}
	if _, err := SanitizeImage(bytes.NewReader([]byte("not an image"))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("fake MIME error = %v", err)
	}
	if _, err := SanitizeImage(bytes.NewReader(bytes.Repeat([]byte{'x'}, MaximumImageBytes+1))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized input error = %v", err)
	}
	if _, err := SanitizeImage(bytes.NewReader(pngHeader(5_000, 5_000))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("pixel bomb error = %v", err)
	}
}

func pngHeader(width, height uint32) []byte {
	result := append([]byte{}, []byte("\x89PNG\r\n\x1a\n")...)
	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8], data[9], data[10], data[11], data[12] = 8, 2, 0, 0, 0
	result = appendPNGChunk(result, "IHDR", data)
	return appendPNGChunk(result, "IEND", nil)
}

func appendPNGChunk(target []byte, kind string, data []byte) []byte {
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	target = append(target, length...)
	payload := append([]byte(kind), data...)
	target = append(target, payload...)
	checksum := make([]byte, 4)
	binary.BigEndian.PutUint32(checksum, crc32.ChecksumIEEE(payload))
	return append(target, checksum...)
}
