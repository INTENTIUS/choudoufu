// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// lockSuffix names the sidecar lockfile PutIfVersion and Delete hold for
// the duration of their read-compare-write critical section. tmpInfix
// names the pattern writeAtomic's scratch files carry before they are
// renamed into place. Both are filtered out of List.
const (
	lockSuffix = ".lock"
	tmpInfix   = ".tmp-"
)

// lockPollInterval is how often a blocked lock acquisition retries.
// lockStaleAfter is how old an uncontested lockfile must be before a new
// acquirer assumes its holder crashed and breaks it — a live holder never
// keeps one this long; a single read-compare-write is microseconds of disk
// I/O.
const (
	lockPollInterval = 20 * time.Millisecond
	lockStaleAfter   = 30 * time.Second
)

// LocalStore is a [Store] backed by a directory of files: one file per key,
// nested directories mirroring any "/" the key contains. It is the
// zero-configuration default — solo development, tests, air-gapped runs —
// mirroring plain local state's own "just works, no backend to configure"
// shape.
//
// # Version
//
// A record's version is its content hash ("sha256:<hex>"), not a
// timestamp or a counter: two writes of byte-identical payloads carry the
// same version, and the version never depends on the clock or on how many
// times the key has been written.
//
// # Atomicity and its limit: single-operator only
//
// [LocalStore.PutIfAbsent] is a single O_CREATE|O_EXCL open — atomic on
// its own, no locking needed, exactly like a real filesystem's create
// primitive already guarantees. [LocalStore.PutIfVersion] and
// [LocalStore.Delete] are a read, a compare, and a write (or removal);
// nothing in POSIX makes that sequence atomic by itself, so each one holds
// a sidecar "<file>.lock" file — itself an O_CREATE|O_EXCL create — for the
// duration, and the write half lands via a temp file plus os.Rename so a
// reader never observes a half-written file.
//
// That gives real compare-and-swap for every writer on one machine: two
// goroutines in one process, or two separate `tofu` invocations racing on
// the same directory, serialize through the lockfile and the loser gets a
// *[VersionConflictError] rather than a silently clobbered write. It gives
// nothing across machines — there is no network protocol here, only local
// filesystem primitives — which is the store's fundamental limit rather
// than an oversight: LocalStore is for a single operator (or a single
// machine's worth of concurrent processes), never for a team sharing state
// across laptops. Reaching for [SSMStore] or [S3Store] is what "more than
// one operator" means in this package.
//
// # What this store does not manage
//
// The directory's location, its presence or absence in version control,
// and its backup story are the caller's to decide — this store only reads
// and writes files under the directory it is given. That is an acceptable
// hands-off position specifically because a micro-state record's blast
// radius is small (an effect re-runs, a random id regenerates) in a way a
// full Terraform state file's loss never was.
type LocalStore struct {
	dir string
}

// NewLocalStore builds a [LocalStore] rooted at dir, creating dir (and any
// missing parents) if it does not exist yet.
func NewLocalStore(dir string) (*LocalStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("staterecord: local: dir must not be empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("staterecord: local: resolving %q: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("staterecord: local: creating %q: %w", dir, err)
	}
	return &LocalStore{dir: abs}, nil
}

// pathFor resolves key to the file it lives in, rejecting anything that
// would resolve outside s.dir.
func (s *LocalStore) pathFor(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	full := filepath.Join(s.dir, filepath.FromSlash(key))
	if full != s.dir && !strings.HasPrefix(full, s.dir+string(filepath.Separator)) {
		return "", fmt.Errorf("staterecord: local: key %q resolves outside the store directory", key)
	}
	return full, nil
}

// contentVersion is the version this store assigns to payload: its sha256,
// hex-encoded and tagged with the algorithm name so a version string is
// self-describing rather than a bare hex blob.
func contentVersion(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readRecord reads path's current content. A missing file is reported as
// exists == false with a nil error, not as a failure.
func readRecord(path string) ([]byte, string, bool, error) {
	payload, err := os.ReadFile(path) //nolint:gosec // path is built by pathFor from a validated key
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("staterecord: local: reading %s: %w", path, err)
	}
	return payload, contentVersion(payload), true, nil
}

// writeAtomic replaces path's content with payload via a temp file in the
// same directory (so the rename is same-filesystem, hence atomic) plus
// os.Rename — the mechanism [LocalStore]'s doc comment promises: a reader
// never observes a partially written file, whether path is being created
// for the first time or replaced.
func writeAtomic(path string, payload []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+tmpInfix+"*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// acquireFileLock takes lockPath as a mutual-exclusion primitive (a bare
// O_CREATE|O_EXCL create, same mechanism [LocalStore.PutIfAbsent] uses for
// its own atomicity), retrying until it succeeds, ctx is canceled, or it
// finds and breaks a lock old enough to belong to a crashed holder. The
// returned release removes the lockfile.
func acquireFileLock(ctx context.Context, lockPath string) (release func() error, err error) {
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path derives from a validated key
		if err == nil {
			_ = f.Close()
			return func() error { return os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("staterecord: local: acquiring the lock at %s: %w", lockPath, err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// Get implements [Store].
func (s *LocalStore) Get(_ context.Context, key string) ([]byte, string, bool, error) {
	path, err := s.pathFor(key)
	if err != nil {
		return nil, "", false, err
	}
	return readRecord(path)
}

// PutIfAbsent implements [Store]. It is a single O_CREATE|O_EXCL open, so
// unlike [LocalStore.PutIfVersion] it needs no lockfile of its own: the
// filesystem's own create primitive is already the atomicity.
func (s *LocalStore) PutIfAbsent(_ context.Context, key string, payload []byte) (string, error) {
	path, err := s.pathFor(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("staterecord: local: creating the directory for %q: %w", key, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path derives from a validated key
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			_, actual, exists, gerr := readRecord(path)
			if gerr != nil {
				return "", gerr
			}
			av := ""
			if exists {
				av = actual
			}
			return "", &VersionConflictError{Key: key, ExpectedVersion: "", ActualVersion: av}
		}
		return "", fmt.Errorf("staterecord: local: creating %q: %w", key, err)
	}
	_, werr := f.Write(payload)
	cerr := f.Close()
	if werr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("staterecord: local: writing %q: %w", key, werr)
	}
	if cerr != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("staterecord: local: closing %q: %w", key, cerr)
	}
	return contentVersion(payload), nil
}

// PutIfVersion implements [Store]. expectedVersion == "" delegates to
// [LocalStore.PutIfAbsent], which needs no lockfile; any other value takes
// this key's lockfile for a read-compare-write critical section — see the
// type doc's "Atomicity and its limit" section for exactly what that does
// and does not protect against.
func (s *LocalStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	if expectedVersion == "" {
		return s.PutIfAbsent(ctx, key, payload)
	}
	path, err := s.pathFor(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("staterecord: local: creating the directory for %q: %w", key, err)
	}
	release, err := acquireFileLock(ctx, path+lockSuffix)
	if err != nil {
		return "", err
	}
	defer func() { _ = release() }()

	_, currentVersion, exists, err := readRecord(path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: ""}
	}
	if currentVersion != expectedVersion {
		return "", &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: currentVersion}
	}
	if err := writeAtomic(path, payload); err != nil {
		return "", fmt.Errorf("staterecord: local: writing %q: %w", key, err)
	}
	return contentVersion(payload), nil
}

// Delete implements [Store], under the same lockfile discipline as
// [LocalStore.PutIfVersion].
func (s *LocalStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	path, err := s.pathFor(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("staterecord: local: creating the directory for %q: %w", key, err)
	}
	release, err := acquireFileLock(ctx, path+lockSuffix)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	_, currentVersion, exists, err := readRecord(path)
	if err != nil {
		return err
	}
	if !exists {
		if expectedVersion == "" {
			return nil
		}
		return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: ""}
	}
	if currentVersion != expectedVersion {
		return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: currentVersion}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("staterecord: local: deleting %q: %w", key, err)
	}
	return nil
}

// List implements [Store] by walking the whole directory tree and
// filtering by a plain string prefix — the store's own layout already
// mirrors key hierarchy in directories, but List's contract is the
// interface's ordinary string-prefix match, not a path-boundary match, so
// this walks everything under s.dir rather than trying to shortcut to a
// subdirectory.
func (s *LocalStore) List(_ context.Context, keyPrefix string) ([]string, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, err
	}
	var keys []string
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), lockSuffix) || strings.Contains(d.Name(), tmpInfix) {
			return nil
		}
		rel, err := filepath.Rel(s.dir, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, keyPrefix) {
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("staterecord: local: listing %q: %w", keyPrefix, err)
	}
	sort.Strings(keys)
	return keys, nil
}
