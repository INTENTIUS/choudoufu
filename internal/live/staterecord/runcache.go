// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// RunCache is the estate's records loaded the way stock OpenTofu loads its
// state file: once, in bulk, at the moment something first asks for any of
// it, held in memory for the rest of the read phase, and never written back
// through.
//
// # What it replaces
//
// A migrated plan asks the store about the same instance three to eight
// times. Every accessor on projection's RecordStore - identity, residue, the
// provisioner bit, the deposed set, the envelope kind - decodes the same
// physical key, and each went to the store for itself. Measured at scale 1
// over 78 instances: 377 trips over 80 distinct keys. Caching the repeats
// takes that to 80, one per instance. Loading the namespace in bulk takes it
// to 1, which is what stock pays, and is the only figure that makes the two
// state models comparable at all.
//
// The bulk load is lazy - it happens on the first read under the namespace,
// not at construction - because that is exactly when stock reads its state
// file, and because a command that touches no record should pay for none.
// A store that does not implement [BulkReader] silently keeps the per-key
// behaviour: still one trip per key instead of one per accessor.
//
// # Why it cannot serve a stale value
//
// One rule, and it is not a balance of risks: **the cache is switched off
// permanently, process-wide, by the first write through any RunCache.**
// Nothing it serves was ever read after something was written. A plan writes
// nothing, so a plan is served entirely from the snapshot; the instant a
// write-back, a migration or a seeder writes anything, every later read in
// that process goes to the store, for good.
//
// That is deliberately blunter than invalidating the key that was written.
// Invalidation has to be right about which reads a write can affect, and the
// three reads it must never be wrong about - projection's mergeEnvelope
// read-modify-write, its currentVersion, and the seeders' read-before-write
// halves - are the ones where being wrong means a lost update rather than a
// slow run. A switch that is simply off after the first write cannot be
// wrong about any of them. Those three call sites additionally bypass this
// cache explicitly, through [RunCache.Uncached], so they are correct even
// before the first write has happened.
//
// Nothing conditional is ever decided here either: PutIfVersion, PutIfAbsent
// and Delete always go to the wrapped store, so the compare-and-swap that
// guards against a writer OUTSIDE this process is performed by the store on
// the store's own current version, exactly as before.
//
// # Lifetime
//
// The process, and never longer. A record is live state; whether one has
// gone stale between runs is the charter's business, and a cache must never
// be what answers it. There is no expiry, no file, no shared daemon.
type RunCache struct {
	inner Store

	// prefix is the namespace a bulk load covers - the record key prefix
	// the store was built for. A read under it triggers, and is then served
	// by, the snapshot; a read outside it (the hint and root-output
	// namespaces share the same backend) falls back to per-key caching.
	prefix string

	mu sync.Mutex
	// loaded is true once the snapshot covers prefix completely, which is
	// what lets a key absent from entries answer "no record" without a
	// trip.
	loaded  bool
	entries map[string]cacheEntry
	lists   map[string][]string
}

type cacheEntry struct {
	payload []byte
	version string
	exists  bool
}

// wroteSomething records that a write has happened through some RunCache in
// this process. It is global rather than per-cache because a run can build
// several wrappers over one backend - internal/live/discovery builds a fresh
// one per call site - and a value served by the wrapper that did not make
// the write would be exactly as stale as one served by the wrapper that did.
var wroteSomething atomic.Bool

// NewRunCache wraps inner, snapshotting the namespace at prefix. A nil inner
// returns nil, so a caller that already treats "no store configured" as nil
// keeps doing so. An empty prefix disables the bulk load and leaves ordinary
// per-key caching.
func NewRunCache(inner Store, prefix string) Store {
	if inner == nil {
		return nil
	}
	return &RunCache{
		inner:   inner,
		prefix:  prefix,
		entries: map[string]cacheEntry{},
		lists:   map[string][]string{},
	}
}

// Uncached returns the wrapped store, for the reads that must never be
// served from a snapshot: a read-modify-write's own read, a version observed
// so a compare-and-swap can catch an outside writer, and a seeder's
// read-before-write. See this type's doc comment.
//
// It is a method rather than a field so the intent is stated at every call
// site that needs it, and so a store that is not a RunCache needs no special
// case: see [Fresh].
func (c *RunCache) Uncached() Store { return c.inner }

// Fresh returns the store beneath any read cache in s, or s itself when
// there is none. A caller that must not read a remembered value asks for
// this rather than testing for a cache type it should not have to know
// about.
func Fresh(s Store) Store {
	for {
		c, ok := s.(*RunCache)
		if !ok {
			return s
		}
		s = c.Uncached()
	}
}

// disabled reports whether anything has been written in this process yet.
func disabled() bool { return wroteSomething.Load() }

// noteWrite switches every cache in the process off for the rest of the run.
// Called after every write attempt, successful or not: a conditional write
// that came back with a conflict changed nothing, but it did prove some
// other writer is active, and from that point a remembered value is a guess.
func (c *RunCache) noteWrite() {
	wroteSomething.Store(true)
	c.mu.Lock()
	c.loaded = false
	c.entries = map[string]cacheEntry{}
	c.lists = map[string][]string{}
	c.mu.Unlock()
}

// covers reports whether key is inside the namespace a bulk load snapshots.
func (c *RunCache) covers(key string) bool {
	return c.prefix != "" && (key == c.prefix || strings.HasPrefix(key, c.prefix+"/"))
}

// ensureLoaded performs the one bulk read, if the wrapped store can do one
// and nothing has been written yet. Callers must NOT hold c.mu.
func (c *RunCache) ensureLoaded(ctx context.Context) {
	bulk, ok := c.inner.(BulkReader)
	if !ok {
		return
	}
	c.mu.Lock()
	if c.loaded {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	all, err := bulk.GetAll(ctx, c.prefix)
	if err != nil {
		// A bulk read that fails is not this layer's error to raise: the
		// caller asked for one key and there is a per-key path that answers
		// exactly that question, with exactly that question's error
		// handling. Falling back costs a trip; failing here would turn an
		// optimization into a new way for a plan to stop.
		return
	}

	c.mu.Lock()
	if !disabled() && !c.loaded {
		for key, rec := range all {
			c.entries[key] = cacheEntry{payload: rec.Payload, version: rec.Version, exists: true}
		}
		c.loaded = true
	}
	c.mu.Unlock()
}

func (c *RunCache) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if disabled() {
		return c.inner.Get(ctx, key)
	}
	if c.covers(key) {
		c.ensureLoaded(ctx)
	}

	c.mu.Lock()
	hit, ok := c.entries[key]
	// The snapshot is complete for its namespace, so a key inside it that is
	// not in the map holds no record. This is the half a hit-only cache
	// leaves behind, and it is not a rounding error: at scale 1 the estate
	// has 79 declared addresses and 78 record files, and every read of the
	// missing one is a read that returns "nothing".
	if !ok && c.loaded && c.covers(key) {
		hit, ok = cacheEntry{}, true
	}
	c.mu.Unlock()
	if ok {
		return clone(hit.payload), hit.version, hit.exists, nil
	}

	payload, version, exists, err := c.inner.Get(ctx, key)
	if err != nil {
		return payload, version, exists, err
	}
	c.mu.Lock()
	if !disabled() {
		c.entries[key] = cacheEntry{payload: clone(payload), version: version, exists: exists}
	}
	c.mu.Unlock()
	return payload, version, exists, nil
}

func (c *RunCache) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	defer c.noteWrite()
	return c.inner.PutIfVersion(ctx, key, payload, expectedVersion)
}

func (c *RunCache) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	defer c.noteWrite()
	return c.inner.PutIfAbsent(ctx, key, payload)
}

func (c *RunCache) Delete(ctx context.Context, key string, expectedVersion string) error {
	defer c.noteWrite()
	return c.inner.Delete(ctx, key, expectedVersion)
}

func (c *RunCache) List(ctx context.Context, keyPrefix string) ([]string, error) {
	if disabled() {
		return c.inner.List(ctx, keyPrefix)
	}
	// A namespace already snapshotted knows its own key set; listing it
	// again asks the store a question it has already answered.
	if c.covers(keyPrefix) || keyPrefix == c.prefix {
		c.ensureLoaded(ctx)
		c.mu.Lock()
		if c.loaded {
			keys := make([]string, 0, len(c.entries))
			for key := range c.entries {
				if strings.HasPrefix(key, keyPrefix) {
					keys = append(keys, key)
				}
			}
			c.mu.Unlock()
			sort.Strings(keys)
			return keys, nil
		}
		c.mu.Unlock()
	}

	c.mu.Lock()
	hit, ok := c.lists[keyPrefix]
	c.mu.Unlock()
	if ok {
		out := make([]string, len(hit))
		copy(out, hit)
		return out, nil
	}

	keys, err := c.inner.List(ctx, keyPrefix)
	if err != nil {
		return keys, err
	}
	c.mu.Lock()
	if !disabled() {
		stored := make([]string, len(keys))
		copy(stored, keys)
		c.lists[keyPrefix] = stored
	}
	c.mu.Unlock()
	return keys, nil
}

// GetAll passes a bulk read through to the wrapped store, so a caller that
// wants the whole namespace still gets it in one call. It is deliberately
// NOT served from the snapshot: the only caller of a bulk read that is not
// this cache is one that wants the store's own current answer.
func (c *RunCache) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	bulk, ok := c.inner.(BulkReader)
	if !ok {
		return nil, errNoBulkReader
	}
	return bulk.GetAll(ctx, keyPrefix)
}

// errNoBulkReader is what [RunCache.GetAll] reports when the store beneath
// it cannot enumerate values.
var errNoBulkReader = errors.New("staterecord: the wrapped store does not support a bulk read")

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
