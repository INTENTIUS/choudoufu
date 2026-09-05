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
	"github.com/intentius/choudoufu/internal/live/identity"
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

// GitHub issue #683. DefaultReadParallelism above bounds the calls; this
// bounds the ANSWERS, and until #683 one number did both jobs.
//
// The consuming loop walks instances in build order and waits for each one's
// answer in turn. When a single read is slow - one aws_route53_record in SDK
// backoff, 26.20 seconds from first throttle to success, 25.93 of them spent
// sleeping - every read behind it in the order had already landed and was
// waiting to be consumed. Holding the same slot for "this call is in flight"
// and for "this answer is fetched and unconsumed" meant those landed answers
// went on holding the bound: the launcher blocked, and a plan with 745 reads
// to make made one, intermittently, for 26 seconds. That is head-of-line
// blocking, and no peak-concurrency statistic shows it, because ten reads
// really are outstanding - they just cannot retire.
//
// So there are two bounds now. A read in flight holds one of
// [readParallelism]; an answer fetched and not yet consumed holds one of
// [readBuffer]; a slow read holds the first and none of the second, and the
// launcher keeps starting reads behind it.
//
// The memory bound is the half that must survive, and it is why the fix is a
// split rather than a release-on-completion: an unbounded read-ahead would
// hold every one of an estate's answers at once whenever the consumer lags,
// which is a far worse regression than the slowness it fixes.
//
// DefaultReadBufferFactor is how deep that buffer is per in-flight slot when
// nothing else settles it: one hundred, so the default pass holds a thousand
// unconsumed answers and the nine further ones its in-flight reads may land
// behind the gate.
//
// A hundred rather than a round number picked for looking round. The bound
// has to clear the estates this fork is measured on - the largest real-AWS
// plan behind #683 is 745 instances - or the buffer, not the straggler,
// becomes what stops the pass, and the defect returns wearing a different
// number. It also has to stay a multiple of the WIDTH rather than of the
// estate, so that a projection over ten thousand instances still holds a
// thousand answers and not ten thousand. A hundred per slot is the smallest
// round multiple that does both.
//
// What it costs is bounded and small: an unconsumed answer holds the same
// object the consumer is about to write into prior state, which the
// projection holds for every instance by the end of the pass either way. The
// buffer bounds the DUPLICATION, not the residency.
//
// Set it per run with [Options.ReadBuffer].
const DefaultReadBufferFactor = 100

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

	// inflight is the concurrency bound, and only that. A worker holds one
	// from before its call until the moment that call returns - see
	// [readPrefetch.publish], which releases it - so what it bounds is
	// requests the cloud is answering right now, which is what
	// [Options.ReadParallelism] has always meant.
	//
	// Until issue #683 this was one channel doing this job and the buffer's
	// below, and a read that was slow therefore held the width down while
	// every answer behind it sat waiting to be consumed.
	inflight chan struct{}

	wg sync.WaitGroup

	// mu guards buffered, taken and mismatched, and is bufferFree's lock.
	// Every other field is written before any worker starts (entries, order,
	// inflight, buffer) or is per-entry and published through readFetch.done.
	mu sync.Mutex

	// buffer is the memory bound: how many answers may be fetched and not yet
	// consumed before the launcher stops starting reads. buffered is how many
	// there are, incremented by [readPrefetch.publish] when an answer lands
	// and decremented by [readPrefetch.consumed] when the loop takes it.
	//
	// The launcher waits on bufferFree rather than on a channel receive
	// because the token is produced by a CONSUMER and spent by the LAUNCHER,
	// with the worker in between doing neither: a worker that blocked on a
	// full buffer would be holding an answer the consumer might be waiting
	// for, which is the head-of-line stall again one goroutine over.
	//
	// The peak this bounds is buffer + ReadParallelism - 1 answers. The
	// launcher passes this gate and then waits for an in-flight slot, so it
	// is admitted while buffered is at most buffer-1 and the reads already in
	// flight may all land behind it. That is a multiple of the two bounds and
	// never of the estate, which is the property the single slot channel used
	// to carry.
	buffer     int
	buffered   int
	bufferFree *sync.Cond

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

// readBuffer is how many fetched answers this projection will hold ahead of
// the consuming loop - [DefaultReadBufferFactor] per in-flight slot when
// [Options.ReadBuffer] does not settle it.
//
// It is never below one: a zero buffer would be a launcher that may not start
// a read until the previous answer has been consumed, which is the sequential
// pass with extra goroutines.
func readBuffer(opts Options) int {
	if opts.ReadBuffer > 0 {
		return opts.ReadBuffer
	}
	return readParallelism(opts) * DefaultReadBufferFactor
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
		entries:  make(map[string]*readFetch, len(ws)),
		order:    make([]string, 0, len(ws)),
		inflight: make(chan struct{}, par),
		buffer:   readBuffer(b.opts),
		taken:    make(map[string]bool, len(ws)),
	}
	pf.bufferFree = sync.NewCond(&pf.mu)

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
		prep := b.prepareRead(ctx, w)
		if prep.terminal == nil && b.cacheWouldServe(ctx, w, prep) {
			// Issue #692 increment 2's other half: the consumer's
			// [builder.cacheHit] is going to answer this instance from the
			// cache, and a wire read planned here anyway would spend the
			// whole saving - a hit that still pays for its read is
			// bookkeeping, not an optimisation. No entry is registered, so
			// if the consumer's own check ever disagreed (it cannot: both
			// evaluate the same pure inputs) the instance reads inline
			// through [builder.readFor]'s fallback, exactly as before.
			continue
		}
		e := &readFetch{done: make(chan struct{}), want: w, prep: prep}
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
			// Both bounds, in this order: the buffer says the consumer is
			// not already holding all the answers it may hold, and the
			// in-flight channel says the cloud is not already answering all
			// the calls it may answer. A read that is slow keeps the second
			// and never reaches the first, so the reads behind it launch.
			pf.reserveBuffer()
			pf.inflight <- struct{}{}
			pf.wg.Add(1)
			go func(e *readFetch) {
				defer pf.wg.Done()
				defer pf.publish(e)
				runReadFetch(ctx, e)
			}(e)
		}
	}()

	return pf
}

// startRecordFirstPrefetch plans the reads [builder.applyRecordFirst]'s loop
// is about to make, so that they are in flight while that loop consumes them.
//
// It re-reads each resolution's record rather than handing the loop a
// precomputed one, and that is deliberate: [builder.materializeFromRecord]
// stays byte-for-byte what it was, so every diagnostic it writes, every
// envelope version it records, every stale-record fallthrough and the order of
// all three are produced by unchanged code - the same argument
// [builder.startReadPrefetch] makes for the concrete phase. The second read is
// free: [RecordStore.GetIdentity] is served from the run cache issue #636
// introduced, so the whole intercept is still the one GetAll that issue
// measured, and TestRecordTripsAgainstFloci is what holds that.
//
// A resolution this pass declines to plan for - record-backed or
// record-located (the loop skips those itself), no record, or a record that
// will not decode - simply gets no entry, and the loop reads it inline through
// [builder.readFor]'s own fallback, which is what every instance did before
// this existed.
func (b *builder) startRecordFirstPrefetch(ctx context.Context, resolutions []identity.Resolution) *readPrefetch {
	ws := make([]wanted, 0, len(resolutions))
	for _, r := range resolutions {
		switch r.Class {
		case identity.ClassRecordBacked, identity.ClassRecordLocated:
			continue
		}
		rec, _, _, identityFound, err := b.opts.RecordStore.GetIdentity(ctx, r.Addr)
		if err != nil || !identityFound {
			continue
		}
		ws = append(ws, wanted{
			addr:        r.Addr,
			importID:    rec.ImportID,
			values:      recordFirstStubValues(rec),
			undeclared:  r.Undeclared,
			recordFirst: true,
		})
	}
	return b.startReadPrefetch(ctx, ws)
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
	// The cache is consulted before the prefetch, so a hit costs no read on
	// this goroutine. prepareRead is pure - configuration lookup and schema
	// resolution, no provider call - so paying it first is free, and the
	// cache needs its schema to decode.
	//
	// A hit on the concrete phase still leaves a prefetched answer unconsumed,
	// which readPrefetch.finish reports as wasted. That accounting is correct
	// and deliberately left visible: the saving on that phase comes from not
	// PLANNING the read, which is a separate change, while the record-first
	// intercept - 78 of 79 instances on a migrated estate - reads inline
	// through here and is saved outright.
	prep := b.prepareRead(ctx, w)
	if prep.terminal == nil {
		if hit := b.cacheHit(ctx, w, prep); hit != nil {
			return hit
		}
	}
	if e := b.readPrefetch.take(w); e != nil {
		return e
	}
	e := &readFetch{want: w, prep: prep}
	if e.prep.terminal != nil {
		return e
	}
	runReadFetch(ctx, e)
	return e
}

// cacheHit is issue #685's whole point: the answer for w taken from the state
// cache instead of from a provider read, or nil to read.
//
// Two conditions, and the second is what makes this safe in a way it would not
// be for stock OpenTofu.
//
//  1. The cache holds an object for this exact instance.
//  2. Ownership.Verified holds the address - meaning the estate sweep found a
//     live object carrying this instance's own tofu-address marker, in this
//     run, moments ago.
//
// A second arm (issue #692 increment 2) serves when the tag index could
// not answer: the run's listing pass proves existence and identity, and
// the record envelope attests ownership - see [builder.envelopeVouched].
//
// So a cached entry is a CANDIDATE and the tag index is the oracle. The cache
// is never trusted on its own: it supplies attributes for an object the cloud
// has just confirmed exists and is ours. That is why staleness costs reads
// rather than correctness, and why the one-sided oracle is unchanged - a
// marker present proves existence, and a marker absent means no hit and an
// ordinary read.
//
// What this does NOT do is skip the read for anything the sweep did not
// verify: an untaggable instance, an instance whose marker is gone, an
// instance the cache has never seen. Those all fall through and read.
func (b *builder) cacheHit(ctx context.Context, w wanted, prep readPrep) *readFetch {
	if b.opts.StateCache == nil {
		return nil
	}
	// Issue #712: a default plan reads every instance - the read IS drift
	// detection, and only -refresh=false (Options.CacheServesReads) opts
	// into serving attributes from the cache, the same trade stock's own
	// flag makes, made safer by the sweep's existence-and-ownership vouch.
	if !b.opts.CacheServesReads {
		return nil
	}
	// Verified is the tag index's own answer. Without it, the second arm
	// (issue #692 increment 2) may still vouch: the listing pass proves
	// existence and identity, the record envelope attests ownership. No
	// arm vouches, no hit.
	obj, ok := b.cacheCandidate(w, prep)
	if !ok {
		return nil
	}
	vouch := "marker verified by the estate sweep"
	if !b.opts.Ownership.verified(w.addr) {
		if !b.envelopeVouched(ctx, w, prep) {
			return nil
		}
		vouch = "listed live this run, ownership record-attested"
		if b.envelopeAdmitted == nil {
			b.envelopeAdmitted = map[string]bool{}
		}
		b.envelopeAdmitted[w.addr.String()] = true
	}
	b.cacheHits++
	log.Printf("[DEBUG] projection: state cache hit for %s, %s; no provider read", w.addr, vouch)
	return &readFetch{want: w, prep: prep, obj: obj, status: statusMaterialized}
}

// cacheCandidate is the evidence-free half of the hit rule: the gates and
// the cache's own entry, decoded. Everything here is pure - options,
// cache bytes, schema - which is what lets [builder.cacheWouldServe]
// evaluate it on the planning goroutine and trust the consumer to reach
// the same answer.
func (b *builder) cacheCandidate(w wanted, prep readPrep) (*states.ResourceInstanceObject, bool) {
	if b.opts.StateCache == nil || !b.opts.CacheServesReads {
		return nil, false
	}
	ms := b.opts.StateCache.Module(w.addr.Module)
	if ms == nil {
		return nil, false
	}
	is := ms.ResourceInstance(w.addr.Resource)
	if is == nil || !is.HasCurrent() {
		return nil, false
	}
	obj, err := is.Current.Decode(prep.schema.Block.ImpliedType())
	if err != nil {
		// A cache we cannot decode is a cache we ignore. It is not an error:
		// the file may have been written by another version, and the read
		// path is always correct.
		log.Printf("[DEBUG] projection: state cache holds %s but it did not decode (%s); reading instead", w.addr, err)
		return nil, false
	}
	return obj, true
}

// cacheWouldServe answers, on the prefetch launcher's planning pass, the
// question [builder.cacheHit] will answer for the consumer: is this
// instance served from the cache? Both read the same pure inputs, so the
// launcher may skip planning the wire read outright (issue #692
// increment 2's request-count half). The one side effect - registering an
// envelope admission - stays with cacheHit, which always runs.
func (b *builder) cacheWouldServe(ctx context.Context, w wanted, prep readPrep) bool {
	if _, ok := b.cacheCandidate(w, prep); !ok {
		return false
	}
	if b.opts.Ownership.verified(w.addr) {
		return true
	}
	return b.envelopeVouched(ctx, w, prep)
}

// envelopeVouched is issue #692 increment 2's second eligibility arm: the
// evidence a full read provides - existence, identity, ownership -
// assembled from cheaper sources for an instance the tag index could not
// vouch (a type the tagging API does not serve, listed by a call that
// returns no tags).
//
//   - Existence and identity: [Options.CacheVouchSightings] holds the
//     identities this run's listing pass saw live. For a client-named
//     type, the listed name is the import identity, so a sighting of
//     prep's own target identity is a sighting of this instance -
//     provided the sighting came from THIS instance's own provider
//     configuration's pass (issue #745). An estate that mirrors one
//     client-chosen name into two regions has two live objects answering
//     to one import identity, and the other region's listing says
//     nothing about whether this one still exists.
//   - Ownership: the record envelope, already bulk-loaded through
//     [Options.RecordStore]'s run cache at zero additional calls, is
//     authoritative for which object an address owns (issue #364). It
//     must name exactly the identity the read would have fetched.
//
// Every leg fails toward reading: no sightings, a multi-component
// identity (ImportID empty), a missing or mismatched record, a store
// error - all return false and the instance takes the ordinary read.
// [Options.EnvelopeVouchServes] carries the maintainer ruling's bound:
// plan-shaped operations only.
func (b *builder) envelopeVouched(ctx context.Context, w wanted, prep readPrep) bool {
	if !b.opts.EnvelopeVouchServes || b.opts.RecordStore == nil {
		return false
	}
	if prep.target.ID == "" {
		return false
	}
	typeName := w.addr.Resource.Resource.Type
	if !b.opts.CacheVouchSightings.Sighted(prep.providerAddr, typeName, prep.target.ID) {
		return false
	}
	rec, _, _, identityFound, _, err := b.locatedIdentityWithAliases(ctx, w.addr)
	if err != nil || !identityFound {
		return false
	}
	return rec.ImportID == prep.target.ID
}

// reserveBuffer blocks the launcher until there is room for one more
// unconsumed answer. It is the memory half of issue #683's split.
//
// It cannot deadlock a consumer waiting on an instance the launcher has not
// started yet. Reads are launched in order and answers are consumed in that
// same order, so if the launcher is waiting here before instance i, every
// answer that could be counted in buffered belongs to an instance before i -
// and the consumer, to be waiting on i, has taken all of those. buffered is
// therefore zero and the wait is over. The same argument covers
// [readPrefetch.finish]'s drain, which consumes in that same order.
func (pf *readPrefetch) reserveBuffer() {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	for pf.buffered >= pf.buffer {
		pf.bufferFree.Wait()
	}
}

// publish is what a worker does with an answer: count it against the buffer,
// hand it to the consumer, and give the call's slot back.
//
// The order of the three is the whole of the accounting. Counting before the
// close is what makes the count safe against a consumer that takes the answer
// the instant it lands - [readPrefetch.consumed]'s decrement happens after a
// receive from done, so it can never run before the increment it undoes.
// Releasing the in-flight slot last is what makes the bound honest: the slot
// is held for exactly as long as the provider was being asked.
func (pf *readPrefetch) publish(e *readFetch) {
	pf.mu.Lock()
	pf.buffered++
	pf.mu.Unlock()
	close(e.done)
	<-pf.inflight
}

// consumed gives back the buffer slot the answer for one instance held.
func (pf *readPrefetch) consumed() {
	pf.mu.Lock()
	pf.buffered--
	pf.mu.Unlock()
	pf.bufferFree.Signal()
}

// take is the prefetched answer for w, or nil for a caller to read inline.
//
// It blocks until w's own call has landed, and marks it taken, releasing its
// buffer slot so the launcher can start another read. The call's own slot -
// [readPrefetch.inflight] - was released when the call returned, which is
// issue #683: a consumer that is still working through the instances ahead of
// this one is no longer what decides whether the cloud is being asked
// anything.
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
		pf.consumed()
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
		// Drain in loop order. Issue #585 needed that order to guarantee the
		// launcher's awaited slot came back before this loop reached an
		// instance whose worker had not started; issue #683 splits the slot
		// in two and that argument has to be re-derived rather than
		// inherited. It survives, in [readPrefetch.reserveBuffer]'s own
		// terms: this drain consumes in the same order the launcher launches
		// in, so a launcher waiting for buffer room before instance i is
		// waiting on answers to instances before i, all of which this loop
		// has already taken by the time it reaches i. The in-flight half
		// needs no ordering argument at all - a worker gives that slot back
		// when its call returns, with no consumer involved.
		e := pf.entries[key]
		pf.mu.Lock()
		pf.taken[key] = true
		pf.mu.Unlock()
		<-e.done
		if e.prep.terminal == nil {
			pf.consumed()
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
