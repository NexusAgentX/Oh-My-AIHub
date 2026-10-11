package channel

import (
	"bytes"
	"sync"
	"sync/atomic"
)

// openedCredentials keeps the decrypted upstream key of each channel for the
// lifetime of the process (ADR-0036, decision 8), so the gateway does not
// build a cipher and open the same sealed key again on every attempt. It
// belongs to the Keyring that opens the keys and is shared by everything that
// shares that Keyring.
//
// There is one entry per channel, and an entry answers only for the exact
// sealed credential it was opened from (version, key ID, nonce and
// ciphertext). A channel that gets a new key, or whose key is resealed under
// another key ID, carries a different nonce and ciphertext, so the old entry
// stops matching on its own; it is dropped on the next use, and the channel
// service also drops it as soon as it writes a new key or deletes the channel.
// The cache therefore never holds more than one plaintext per channel and is
// bounded by the number of channels.
//
// The plaintext stays in process memory until the entry is dropped or the
// process ends; the maintainer accepted that cost (ADR-0036). It is never
// logged and never leaves the process: callers only hand it to the upstream
// request and to scrub, which removes it from error text.
type openedCredentials struct {
	entries sync.Map // channel ID -> *openedCredential
	// decrypts counts the credentials actually decrypted, hits excluded.
	decrypts atomic.Uint64
}

// openedCredential is the plaintext of one channel's key together with the
// sealed credential it came from. It is never modified after it is stored.
type openedCredential struct {
	version    int64
	keyID      string
	nonce      []byte
	ciphertext []byte
	plaintext  string
}

func newOpenedCredential(sealed EncryptedCredential, plaintext string) *openedCredential {
	return &openedCredential{
		version: sealed.Version, keyID: sealed.KeyID, plaintext: plaintext,
		// Copies, so a caller reusing its buffers cannot change what the entry
		// answers for.
		nonce: bytes.Clone(sealed.Nonce), ciphertext: bytes.Clone(sealed.Ciphertext),
	}
}

// matches reports whether sealed is exactly the credential this entry was
// opened from. The nonce is what tells two sealings of the same plaintext
// apart; the ciphertext is compared as well, so a stored value that was
// altered without its nonce changing is opened again (and fails) instead of
// being answered from memory.
func (c *openedCredential) matches(sealed EncryptedCredential) bool {
	return c.version == sealed.Version && c.keyID == sealed.KeyID &&
		bytes.Equal(c.nonce, sealed.Nonce) && bytes.Equal(c.ciphertext, sealed.Ciphertext)
}

// OpenCredential returns the plaintext of the upstream key of a channel. The
// first call for a sealed credential decrypts it (exactly as Decrypt does:
// the channel ID, version and key ID are part of what is authenticated);
// later calls with the same sealed credential are answered from memory.
// A failure is returned as it is and never cached. It is safe for concurrent
// use.
//
// Only channel upstream keys go through it, from the gateway. Platform API
// keys and everything else keep using Decrypt and are never held in memory.
func (k *Keyring) OpenCredential(channelID string, sealed EncryptedCredential) (string, error) {
	if value, found := k.opened.entries.Load(channelID); found {
		entry := value.(*openedCredential)
		if entry.matches(sealed) {
			return entry.plaintext, nil
		}
		// The channel's key is not the one that was opened: drop the stale
		// plaintext now, whether or not the new one can be opened.
		k.opened.entries.CompareAndDelete(channelID, value)
	}
	k.opened.decrypts.Add(1)
	plaintext, err := k.Decrypt(channelID, sealed)
	if err != nil {
		return "", err
	}
	k.opened.entries.Store(channelID, newOpenedCredential(sealed, plaintext))
	return plaintext, nil
}

// ForgetCredential drops what is held for a channel, for when its key is
// replaced or the channel is deleted. A nil Keyring ignores it. An attempt
// that read the channel before the change but opens its key only after this
// call can put the old entry back; the channel's next use replaces it, and for
// a deleted channel, which nothing routes to any more, it stays until the
// process ends.
func (k *Keyring) ForgetCredential(channelID string) {
	if k != nil {
		k.opened.entries.Delete(channelID)
	}
}

// CredentialDecryptions is the number of channel credentials decrypted by
// OpenCredential since the Keyring was made; a call answered from memory does
// not count.
func (k *Keyring) CredentialDecryptions() uint64 { return k.opened.decrypts.Load() }
