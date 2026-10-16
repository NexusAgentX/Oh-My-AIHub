package gateway

// mediaProjection keeps JSON structure/numbers and short strings for the usage
// observer, replacing long strings with "". It is never applied to bytes sent
// to the client. Base64 images can be arbitrarily long without occupying the
// observer's JSON/SSE buffers; keys, identifiers and usage numbers remain intact.
// One reusable output chunk and a 512-byte string scratch bound its memory.
type mediaProjection struct {
	inString, escaped, large bool
	token                    [512]byte
	used                     int
	output                   []byte
}

func (p *mediaProjection) write(chunk []byte) []byte {
	p.output = p.output[:0]
	for _, b := range chunk {
		if !p.inString {
			if b == '"' {
				p.inString = true
				p.used = 0
				p.large = false
				p.escaped = false
			} else {
				p.output = append(p.output, b)
			}
			continue
		}
		if b == '"' && !p.escaped {
			p.output = append(p.output, '"')
			if !p.large {
				p.output = append(p.output, p.token[:p.used]...)
			}
			p.output = append(p.output, '"')
			p.inString = false
			continue
		}
		if p.used < len(p.token) {
			p.token[p.used] = b
			p.used++
		} else {
			p.large = true
		}
		if b == '\\' && !p.escaped {
			p.escaped = true
		} else {
			p.escaped = false
		}
	}
	return p.output
}
