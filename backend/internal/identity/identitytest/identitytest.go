// Package identitytest builds identity services for tests whose password
// hashing is cheap enough to run many times under the race detector.
//
// Production wiring (cmd/server) never imports this package and keeps the
// parameters of identity.DefaultPasswordParams.
package identitytest

import (
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
)

// Params is the cheapest Argon2id configuration the password verifier still
// accepts (8 MiB, 1 iteration, parallelism 1). It sits exactly on the
// verifier's floor, so no verification limit is relaxed for tests; if that
// floor ever rises, NewService fails and this value must follow it.
var Params = identity.PasswordParams{Memory: 8 * 1024, Iterations: 1, Parallelism: 1}

// NewService is identity.NewService with Params as the hashing cost.
func NewService(store identity.Store, sessionLifetime time.Duration) (*identity.Service, error) {
	return identity.NewService(store, sessionLifetime, identity.WithPasswordParams(Params))
}
