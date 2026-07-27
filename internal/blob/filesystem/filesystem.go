package filesystem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/FynxLabs/reeve/internal/blob"
)

// Store is the filesystem:// blob adapter. Atomic writes via tmpfile +
// rename, conditional writes via hashing the existing contents. Used for
// local testing and `reeve run preview --local`.
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
	return &Store{root: abs}, nil
}

func (s *Store) path(key string) (string, error) {
	if strings.HasPrefix(key, "/") || strings.HasPrefix(key, "..") ||
		strings.Contains(key, "/../") || strings.HasSuffix(key, "/..") {
		return "", errors.New("blob key escapes root")
	}
	full := filepath.Join(s.root, filepath.FromSlash(key))
	if !strings.HasPrefix(full, s.root+string(filepath.Separator)) && full != s.root {
		return "", errors.New("blob key escapes root")
	}
	return full, nil
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, *blob.Metadata, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, nil, err
	}
	// #nosec G304 -- p comes from Store.path, which rejects keys starting with / or .., containing
	// /../ or ending /.., then re-verifies the joined path is still under root
	f, err := os.Open(p)
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
	etag, err := hashFile(p)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, &blob.Metadata{ETag: etag, LastModified: st.ModTime().Unix(), Size: st.Size()}, nil
}

func (s *Store) Put(ctx context.Context, key string, r io.Reader) (*blob.Metadata, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return s.writeAtomic(p, r)
}

func (s *Store) PutIfMatch(ctx context.Context, key string, r io.Reader, ifMatch string) (*blob.Metadata, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	// Lock the target path via a sibling lockfile.
	lock, err := acquireLock(p)
	if err != nil {
		return nil, err
	}
	defer lock.release()

	current, statErr := hashFileOrMissing(p)
	if ifMatch == "" {
		if statErr == nil {
			return nil, blob.ErrPreconditionFailed // exists, but we required absence
		}
	} else if current != ifMatch {
		return nil, blob.ErrPreconditionFailed
	}

	return s.writeAtomic(p, r)
}

func (s *Store) Delete(ctx context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	p, err := s.path(prefix)
	if err != nil {
		return nil, err
	}
	var out []string
	walkRoot := p
	info, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		// Treat as a single-key match.
		rel, _ := filepath.Rel(s.root, p)
		return []string{filepath.ToSlash(rel)}, nil
	}
	err = filepath.Walk(walkRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func (s *Store) writeAtomic(target string, r io.Reader) (*blob.Metadata, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".reeve-tmp-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	hasher := sha256.New()
	tee := io.TeeReader(r, hasher)
	n, err := io.Copy(tmp, tee)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return nil, err
	}
	etag := hex.EncodeToString(hasher.Sum(nil))
	return &blob.Metadata{ETag: etag, Size: n}, nil
}

// --- helpers ---

func hashFile(path string) (string, error) {
	// #nosec G304 -- callers pass a path already validated by Store.path (see the containment
	// check there)
	f, err := os.Open(path)
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

func hashFileOrMissing(path string) (string, error) {
	h, err := hashFile(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return h, err
}

type fileLock struct {
	f *os.File
}

func (l *fileLock) release() {
	if l.f != nil {
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
		_ = l.f.Close()
		_ = os.Remove(l.f.Name())
	}
}

func acquireLock(target string) (*fileLock, error) {
	lockPath := target + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o750); err != nil {
		return nil, err
	}
	// #nosec G304 -- lockPath is <target>.lock where target was already validated by Store.path
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &fileLock{f: f}, nil
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
