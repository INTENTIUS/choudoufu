// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"log"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #605. The estate-wide sweep makes one list call per admitted
// type, and against a real account that is very nearly the whole of a
// stateless plan's wall clock. #578's certification run, scale 1, 79
// resources, us-east-2, three runs a side, every plan empty: stock 3s/4s/3s
// against this fork's 203s/211s/200s. The sweep is 558 of those calls, so
// the 201.3s mean difference over stock is 0.36s per sweep call - one
// network round trip apiece - and the sweep is about 201 of the plan's 205
// seconds. The read pass around it is already at exact call parity with
// stock (148/556/1372 at scales 1/4/10, both sides), so there is nothing
// else in it to remove - only the waiting to overlap.
//
// Quote a rate with the denominator it was divided by. 0.36s/call is
// 201.3 / 558, the whole sweep. The native leg alone is 521 of those 558
// calls and its own pair is 521 x 0.39s = 203s; 0.367 is 205 / 558 and so
// belongs beside 558, never beside 521 (issue #618 - 0.367 x 521 is 191).
//
// Those seconds predate this file. #578 ran the sweep sequentially, and
// nobody has repeated the real-AWS run since #605 made it concurrent, so
// 205s is the pre-concurrency plan and the post-#605 real-AWS cost is
// unmeasured. What has been measured against the emulator is that the call
// count does not move with the parallelism, which is the property this file
// is responsible for; see TestSweepParallelismAgainstFloci.
//
// DefaultSweepParallelism is how many of those calls this fork has in flight
// at once when nothing else settles it.
//
// Ten, for the same reason [liveimport.DefaultParallelism] is ten (issue
// #583): it is the number stock uses. An OpenTofu plan of the very same
// estate walks its graph at -parallelism 10 by default
// (internal/tofu/context.go), and every one of those slots is a provider
// round trip against the same account through the same provider process. An
// operator who can plan this configuration has already been asking the
// account for ten concurrent reads at every plan, so a sweep that does the
// same asks for nothing new - which is the whole argument, and it is why the
// number is not tuned up on the strength of an emulator run. floci does not
// throttle (issue #567's opening paragraph says so), so no measurement taken
// against it can justify a number above the one real AWS is already known to
// tolerate.
//
// Set it per run with [Request.SweepParallelism]. One reproduces the
// sequential loop exactly: one worker, started in universe order, and a
// consumer that waits on each type in that same order.
const DefaultSweepParallelism = 10

// GitHub issue #839. DefaultSweepParallelism above bounds the list CALLS;
// this bounds the ANSWERS, and until #839 one number did both jobs.
//
// The consuming loop walks the universe in [sweepTypes] order and waits for
// each type's listing in turn. A list call that lands in SDK backoff
// therefore held the width down for every type behind it in that order: the
// nine calls launched beside it had already returned, and their answers went
// on holding slots because the slot a worker took was released by the
// CONSUMER, not by the call. The launcher blocked, and a sweep with hundreds
// of types left to list started none of them until the slow one answered.
// That is head-of-line blocking, and no peak-concurrency statistic shows it,
// because ten calls really are outstanding - they just cannot retire.
//
// So there are two bounds now. A list call in flight holds one of
// [sweepParallelism]; a listing fetched and not yet consumed holds one of
// [sweepBuffer]; a slow call holds the first and none of the second, and the
// launcher keeps starting calls behind it.
//
// The memory bound is the half that must survive, and it is why the fix is a
// split rather than a release-on-completion. An unconsumed answer here is a
// whole type's listing - every live object of that type the account returned
// - and it is NOT something the run holds anyway: [scanType] consumes a
// listing, files its scan row, its claims and its gaps, and drops the
// objects. So unlike the read pass's answers (issue #683, where an
// unconsumed answer duplicates an object prior state ends up holding either
// way), the sweep's buffer is residency the run would not otherwise pay, and
// an unbounded read-ahead would hold the whole admission table's listings at
// once.
//
// DefaultSweepBufferFactor is how deep that buffer is per in-flight slot when
// nothing else settles it: ten, so the default sweep holds a hundred
// unconsumed listings.
//
// The number is derived from the straggler it exists to cover rather than
// from the universe. The buffer stops helping the moment the launcher blocks
// on it, so what it has to be worth is the WORK the slow call is holding up.
// #578's certification run puts the sweep at 0.36s per call - 201.3 seconds
// over the whole sweep's 558 calls, the denominator this file's opening
// comment insists on - and the backoff #683 measured on the read path was
// 26.20 seconds. A hundred calls of read-ahead is about thirty-six seconds of
// sweep, which clears that. Ten per slot rather than the read pass's hundred
// because of the paragraph above: a listing is not one object, and buying
// more read-ahead than the straggler costs would buy it in memory the sweep
// does not otherwise spend.
//
// It stays a multiple of the WIDTH and never of the admission table, which is
// the property the single slot channel used to carry: a sweep over a thousand
// types still holds a hundred listings at the default width, and a sweep at
// parallelism one holds ten.
//
// # Measured on real AWS, and what the measurement could not settle
//
// Issue #839 shipped this split with the mechanism proven by a fake and the
// real-AWS number owed, because floci does not throttle. Issue #867 took it,
// on the 745-instance terralith (us-east-2, provider 6.59.0, choudoufu built
// from `d455a2fed4`, harness and instrument `3889d2476c`, 2026-09-06): three
// steady-state plans, every idle gap of at least 0.8s attributed to the pass
// whose OWN call was in backoff, read off the tf_rpc the provider stamps on
// its `retrying request` line - ListResource is this file, ReadResource is
// the read pass.
//
//	              sweep stalls   largest sweep stall   read-pass stalls
//	choudoufu 1        1                1.23s                 3
//	choudoufu 2        1                1.51s                 1
//	choudoufu 3        0                    -                 5
//
// Two throttled list calls across three runs, costing 1.23s and 1.51s. No
// straggler: nothing on this side of the run came near the 26.20 seconds of
// backoff #683 measured on the read path, and the buffer was never what
// stopped the launcher.
//
// The reason is structural, and it is the half worth carrying forward,
// because it says the run could not have found a straggler however long it
// ran. Of the types this estate's sweep covers, thirty-two are answered by
// ONE estate-filtered GetResources through the tagging leg (tagging.go's
// sweepViaTagging), which is not per-type work and takes no slot here. Only
// three - aws_ecs_service, aws_iam_policy, aws_iam_role - take the per-type
// list path this file bounds, on the first post-migration plan and on a
// steady-state one alike. Three calls against a width of ten means at most
// three listings are ever fetched-and-unconsumed, so [sweepBuffer] is never
// reached and a factor of one would have produced the identical run. The
// largest real-AWS estate this fork is measured on does not exercise this
// number.
//
// So ten still rests on the derivation above and not on a measurement, and
// #867 is cited here for the negative result rather than as a confirmation.
// What would test it is an estate whose types mostly lack a server-side tag
// filter, so the concurrent leg is hundreds of list calls rather than three;
// that is also the only shape in which #839's defect could have cost
// anything.
//
// Set it per run with [Request.SweepBuffer].
const DefaultSweepBufferFactor = 10

// sweepFetchKind is which transport a swept type's listing goes over, decided
// by [planSweepFetch] before any goroutine starts.
type sweepFetchKind int

const (
	// fetchNone is a type the sweep makes no call for at all: the provider
	// cannot list it, its schema carries no tags for a marker to live on, its
	// list configuration would not build, Cloud Control has no listable CFN
	// type for it, or the registry says that CFN type is untaggable. Every
	// one of those is a case [scanType] and [scanTypeCloudControl] answer
	// from a refusal or a [SweepGap] without touching the network, and this
	// planner reaches the same answer from the same predicates.
	fetchNone sweepFetchKind = iota

	// fetchNative is the provider's own list resource - [listclient.List].
	fetchNative

	// fetchCloudControl is Cloud Control's ListResources on the CFN type
	// [cloudControlSource] maps this type to.
	fetchCloudControl
)

// sweepFetch is one swept type's list call: the decision to make it, the
// configuration it is made with, and - once done is closed - what came back.
//
// Nothing here is bookkeeping. A fetch carries the transport's own answer and
// nothing else, because the whole point of issue #605's shape is that the
// answer is then handed to the SAME sequential [scanType] body that would
// have called for it, in the SAME order, so that every diagnostic, every
// appended scan row, every claim filed against decl and every ordering
// property of the result is produced by unchanged code. Concurrency here buys
// the waiting back and changes nothing else.
type sweepFetch struct {
	// done is closed by the worker once every field below is written. A
	// consumer must not read them before it has received from done; that
	// receive is the happens-before edge the whole design rests on.
	done chan struct{}

	kind sweepFetchKind

	// config is the list configuration [planSweepFetch] built for a
	// fetchNative call. The consumer compares it against the configuration
	// [scanType] builds for itself and declines the prefetched answer if the
	// two ever disagree - see [sweepPrefetch.takeNative].
	config cty.Value

	// results and diags are a fetchNative answer, exactly as
	// [listclient.List] returned them.
	results []listclient.Result
	diags   tfdiags.Diagnostics

	// cfnType, descs and err are a fetchCloudControl answer, exactly as
	// [cloudcontrol.Client.ListResources] returned them.
	cfnType string
	descs   []cloudcontrol.ResourceDescription
	err     error
}

// sweepPrefetch is one sweep loop's worth of in-flight list calls.
//
// The universe is planned synchronously, on the caller's goroutine, before a
// single worker starts: a plan reads decl, and the consuming loop writes to
// decl (claimants, count-block extras), so planning on the workers would race
// the very loop they feed. What the workers do is the transport call and
// nothing else.
type sweepPrefetch struct {
	entries map[string]*sweepFetch
	order   []string

	// inflight is the concurrency bound, and only that. A worker holds one
	// from before its list call until the moment that call returns - see
	// [sweepPrefetch.publish], which releases it - so what it bounds is
	// listings the cloud is producing right now, which is what
	// [Request.SweepParallelism] has always meant.
	//
	// Until issue #839 this was one channel doing this job and the buffer's
	// below, and a list call that was slow therefore held the width down
	// while every listing behind it sat waiting to be consumed.
	inflight chan struct{}

	wg sync.WaitGroup

	// mu guards buffered, taken and mismatched, and is bufferFree's lock.
	// Every other field is written before any worker starts (entries, order,
	// inflight, buffer) or is per-entry and published through sweepFetch.done.
	mu sync.Mutex

	// buffer is the memory bound: how many listings may be fetched and not
	// yet consumed before the launcher stops starting calls. buffered is how
	// many there are, incremented by [sweepPrefetch.publish] when a listing
	// lands and decremented by [sweepPrefetch.consumed] when the scan loop
	// takes it.
	//
	// The launcher waits on bufferFree rather than on a channel receive
	// because the token is produced by the CONSUMER and spent by the
	// LAUNCHER, with the worker in between doing neither: a worker that
	// blocked on a full buffer would be holding a listing the scan loop might
	// be waiting for, which is the head-of-line stall again one goroutine
	// over.
	//
	// The peak this bounds is buffer + SweepParallelism - 1 listings. The
	// launcher passes this gate and then waits for an in-flight slot, so it
	// is admitted while buffered is at most buffer-1 and the calls already in
	// flight may all land behind it. That is a multiple of the two bounds and
	// never of the admission table, which is the property the single slot
	// channel used to carry.
	buffer     int
	buffered   int
	bufferFree *sync.Cond

	taken map[string]bool

	// mismatched counts the answers a consumer declined because the
	// configuration it built for itself was not the one the plan fetched
	// with. It is always zero; it is a field rather than a panic so a test
	// can assert that, and so a real run degrades into an extra list call
	// rather than into a wrong one.
	mismatched int
}

// sweepParallelism is how many list calls this request wants in flight.
func sweepParallelism(req Request) int {
	if req.SweepParallelism > 0 {
		return req.SweepParallelism
	}
	return DefaultSweepParallelism
}

// sweepBuffer is how many fetched listings this sweep will hold ahead of the
// consuming loop - [DefaultSweepBufferFactor] per in-flight slot when
// [Request.SweepBuffer] does not settle it.
//
// It is never below one: a zero buffer would be a launcher that may not start
// a list call until the previous listing has been consumed, which is the
// sequential sweep with extra goroutines.
func sweepBuffer(req Request) int {
	if req.SweepBuffer > 0 {
		return req.SweepBuffer
	}
	return sweepParallelism(req) * DefaultSweepBufferFactor
}

// startSweepPrefetch plans every type in universe and starts issuing their
// list calls, up to [sweepParallelism] at a time, in universe order.
//
// collectUnclaimed answers the same per-type question the sweep loops ask
// before calling [scanTypeReporting], and for the same reason: it is what
// decides whether a type is listed with a server-side estate filter or
// unfiltered, and so it is part of the configuration the call is made with.
//
// The returned prefetch is inert if universe is empty, and every method on it
// tolerates a nil receiver, so a caller that never starts one still compiles
// down to today's behaviour.
func startSweepPrefetch(ctx context.Context, req Request, schemas listclient.Schemas, decl *declared, universe []string, collectUnclaimed func(string) bool) *sweepPrefetch {
	if len(universe) == 0 {
		return nil
	}

	par := sweepParallelism(req)
	pf := &sweepPrefetch{
		entries:  make(map[string]*sweepFetch, len(universe)),
		order:    make([]string, 0, len(universe)),
		inflight: make(chan struct{}, par),
		buffer:   sweepBuffer(req),
		taken:    make(map[string]bool, len(universe)),
	}
	pf.bufferFree = sync.NewCond(&pf.mu)

	// Plan first, synchronously, in universe order. Every read of decl,
	// req and schemas that decides a call happens here, on this goroutine,
	// before the consuming loop has mutated anything.
	planned := make([]*sweepFetch, 0, len(universe))
	for _, typeName := range universe {
		if pf.entries[typeName] != nil {
			// A universe with a repeated type would otherwise fetch twice and
			// hand the second answer to nobody. sweepTypes sorts and does not
			// repeat, but Request.SweepTypes is a caller's own list.
			continue
		}
		e := planSweepFetch(req, schemas, decl, typeName, collectUnclaimed(typeName))
		pf.entries[typeName] = e
		pf.order = append(pf.order, typeName)
		planned = append(planned, e)
	}

	// One launcher goroutine, so that startSweepPrefetch returns immediately
	// and the consuming loop can begin on the first type as soon as its call
	// lands rather than after the whole universe has.
	pf.wg.Add(1)
	go func() {
		defer pf.wg.Done()
		for i, typeName := range pf.order {
			e := planned[i]
			if e.kind == fetchNone {
				// No call to make: publish immediately and take no slot, so a
				// universe that is mostly unlistable does not spend its
				// parallelism on types that never touch the network.
				close(e.done)
				continue
			}
			// Both bounds, in this order: the buffer says the scan loop is
			// not already holding all the listings it may hold, and the
			// in-flight channel says the cloud is not already producing all
			// the listings it may produce. A call that is slow keeps the
			// second and never reaches the first, so the calls behind it
			// launch.
			pf.reserveBuffer()
			pf.inflight <- struct{}{}
			pf.wg.Add(1)
			go func(typeName string, e *sweepFetch) {
				defer pf.wg.Done()
				defer pf.publish(e)
				runSweepFetch(ctx, req, typeName, e)
			}(typeName, e)
		}
	}()

	return pf
}

// planSweepFetch decides, without making any call, how the sweep would list
// typeName - and builds the configuration it would list it with.
//
// It mirrors the sweep=true path through [scanType]'s head and
// [scanTypeCloudControl]'s, and it is deliberately a mirror rather than a
// shared helper: the sequential body stays untouched, which is what makes the
// diagnostics, the ordering and the failure semantics provably unchanged. The
// mirror is held to its original by two runtime checks rather than by
// inspection - [sweepPrefetch.takeNative] refuses an answer fetched with a
// different configuration than the body builds, and [sweepPrefetch.finish]
// reports any planned call the body never asked for. Both are asserted zero
// by TestSweepPrefetchPlansExactlyTheCallsTheScanMakes.
//
// Only sweep=true is modelled. The config-driven scan is a different set of
// branches (the marker and located fallbacks, which are gated on !sweep) and
// is not prefetched.
func planSweepFetch(req Request, schemas listclient.Schemas, decl *declared, typeName string, collectUnclaimed bool) *sweepFetch {
	e := &sweepFetch{done: make(chan struct{})}

	ts, ok := schemas.Get(typeName)
	if !ok {
		// scanType's no-native-list head, sweep leg. Content match comes
		// first there and refuses a sweep outright without listing anything
		// (contentmatch.go's `if sweep` branch), so a type it claims makes no
		// call at all - the same answer, reached the same way.
		if req.CloudControl != nil {
			if _, isContentMatch := identity.ContentMatchTypes[typeName]; isContentMatch {
				if _, byName := uniqueNameIndexFor(decl, typeName); !byName {
					return e
				}
			}
		}
		cfnType, ccOK := cloudControlSource(req, typeName)
		if !ccOK {
			return e
		}
		// scanTypeCloudControl's own first gate: an untaggable CFN type is a
		// SweepGap with no call behind it.
		if taggable, _ := req.Roster.TaggableKnown(cfnType); !taggable {
			return e
		}
		e.kind = fetchCloudControl
		e.cfnType = cfnType
		return e
	}

	// A type whose schema carries no tags can hold no marker, so the sweep
	// files a gap and lists nothing.
	if !markerCapable(ts) {
		return e
	}

	config, cfgDiags := buildSweepListConfig(req, ts, collectUnclaimed)
	if cfgDiags.HasErrors() {
		// scanType reports the failure itself, from its own second call to
		// BuildConfig. Nothing is fetched.
		return e
	}
	e.kind = fetchNative
	e.config = config
	return e
}

// buildSweepListConfig is the list configuration a sweep of typeName is made
// with. It is the one piece of [scanType]'s head this file does share with
// it, because it is the argument to the call rather than a decision about it:
// a mirror that drifted here would fetch a DIFFERENT listing than the body
// asks for, which is the one divergence a call-count check cannot see.
func buildSweepListConfig(req Request, ts listclient.TypeSchema, collectUnclaimed bool) (cty.Value, tfdiags.Diagnostics) {
	vals := make(map[string]cty.Value)
	if hasAttr(ts.Config, "region") && req.Region != "" {
		vals["region"] = cty.StringVal(req.Region)
	}
	filterOK, _ := supportsTagFilter(ts)
	if !collectUnclaimed && filterOK {
		vals["filter"] = tagFilter(TagEstate, req.Estate)
	}
	return ts.BuildConfig(vals)
}

// runSweepFetch is the whole of what a prefetch worker does: one transport
// call, its answer stored, nothing else. It touches no shared state but the
// entry it was handed, and reads nothing from decl or res.
func runSweepFetch(ctx context.Context, req Request, typeName string, e *sweepFetch) {
	switch e.kind {
	case fetchNative:
		e.results, e.diags = listclient.List(ctx, req.Provider, typeName, e.config, true)
	case fetchCloudControl:
		e.descs, e.err = req.CloudControl.ListResources(ctx, e.cfnType)
	}
}

// reserveBuffer blocks the launcher until there is room for one more
// unconsumed listing. It is the memory half of issue #839's split.
//
// It cannot deadlock a scan loop waiting on a type the launcher has not
// started yet. Calls are launched in universe order and listings are consumed
// in that same order, so if the launcher is waiting here before type i, every
// listing that could be counted in buffered belongs to a type before i - and
// the scan loop, to be waiting on i, has taken all of those. buffered is
// therefore zero and the wait is over. The same argument covers
// [sweepPrefetch.finish]'s drain, which consumes in that same order.
func (pf *sweepPrefetch) reserveBuffer() {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	for pf.buffered >= pf.buffer {
		pf.bufferFree.Wait()
	}
}

// publish is what a worker does with a listing: count it against the buffer,
// hand it to the scan loop, and give the call's slot back.
//
// The order of the three is the whole of the accounting. Counting before the
// close is what makes the count safe against a consumer that takes the
// listing the instant it lands - [sweepPrefetch.consumed]'s decrement happens
// after a receive from done, so it can never run before the increment it
// undoes. Releasing the in-flight slot last is what makes the bound honest:
// the slot is held for exactly as long as the provider was listing.
func (pf *sweepPrefetch) publish(e *sweepFetch) {
	pf.mu.Lock()
	pf.buffered++
	pf.mu.Unlock()
	close(e.done)
	<-pf.inflight
}

// consumed gives back the buffer slot one type's listing held.
func (pf *sweepPrefetch) consumed() {
	pf.mu.Lock()
	pf.buffered--
	pf.mu.Unlock()
	pf.bufferFree.Signal()
}

// wait blocks until typeName's call has landed and marks it taken, releasing
// its buffer slot so the launcher can start another call. It returns nil for
// a type this prefetch never planned, which is every type when the receiver
// is nil.
//
// The call's own slot - [sweepPrefetch.inflight] - was released when the call
// returned, which is issue #839: a scan loop still working through the types
// ahead of this one is no longer what decides whether the cloud is being
// asked for anything.
func (pf *sweepPrefetch) wait(typeName string) *sweepFetch {
	if pf == nil {
		return nil
	}
	e := pf.entries[typeName]
	if e == nil {
		return nil
	}
	pf.mu.Lock()
	already := pf.taken[typeName]
	pf.taken[typeName] = true
	pf.mu.Unlock()
	<-e.done
	if !already && e.kind != fetchNone {
		pf.consumed()
	}
	return e
}

// takeNative is the answer [scanType] should use for typeName's own list
// call, or ok false to make the call itself.
//
// config is the configuration scanType built for itself. An answer fetched
// with a different one is refused rather than used: a mirror that drifted
// would otherwise substitute a listing of the wrong scope - the estate's own
// resources for the account's, say - which no call count and no diagnostic
// would show.
func (pf *sweepPrefetch) takeNative(typeName string, config cty.Value) ([]listclient.Result, tfdiags.Diagnostics, bool) {
	e := pf.wait(typeName)
	if e == nil || e.kind != fetchNative {
		return nil, nil, false
	}
	if !e.config.RawEquals(config) {
		pf.mu.Lock()
		pf.mismatched++
		pf.mu.Unlock()
		log.Printf("[WARN] stateless/discovery: the sweep prefetched %s with a list configuration the scan then disagreed with; listing it again", typeName)
		return nil, nil, false
	}
	return e.results, e.diags, true
}

// takeCloudControl is takeNative's Cloud Control counterpart.
func (pf *sweepPrefetch) takeCloudControl(typeName, cfnType string) ([]cloudcontrol.ResourceDescription, error, bool) {
	e := pf.wait(typeName)
	if e == nil || e.kind != fetchCloudControl {
		return nil, nil, false
	}
	if e.cfnType != cfnType {
		pf.mu.Lock()
		pf.mismatched++
		pf.mu.Unlock()
		log.Printf("[WARN] stateless/discovery: the sweep prefetched %s as CFN type %s and the scan then asked for %s; listing it again", typeName, e.cfnType, cfnType)
		return nil, nil, false
	}
	return e.descs, e.err, true
}

// mismatches is how many prefetched answers a consumer refused because the
// configuration it built for itself was not the one the plan fetched with.
// Always zero; see [sweepPrefetch.mismatched].
func (pf *sweepPrefetch) mismatches() int {
	if pf == nil {
		return 0
	}
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return pf.mismatched
}

// finish drains anything the consuming loop did not take, waits for every
// worker, and returns the types whose call was made and never used.
//
// A non-empty return is a bug in [planSweepFetch]: it means the sweep spent a
// list call the sequential body would not have spent, which is exactly the
// "call counts must be identical" property issue #605 accepts on. It is
// reported rather than asserted so that a real run degrades into one wasted
// read rather than a panic, and TestSweepPrefetchPlansExactlyTheCallsTheScanMakes
// holds it to empty across every fixture in the package.
func (pf *sweepPrefetch) finish() []string {
	if pf == nil {
		return nil
	}
	var wasted []string
	for _, typeName := range pf.order {
		pf.mu.Lock()
		taken := pf.taken[typeName]
		pf.mu.Unlock()
		if taken {
			continue
		}
		// Drain in universe order. Issue #605 needed that order because the
		// launcher's awaited slot was released by the consumer; issue #839
		// splits that slot in two, so the argument is made again here rather
		// than inherited from a design that no longer exists.
		//
		// What the launcher can still be blocked on is buffer room, in
		// [sweepPrefetch.reserveBuffer], and the argument is that function's
		// own: it is blocked before some type i, everything counted in
		// buffered is a type before i, and this loop takes types in the order
		// the launcher launches them - so by the time it reaches a type whose
		// worker has not started, it has already released every listing the
		// launcher is waiting for. The in-flight half needs no ordering
		// argument at all: a worker gives that slot back when its own list
		// call returns, with no consumer involved.
		e := pf.wait(typeName)
		if e != nil && e.kind != fetchNone {
			wasted = append(wasted, typeName)
		}
	}
	pf.wg.Wait()
	for _, typeName := range wasted {
		log.Printf("[WARN] stateless/discovery: the sweep prefetched a list of %s that the scan never asked for", typeName)
	}
	return wasted
}
