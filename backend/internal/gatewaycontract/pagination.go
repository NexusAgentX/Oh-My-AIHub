package gatewaycontract

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	MaxPageItems   = 100
	MaxScanPages   = 4
	MaxNativeToken = 2048
	MaxPageBytes   = 1 << 20
)

var ErrPage = errors.New("invalid or unavailable resource page")

// CursorCodec is a discovery prototype. F7 must load its encryption key from
// durable protected configuration; a process-local random key is not sufficient
// for restart-safe pagination. Native cursors are metadata, never message bodies.
type CursorCodec struct{ aead cipher.AEAD }

func NewCursorCodec(key []byte) (*CursorCodec, error) {
	if len(key) != 32 {
		return nil, ErrPage
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CursorCodec{aead}, nil
}

func (c *CursorCodec) seal(scope CursorScope, native string) (string, error) {
	if len(native) == 0 || len(native) > MaxNativeToken {
		return "", ErrPage
	}
	ad, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(c.aead.Seal(nonce, nonce, []byte(native), ad)), nil
}

func (c *CursorCodec) open(scope CursorScope, token string) (string, error) {
	if len(token) > 4096 {
		return "", ErrPage
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", ErrPage
	}
	ad, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	n := c.aead.NonceSize()
	native, err := c.aead.Open(nil, data[:n], data[n:], ad)
	if err != nil || len(native) == 0 || len(native) > MaxNativeToken {
		return "", ErrPage
	}
	return string(native), nil
}

type ListedResource struct {
	Reference Reference
	// The native adapter must derive Reference from these same bounded bytes.
	Raw json.RawMessage
}

type NativePage struct {
	Items []ListedResource
	Next  string
}

// FetchPage must bound network/body reads before allocating a page, request at
// most limit items and omit native totals/first/last IDs from the public result.
type FetchPage func(context.Context, string, int) (NativePage, error)

type OwnedPage struct {
	Items []ListedResource
	Next  string
}

// ReadOwnedPage demonstrates bounded server-side filtering. Four native pages
// may yield an empty page with a continuation; the client can continue without
// an unbounded scan or exposure of another owner's IDs, counts or cursor.
// Standard-resource scopes keep Binding; Codex context scopes leave it empty,
// so a continuation cannot create source-channel stickiness.
func ReadOwnedPage(ctx context.Context, actor Principal, scope CursorScope, token string, limit int,
	codec *CursorCodec, fetch FetchPage, lookup Lookup) (OwnedPage, error) {
	if err := scope.Authorize(actor, scope); err != nil {
		return OwnedPage{}, err
	}
	if codec == nil || fetch == nil || lookup == nil || limit < 1 || limit > MaxPageItems || scope.Kind == "" || scope.QueryDigest == "" {
		return OwnedPage{}, ErrPage
	}
	if (!remoteResource(scope.Kind) && !contextOnly(scope.Kind)) ||
		(remoteResource(scope.Kind) && (scope.Binding.GroupID == "" || scope.Binding.ChannelID == "" || scope.Binding.CredentialVersion == "")) ||
		(contextOnly(scope.Kind) && scope.Binding != (Binding{})) {
		return OwnedPage{}, ErrConflict
	}
	native := ""
	if token != "" {
		var err error
		native, err = codec.open(scope, token)
		if err != nil {
			return OwnedPage{}, err
		}
	}
	result := OwnedPage{Items: []ListedResource{}}
	seen := map[string]bool{native: true}
	bytes := 0
	for range MaxScanPages {
		if err := ctx.Err(); err != nil {
			return OwnedPage{}, err
		}
		page, err := fetch(ctx, native, limit-len(result.Items))
		if err != nil {
			return OwnedPage{}, err
		}
		if len(page.Items) > limit-len(result.Items) || len(page.Next) > MaxNativeToken {
			return OwnedPage{}, ErrPage
		}
		for _, item := range page.Items {
			bytes += len(item.Raw)
			if bytes > MaxPageBytes || !json.Valid(item.Raw) || item.Reference.Kind != scope.Kind || item.Reference.ParentID != scope.ParentID {
				return OwnedPage{}, ErrPage
			}
			bound, err := ResolveReferences(actor, []Reference{item.Reference}, lookup)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return OwnedPage{}, err
			}
			if (bound == nil && scope.Binding != (Binding{})) || (bound != nil && *bound != scope.Binding) {
				return OwnedPage{}, ErrConflict
			}
			result.Items = append(result.Items, item)
		}
		if page.Next == "" {
			return result, nil
		}
		if seen[page.Next] {
			return OwnedPage{}, fmt.Errorf("%w: repeated continuation", ErrPage)
		}
		seen[page.Next] = true
		native = page.Next
		if len(result.Items) == limit {
			break
		}
	}
	var err error
	result.Next, err = codec.seal(scope, native)
	if err != nil {
		return OwnedPage{}, err
	}
	return result, nil
}
