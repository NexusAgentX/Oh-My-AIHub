package channelprobe

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

type ImageProperties struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Format      string `json:"format"`
	Transparent bool   `json:"transparent"`
}

func inspectImage(data string) (ImageProperties, bool) {
	p, _, ok := inspectImagePixels(data)
	return p, ok
}

func inspectImagePixels(data string) (ImageProperties, image.Image, bool) {
	if len(data) == 0 || base64.StdEncoding.DecodedLen(len(data)) > 24<<20 {
		return ImageProperties{}, nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return ImageProperties{}, nil, false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	// Check before allocating decoded pixels. Inspection runs only on bounded
	// background detection workers; a compressed image cannot allocate arbitrarily.
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 16384 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > 16<<20 {
		return ImageProperties{}, nil, false
	}
	decoded, actual, err := image.Decode(bytes.NewReader(raw))
	if err != nil || actual != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return ImageProperties{}, nil, false
	}
	p := ImageProperties{Width: config.Width, Height: config.Height, Format: format}
	// A bounded 64x64 grid suffices for alpha observation; absent sampled alpha
	// is not a proof of an opaque image. Transparent options require positive proof.
	for y := 0; y < config.Height; y += max(1, config.Height/64) {
		for x := 0; x < config.Width; x += max(1, config.Width/64) {
			_, _, _, alpha := decoded.At(x+decoded.Bounds().Min.X, y+decoded.Bounds().Min.Y).RGBA()
			p.Transparent = p.Transparent || alpha < 65535
		}
	}
	return p, decoded, true
}
