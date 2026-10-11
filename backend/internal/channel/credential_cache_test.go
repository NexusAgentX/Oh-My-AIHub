package channel

import (
	"context"
	"strings"
	"sync"
	"testing"
)

const (
	cacheChannelA = "00000000-0000-4000-8000-00000000000a"
	cacheChannelB = "00000000-0000-4000-8000-00000000000b"
	cacheSecret   = "sk-upstream-plaintext-1234567890"
)

// cacheKeyrings returns two keyrings over the same keys that differ in the
// active one, so a key sealed under one of them can be opened by the other.
func cacheKeyrings(t *testing.T) (first, second *Keyring) {
	t.Helper()
	keys := "k1=" + encodedKey(1) + ",k2=" + encodedKey(2)
	first, err := ParseKeyring(keys, "k1")
	if err != nil {
		t.Fatal(err)
	}
	second, err = ParseKeyring(keys, "k2")
	if err != nil {
		t.Fatal(err)
	}
	return first, second
}

func seal(t *testing.T, keyring *Keyring, channelID, plaintext string) EncryptedCredential {
	t.Helper()
	sealed, err := keyring.Encrypt(channelID, credentialVersion, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func open(t *testing.T, keyring *Keyring, channelID string, sealed EncryptedCredential) string {
	t.Helper()
	plaintext, err := keyring.OpenCredential(channelID, sealed)
	if err != nil {
		t.Fatalf("OpenCredential: %v", err)
	}
	return plaintext
}

func TestOpenCredentialDecryptsOnceAndThenAnswersFromMemory(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	sealed := seal(t, keyring, cacheChannelA, cacheSecret)
	for range 5 {
		if got := open(t, keyring, cacheChannelA, sealed); got != cacheSecret {
			t.Fatalf("OpenCredential = %q", got)
		}
	}
	if got := keyring.CredentialDecryptions(); got != 1 {
		t.Fatalf("decrypted %d times for the same sealed credential, want once", got)
	}
	// A copy of the same sealed credential (another read of the same row) is
	// the same credential.
	reread := EncryptedCredential{
		Version: sealed.Version, KeyID: sealed.KeyID,
		Nonce: append([]byte(nil), sealed.Nonce...), Ciphertext: append([]byte(nil), sealed.Ciphertext...),
	}
	open(t, keyring, cacheChannelA, reread)
	if got := keyring.CredentialDecryptions(); got != 1 {
		t.Fatalf("a re-read row decrypted again: %d", got)
	}
	// Decrypt itself is not cached: it is what the other callers use.
	if _, err := keyring.Decrypt(cacheChannelA, sealed); err != nil {
		t.Fatal(err)
	}
	if got := keyring.CredentialDecryptions(); got != 1 {
		t.Fatalf("Decrypt counted as an open: %d", got)
	}
}

func TestOpenCredentialDoesNotKeepWhatItWasNotAskedFor(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	sealed := seal(t, keyring, cacheChannelA, cacheSecret)
	open(t, keyring, cacheChannelA, sealed)
	// Another channel presenting the first channel's sealed credential must
	// not be answered from the first channel's entry, nor from anywhere.
	if _, err := keyring.OpenCredential(cacheChannelB, sealed); err == nil {
		t.Fatal("a credential sealed for one channel opened for another")
	}
	if _, found := keyring.opened.entries.Load(cacheChannelB); found {
		t.Fatal("a failed open was cached")
	}
	// The entry of the first channel survives the other channel's attempt.
	before := keyring.CredentialDecryptions()
	open(t, keyring, cacheChannelA, sealed)
	if keyring.CredentialDecryptions() != before {
		t.Fatal("an unrelated failure evicted a valid entry")
	}
}

func TestOpenCredentialFollowsTheSealedCredential(t *testing.T) {
	first, second := cacheKeyrings(t)

	t.Run("a new key", func(t *testing.T) {
		keyring, _ := cacheKeyrings(t)
		old := seal(t, keyring, cacheChannelA, "old-key")
		replaced := seal(t, keyring, cacheChannelA, "new-key")
		if got := open(t, keyring, cacheChannelA, old); got != "old-key" {
			t.Fatalf("got %q", got)
		}
		if got := open(t, keyring, cacheChannelA, replaced); got != "new-key" {
			t.Fatalf("got %q after the channel's key was replaced", got)
		}
		if keyring.CredentialDecryptions() != 2 {
			t.Fatalf("decryptions = %d, want 2", keyring.CredentialDecryptions())
		}
		// The old plaintext is gone with its entry, not served again.
		if got := open(t, keyring, cacheChannelA, replaced); got != "new-key" {
			t.Fatalf("got %q", got)
		}
		value, found := keyring.opened.entries.Load(cacheChannelA)
		if !found || value.(*openedCredential).plaintext != "new-key" {
			t.Fatal("the entry still holds the old plaintext")
		}
	})

	t.Run("the same key sealed again", func(t *testing.T) {
		keyring, _ := cacheKeyrings(t)
		one := seal(t, keyring, cacheChannelA, cacheSecret)
		two := seal(t, keyring, cacheChannelA, cacheSecret)
		open(t, keyring, cacheChannelA, one)
		open(t, keyring, cacheChannelA, two)
		if keyring.CredentialDecryptions() != 2 {
			t.Fatalf("a new nonce did not cause a new decryption: %d", keyring.CredentialDecryptions())
		}
	})

	t.Run("another keyring key", func(t *testing.T) {
		// Sealed under k1, then resealed under k2: same plaintext, new key ID.
		viaK1 := seal(t, first, cacheChannelA, cacheSecret)
		viaK2 := seal(t, second, cacheChannelA, cacheSecret)
		if viaK1.KeyID == viaK2.KeyID {
			t.Fatal("test setup: key IDs are equal")
		}
		keyring, _ := cacheKeyrings(t)
		open(t, keyring, cacheChannelA, viaK1)
		open(t, keyring, cacheChannelA, viaK2)
		if keyring.CredentialDecryptions() != 2 {
			t.Fatalf("a new key ID did not cause a new decryption: %d", keyring.CredentialDecryptions())
		}
		// Same nonce and ciphertext claimed under another key ID is another
		// credential, and it does not open.
		forged := viaK2
		forged.KeyID = viaK1.KeyID
		if _, err := keyring.OpenCredential(cacheChannelA, forged); err == nil {
			t.Fatal("a credential relabelled with another key ID opened")
		}
	})
}

func TestOpenCredentialDoesNotTrustTheNonceAlone(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	sealed := seal(t, keyring, cacheChannelA, cacheSecret)
	open(t, keyring, cacheChannelA, sealed)

	altered := sealed
	altered.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
	altered.Ciphertext[0] ^= 1
	if _, err := keyring.OpenCredential(cacheChannelA, altered); err == nil {
		t.Fatal("a ciphertext altered under an unchanged nonce was answered from memory")
	}
	if _, found := keyring.opened.entries.Load(cacheChannelA); found {
		t.Fatal("the stale entry of a credential that no longer matches was kept")
	}
	// The genuine one opens again, at the price of one more decryption.
	if got := open(t, keyring, cacheChannelA, sealed); got != cacheSecret {
		t.Fatalf("got %q", got)
	}
	wrongVersion := sealed
	wrongVersion.Version++
	if _, err := keyring.OpenCredential(cacheChannelA, wrongVersion); err == nil {
		t.Fatal("a credential of another version opened")
	}
}

func TestOpenCredentialDoesNotCacheFailures(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	other, err := ParseKeyring("other="+encodedKey(9), "other")
	if err != nil {
		t.Fatal(err)
	}
	foreign := seal(t, other, cacheChannelA, cacheSecret)
	for range 3 {
		if _, err := keyring.OpenCredential(cacheChannelA, foreign); err == nil {
			t.Fatal("a credential of an unknown key ID opened")
		}
	}
	if keyring.CredentialDecryptions() != 3 {
		t.Fatalf("a failure was answered from memory: %d decryptions", keyring.CredentialDecryptions())
	}
	if _, found := keyring.opened.entries.Load(cacheChannelA); found {
		t.Fatal("a failed open left an entry")
	}
}

func TestForgetCredentialDropsTheEntry(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	sealed := seal(t, keyring, cacheChannelA, cacheSecret)
	other := seal(t, keyring, cacheChannelB, "other-secret")
	open(t, keyring, cacheChannelA, sealed)
	open(t, keyring, cacheChannelB, other)

	keyring.ForgetCredential(cacheChannelA)
	if _, found := keyring.opened.entries.Load(cacheChannelA); found {
		t.Fatal("the entry survived ForgetCredential")
	}
	if _, found := keyring.opened.entries.Load(cacheChannelB); !found {
		t.Fatal("ForgetCredential dropped another channel's entry")
	}
	before := keyring.CredentialDecryptions()
	open(t, keyring, cacheChannelA, sealed)
	if keyring.CredentialDecryptions() != before+1 {
		t.Fatal("a forgotten credential was not decrypted again")
	}
	keyring.ForgetCredential("never-opened")
	(*Keyring)(nil).ForgetCredential(cacheChannelA)
}

func TestOpenCredentialIsSafeForConcurrentUse(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	sealed := []EncryptedCredential{
		seal(t, keyring, cacheChannelA, "key-0"),
		seal(t, keyring, cacheChannelA, "key-1"),
		seal(t, keyring, cacheChannelA, "key-2"),
	}
	var group sync.WaitGroup
	for worker := range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			for step := range 500 {
				index := (worker + step) % len(sealed)
				got, err := keyring.OpenCredential(cacheChannelA, sealed[index])
				if err != nil || got != "key-"+string(rune('0'+index)) {
					t.Errorf("OpenCredential = %q, %v; want key-%d", got, err, index)
					return
				}
				if step%97 == 0 {
					keyring.ForgetCredential(cacheChannelA)
				}
			}
		}()
	}
	group.Wait()
}

func TestOpenCredentialErrorsCarryNoPlaintext(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	sealed := seal(t, keyring, cacheChannelA, cacheSecret)
	open(t, keyring, cacheChannelA, sealed)

	altered := sealed
	altered.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
	altered.Ciphertext[len(altered.Ciphertext)-1] ^= 1
	unknown := sealed
	unknown.KeyID = "gone"
	short := sealed
	short.Nonce = sealed.Nonce[:3]
	for name, bad := range map[string]EncryptedCredential{"altered": altered, "unknown key": unknown, "short nonce": short} {
		_, err := keyring.OpenCredential(cacheChannelA, bad)
		if err == nil {
			t.Fatalf("%s: opened", name)
		}
		if strings.Contains(err.Error(), cacheSecret) {
			t.Fatalf("%s: the error carries the plaintext: %v", name, err)
		}
	}
}

// ---- the service drops the entry when it writes ----

// writeStore answers the few calls Update and Delete make.
type writeStore struct {
	Store
	owner   string
	updated bool
	deleted bool
	failure error
}

func (s *writeStore) Get(context.Context, string) (Channel, error) {
	return Channel{Owner: Owner{ID: s.owner}}, nil
}

func (s *writeStore) Update(context.Context, string, Update) (Channel, error) {
	s.updated = s.failure == nil
	return Channel{}, s.failure
}

func (s *writeStore) SoftDelete(context.Context, string) error {
	s.deleted = s.failure == nil
	return s.failure
}

func opened(keyring *Keyring, channelID string) bool {
	_, found := keyring.opened.entries.Load(channelID)
	return found
}

func TestDeletingAChannelDropsItsOpenedKey(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	store := &writeStore{owner: "owner"}
	service := NewService(Dependencies{Store: store, Keyring: keyring})
	open(t, keyring, cacheChannelA, seal(t, keyring, cacheChannelA, cacheSecret))
	open(t, keyring, cacheChannelB, seal(t, keyring, cacheChannelB, "other-secret"))

	if err := service.Delete(context.Background(), "owner", cacheChannelA); err != nil {
		t.Fatal(err)
	}
	if !store.deleted || opened(keyring, cacheChannelA) {
		t.Fatalf("deleted %v, plaintext still held %v", store.deleted, opened(keyring, cacheChannelA))
	}
	if !opened(keyring, cacheChannelB) {
		t.Fatal("deleting one channel dropped another's key")
	}
}

func TestADeleteThatFailedKeepsTheOpenedKey(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	store := &writeStore{owner: "owner", failure: ErrNotFound}
	service := NewService(Dependencies{Store: store, Keyring: keyring})
	open(t, keyring, cacheChannelA, seal(t, keyring, cacheChannelA, cacheSecret))
	if err := service.Delete(context.Background(), "owner", cacheChannelA); err == nil {
		t.Fatal("delete did not fail")
	}
	if !opened(keyring, cacheChannelA) {
		t.Fatal("a delete that did not happen dropped the key")
	}
	// A foreign owner never reaches the cache.
	service = NewService(Dependencies{Store: &writeStore{owner: "owner"}, Keyring: keyring})
	if err := service.Delete(context.Background(), "intruder", cacheChannelA); err == nil || !opened(keyring, cacheChannelA) {
		t.Fatalf("err = %v, held = %v", err, opened(keyring, cacheChannelA))
	}
}

func TestReplacingAChannelKeyDropsTheOldOne(t *testing.T) {
	keyring, _ := cacheKeyrings(t)
	store := &writeStore{owner: "owner"}
	service := NewService(Dependencies{Store: store, Keyring: keyring})
	open(t, keyring, cacheChannelA, seal(t, keyring, cacheChannelA, cacheSecret))

	// Updating something else leaves the key alone.
	name := "renamed"
	if _, err := service.Update(context.Background(), "owner", cacheChannelA, UpdateInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if !opened(keyring, cacheChannelA) {
		t.Fatal("renaming a channel dropped its key")
	}
	key := "sk-replacement"
	if _, err := service.Update(context.Background(), "owner", cacheChannelA, UpdateInput{APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	if !store.updated || opened(keyring, cacheChannelA) {
		t.Fatalf("updated %v, old plaintext still held %v", store.updated, opened(keyring, cacheChannelA))
	}

	// An update that failed in the store did not replace the key.
	open(t, keyring, cacheChannelA, seal(t, keyring, cacheChannelA, cacheSecret))
	store.failure = ErrNotFound
	if _, err := service.Update(context.Background(), "owner", cacheChannelA, UpdateInput{APIKey: &key}); err == nil {
		t.Fatal("update did not fail")
	}
	if !opened(keyring, cacheChannelA) {
		t.Fatal("a failed update dropped the key")
	}
}
