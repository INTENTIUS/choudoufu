// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"strings"
)

// Store is a conditional-write key/value backend: a name for a small blob,
// versioned so a writer can prove it is updating what it last read rather
// than clobbering someone else's change. It has no notion of what a key
// names or what a payload contains — that belongs entirely to the caller.
// See doc.go for the full contract every implementation must honor.
type Store interface {
	// Get reads the current record at key. exists is false when no record
	// is there; payload and version are then the zero value and err is nil
	// — a missing key is not itself an error. version is "" if and only if
	// exists is false: no implementation ever assigns "" as a live record's
	// version, so a caller may treat it as a stable "absent" sentinel.
	Get(ctx context.Context, key string) (payload []byte, version string, exists bool, err error)

	// PutIfVersion writes payload to key, but only if the record's current
	// version equals expectedVersion. expectedVersion == "" asserts that no
	// record exists yet at key (the same assertion PutIfAbsent makes,
	// reachable here for callers that hold a uniform "expected version"
	// value rather than branching on whether they have seen the key
	// before). On success it returns the record's new version. On a
	// mismatch, it returns a *VersionConflictError naming both
	// expectedVersion and the version the store actually found — never a
	// bare error a caller has to parse.
	PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (newVersion string, err error)

	// PutIfAbsent creates key with payload, but only if no record exists
	// there yet. It is PutIfVersion(ctx, key, payload, "") under a name
	// that does not require the caller to know the empty-string
	// convention. On success it returns the new record's version; on
	// conflict it returns a *VersionConflictError with ExpectedVersion ""
	// and ActualVersion set to whatever is already there.
	PutIfAbsent(ctx context.Context, key string, payload []byte) (version string, err error)

	// Delete removes key, but only if its current version equals
	// expectedVersion — the same conditional discipline PutIfVersion
	// applies to writes, applied here to removal. Deleting an
	// already-absent key with expectedVersion == "" succeeds silently
	// (idempotent); deleting an already-absent key with a non-empty
	// expectedVersion, or a present key whose version does not match, both
	// return a *VersionConflictError.
	Delete(ctx context.Context, key string, expectedVersion string) error

	// List returns every key currently stored whose name begins with
	// keyPrefix, as an ordinary Go string prefix (not a path-hierarchy
	// match), sorted lexically. keyPrefix == "" lists every key. See each
	// implementation's own doc comment for how closely its underlying
	// primitive matches this — [SSMStore], notably, does not have a native
	// string-prefix list and approximates one; the returned set is exactly
	// this contract regardless.
	List(ctx context.Context, keyPrefix string) ([]string, error)
}

// VersionConflictError reports that a conditional operation's expected
// version did not match what the store actually holds for Key. It names
// both versions so a caller can decide how to react — reread and retry,
// surface a merge conflict, give up loudly — without parsing an error
// string.
//
// ActualVersion is "" when the store holds no record at Key at all
// (including the case where ExpectedVersion was itself "" and the key
// turned out to already exist would instead set ActualVersion to that
// existing version — "" only ever means "no record").
type VersionConflictError struct {
	Key             string
	ExpectedVersion string
	ActualVersion   string
}

func (e *VersionConflictError) Error() string {
	if e.ActualVersion == "" {
		return fmt.Sprintf("staterecord: version conflict for %q: expected version %q, but no record exists", e.Key, e.ExpectedVersion)
	}
	if e.ExpectedVersion == "" {
		return fmt.Sprintf("staterecord: version conflict for %q: expected no record to exist, but found version %q", e.Key, e.ActualVersion)
	}
	return fmt.Sprintf("staterecord: version conflict for %q: expected version %q, found version %q", e.Key, e.ExpectedVersion, e.ActualVersion)
}

// validateKey rejects the ways an opaque key can stop being safe to turn
// into a filesystem path, a parameter name or an object key: empty, a NUL
// byte (illegal in all three), or a ".." path segment (a local-store
// traversal risk this package refuses categorically rather than trusting
// every caller to have sanitized it). It does not enforce a charset beyond
// that — [SSMStore] and [S3Store] each carry their own backend's naming
// rules, and those simply surface as an ordinary error from the underlying
// API when violated.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("staterecord: key must not be empty")
	}
	return validateKeyPrefix(key)
}

// validateKeyPrefix is validateKey without the non-empty requirement — List
// legitimately takes "" to mean "every key".
func validateKeyPrefix(keyPrefix string) error {
	if strings.ContainsRune(keyPrefix, 0) {
		return fmt.Errorf("staterecord: key %q contains a NUL byte", keyPrefix)
	}
	for _, seg := range strings.Split(keyPrefix, "/") {
		if seg == ".." {
			return fmt.Errorf("staterecord: key %q contains a %q path segment", keyPrefix, "..")
		}
	}
	return nil
}
