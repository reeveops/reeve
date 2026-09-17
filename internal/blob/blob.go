// Package blob defines the user-owned storage abstraction: locks, run
// artifacts, drift state, audit logs. Adapters (filesystem, s3, gcs,
// azblob) satisfy use-site interfaces. Conditional-write primitives are
// required for atomic lock transitions.
package blob

import "errors"

// ErrPreconditionFailed is returned by conditional writes when the
// If-Match / generation precondition does not hold. Lock state machines
// treat this as "someone else got there first" and re-read.
var ErrPreconditionFailed = errors.New("blob precondition failed")

// ErrConditionalDeletesUnsupported means a backend could not be verified to
// enforce conditional deletes. Retention must stop before touching user
// objects when delete preconditions are unavailable or cannot be tested.
var ErrConditionalDeletesUnsupported = errors.New("bucket does not enforce conditional deletes")

// ErrConditionalDeleteProbeFailed means the adapter could not determine
// whether a backend enforces conditional deletes. Retention must stop because
// deleting user objects would be unsafe without a confirmed capability.
var ErrConditionalDeleteProbeFailed = errors.New("conditional-delete capability probe failed")

// ErrNotFound is returned for missing objects. Adapters normalize
// platform-specific 404 shapes to this sentinel.
var ErrNotFound = errors.New("blob not found")
