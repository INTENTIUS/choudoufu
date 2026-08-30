// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"log"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #585. The read pass makes one ImportResourceState and one
// ReadResource per managed instance, and issue #584 measured those calls to be
// exactly the calls stock's refresh makes - 148, 556 and 1372 at scales 1, 4
// and 10, call for call. Stock makes them at -parallelism 10. This package
// made them one at a time: no `go func`, no errgroup, no WaitGroup, no
// semaphore anywhere in it. At scale 10 that is 1372 round trips at the ~0.138
// seconds each stock's own 19-second refresh implies, which is ~190 seconds -
// about 59% of the 322 seconds issue #617 measured for the whole plan. The
// work was never the cost; the waiting was.
//
// DefaultReadParallelism is how many of those round trips this fork has in
// flight at once when nothing else settles it.
//
// Ten, for the same reason [liveimport.DefaultParallelism] and
// [discovery.DefaultSweepParallelism] are ten: it is the number stock uses. An
// OpenTofu plan of the very same estate walks its graph at -parallelism 10
// (internal/tofu/context.go), and every one of those slots is a provider round
// trip against the same account through the same provider process. An operator
// who can plan this configuration is already asking the account for ten
// concurrent reads at every plan, so a read pass that does the same asks for
// nothing new. That is the whole argument, and it is why the number is not
// tuned up on the strength of an emulator run: throttling on the READ path is
// unmeasured (issue #567 measured it for parallel writes, not reads), floci
// does not throttle at all, and so no measurement taken against the emulator
// can justify a number above the one real AWS is already known to tolerate.
//
// Set it per run with [Options.ReadParallelism]. One reproduces the sequential
// loop exactly: one worker, started in loop order, and a consumer that waits
// on each instance in that same order.
const DefaultReadParallelism = 10

// readPrep is everything [builder.materialize] settles before it reads. See
// [builder.prepareRead], which is the whole of materialize's former head and
// the only thing that ever builds one.
type readPrep struct {
	// terminal, when non-nil, is a head that decided the instance cannot be
	// read at all: no resource block, no provider, an unusable provider or an
	// unusable schema. No call is made for it.
	terminal *readTerminal

	rc           *configs.Resource
	modPath      addrs.Module
	providerAddr addrs.AbsProviderConfig
	entry        *providerEntry
	schema       providers.Schema
	target       providers.ImportTarget

	attrsSeed      map[string]cty.Value
	attrsSeedMarks []cty.PathValueMarks
}

// readTerminal is one of [builder.prepareRead]'s four refusals, carried as
// data rather than written when it is decided.
//
// The distinction is the whole ordering guarantee: a prepared read is settled
// before the instances ahead of it in the loop have finished materializing, so
// a diagnostic appended at that moment would appear in the run's output ahead
// of diagnostics from instances that come before it. [builder.materialize]
// applies this at its own point in the sequence, which is where the sequential
// pass always appended it.
type readTerminal struct {
	diags  tfdiags.Diagnostics
	reason Reason
	detail string
	cause  string
}

// readFetch is one instance's read: the plan for it, and - once done is
// closed - what the provider answered.
//
// Nothing here is bookkeeping. A fetch carries the transport's answer and
// nothing else, because the point of this shape (issue #605's, on the sweep)
// is that the answer is handed to the SAME sequential [builder.materialize]
// body that would have called for it, in the SAME order, so that every
// diagnostic, every state write, every ownership verdict and every ordering
// property of the result is produced by unchanged code. Concurrency here buys
// the waiting back and changes nothing else.
type readFetch struct {
	// done is closed by the worker once every answer field below is written,
	// and is nil for a fetch [builder.readFor] made inline on the caller's own
	// goroutine (which has therefore already written them). A consumer must
	// not read them before it has received from done; that receive is the
	// happens-before edge the whole design rests on.
	done chan struct{}

	// want is the [wanted] this fetch was planned and called for.
	// [readPrefetch.take] refuses to hand its answer to a caller asking about
	// a different one - see [sameWanted].
	want wanted

	prep readPrep

	obj        *states.ResourceInstanceObject
	importStub cty.Value
	status     materializeStatus
	diags      tfdiags.Diagnostics
}

// readPrefetch is one concrete phase's worth of in-flight reads.
//
// Every read is PLANNED synchronously, on the caller's goroutine, before a
// single worker starts. A plan reads the configuration, [Options], the
// provider cache and the estate's record store; the consuming loop writes into
// the builder's live map, its state, its diagnostics and its omission list.
// Planning on the workers would race the very loop they feed, and planning
// what a worker calls with is also what keeps the calls identical: a worker
// does the transport call and nothing else.
type readPrefetch struct {
	entries map[string]*readFetch
	order   []string

	// slots is the concurrency bound AND the backpressure. A worker acquires a
	// slot before its call and never releases it; the CONSUMER releases one
	// when it takes that instance's answer. So at most ReadParallelism reads
	// are ever fetched-but-unconsumed, and the pass's peak memory stays a
	// multiple of the parallelism rather than of the estate - which matters,
	// because a projection over a thousand instances holding every read
	// object at once is a far worse regression than the slowness this fixes.
	slots chan struct{}

	wg sync.WaitGroup

	// mu guards taken and mismatched. Every other field is written before any
	// worker starts (entries, order, slots) or is per-entry and published
	// through readFetch.done.
	mu    sync.Mutex
	taken map[string]bool

	// mismatched counts the answers a consumer declined because the instance
	// it asked about was not the one the plan fetched for. It is always zero;
	// it is a field rather than a panic so a test can assert that, and so a
	// real run degrades into an extra read rather than into a wrong one.
	mismatched int
}

// readParallelism is how many reads this projection wants in flight.
func readParallelism(opts Options) int {
	if opts.ReadParallelism > 0 {
		return opts.ReadParallelism
	}
	return DefaultReadParallelism
}

// startReadPrefetch plans every instance in ws and starts issuing their reads,
// up to [readParallelism] at a time, in ws order.
//
// The returned prefetch is inert if ws is empty, and every method on it
// tolerates a nil receiver, so a caller that never starts one compiles down to
// today's behaviour.
func (b *builder) startReadPrefetch(ctx context.Context, ws []wanted) *readPrefetch {
	if len(ws) == 0 {
		return nil
	}

	par := readParallelism(b.opts)
	pf := &readPrefetch{
		entries: make(map[string]*readFetch, len(ws)),
		order:   make([]string, 0, len(ws)),
		slots:   make(chan struct{}, par),
		taken:   make(map[string]bool, len(ws)),
	}

	// Plan first, synchronously, in ws order. Every read of the
	// configuration, the provider cache and the record store that decides a
	// call happens here, on this goroutine, before the consuming loop has
	// mutated anything.
	planned := make([]*readFetch, 0, len(ws))
	for _, w := range ws {
		key := w.addr.String()
		if pf.entries[key] != nil {
			// Two resolutions at one address would otherwise read twice and
			// hand the second answer to nobody. [orderWork] sorts by address
			// and identity resolution does not produce a repeat, but this
			// loop does not depend on that staying true.
			continue
		}
		e := &readFetch{done: make(chan struct{}), want: w, prep: b.prepareRead(ctx, w)}
		pf.entries[key] = e
		pf.order = append(pf.order, key)
		planned = append(planned, e)
	}

	// One launcher goroutine, so that startReadPrefetch returns immediately
	// and the consuming loop can begin on the first instance as soon as its
	// answer lands rather than after the whole estate's have.
	pf.wg.Add(1)
	go func() {
		defer pf.wg.Done()
		for _, e := range planned {
			if e.prep.terminal != nil {
				// No call to make: publish immediately and take no slot, so an
				// estate that is mostly unreadable does not spend its
				// parallelism on instances that never touch the network.
				close(e.done)
				continue
			}
			pf.slots <- struct{}{}
			pf.wg.Add(1)
			go func(e *readFetch) {
				defer pf.wg.Done()
				defer close(e.done)
				runReadFetch(ctx, e)
			}(e)
		}
	}()

	return pf
}

// runReadFetch is the whole of what a prefetch worker does: one
// [importAndRead], its answer stored, nothing else. It touches no shared state
// but the entry it was handed, and reads nothing from the builder.
func runReadFetch(ctx context.Context, e *readFetch) {
	p := e.prep
	e.obj, e.importStub, e.status, e.diags = importAndRead(
		ctx, p.entry.provider, p.schema, e.want.addr.Resource.Resource.Type,
		p.target, e.want.importID, e.want.values, p.attrsSeed, p.attrsSeedMarks,
	)
}

// readFor is [builder.materialize]'s plan and answer for w: the prefetched
// pair when this pass planned one for exactly this instance, and the same plan
// and the same call, made here and now on this goroutine, when it did not.
//
// The inline path is the one every phase other than the concrete loop takes -
// the record-first intercept, the located, derived and undeclared loops - and
// it is byte-for-byte the sequence materialize ran before this file existed:
// prepare, then read, then return.
func (b *builder) readFor(ctx context.Context, w wanted) *readFetch {
	if e := b.readPrefetch.take(w); e != nil {
		return e
	}
	e := &readFetch{want: w, prep: b.prepareRead(ctx, w)}
	if e.prep.terminal != nil {
		return e
	}
	runReadFetch(ctx, e)
	return e
}

// take is the prefetched answer for w, or nil for a caller to read inline.
//
// It blocks until w's own call has landed, and marks it taken, releasing its
// slot so the launcher can start another.
//
// An entry planned for a different [wanted] than the caller is asking about is
// refused rather than used: the plan and the loop are driven from the same
// slice, so they cannot disagree, but if they ever did the answer would be a
// read of some other identity - which no call count and no diagnostic would
// show.
func (pf *readPrefetch) take(w wanted) *readFetch {
	if pf == nil {
		return nil
	}
	key := w.addr.String()
	e := pf.entries[key]
	if e == nil {
		return nil
	}
	pf.mu.Lock()
	already := pf.taken[key]
	pf.taken[key] = true
	pf.mu.Unlock()
	<-e.done
	if !already && e.prep.terminal == nil {
		<-pf.slots
	}
	if !sameWanted(e.want, w) {
		pf.mu.Lock()
		pf.mismatched++
		pf.mu.Unlock()
		log.Printf("[WARN] projection: the read pass prefetched %s for a different resolution than the one it then materialized; reading it again", key)
		return nil
	}
	return e
}

// mismatches is how many prefetched answers a consumer refused because the
// instance it asked about was not the one the plan fetched for. Always zero;
// see [readPrefetch.mismatched].
func (pf *readPrefetch) mismatches() int {
	if pf == nil {
		return 0
	}
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return pf.mismatched
}

// finish drains anything the consuming loop did not take, waits for every
// worker, and returns the instances whose read was made and never used.
//
// A non-empty return is a bug in [builder.startReadPrefetch]: it means the
// read pass spent a provider round trip the sequential body would not have
// spent, which is exactly the "call counts must be identical" property issue
// #585 accepts on. It is reported rather than asserted so that a real run
// degrades into one wasted read rather than a panic, and
// TestReadPrefetchPlansExactlyTheCallsTheLoopMakes holds it to empty.
func (pf *readPrefetch) finish() []string {
	if pf == nil {
		return nil
	}
	var wasted []string
	for _, key := range pf.order {
		pf.mu.Lock()
		taken := pf.taken[key]
		pf.mu.Unlock()
		if taken {
			continue
		}
		// Drain in loop order. The launcher may be blocked acquiring a slot
		// for a later instance, and the slot it needs is one of these; taking
		// them in order is what guarantees the one it is waiting on is
		// released before this loop reaches an instance whose worker has not
		// started.
		e := pf.entries[key]
		pf.mu.Lock()
		pf.taken[key] = true
		pf.mu.Unlock()
		<-e.done
		if e.prep.terminal == nil {
			<-pf.slots
			wasted = append(wasted, key)
		}
	}
	pf.wg.Wait()
	for _, key := range wasted {
		log.Printf("[WARN] projection: the read pass prefetched %s and then never materialized it", key)
	}
	return wasted
}

// sameWanted reports whether two [wanted] values name the same read: the same
// instance, the same identity to import it by, and the same routing flags,
// which between them are every input [builder.prepareRead] and [importAndRead]
// take from a wanted.
func sameWanted(a, b wanted) bool {
	if a.addr.String() != b.addr.String() ||
		a.importID != b.importID ||
		a.undeclared != b.undeclared ||
		a.located != b.located ||
		a.recordFirst != b.recordFirst {
		return false
	}
	if len(a.values) != len(b.values) {
		return false
	}
	for name, v := range a.values {
		if bv, ok := b.values[name]; !ok || bv != v {
			return false
		}
	}
	if len(a.dependsOn) != len(b.dependsOn) {
		return false
	}
	for i := range a.dependsOn {
		if a.dependsOn[i].String() != b.dependsOn[i].String() {
			return false
		}
	}
	switch {
	case a.identity == cty.NilVal && b.identity == cty.NilVal:
		return true
	case a.identity == cty.NilVal || b.identity == cty.NilVal:
		return false
	}
	return a.identity.RawEquals(b.identity)
}
