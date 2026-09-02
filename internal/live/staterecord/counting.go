// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"io"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// CountingStore wraps a [Store] and counts every operation that reaches it,
// which is the record-store half of what
// internal/live/flocitest's CountingProxy counts for provider traffic. The
// proxy stands in front of the AWS endpoint and is therefore blind to these:
// a record read
// goes to a local directory, an SSM parameter or an S3 object, none of which
// the provider's endpoint ever sees. Until this existed, no instrument in
// this repository counted them at all, so "a plan costs N calls" was a
// partial number by construction.
//
// A "trip" is one call to the wrapped store, because that is the unit that
// costs something: against [LocalStore] a stat plus a read, against
// [SSMStore] or [S3Store] a network round trip. Stock OpenTofu makes zero of
// them — it reads its whole state once, from one file.
//
// Each trip records more than a method name, because a bare per-method total
// cannot answer either question a reduction needs answered:
//
//   - Site is the first frame outside this package and outside
//     projection.(*RecordStore) — the code that actually wanted the record.
//     This is the per-site breakdown; without it, "158 Gets" names no line
//     to fix.
//   - Via is the outermost projection.(*RecordStore) method the call came
//     through (GetIdentity, GetResidue, getProvisioned, ...), so several
//     sites reading the same underlying envelope through different accessors
//     stay distinguishable.
//   - Key is the store key, so [CountingStore.RepeatTrips] can report how
//     many trips re-read a key some earlier trip already read. That number
//     is the size of the prize a cache can win, measured rather than assumed.
//
// It is safe for concurrent use; the sweep and the projection both run
// goroutines.
type CountingStore struct {
	inner Store

	mu    sync.Mutex
	trips []Trip

	// log, when non-nil, receives one TSV line per trip as it happens.
	// It exists for the out-of-process case: the measurement that matters
	// is a real `tofu plan` in its own subprocess, so the counts have to
	// leave that process somehow, and a line appended per trip survives a
	// crash mid-run where an end-of-run dump would not.
	log io.Writer
}

// Trip is one operation that reached the wrapped store. See [CountingStore]
// for what each field is for.
type Trip struct {
	Method string
	Key    string
	Via    string
	Site   string
}

// String renders a trip as the one TSV line [CountingStore]'s log writes and
// [ParseTripLog] reads back.
func (t Trip) String() string {
	return strings.Join([]string{t.Method, t.Via, t.Site, t.Key}, "\t")
}

// NewCountingStore wraps inner. log may be nil; when it is not, every trip is
// written to it as a single [Trip.String] line followed by a newline, under
// this store's own lock. A caller sharing one writer between several counting
// stores is responsible for that writer being safe to call from several
// goroutines.
func NewCountingStore(inner Store, log io.Writer) *CountingStore {
	return &CountingStore{inner: inner, log: log}
}

func (c *CountingStore) note(method, key string) {
	via, site := callSite()
	t := Trip{Method: method, Key: key, Via: via, Site: site}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trips = append(c.trips, t)
	if c.log != nil {
		fmt.Fprintln(c.log, t.String())
	}
}

func (c *CountingStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	c.note("Get", key)
	return c.inner.Get(ctx, key)
}

func (c *CountingStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	c.note("PutIfVersion", key)
	return c.inner.PutIfVersion(ctx, key, payload, expectedVersion)
}

func (c *CountingStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	c.note("PutIfAbsent", key)
	return c.inner.PutIfAbsent(ctx, key, payload)
}

func (c *CountingStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	c.note("Delete", key)
	return c.inner.Delete(ctx, key, expectedVersion)
}

func (c *CountingStore) List(ctx context.Context, keyPrefix string) ([]string, error) {
	c.note("List", keyPrefix)
	return c.inner.List(ctx, keyPrefix)
}

// GetAll forwards the optional bulk read, counting it as the one trip it is.
// Forwarding matters as much as counting: without it a [RunCache] stacked
// above this counter could not see that the store beneath can bulk-read, and
// the measurement would report the per-key cost of a stack that only has it
// because it is being measured.
func (c *CountingStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	bulk, ok := c.inner.(BulkReader)
	if !ok {
		return nil, errNoBulkReader
	}
	c.note("GetAll", keyPrefix)
	return bulk.GetAll(ctx, keyPrefix)
}

// Trips returns every trip so far, in order, as a copy.
func (c *CountingStore) Trips() []Trip {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Trip, len(c.trips))
	copy(out, c.trips)
	return out
}

// Total is how many trips reached the wrapped store.
func (c *CountingStore) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.trips)
}

// Reset discards every trip recorded so far, so one process can measure
// several phases separately. It does not touch the log.
func (c *CountingStore) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trips = nil
}

// TripCounts is a set of trips summarized several ways at once. Every field
// is a total over the same trips, so a reader can check them against each
// other rather than having to trust one.
type TripCounts struct {
	Total int

	ByMethod map[string]int
	BySite   map[string]int
	ByVia    map[string]int

	// DistinctKeys is how many different keys the trips touched, and
	// RepeatTrips is Total minus DistinctKeys: the trips that re-read
	// something an earlier trip had already read. A cache can remove at
	// most RepeatTrips of them and never fewer than zero of the rest,
	// which is the whole reason this is reported beside the total.
	DistinctKeys int
	RepeatTrips  int
}

// Summarize buckets trips every way [TripCounts] names.
func Summarize(trips []Trip) TripCounts {
	out := TripCounts{
		Total:    len(trips),
		ByMethod: map[string]int{},
		BySite:   map[string]int{},
		ByVia:    map[string]int{},
	}
	keys := map[string]bool{}
	for _, t := range trips {
		out.ByMethod[t.Method]++
		out.BySite[t.Site]++
		out.ByVia[t.Via]++
		keys[t.Method+" "+t.Key] = true
	}
	out.DistinctKeys = len(keys)
	out.RepeatTrips = out.Total - out.DistinctKeys
	return out
}

// Counts summarizes this store's own trips.
func (c *CountingStore) Counts() TripCounts { return Summarize(c.Trips()) }

// ParseTripLog reads back what [CountingStore]'s log writer wrote. A line
// that does not have the four tab-separated fields is an error rather than a
// skipped line: a partially readable log would under-report, and an
// under-reported cost is exactly the failure this instrument exists to end.
func ParseTripLog(data []byte) ([]Trip, error) {
	var out []Trip
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("staterecord: trip log line %d has %d fields, want 4: %q", i+1, len(parts), line)
		}
		out = append(out, Trip{Method: parts[0], Via: parts[1], Site: parts[2], Key: parts[3]})
	}
	return out, nil
}

// SortedByCount renders a bucket map as lines ordered by descending count,
// ties broken by name, for a report a human reads.
func SortedByCount(m map[string]int) []string {
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, fmt.Sprintf("%6d  %s", p.v, p.k))
	}
	return out
}

// modulePrefix is trimmed off every recorded frame so a site reads as
// "live/projection.(*builder).applyRecordFirst" rather than as a full import
// path repeated on every line.
const modulePrefix = "github.com/intentius/choudoufu/internal/"

// recordStoreFrame is the wrapper whose frames are attributed to Via rather
// than to Site: it is the accessor layer, never the code that wanted the
// record.
const recordStoreFrame = "projection.(*RecordStore)"

// callSite walks up from a store method to the code that asked for the
// record. The counter's own frames are skipped by name rather than by a
// frame count, because a count is wrong the moment the compiler inlines one
// of them; frames on projection.(*RecordStore) are the accessor the call
// came through, and the outermost of those becomes Via. The first frame that
// is neither is the site.
//
// The skip test names this package's counter specifically rather than the
// package, so a caller that lives in this package too — a test — is still
// attributed to itself. A store implementation is always BELOW these frames,
// never above, so it can never be mistaken for a site.
func callSite() (via, site string) {
	var pcs [32]uintptr
	n := runtime.Callers(1, pcs[:])
	if n == 0 {
		return "", "unknown"
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		switch {
		case f.Function == "":
			// nothing to attribute; keep walking
		case strings.HasPrefix(f.Function, "runtime."):
			// runtime.Callers itself
		case strings.HasSuffix(f.Function, "staterecord.callSite"),
			strings.Contains(f.Function, "staterecord.(*CountingStore)."),
			strings.Contains(f.Function, "staterecord.(*RunCache)."):
			// the counter's own frames, and the decorators stacked with it:
			// [RunCache] sits above the counter, so without this every
			// cache miss would be attributed to the cache rather than to
			// the code that wanted the record
		case strings.Contains(f.Function, recordStoreFrame):
			via = shortFunc(f.Function)
		default:
			return via, fmt.Sprintf("%s %s:%d", shortFunc(f.Function), path.Base(f.File), f.Line)
		}
		if !more {
			return via, "unknown"
		}
	}
}

func shortFunc(fn string) string {
	if i := strings.Index(fn, modulePrefix); i >= 0 {
		return fn[i+len(modulePrefix):]
	}
	if i := strings.LastIndex(fn, "/"); i >= 0 {
		return fn[i+1:]
	}
	return fn
}
