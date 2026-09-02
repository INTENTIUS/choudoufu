// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #605: the estate-wide sweep's list calls go out concurrently.
// These tests are about the three properties that outrank the speedup, in the
// order the issue puts them.
//
//  1. Determinism. The answers are consumed in universe order, never in
//     completion order, so every scan row, every problem and every diagnostic
//     reads the same as it did sequentially. [sweepGate] makes completion
//     order the exact REVERSE of universe order by construction - not by
//     timing, which is what issue #597 cost this repository on the write path
//     - so an "append as they arrive" collector produces a visibly backwards
//     result and cannot pass.
//  2. Call counts. Concurrency must overlap the waiting and change nothing
//     else. The prefetch decides which call to make from a mirror of
//     [scanType]'s own head, and a mirror can drift, so the run reports both
//     ways it can: a call planned and never consumed, and an answer fetched
//     with a configuration the scan then disagreed with.
//  3. Failure semantics. The sequential sweep continued past a failed listing
//     with a [SweepGap] per failing type and never aborted; so does this one.
//
// Every test that asserts concurrency also proves the concurrency happened -
// through the gate's arrival barrier, which only opens when every call is in
// flight at once. Without that, they would pass just as happily against the
// sequential loop they exist to protect.

// ---------------------------------------------------------------------------
// The reversal, by construction
// ---------------------------------------------------------------------------

// sweepGate forces the sweep's list calls to COMPLETE in the exact reverse of
// the order they were asked for, on every run, at every load and at every
// GOMAXPROCS, with no clock in the success path. It is the read path's
// [reverseGate] (internal/live/liveimport/stamp_concurrency_test.go, GitHub
// issue #597), and it is built the same way, from two happens-before edges
// per gated type:
//
//  1. No call returns until every gated call is inside the provider at once -
//     the arrival barrier. That is also the overlap evidence, and it is exact
//     rather than probabilistic: peak concurrency is n, not "at least 2 if
//     the scheduler was kind".
//  2. Type i's call then does not return until type i+1's call has already
//     returned. The last type waits for nobody, so it is the only one that
//     can move first; it releases the one before it, and so on down to the
//     first.
//
// stuckAfter is a hang-breaker, never a timing device. Nothing here SUCCEEDS
// because of it: every wait in the success path is satisfied by a channel
// close from a goroutine that is already running and already past the
// barrier. It exists so that a sweep which does NOT run its calls
// concurrently fails with a diagnosis instead of hanging until the package
// timeout - and [TestSweepParallelismOneReproducesTheSequentialLoop] asserts
// that it is reached at parallelism 1, which is what proves the barrier is a
// real constraint rather than decoration.
type sweepGate struct {
	// order is the gated type names, in the order the sweep asks for them.
	order []string
	index map[string]int

	stuckAfter time.Duration

	arrived chan struct{}
	done    []chan struct{}

	mu       sync.Mutex
	count    int
	inFlight int
	peak     int
	returned []string
	abandon  chan struct{}
	stuck    string
	closed   []bool
}

func newSweepGate(order []string, stuckAfter time.Duration) *sweepGate {
	g := &sweepGate{
		order:      order,
		index:      make(map[string]int, len(order)),
		stuckAfter: stuckAfter,
		arrived:    make(chan struct{}),
		done:       make([]chan struct{}, len(order)),
		closed:     make([]bool, len(order)),
		abandon:    make(chan struct{}),
	}
	for i, t := range order {
		g.index[t] = i
		g.done[i] = make(chan struct{})
	}
	return g
}

// enter is called at the top of a gated list call and returns only when it is
// this type's turn to be the next one to finish.
func (g *sweepGate) enter(typeName string) {
	i, ok := g.index[typeName]
	if !ok {
		return
	}

	g.mu.Lock()
	g.count++
	g.inFlight++
	if g.inFlight > g.peak {
		g.peak = g.inFlight
	}
	if g.count == len(g.order) {
		close(g.arrived)
	}
	g.mu.Unlock()

	g.wait(g.arrived, fmt.Sprintf("%s waited for all %d sweep calls to be in flight at once and only %%d ever were", typeName, len(g.order)))
	if i+1 < len(g.order) {
		g.wait(g.done[i+1], fmt.Sprintf("%s waited for %s to finish first", typeName, g.order[i+1]))
	}
}

// leave is called as the gated list call returns, and releasing this type is
// what lets the one before it return.
func (g *sweepGate) leave(typeName string) {
	i, ok := g.index[typeName]
	if !ok {
		return
	}
	g.mu.Lock()
	g.inFlight--
	g.returned = append(g.returned, typeName)
	if !g.closed[i] {
		g.closed[i] = true
		close(g.done[i])
	}
	g.mu.Unlock()
}

// wait blocks on ch, recording a diagnosis and abandoning the whole gate if
// the wait cannot be satisfied. Reaching stuckAfter always FAILS a test that
// expected concurrency; it can never become a pass.
func (g *sweepGate) wait(ch <-chan struct{}, why string) {
	select {
	case <-ch:
		return
	case <-g.abandon:
		return
	default:
	}
	timer := time.NewTimer(g.stuckAfter)
	defer timer.Stop()
	select {
	case <-ch:
	case <-g.abandon:
	case <-timer.C:
		g.mu.Lock()
		if g.stuck == "" {
			g.stuck = fmt.Sprintf(why, g.count)
			close(g.abandon)
		}
		g.mu.Unlock()
	}
}

func (g *sweepGate) stuckAt() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stuck
}

func (g *sweepGate) peakInFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

// completionOrder is the order the gated calls actually returned in.
func (g *sweepGate) completionOrder() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.returned...)
}

// gatedCloud is [fakeCloud] with a gate in front of its list calls. It embeds
// the fake rather than reimplementing it, so the schema, the objects, the
// filtering and the request log are all exactly the ones every other test in
// this package uses.
type gatedCloud struct {
	*fakeCloud
	gate *sweepGate
}

func (g *gatedCloud) ListResourceStream(ctx context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	g.gate.enter(req.TypeName)
	defer g.gate.leave(req.TypeName)
	return g.fakeCloud.ListResourceStream(ctx, req, emit)
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// gatedSweepTypes is the sweep universe these tests use: six admitted types
// the P0.1 fixture does not declare at all, so every one of them is a sweep
// call and nothing else. Already in the order [sweepTypes] would sort them
// into, which is the order every assertion below is about.
var gatedSweepTypes = []string{
	"aws_amplify_app",
	"aws_api_gateway_api_key",
	"aws_apigatewayv2_api",
	"aws_backup_plan",
	"aws_cloudfront_distribution",
	"aws_cognito_user_pool",
}

// failingCloud makes one type's native listing fail, which [fakeCloud] has no
// fixture for. It wraps rather than extends so that discovery_test.go's own
// fake is untouched, and it still records the request: a failed list call is
// a call, and issue #605's call-count parity is about calls made, not calls
// that succeeded.
type failingCloud struct {
	*fakeCloud
	fails map[string]bool
}

func (f *failingCloud) ListResourceStream(ctx context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	if !f.fails[req.TypeName] {
		return f.fakeCloud.ListResourceStream(ctx, req, emit)
	}
	f.fakeCloud.mu.Lock()
	f.fakeCloud.requests = append(f.fakeCloud.requests, req)
	f.fakeCloud.mu.Unlock()
	var diags tfdiags.Diagnostics
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Simulated list failure",
		fmt.Sprintf("failingCloud: listing %s failed (issue #605 fixture).", req.TypeName),
	))
}

// newGatedFixture builds a fake cloud that serves the whole estate plus one
// owned, undeclared resource of every gated type - so each sweep call has
// something to find and a scan row to file.
func newGatedFixture(t *testing.T, stuckAfter time.Duration) *gatedCloud {
	t.Helper()

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	for i, typeName := range gatedSweepTypes {
		cloud.listable(typeName)
		cloud.own(typeName, fmt.Sprintf("id-%d", i), typeName+".deleted")
	}
	return &gatedCloud{fakeCloud: cloud, gate: newSweepGate(gatedSweepTypes, stuckAfter)}
}

// discoverGated runs Discover against a gated fixture with the gated universe
// as the sweep's whole universe.
func discoverGated(t *testing.T, cloud *gatedCloud, parallelism int) (*Result, tfdiags.Diagnostics) {
	t.Helper()

	cfg := loadConfig(t, estateDir(t))
	req := Request{
		Estate:           estateName,
		Config:           cfg,
		Resolutions:      resolveOrFail(t, cfg).All(),
		Provider:         cloud,
		Sweep:            true,
		SweepTypes:       gatedSweepTypes,
		SweepParallelism: parallelism,
	}
	return Discover(context.Background(), req)
}

// sweptScanOrder is the type names of the sweep's scan rows, in the order the
// run filed them. This is the ordering evidence: [Result.Scans] is one of the
// two slices [Result.sortEverything] deliberately leaves alone, so it still
// carries the order the loop produced.
func sweptScanOrder(res *Result) []string {
	var out []string
	for _, s := range res.Scans {
		if s.Sweep {
			out = append(out, s.TypeName)
		}
	}
	return out
}

func reversed(in []string) []string {
	out := make([]string, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, in[i])
	}
	return out
}

// ---------------------------------------------------------------------------
// 1. Determinism
// ---------------------------------------------------------------------------

// TestSweepConsumesInUniverseOrderNotCompletionOrder is issue #605's first
// constraint, with the fixture arranged so that the two orders are opposites.
//
// The gate holds every call until all six are in flight (the overlap proof),
// then releases them strictly backwards, so the provider answers
// aws_sqs_queue first and aws_cloudwatch_log_group last. The scan rows must
// still read forwards. A collector that appended answers as they arrived
// would produce exactly the reverse of what this asserts.
func TestSweepConsumesInUniverseOrderNotCompletionOrder(t *testing.T) {
	cloud := newGatedFixture(t, 30*time.Second)

	res, diags := discoverGated(t, cloud, len(gatedSweepTypes))
	assertNoErrors(t, diags)

	if why := cloud.gate.stuckAt(); why != "" {
		t.Fatalf("the sweep did not run its list calls concurrently: %s", why)
	}
	if peak := cloud.gate.peakInFlight(); peak != len(gatedSweepTypes) {
		t.Fatalf("peak concurrent list calls = %d, want %d - the arrival barrier is the whole fixture", peak, len(gatedSweepTypes))
	}

	// The fixture actually reversed. Without this the ordering assertion
	// below would hold trivially.
	gotCompletion := cloud.gate.completionOrder()
	wantCompletion := reversed(gatedSweepTypes)
	if strings.Join(gotCompletion, ",") != strings.Join(wantCompletion, ",") {
		t.Fatalf("the gate did not reverse completion order: got %v, want %v", gotCompletion, wantCompletion)
	}

	got := sweptScanOrder(res)
	if strings.Join(got, ",") != strings.Join(gatedSweepTypes, ",") {
		t.Errorf("sweep scan rows read %v\nwant %v (universe order), even though the answers arrived %v", got, gatedSweepTypes, gotCompletion)
	}

	// Every one of them found its orphan, so the ordering above is over rows
	// that carry real content rather than six empty refusals.
	if len(res.Orphans) != len(gatedSweepTypes) {
		t.Errorf("found %d orphans, want %d:\n%s", len(res.Orphans), len(gatedSweepTypes), res)
	}
}

// TestSweepParallelismOneReproducesTheSequentialLoop is the control, and it
// is also what proves the gate in the test above is a real constraint: at
// parallelism 1 there is only ever one call in flight, the arrival barrier
// can never open, and the gate has to abandon. The scan rows still read
// forwards, because one worker started in universe order and a consumer that
// waits on each type in that same order IS the sequential loop.
func TestSweepParallelismOneReproducesTheSequentialLoop(t *testing.T) {
	cloud := newGatedFixture(t, 2*time.Second)

	res, diags := discoverGated(t, cloud, 1)
	assertNoErrors(t, diags)

	if why := cloud.gate.stuckAt(); why == "" {
		t.Fatal("the arrival barrier opened at parallelism 1, so it is not evidence of concurrency in the test above")
	}
	if peak := cloud.gate.peakInFlight(); peak != 1 {
		t.Errorf("peak concurrent list calls at parallelism 1 = %d, want 1", peak)
	}

	got := sweptScanOrder(res)
	if strings.Join(got, ",") != strings.Join(gatedSweepTypes, ",") {
		t.Errorf("sweep scan rows read %v, want %v", got, gatedSweepTypes)
	}
	if len(res.Orphans) != len(gatedSweepTypes) {
		t.Errorf("found %d orphans, want %d:\n%s", len(res.Orphans), len(gatedSweepTypes), res)
	}
}

// TestSweepIsByteIdenticalAtEveryParallelism is the broad determinism
// evidence: the full default sweep universe - the whole admission table, not
// six hand-picked types - rendered and compared across four settings.
//
// It compares the rendered Result, the diagnostic sequence AND the unsorted
// scan-row order, because the first two would survive a reordering that the
// third catches: [Result.sortEverything] sorts almost everything on the way
// out, which is exactly why a test that only reads the sorted fields cannot
// see an ordering defect.
func TestSweepIsByteIdenticalAtEveryParallelism(t *testing.T) {
	render := func(parallelism int) string {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
		cloud.listable("aws_sqs_queue")
		cloud.obj("aws_sqs_queue", "https://sqs/other", map[string]string{TagEstate: "somebody-else"})

		res, diags := discoverFixture(t, cloud, Request{
			Sweep:            true,
			CollectUnclaimed: true,
			SweepParallelism: parallelism,
		})

		var b strings.Builder
		fmt.Fprintf(&b, "%s\n", res)
		b.WriteString("--- scans, in the order the run filed them ---\n")
		for _, s := range res.Scans {
			fmt.Fprintf(&b, "%s sweep=%v listed=%d unclaimed=%d other=%d joined=%d\n",
				s.TypeName, s.Sweep, s.Listed, s.Unclaimed, s.OtherEstate, s.Joined)
		}
		b.WriteString("--- diagnostics, in order ---\n")
		for _, d := range diags {
			desc := d.Description()
			fmt.Fprintf(&b, "%v %s | %s\n", d.Severity(), desc.Summary, desc.Detail)
		}
		b.WriteString("--- problems, in order ---\n")
		for _, p := range res.Problems {
			fmt.Fprintf(&b, "%v %s %v\n", p.Kind, p.TypeName, p.LiveIDs)
		}
		return b.String()
	}

	base := render(1)
	for _, par := range []int{2, 10, 64} {
		if got := render(par); got != base {
			t.Errorf("parallelism %d rendered differently from parallelism 1.\n--- parallelism 1 ---\n%s\n--- parallelism %d ---\n%s", par, base, par, got)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Call counts, and the mirror that decides them
// ---------------------------------------------------------------------------

// TestSweepPrefetchPlansExactlyTheCallsTheScanMakes holds [planSweepFetch] to
// the head of [scanType] it mirrors.
//
// The two ways a mirror can drift are both reported by the run itself:
// sweepPrefetchWasted is a call planned that the scan never asked for - one
// more list call than the sequential loop made, which is exactly the property
// issue #605 accepts on - and sweepPrefetchMismatched is an answer fetched
// with a list configuration the scan then disagreed with, which is the one
// divergence a call count cannot see, because the count is right and the
// LISTING is wrong.
//
// The scenarios cover every branch the mirror models: a plain taggable type,
// a type whose schema carries no tags (no call), a type with no list resource
// at all (no call), an unfiltered type (client-side scope), and
// CollectUnclaimed, which changes the configuration the call is made with.
func TestSweepPrefetchPlansExactlyTheCallsTheScanMakes(t *testing.T) {
	scenarios := []struct {
		name  string
		build func(*fakeCloud)
		req   Request
	}{
		{
			name:  "plain taggable sweep",
			build: func(c *fakeCloud) { c.listable("aws_cloudwatch_log_group") },
			req:   Request{Sweep: true},
		},
		{
			name: "an untaggable type files a gap and lists nothing",
			build: func(c *fakeCloud) {
				c.listableUntagged("aws_cloudwatch_log_group")
			},
			req: Request{Sweep: true},
		},
		{
			name: "a type the provider cannot list at all",
			build: func(c *fakeCloud) {
				c.listable("aws_cloudwatch_log_group")
				c.missing["aws_cloudwatch_log_group"] = true
			},
			req: Request{Sweep: true},
		},
		{
			name: "a type with no server-side tag filter",
			build: func(c *fakeCloud) {
				c.listable("aws_cloudwatch_log_group")
				c.unfilter["aws_cloudwatch_log_group"] = true
			},
			req: Request{Sweep: true},
		},
		{
			name:  "collecting unclaimed widens the configuration",
			build: func(c *fakeCloud) { c.listable("aws_cloudwatch_log_group") },
			req:   Request{Sweep: true, CollectUnclaimed: true},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			for _, par := range []int{1, 4, 32} {
				cloud := newFakeCloud()
				ownWholeEstate(cloud)
				sc.build(cloud)
				req := sc.req
				req.SweepParallelism = par
				res, _ := discoverFixture(t, cloud, req)

				if len(res.sweepPrefetchWasted) != 0 {
					t.Errorf("parallelism %d: the sweep prefetched %v and the scan never asked for them, so the run spent list calls the sequential loop would not have", par, res.sweepPrefetchWasted)
				}
				if res.sweepPrefetchMismatched != 0 {
					t.Errorf("parallelism %d: %d prefetched answers were fetched with a list configuration the scan disagreed with", par, res.sweepPrefetchMismatched)
				}
			}
		})
	}
}

// TestSweepCallCountsAreIdenticalAtEveryParallelism is issue #605's headline
// acceptance in one assertion: concurrency overlaps the waiting and makes not
// one extra call, at any setting.
func TestSweepCallCountsAreIdenticalAtEveryParallelism(t *testing.T) {
	count := func(parallelism int) (int, map[string]int) {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
		discoverFixture(t, cloud, Request{Sweep: true, CollectUnclaimed: true, SweepParallelism: parallelism})

		perType := map[string]int{}
		for _, r := range cloud.requests {
			perType[r.TypeName]++
		}
		return len(cloud.requests), perType
	}

	baseTotal, basePerType := count(1)
	if baseTotal == 0 {
		t.Fatal("the sequential baseline made no list calls at all, so this test proves nothing")
	}
	// An absolute anchor, not only a comparison. A prefetch whose answer the
	// scan never uses lists every swept type twice, and it does that at EVERY
	// setting - so a test that only compares parallelism N against parallelism
	// 1 reads a uniform doubling as agreement. Each type is listed at most
	// once per run: the config-driven scan and the sweep have disjoint
	// universes ([sweepTypes] skips a type decl.types holds), and neither
	// lists a type twice.
	for typeName, n := range basePerType {
		if n != 1 {
			t.Errorf("parallelism 1 listed %s %d times; every type is listed exactly once per run", typeName, n)
		}
	}
	for _, par := range []int{2, 10, 64} {
		total, perType := count(par)
		for typeName, n := range perType {
			if n != 1 {
				t.Errorf("parallelism %d listed %s %d times; every type is listed exactly once per run", par, typeName, n)
			}
		}
		if total != baseTotal {
			t.Errorf("parallelism %d made %d list calls, parallelism 1 made %d", par, total, baseTotal)
		}
		for typeName, n := range perType {
			if basePerType[typeName] != n {
				t.Errorf("parallelism %d listed %s %d times, parallelism 1 listed it %d times", par, typeName, n, basePerType[typeName])
			}
		}
		for typeName, n := range basePerType {
			if perType[typeName] != n {
				t.Errorf("parallelism %d listed %s %d times, parallelism 1 listed it %d times", par, typeName, perType[typeName], n)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 3. Failure semantics
// ---------------------------------------------------------------------------

// TestSweepContinuesPastAFailedListingConcurrently pins what the sequential
// loop did when one type's listing failed: it filed a [SweepGapListFailed]
// for that type, did NOT append the provider's error to the run's own
// diagnostics (a type the configuration never mentions must not fail the
// plan), and carried on listing every other type.
//
// A concurrent version built on an errgroup would have cancelled the rest at
// the first error. This one has no errgroup and no cancellation for exactly
// that reason, and this test is what says so.
func TestSweepContinuesPastAFailedListingConcurrently(t *testing.T) {
	for _, par := range []int{1, 10} {
		base := newFakeCloud()
		ownWholeEstate(base)
		for i, typeName := range gatedSweepTypes {
			base.listable(typeName)
			base.own(typeName, fmt.Sprintf("id-%d", i), typeName+".deleted")
		}
		// The middle of the universe fails, so a run that aborted on the
		// first error would still show the types before it.
		failing := gatedSweepTypes[2]
		cloud := &failingCloud{fakeCloud: base, fails: map[string]bool{failing: true}}

		cfg := loadConfig(t, estateDir(t))
		res, diags := Discover(context.Background(), Request{
			Estate:           estateName,
			Config:           cfg,
			Resolutions:      resolveOrFail(t, cfg).All(),
			Provider:         cloud,
			Sweep:            true,
			SweepTypes:       gatedSweepTypes,
			SweepParallelism: par,
		})
		assertNoErrors(t, diags)

		if got := sweptScanOrder(res); strings.Join(got, ",") != strings.Join(gatedSweepTypes, ",") {
			t.Errorf("parallelism %d: scan rows %v, want every type in universe order %v", par, got, gatedSweepTypes)
		}
		var gaps []string
		for _, g := range res.SweepGaps {
			if g.Reason == SweepGapListFailed {
				gaps = append(gaps, g.TypeName)
			}
		}
		if len(gaps) != 1 || gaps[0] != failing {
			t.Errorf("parallelism %d: list-failed gaps = %v, want exactly [%s]", par, gaps, failing)
		}
		// Every other type still found its orphan: the failure cost coverage
		// of one type and nothing else.
		if len(res.Orphans) != len(gatedSweepTypes)-1 {
			t.Errorf("parallelism %d: found %d orphans, want %d - a failure must not abandon the rest of the sweep:\n%s", par, len(res.Orphans), len(gatedSweepTypes)-1, res)
		}
	}
}
