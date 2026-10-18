// Package keyringcheck proves at startup that every stored secret the
// application can still read decrypts with the configured keyrings, so a
// missing historical key ID or a damaged ciphertext stops the server at boot
// instead of surfacing when someone uses the channel, API key or order.
//
// It covers the three places ciphertext lives: upstream keys of retained
// channel identities and undeleted platform API keys (both under the upstream credential keyring)
// and the payment methods of C2C orders (under the C2C private data keyring).
// Plaintext is discarded the moment it is decrypted; nothing is cached. There
// is no re-encryption here: an old key ID can only be dropped once no stored
// secret refers to it, and this check is what would tell.
package keyringcheck

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

const (
	// pageSize is how many rows one query returns, bounding memory however many
	// secrets are stored.
	pageSize = 500
	// maxReported bounds the failures an Error lists; Total still counts all.
	maxReported = 20
)

// Record kinds named in a Failure.
const (
	KindChannel  = "channel"
	KindAPIKey   = "api_key"
	KindC2COrder = "c2c_order"
)

// CredentialSource pages sealed credentials by record ID.
type CredentialSource interface {
	// CredentialsAfter returns up to limit credentials whose record ID is greater
	// than afterID (all of them when afterID is empty), in ascending ID order.
	CredentialsAfter(ctx context.Context, afterID string, limit int) ([]channel.StoredCredential, error)
}

// MethodsSource pages sealed C2C payment methods by order ID.
type MethodsSource interface {
	// OrderMethodsAfter has the paging contract of CredentialSource.
	OrderMethodsAfter(ctx context.Context, afterID string, limit int) ([]c2c.StoredMethods, error)
}

// Sources are the stores to read the ciphertext from. Channels includes retained
// history identities even after business deletion; configuration retirement
// cannot remove their historical key IDs. APIKeys returns undeleted keys.
// C2COrders returns every order, including finished historical orders.
type Sources struct {
	Channels  CredentialSource
	APIKeys   CredentialSource
	C2COrders MethodsSource
}

// Keyrings are the keyrings the secrets are checked against.
type Keyrings struct {
	Upstream *channel.Keyring
	C2C      *c2c.Keyring
}

// Report counts the records that were read, failed or not.
type Report struct {
	Channels  int
	APIKeys   int
	C2COrders int
}

// Failure is one record that could not be decrypted. Err says why (an absent
// key ID, an authentication failure) and never contains plaintext or
// ciphertext.
type Failure struct {
	Kind string
	ID   string
	Err  error
}

// Error is returned when records cannot be decrypted. Failures lists the first
// ones; Total counts all of them.
type Error struct {
	Total    int
	Failures []Failure
}

func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		parts = append(parts, fmt.Sprintf("%s %s: %v", failure.Kind, failure.ID, failure.Err))
	}
	message := fmt.Sprintf("%d stored secrets cannot be decrypted with the configured keyrings: %s", e.Total, strings.Join(parts, "; "))
	if hidden := e.Total - len(e.Failures); hidden > 0 {
		message += fmt.Sprintf("; and %d more", hidden)
	}
	return message
}

// Verify decrypts every stored secret once. Decryption failures are collected
// and returned together as an *Error, so one start reveals every affected
// record; a failure to read the database aborts at once with that error.
func Verify(ctx context.Context, sources Sources, keyrings Keyrings) (Report, error) {
	if keyrings.Upstream == nil || keyrings.C2C == nil {
		return Report{}, errors.New("both keyrings are required")
	}
	var (
		report   Report
		failures Error
		err      error
	)
	credentialID := func(stored channel.StoredCredential) string { return stored.RecordID }
	openCredential := func(stored channel.StoredCredential) error {
		_, err := keyrings.Upstream.Decrypt(stored.RecordID, stored.Sealed)
		return err
	}
	if report.Channels, err = scan(ctx, &failures, KindChannel, sources.Channels.CredentialsAfter, credentialID, openCredential); err != nil {
		return report, err
	}
	if report.APIKeys, err = scan(ctx, &failures, KindAPIKey, sources.APIKeys.CredentialsAfter, credentialID, openCredential); err != nil {
		return report, err
	}
	if report.C2COrders, err = scan(ctx, &failures, KindC2COrder, sources.C2COrders.OrderMethodsAfter,
		func(stored c2c.StoredMethods) string { return stored.OrderID }, keyrings.C2C.VerifyPaymentMethods); err != nil {
		return report, err
	}
	if failures.Total > 0 {
		return report, &failures
	}
	return report, nil
}

// scan pages through one kind of record, opening each and noting failures. It
// returns how many records it read.
func scan[T any](
	ctx context.Context, failures *Error, kind string,
	list func(ctx context.Context, afterID string, limit int) ([]T, error),
	idOf func(T) string, open func(T) error,
) (int, error) {
	count := 0
	afterID := ""
	for {
		page, err := list(ctx, afterID, pageSize)
		if err != nil {
			return count, fmt.Errorf("reading stored %s secrets: %w", kind, err)
		}
		for _, record := range page {
			count++
			if err := open(record); err != nil {
				failures.Total++
				if len(failures.Failures) < maxReported {
					failures.Failures = append(failures.Failures, Failure{Kind: kind, ID: idOf(record), Err: err})
				}
			}
		}
		if len(page) < pageSize {
			return count, nil
		}
		afterID = idOf(page[len(page)-1])
	}
}
