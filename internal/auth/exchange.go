package auth

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Errors every credential exchange can return. They are sentinels so
// callers and tests can match with errors.Is rather than on message text.
var (
	// ErrNoToken means the provider returned a successful response that
	// carried no token. Passing it on would hand the engine an empty
	// bearer, which surfaces as a confusing rejection from the cloud
	// provider instead of a clear local failure.
	ErrNoToken = errors.New("credential exchange returned no token")

	// ErrNoExpiry means the response carried no usable expiry.
	// Credential.ExpiresAt documents the zero value as "no expiry", so
	// accepting one advertises a short-lived token as permanent.
	ErrNoExpiry = errors.New("credential exchange returned no expiry")

	// ErrExpired means the credential was already dead on arrival. Better
	// to fail here than to hand it to an engine that will fail opaquely
	// partway through an apply.
	ErrExpired = errors.New("credential exchange returned an already-expired credential")
)

// MaxExpiresInSeconds is the largest expires_in a provider may report.
// time.Duration is an int64 count of nanoseconds, so anything above this
// overflows when multiplied by time.Second and yields a garbage expiry -
// frequently one in the past.
const MaxExpiresInSeconds = int64(math.MaxInt64 / int64(time.Second))

// ValidateExchange enforces the invariants every credential exchange must
// hold: a usable token, and an expiry that is both present and still in the
// future. now is injectable for tests.
func ValidateExchange(token string, expiresAt, now time.Time) error {
	if token == "" {
		return ErrNoToken
	}
	if expiresAt.IsZero() {
		return ErrNoExpiry
	}
	if !expiresAt.After(now) {
		return fmt.Errorf("%w (expired at %s)", ErrExpired, expiresAt.UTC().Format(time.RFC3339))
	}
	return nil
}

// ExpiresInToTime converts a provider's expires_in (seconds from now) into
// an absolute expiry, rejecting values that are absent, negative, or large
// enough to overflow time.Duration.
func ExpiresInToTime(seconds int64, now time.Time) (time.Time, error) {
	if seconds <= 0 {
		return time.Time{}, fmt.Errorf("%w (expires_in=%d)", ErrNoExpiry, seconds)
	}
	if seconds > MaxExpiresInSeconds {
		return time.Time{}, fmt.Errorf("%w (expires_in=%d overflows a duration)", ErrNoExpiry, seconds)
	}
	return now.Add(time.Duration(seconds) * time.Second), nil
}
