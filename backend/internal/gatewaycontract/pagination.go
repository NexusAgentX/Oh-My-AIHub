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
	// Stop ends this bounded scan while preserving an encrypted continuation.
	// Adapters use it when the shared outbound-attempt budget is exhausted.
	Stop bool
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
// Scopes keep their Binding, so a continuation cannot create source-channel
// stickiness.
func ReadOwnedPage(ctx context.Context, actor Principal, scope CursorScope, token string, limit int,
	codec *CursorCodec, fetch FetchPage, lookup Lookup) (OwnedPage, error) {
	if err := scope.Authorize(actor, scope); err != nil {
		return OwnedPage{}, err
	}
	if codec == nil || fetch == nil || lookup == nil || limit < 1 || limit > MaxPageItems || scope.Kind == "" || scope.QueryDigest == "" {
		return OwnedPage{}, ErrPage
	}
	if !remoteResource(scope.Kind) ||
		scope.Binding.GroupID == "" || scope.Binding.ChannelID == "" || scope.Binding.CredentialVersion == "" {
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
		if len(result.Items) == limit || page.Stop {
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

// WrapPageCursor carries the authenticated scope needed to select a bound
// channel before ReadOwnedPage opens its native continuation. Native IDs and
// engine metadata remain encrypted; caller-controlled routing hints are absent.
func (c *CursorCodec) WrapPageCursor(scope CursorScope, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	if c == nil || len(token) > 4096 {
		return "", ErrPage
	}
	raw, err := json.Marshal(struct {
		Scope CursorScope `json:"scope"`
		Token string      `json:"token"`
	}{scope, token})
	if err != nil || len(raw) > 10<<10 {
		return "", ErrPage
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(c.aead.Seal(nonce, nonce, raw, []byte("aihub-resource-page-v1"))), nil
}
func (c *CursorCodec) UnwrapPageCursor(actor Principal, token string) (CursorScope, string, error) {
	if c == nil || len(token) > 16<<10 {
		return CursorScope{}, "", ErrPage
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) < c.aead.NonceSize()+c.aead.Overhead() {
		return CursorScope{}, "", ErrPage
	}
	n := c.aead.NonceSize()
	raw, err := c.aead.Open(nil, data[:n], data[n:], []byte("aihub-resource-page-v1"))
	if err != nil {
		return CursorScope{}, "", ErrPage
	}
	var page struct {
		Scope CursorScope `json:"scope"`
		Token string      `json:"token"`
	}
	if json.Unmarshal(raw, &page) != nil || page.Scope.Authorize(actor, page.Scope) != nil || len(page.Token) > 4096 || page.Token == "" {
		return CursorScope{}, "", ErrPage
	}
	return page.Scope, page.Token, nil
}

// OwnedBoundary admits an SDK's own visible last_id as a boundary after the
// handler has resolved its durable ownership. Shared-account raw cursors and
// client ownership claims must never be passed as resource metadata.
func (c *CursorCodec) OwnedBoundary(actor Principal, scope CursorScope, resource Resource) (string, error) {
	if c == nil || scope.Authorize(actor, scope) != nil || resource.AccountID != actor.AccountID || resource.KeyID != actor.KeyID || resource.Kind != scope.Kind || resource.ParentID != scope.ParentID || resource.Binding != scope.Binding || resource.CredentialScope == "" || resource.CreationID == "" {
		return "", ErrPage
	}
	return c.seal(scope, resource.ID)
}
