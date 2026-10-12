package supplier

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const maxIconBytes = 256 << 10

// NormalizeIcon decodes and re-encodes raster data. Executable SVG/HTML and
// oversized images are never stored or returned to a browser.
func NormalizeIcon(data string) (string, error) {
	if len(data) > 350000 {
		return "", ErrInvalid
	}
	prefix, raw, ok := strings.Cut(data, ",")
	if !ok || !strings.HasPrefix(prefix, "data:image/") || !strings.HasSuffix(prefix, ";base64") {
		return "", ErrInvalid
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", ErrInvalid
	}
	return rasterIcon(decoded)
}
func rasterIcon(raw []byte) (string, error) {
	if len(raw) > maxIconBytes {
		return "", ErrInvalid
	}
	// ICO files can contain PNG images. Unsupported bitmap-only ICOs fall back
	// to another website icon, or the directory's initial-letter placeholder.
	if len(raw) >= 22 && bytes.Equal(raw[:4], []byte{0, 0, 1, 0}) {
		count := int(binary.LittleEndian.Uint16(raw[4:6]))
		for i := 0; i < count && 6+(i+1)*16 <= len(raw); i++ {
			entry := raw[6+i*16 : 6+(i+1)*16]
			size, offset := uint64(binary.LittleEndian.Uint32(entry[8:12])), uint64(binary.LittleEndian.Uint32(entry[12:16]))
			if offset+size <= uint64(len(raw)) && size >= 8 && bytes.Equal(raw[offset:offset+8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
				raw = raw[offset : offset+size]
				break
			}
		}
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return "", ErrInvalid
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", ErrInvalid
	}
	// Small, bounded local copy; no new image processing dependency.
	width, height := config.Width, config.Height
	if width > 128 || height > 128 {
		if width >= height {
			height = max(1, height*128/width)
			width = 128
		} else {
			width = max(1, width*128/height)
			height = 128
		}
	}
	thumbnail := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			thumbnail.Set(x, y, img.At(img.Bounds().Min.X+x*config.Width/width, img.Bounds().Min.Y+y*config.Height/height))
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, thumbnail); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

// FetchIcon is administrator-only at the HTTP boundary. Each request uses
// the existing DNS-pinned egress policy; redirects, credentials and proxies
// are not permitted. No website is contacted during directory browsing.
func (s *Service) FetchIcon(ctx context.Context, raw string) (string, string, error) {
	if !WebURL(raw, false) {
		return "", "", ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	website, _ := url.Parse(raw)
	candidates := []string{}
	page, err := s.fetch(ctx, website.String(), 512<<10)
	if err == nil {
		tokenizer := html.NewTokenizer(bytes.NewReader(page))
		for {
			token := tokenizer.Next()
			if token == html.ErrorToken {
				break
			}
			if token != html.StartTagToken && token != html.SelfClosingTagToken {
				continue
			}
			node := tokenizer.Token()
			if node.Data != "link" {
				continue
			}
			rel, href := "", ""
			for _, a := range node.Attr {
				if a.Key == "rel" {
					rel = strings.ToLower(a.Val)
				}
				if a.Key == "href" {
					href = a.Val
				}
			}
			if (rel == "icon" || rel == "shortcut icon" || rel == "apple-touch-icon") && href != "" {
				if u, e := url.Parse(href); e == nil {
					candidates = append(candidates, website.ResolveReference(u).String())
				}
			}
			if len(candidates) == 4 {
				break
			}
		}
	}
	for _, path := range []string{"/apple-touch-icon.png", "/favicon.png", "/favicon.ico"} {
		candidates = append(candidates, website.ResolveReference(&url.URL{Path: path}).String())
	}
	for _, candidate := range candidates {
		body, e := s.fetch(ctx, candidate, maxIconBytes)
		if e != nil {
			continue
		}
		icon, e := rasterIcon(body)
		if e == nil {
			return icon, candidate, nil
		}
	}
	return "", "", ErrInvalid
}
func (s *Service) fetch(ctx context.Context, raw string, limit int64) ([]byte, error) {
	if !WebURL(raw, false) {
		return nil, ErrInvalid
	}
	u, _ := url.Parse(raw)
	origin := u.Scheme + "://" + u.Host
	base, err := s.Outbound.NormalizeBaseURL(origin)
	if err != nil {
		return nil, err
	}
	client, err := s.Outbound.FreshClient(ctx, base)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, ErrInvalid
	}
	return body, nil
}
