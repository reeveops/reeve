package auth

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestValidateExchange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		token   string
		expires time.Time
		want    error
	}{
		{"good", "tok", now.Add(time.Hour), nil},
		{"empty token", "", now.Add(time.Hour), ErrNoToken},
		// The zero time is what a missing timestamp decodes to, and
		// Credential.ExpiresAt reads it as "never expires".
		{"zero expiry", "tok", time.Time{}, ErrNoExpiry},
		{"already expired", "tok", now.Add(-time.Second), ErrExpired},
		// Exactly now is not in the future: the credential is dead.
		{"expires exactly now", "tok", now, ErrExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateExchange(tc.token, tc.expires, now)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.want)
			}
		})
	}
}

func TestExpiresInToTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		seconds int64
		want    error
	}{
		{"one hour", 3600, nil},
		{"largest safe value", MaxExpiresInSeconds, nil},
		{"zero", 0, ErrNoExpiry},
		{"negative", -1, ErrNoExpiry},
		// seconds * time.Second overflows int64 nanoseconds above the
		// bound, which silently produced an expiry in the PAST.
		{"overflow boundary", MaxExpiresInSeconds + 1, ErrNoExpiry},
		{"max int64", math.MaxInt64, ErrNoExpiry},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExpiresInToTime(tc.seconds, now)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want errors.Is(%v)", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.After(now) {
				t.Fatalf("expiry %v is not after %v - overflow slipped through", got, now)
			}
		})
	}
}
