package c2c

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
)

func SanitizeImage(reader io.Reader) (SanitizedImage, error) {
	limited := io.LimitReader(reader, MaximumImageBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil || len(raw) == 0 || len(raw) > MaximumImageBytes {
		return SanitizedImage{}, ErrInvalidInput
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png") || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > MaximumImagePixels {
		return SanitizedImage{}, ErrInvalidInput
	}
	decoded, actualFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil || actualFormat != format {
		return SanitizedImage{}, ErrInvalidInput
	}
	var encoded bytes.Buffer
	var mime string
	switch format {
	case "jpeg":
		mime = "image/jpeg"
		err = jpeg.Encode(&encoded, decoded, &jpeg.Options{Quality: 90})
	case "png":
		mime = "image/png"
		err = png.Encode(&encoded, decoded)
	default:
		err = errors.New("unsupported image")
	}
	if err != nil || encoded.Len() == 0 || encoded.Len() > MaximumImageBytes {
		return SanitizedImage{}, ErrInvalidInput
	}
	clean := encoded.Bytes()
	return SanitizedImage{
		MIME: mime, Bytes: append([]byte(nil), clean...), SHA256: sha256.Sum256(clean),
		Width: config.Width, Height: config.Height,
	}, nil
}
