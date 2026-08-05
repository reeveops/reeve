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

// ErrNotFound is returned for missing objects. Adapters normalize
// platform-specific 404 shapes to this sentinel.
var ErrNotFound = errors.New("blob not found")
