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
// type, and against a real account that is the whole of a stateless plan's
// wall clock: 521 calls at 0.367s of network latency each, measured at scale
// 1 over 79 resources, is 203 of the plan's 205 seconds. The read pass around
// them is already at exact call parity with stock, so there is nothing else
// in it to remove - only the waiting to overlap.
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

	// slots is the concurrency bound AND the backpressure. A worker acquires
	// a slot before its call and never releases it; the CONSUMER releases one
	// when it takes that type's answer. So at most SweepParallelism types are
	// ever fetched-but-unconsumed, and the sweep's peak memory stays a
	// multiple of the parallelism rather than of the admission table - which
	// matters, because a sweep over a thousand types buffering every type's
	// listed objects at once is a far worse regression than the slowness this
	// fixes.
	slots chan struct{}

	wg sync.WaitGroup

	// mu guards taken. Every other field is written before any worker starts
	// (entries, order, slots) or is per-entry and published through
	// sweepFetch.done.
	mu    sync.Mutex
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
		entries: make(map[string]*sweepFetch, len(universe)),
		order:   make([]string, 0, len(universe)),
		slots:   make(chan struct{}, par),
		taken:   make(map[string]bool, len(universe)),
	}

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
			pf.slots <- struct{}{}
			pf.wg.Add(1)
			go func(typeName string, e *sweepFetch) {
				defer pf.wg.Done()
				defer close(e.done)
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

// wait blocks until typeName's call has landed and marks it taken, releasing
// its slot so the launcher can start another. It returns nil for a type this
// prefetch never planned, which is every type when the receiver is nil.
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
		<-pf.slots
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
		// Drain in universe order. The launcher may be blocked acquiring a
		// slot for a later type, and the slot it needs is one of these; taking
		// them in order is what guarantees the one it is waiting on is
		// released before this loop reaches a type whose worker has not
		// started.
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
