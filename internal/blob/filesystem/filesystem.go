package filesystem

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/reeveops/reeve/internal/blob"
)

// Store is the filesystem:// blob adapter. Atomic writes via tmpfile +
// rename, conditional writes via hashing the existing contents. Used for
// local testing and `reeve run preview --local`.
//
// Containment: every operation resolves its key through an *os.Root opened
// on the bucket directory, so the kernel refuses any path that leaves it.
// This replaces a hand-rolled string check that inspected the key for "/.."
// and friends. The string check could not see symlinks: a symlinked
// subdirectory under the bucket passed it and then wrote wherever the link
// pointed. os.Root resolves each component against the root descriptor and
// rejects the traversal instead.
type Store struct {
	root string
}

// New returns a Store rooted at dir. dir is created if missing.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Fail here rather than on first use if the bucket path is unusable.
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	_ = r.Close()
	return &Store{root: abs}, nil
}

// openRoot opens a root handle for one operation. Opening per call rather
// than holding one for the Store's lifetime keeps the descriptor count flat
// (tests build many Stores) and costs a single openat.
func (s *Store) openRoot() (*os.Root, error) { return os.OpenRoot(s.root) }

// cleanKey normalizes a blob key to a slash-separated path relative to the
// root and rejects lexical escapes.
//
// This is no longer the security boundary - os.Root is - but it is kept
// because it catches the common mistake with a message that names the
// problem ("escapes root") instead of surfacing a syscall-level error, and
// because it normalizes "a/../b" before it reaches the filesystem. The
// escapes it cannot see (symlinks) are exactly the ones os.Root stops.
func cleanKey(key string) (string, error) {
	k := filepath.ToSlash(key)
	if strings.TrimSpace(k) == "" {
		return "", errors.New("blob key is empty")
	}
	if strings.HasPrefix(k, "/") {
		return "", errors.New("blob key escapes root: must be relative to the bucket")
	}
	k = path.Clean(k)
	if k == ".." || strings.HasPrefix(k, "../") {
		return "", errors.New("blob key escapes root")
	}
	if k == "." {
		return "", errors.New("blob key is empty")
	}
	// The lock namespace is reserved: it holds internal lockfiles, never
	// objects. Refusing it here is what lets List skip the whole subtree
	// without ever hiding a real key.
	if k == lockNamespace || strings.HasPrefix(k, lockNamespace+"/") {
		return "", fmt.Errorf("blob key %q is inside the reserved lock namespace %q", key, lockNamespace)
	}
	return k, nil
}

// osKey converts a cleaned key to the platform path os.Root expects.
func osKey(k string) string { return filepath.FromSlash(k) }

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	k, err := cleanKey(key)
	if err != nil {
		return nil, nil, err
	}
	r, err := s.openRoot()
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()

	f, err := r.Open(osKey(k))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, blob.ErrNotFound
		}
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	etag, err := hashKey(r, k)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	// f outlives the root handle deliberately: an *os.File stays valid after
	// the directory descriptor it was opened from is closed.
	return f, &blob.Metadata{ETag: etag, LastModified: st.ModTime().Unix(), Size: st.Size()}, nil
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	k, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return writeAtomic(root, k, r)
}

func (s *Store) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	k, err := cleanKey(key)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()

	// Lock the target key via a sibling lockfile.
	lock, err := acquireLock(root, k)
	if err != nil {
		return nil, err
	}
	defer lock.release()

	current, statErr := hashKey(root, k)
	// Only "not there" may be read as absence. Any other stat/read failure
	// (a permissions problem, a truncated read) previously fell through the
	// ifMatch=="" branch and CREATED the object, overwriting whatever was
	// really there - a fail-open on the create-if-absent path.
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("read current object %q: %w", key, statErr)
	}
	if ifMatch == "" {
		if statErr == nil {
			return nil, blob.ErrPreconditionFailed // exists, but we required absence
		}
	} else if current != ifMatch {
		return nil, blob.ErrPreconditionFailed
	}

	return writeAtomic(root, k, r)
}

func (s *Store) Delete(ctx context.Context, key string) error {
	k, err := cleanKey(key)
	if err != nil {
		return err
	}
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(osKey(k)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	fsys := root.FS()

	// An empty prefix lists the whole bucket; io/fs spells that ".".
	start := "."
	if strings.TrimSpace(prefix) != "" {
		k, err := cleanKey(prefix)
		if err != nil {
			return nil, err
		}
		start = path.Clean(k)
	}

	info, err := fs.Stat(fsys, start)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		// Treat as a single-key match.
		return []string{start}, nil
	}

	var out []string
	err = fs.WalkDir(fsys, start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == lockNamespace {
				return fs.SkipDir
			}
			return nil
		}
		// Lockfiles are internal bookkeeping, not objects.
		if p == lockNamespace || strings.HasPrefix(p, lockNamespace+"/") {
			return nil
		}
		// WalkDir yields slash paths already relative to the bucket root,
		// which is exactly the key form callers expect back.
		out = append(out, p)
		return nil
	})
	return out, err
}

// writeAtomic writes r to key via a sibling temp file plus rename. Both the
// temp and the target are resolved through root, so neither can land outside
// the bucket.
func writeAtomic(root *os.Root, key string, r io.Reader) (*blob.Metadata, error) {
	dir := path.Dir(key)
	if dir != "." {
		if err := root.MkdirAll(osKey(dir), 0o750); err != nil {
			return nil, err
		}
	}

	tmpKey, tmp, err := createTemp(root, dir)
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = root.Remove(osKey(tmpKey)) }

	hasher := sha256.New()
	n, err := io.Copy(tmp, io.TeeReader(r, hasher))
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return nil, err
	}
	if err := root.Rename(osKey(tmpKey), osKey(key)); err != nil {
		cleanup()
		return nil, err
	}
	return &blob.Metadata{ETag: hex.EncodeToString(hasher.Sum(nil)), Size: n}, nil
}

// createTemp makes an exclusive temp file beside the target. os.Root has no
// CreateTemp, so this is the same retry-on-collision loop with O_EXCL.
func createTemp(root *os.Root, dir string) (string, *os.File, error) {
	var lastErr error
	for i := 0; i < 10; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", nil, err
		}
		name := ".reeve-tmp-" + hex.EncodeToString(b[:])
		key := name
		if dir != "." {
			key = dir + "/" + name
		}
		f, err := root.OpenFile(osKey(key), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return key, f, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, err
		}
		lastErr = err
	}
	return "", nil, lastErr
}

// --- helpers ---

func hashKey(root *os.Root, key string) (string, error) {
	f, err := root.Open(osKey(key))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type fileLock struct {
	root *os.Root
	key  string
	f    *os.File
}

// release drops the flock. It deliberately does NOT unlink the lockfile.
//
// Unlinking races: with A holding the lock and B already blocked in
// Flock on the same inode, A's unlink lets B proceed on a now-unlinked
// inode while C opens the path fresh, creates a NEW inode and locks that.
// B and C then both believe they hold the lock and their PutIfMatch
// bodies interleave. Keeping the inode in place means every waiter
// contends on the same one. The files are empty and are filtered out of
// List, so leaving them costs a directory entry per key.
func (l *fileLock) release() {
	if l.f != nil {
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
		_ = l.f.Close()
	}
}

// lockNamespace is a reserved directory holding lockfiles. It is NOT part
// of the object key space: cleanKey refuses keys inside it and List skips
// it, so a lockfile can never collide with, hide, or be mistaken for an
// object.
//
// Locks used to sit beside their target as "<key>.lock", which put them in
// the key space: an object legitimately named "foo.lock" collided with the
// lock for "foo", and classifying by suffix forced List to hide real
// objects ending in .lock. Naming by hash also keeps this directory flat,
// so no per-key intermediate directories are created.
const lockNamespace = ".reeve-locks"

func lockPathFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	return lockNamespace + "/" + hex.EncodeToString(sum[:])
}

func acquireLock(root *os.Root, key string) (*fileLock, error) {
	lockKey := lockPathFor(key)
	if err := root.MkdirAll(osKey(lockNamespace), 0o750); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(osKey(lockKey), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &fileLock{root: root, key: lockKey, f: f}, nil
}

// compile-time check
var _ blob.Store = (*Store)(nil)

// ReadBytes is a convenience wrapper that reads a key into a byte slice.
func ReadBytes(ctx context.Context, s blob.Store, key string) ([]byte, *blob.Metadata, error) {
	rc, md, err := s.Get(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), md, nil
}
