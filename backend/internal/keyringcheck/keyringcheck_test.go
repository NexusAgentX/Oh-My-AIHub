package keyringcheck

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

const (
	upstreamSecret = "sk-upstream-very-secret-value"
	platformSecret = "sk-aih-platform-very-secret-value"
	paymentSecret  = `[{"channel":"alipay","account":"seller-very-secret@example.com"}]`

	// The AEAD versions and the C2C purpose are part of what is stored; the
	// literals here guard against changing them without a migration.
	credentialVersion = 1
	paymentPurpose    = "payment_methods"
)

// keyMaterial derives the 32 key bytes from the key ID and a seed, so the same
// ID always has the same key unless a test asks for different material.
func keyMaterial(id, seed string) string {
	sum := sha256.Sum256([]byte(seed + "/" + id))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func upstreamKeyring(t *testing.T, ids ...string) *channel.Keyring {
	t.Helper()
	return upstreamKeyringSeeded(t, "", ids...)
}

// upstreamKeyringSeeded holds the given key IDs, the last one active.
func upstreamKeyringSeeded(t *testing.T, seed string, ids ...string) *channel.Keyring {
	t.Helper()
	entries := make([]string, len(ids))
	for index, id := range ids {
		entries[index] = id + "=" + keyMaterial(id, seed)
	}
	keyring, err := channel.ParseKeyring(strings.Join(entries, ","), ids[len(ids)-1])
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func privateKeyring(t *testing.T, ids ...string) *c2c.Keyring {
	t.Helper()
	entries := make([]string, len(ids))
	for index, id := range ids {
		entries[index] = id + "=" + keyMaterial(id, "private")
	}
	keyring, err := c2c.ParseKeyring(strings.Join(entries, ","), ids[len(ids)-1])
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func recordID(number int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", number) }

// credentialStore is an in-memory CredentialSource with the paging contract.
type credentialStore struct {
	rows  []channel.StoredCredential
	calls int
	err   error
}

func (s *credentialStore) add(t *testing.T, keyring *channel.Keyring, id, secret string) channel.StoredCredential {
	t.Helper()
	sealed, err := keyring.Encrypt(id, credentialVersion, secret)
	if err != nil {
		t.Fatal(err)
	}
	row := channel.StoredCredential{RecordID: id, Sealed: sealed}
	s.rows = append(s.rows, row)
	return row
}

func (s *credentialStore) CredentialsAfter(_ context.Context, afterID string, limit int) ([]channel.StoredCredential, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	sort.Slice(s.rows, func(i, j int) bool { return s.rows[i].RecordID < s.rows[j].RecordID })
	var page []channel.StoredCredential
	for _, row := range s.rows {
		if row.RecordID > afterID && len(page) < limit {
			page = append(page, row)
		}
	}
	return page, nil
}

type methodsStore struct {
	rows  []c2c.StoredMethods
	calls int
}

func (s *methodsStore) add(t *testing.T, keyring *c2c.Keyring, id string) c2c.StoredMethods {
	t.Helper()
	sealed, err := keyring.Encrypt(id, paymentPurpose, []byte(paymentSecret))
	if err != nil {
		t.Fatal(err)
	}
	row := c2c.StoredMethods{OrderID: id, Sealed: sealed}
	s.rows = append(s.rows, row)
	return row
}

func (s *methodsStore) OrderMethodsAfter(_ context.Context, afterID string, limit int) ([]c2c.StoredMethods, error) {
	s.calls++
	sort.Slice(s.rows, func(i, j int) bool { return s.rows[i].OrderID < s.rows[j].OrderID })
	var page []c2c.StoredMethods
	for _, row := range s.rows {
		if row.OrderID > afterID && len(page) < limit {
			page = append(page, row)
		}
	}
	return page, nil
}

type stores struct {
	channels, apiKeys *credentialStore
	orders            *methodsStore
}

func newStores() *stores {
	return &stores{channels: &credentialStore{}, apiKeys: &credentialStore{}, orders: &methodsStore{}}
}

func (s *stores) sources() Sources {
	return Sources{Channels: s.channels, APIKeys: s.apiKeys, C2COrders: s.orders}
}

func TestVerifyPassesWhenEveryKeyIDIsInTheKeyrings(t *testing.T) {
	oldUpstream, currentUpstream := upstreamKeyring(t, "v1"), upstreamKeyring(t, "v1", "v2")
	oldPrivate, currentPrivate := privateKeyring(t, "c1"), privateKeyring(t, "c1", "c2")
	data := newStores()
	// More channels than one page, written under both the old and the current key.
	for number := 1; number <= 2*pageSize+7; number++ {
		keyring := oldUpstream
		if number%2 == 0 {
			keyring = currentUpstream
		}
		data.channels.add(t, keyring, recordID(number), upstreamSecret)
	}
	data.apiKeys.add(t, oldUpstream, recordID(1), platformSecret)
	data.apiKeys.add(t, currentUpstream, recordID(2), platformSecret)
	data.orders.add(t, oldPrivate, recordID(1))
	data.orders.add(t, currentPrivate, recordID(2))

	report, err := Verify(context.Background(), data.sources(), Keyrings{Upstream: currentUpstream, C2C: currentPrivate})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report != (Report{Channels: 2*pageSize + 7, APIKeys: 2, C2COrders: 2}) {
		t.Fatalf("report = %+v", report)
	}
	if data.channels.calls != 3 {
		t.Fatalf("channel pages = %d, want 3", data.channels.calls)
	}
}

func TestVerifyAcceptsEmptyStores(t *testing.T) {
	report, err := Verify(context.Background(), newStores().sources(), Keyrings{Upstream: upstreamKeyring(t, "v1"), C2C: privateKeyring(t, "c1")})
	if err != nil || report != (Report{}) {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
}

func TestVerifyNamesEveryRecordWhoseKeyIDIsMissing(t *testing.T) {
	historical, current := upstreamKeyring(t, "v1"), upstreamKeyring(t, "v2")
	historicalPrivate, currentPrivate := privateKeyring(t, "c1"), privateKeyring(t, "c2")
	data := newStores()
	lostChannel := data.channels.add(t, historical, recordID(1), upstreamSecret)
	data.channels.add(t, current, recordID(2), upstreamSecret)
	lostKey := data.apiKeys.add(t, historical, recordID(3), platformSecret)
	data.apiKeys.add(t, current, recordID(4), platformSecret)
	lostOrder := data.orders.add(t, historicalPrivate, recordID(5))
	data.orders.add(t, currentPrivate, recordID(6))

	// Both keyrings were restored without their historical key.
	report, err := Verify(context.Background(), data.sources(), Keyrings{Upstream: current, C2C: currentPrivate})
	var failed *Error
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if report != (Report{Channels: 2, APIKeys: 2, C2COrders: 2}) {
		t.Fatalf("every record is still read: report = %+v", report)
	}
	if failed.Total != 3 || len(failed.Failures) != 3 {
		t.Fatalf("failures = %+v", failed)
	}
	for index, want := range []struct{ kind, id string }{
		{KindChannel, lostChannel.RecordID}, {KindAPIKey, lostKey.RecordID}, {KindC2COrder, lostOrder.OrderID},
	} {
		if got := failed.Failures[index]; got.Kind != want.kind || got.ID != want.id {
			t.Fatalf("failure %d = %s %s, want %s %s", index, got.Kind, got.ID, want.kind, want.id)
		}
	}
	message := err.Error()
	for _, want := range []string{
		"channel " + lostChannel.RecordID, "api_key " + lostKey.RecordID, "c2c_order " + lostOrder.OrderID, `"v1"`, `"c1"`,
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message lacks %q: %s", want, message)
		}
	}
	assertNoSecrets(t, message, lostChannel.Sealed.Ciphertext, lostChannel.Sealed.Nonce, lostKey.Sealed.Ciphertext, lostOrder.Sealed.Ciphertext, lostOrder.Sealed.Nonce)
	for _, healthy := range []string{recordID(2), recordID(4), recordID(6)} {
		if strings.Contains(message, healthy) {
			t.Fatalf("message names a healthy record %s: %s", healthy, message)
		}
	}
}

func TestVerifyDetectsDamagedAndMisboundCiphertext(t *testing.T) {
	keyring, private := upstreamKeyring(t, "v1"), privateKeyring(t, "c1")
	data := newStores()
	flipped := data.channels.add(t, keyring, recordID(1), upstreamSecret)
	flipped.Sealed.Ciphertext[0] ^= 0xff // shares the stored bytes
	truncated := data.channels.add(t, keyring, recordID(2), upstreamSecret)
	data.channels.rows[len(data.channels.rows)-1].Sealed.Nonce = truncated.Sealed.Nonce[:4]
	// The key ID is present but its key material is not the one that sealed it.
	data.channels.add(t, upstreamKeyringSeeded(t, "other", "v1"), recordID(3), upstreamSecret)
	data.channels.add(t, keyring, recordID(4), upstreamSecret) // healthy
	// A ciphertext copied onto another record fails: the record ID is bound in.
	moved := data.apiKeys.add(t, keyring, recordID(5), platformSecret)
	data.apiKeys.rows[len(data.apiKeys.rows)-1].RecordID = recordID(6)
	// Same for payment methods, and a different purpose is not accepted either.
	data.orders.add(t, private, recordID(7))
	data.orders.rows[len(data.orders.rows)-1].OrderID = recordID(8)
	otherPurpose, err := private.Encrypt(recordID(9), "something_else", []byte(paymentSecret))
	if err != nil {
		t.Fatal(err)
	}
	data.orders.rows = append(data.orders.rows, c2c.StoredMethods{OrderID: recordID(9), Sealed: otherPurpose})

	_, err = Verify(context.Background(), data.sources(), Keyrings{Upstream: keyring, C2C: private})
	var failed *Error
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *Error", err)
	}
	var got []string
	for _, failure := range failed.Failures {
		got = append(got, failure.Kind+" "+failure.ID)
	}
	want := []string{
		"channel " + recordID(1), "channel " + recordID(2), "channel " + recordID(3),
		"api_key " + recordID(6), "c2c_order " + recordID(8), "c2c_order " + recordID(9),
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("failures = %v, want %v", got, want)
	}
	assertNoSecrets(t, err.Error(), moved.Sealed.Ciphertext, flipped.Sealed.Ciphertext, otherPurpose.Ciphertext)
}

func TestVerifyListsAtMostMaxReportedFailuresButCountsAll(t *testing.T) {
	data := newStores()
	lost := upstreamKeyring(t, "v1")
	for number := 1; number <= maxReported+5; number++ {
		data.channels.add(t, lost, recordID(number), upstreamSecret)
	}
	_, err := Verify(context.Background(), data.sources(), Keyrings{Upstream: upstreamKeyring(t, "v2"), C2C: privateKeyring(t, "c1")})
	var failed *Error
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if failed.Total != maxReported+5 || len(failed.Failures) != maxReported {
		t.Fatalf("total = %d, listed = %d", failed.Total, len(failed.Failures))
	}
	if !strings.HasPrefix(err.Error(), fmt.Sprintf("%d stored secrets", maxReported+5)) || !strings.HasSuffix(err.Error(), "and 5 more") {
		t.Fatalf("message = %s", err.Error())
	}
}

func TestVerifyReturnsReadErrorsInsteadOfPassing(t *testing.T) {
	data := newStores()
	data.apiKeys.err = errors.New("connection reset")
	_, err := Verify(context.Background(), data.sources(), Keyrings{Upstream: upstreamKeyring(t, "v1"), C2C: privateKeyring(t, "c1")})
	var failed *Error
	if err == nil || errors.As(err, &failed) || !strings.Contains(err.Error(), "api_key") || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRequiresBothKeyrings(t *testing.T) {
	if _, err := Verify(context.Background(), newStores().sources(), Keyrings{Upstream: upstreamKeyring(t, "v1")}); err == nil {
		t.Fatal("missing C2C keyring accepted")
	}
	if _, err := Verify(context.Background(), newStores().sources(), Keyrings{C2C: privateKeyring(t, "c1")}); err == nil {
		t.Fatal("missing upstream keyring accepted")
	}
}

// assertNoSecrets fails when message holds a plaintext or any of the sealed
// bytes, raw, hex or base64 encoded.
func assertNoSecrets(t *testing.T, message string, sealed ...[]byte) {
	t.Helper()
	forbidden := []string{upstreamSecret, platformSecret, paymentSecret, "seller-very-secret"}
	for _, value := range sealed {
		forbidden = append(forbidden, string(value), hex.EncodeToString(value), base64.StdEncoding.EncodeToString(value), base64.RawURLEncoding.EncodeToString(value))
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(message, value) {
			t.Fatalf("message leaks %q: %s", value, message)
		}
	}
}
