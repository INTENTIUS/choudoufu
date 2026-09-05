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

// ---------------------------------------------------------------------------
// 4. One slow list call (GitHub issue #839)
// ---------------------------------------------------------------------------

// sweepStallStuckAfter is the hang-breaker for the two tests below, never a
// timing device. Every wait in their success path is satisfied by a channel a
// list call closes; this exists so a sweep that cannot make those calls fails
// naming the count it reached instead of hanging until the package timeout.
const sweepStallStuckAfter = 30 * time.Second

// sweepStartsBuffered is [stallingCloud.starts]'s depth. Comfortably more
// than any fixture here sweeps, so a test that reads the channel late still
// sees every start rather than a truncated count that would read as the
// defect.
const sweepStartsBuffered = 64

// stallingCloud holds ONE type's list call inside the provider until the test
// releases it - the straggler issue #839 is about, with the SDK's twenty-six
// seconds of backoff replaced by a channel a test controls.
//
// It is [sweepGate]'s sibling and deliberately not the same fixture. The gate
// reverses completion order and needs every gated call in flight at once,
// which is the wrong shape here: these tests run at a parallelism the
// universe is bigger than, so an arrival barrier over the whole universe
// could never open. What this one guarantees instead is the single edge these
// two tests need - while any other swept list call is inside the provider,
// the straggler is inside it too, still holding its in-flight slot.
//
// That edge is not decoration. Asserting on peak concurrency alone passes on
// eight processors and fails on CI's one: at GOMAXPROCS=1 the runtime runs
// the most recently spawned goroutine first, so the launcher's FIRST worker -
// the straggler - is scheduled LAST, and a fixture that merely hoped for
// overlap would have its straggler make its call after every call it was
// supposed to be overlapping. Same bet, same loss, as issue #597 on the
// stamping path and [sweepGate]'s own opening comment.
type stallingCloud struct {
	*fakeCloud

	// watch is the set of types these tests count and gate: the swept
	// universe alone, never the config-driven scan's own declared types,
	// which list before the sweep starts and are no part of this.
	watch map[string]bool

	// stalled is the type whose listing is held inside the provider, and
	// release is what lets it out.
	stalled string
	release chan struct{}

	// entered is closed by the stalled call once it is inside the provider,
	// and every other watched call waits for it before listing anything.
	entered chan struct{}

	// abandon frees the rest once one call has given up waiting for entered,
	// so a broken fixture costs one [sweepStallStuckAfter] for the whole run
	// rather than one per type. Every test here asserts stuckOnEntry is
	// empty; reaching the hang-breaker is never a pass.
	abandon chan struct{}

	// starts carries one type name per list call STARTED, which is the
	// quantity issue #839 is about and the one peak concurrency cannot show:
	// two calls outstanding reads two either way, and what the defect changes
	// is whether a third is ever begun. Sends are non-blocking.
	starts chan string

	mu         sync.Mutex
	started    []string
	entryStuck string
	inFlight   int
	peak       int
}

// newStallingFixture builds a fake cloud that serves the whole estate plus
// one owned, undeclared resource of every swept type - so each list call has
// something to find and a scan row to file - with stalled's own call held
// inside the provider until the returned cloud's release channel is closed.
func newStallingFixture(t *testing.T, stalled string) *stallingCloud {
	t.Helper()

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	watch := make(map[string]bool, len(gatedSweepTypes))
	for i, typeName := range gatedSweepTypes {
		cloud.listable(typeName)
		cloud.own(typeName, fmt.Sprintf("id-%d", i), typeName+".deleted")
		watch[typeName] = true
	}
	if !watch[stalled] {
		t.Fatalf("newStallingFixture asked to stall %s, which is not one of the swept types %v", stalled, gatedSweepTypes)
	}
	return &stallingCloud{
		fakeCloud: cloud,
		watch:     watch,
		stalled:   stalled,
		release:   make(chan struct{}),
		entered:   make(chan struct{}),
		abandon:   make(chan struct{}),
		starts:    make(chan string, sweepStartsBuffered),
	}
}

func (c *stallingCloud) ListResourceStream(ctx context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	if !c.watch[req.TypeName] {
		return c.fakeCloud.ListResourceStream(ctx, req, emit)
	}

	c.enter(req.TypeName)
	defer c.leave()

	if req.TypeName == c.stalled {
		close(c.entered)
		<-c.release
	} else {
		c.waitForStallEntry(req.TypeName)
	}
	return c.fakeCloud.ListResourceStream(ctx, req, emit)
}

// enter records a list call as started and in flight. A watched call is in
// flight for the whole time it is gated, which is what makes the peak below
// the in-flight bound reading its own setting.
func (c *stallingCloud) enter(typeName string) {
	c.mu.Lock()
	c.started = append(c.started, typeName)
	c.inFlight++
	if c.inFlight > c.peak {
		c.peak = c.inFlight
	}
	c.mu.Unlock()

	select {
	case c.starts <- typeName:
	default:
	}
}

func (c *stallingCloud) leave() {
	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
}

// waitForStallEntry parks a list call until the straggler is inside the
// provider. Reaching [sweepStallStuckAfter] records the type that gave up
// rather than letting the run hang - every caller asserts
// [stallingCloud.stuckOnEntry] is empty, so it can never become a pass.
func (c *stallingCloud) waitForStallEntry(typeName string) {
	select {
	case <-c.entered:
		return
	case <-c.abandon:
		return
	default:
	}
	timer := time.NewTimer(sweepStallStuckAfter)
	defer timer.Stop()
	select {
	case <-c.entered:
	case <-c.abandon:
	case <-timer.C:
		c.mu.Lock()
		if c.entryStuck == "" {
			c.entryStuck = typeName
			close(c.abandon)
		}
		c.mu.Unlock()
	}
}

// stuckOnEntry is the first type that gave up waiting for the straggler to
// begin, and empty when the fixture did what it exists to do.
func (c *stallingCloud) stuckOnEntry() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entryStuck
}

// startedCount is how many watched list calls have been STARTED, whether or
// not they have returned.
func (c *stallingCloud) startedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.started)
}

func (c *stallingCloud) peakInFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

// waitForStarts returns once n watched list calls have been started, or
// everything it saw if they never are. The caller decides a short answer is a
// failure, and every caller does.
func (c *stallingCloud) waitForStarts(n int) []string {
	return c.waitForStartsWithin(n, sweepStallStuckAfter)
}

// waitForStartsWithin is [stallingCloud.waitForStarts] with a budget of the
// caller's own, for the one caller that is waiting for calls it hopes will
// NOT be started.
func (c *stallingCloud) waitForStartsWithin(n int, budget time.Duration) []string {
	if n <= 0 {
		return nil
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	seen := make([]string, 0, n)
	for len(seen) < n {
		select {
		case typeName := <-c.starts:
			seen = append(seen, typeName)
		case <-timer.C:
			return seen
		}
	}
	return seen
}

// sweepRun is one Discover call's answer, written by the goroutine below and
// read only after its finished channel is closed - the happens-before edge
// that makes reading it safe.
type sweepRun struct {
	res   *Result
	diags tfdiags.Diagnostics
}

// discoverStalledAsync runs Discover against a stalling fixture on its own
// goroutine, because the two tests below have to observe the sweep while it
// is still running: a stalled list call is not visible from the far side of
// it.
func discoverStalledAsync(t *testing.T, cloud *stallingCloud, parallelism, buffer int) (*sweepRun, <-chan struct{}) {
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
		SweepBuffer:      buffer,
	}

	run := &sweepRun{}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		run.res, run.diags = Discover(context.Background(), req)
	}()
	return run, finished
}

// TestOneStalledListDoesNotStopTheSweepLaunchingTheRest is issue #839 itself.
//
// The sweep makes one list call per admitted type and consumes the answers in
// universe order. Until this fix a worker took a slot before its call and the
// CONSUMER gave it back, so a call that landed in SDK backoff held the width
// down for every type behind it: the calls launched beside it had already
// returned and their listings went on holding slots, and the launcher could
// not start another. That is the read pass's #683 one phase over, and the
// sweep's own comment said so in the same words.
//
// The fixture is that in miniature. The first swept type is held inside the
// provider until this test releases it, and every other swept call waits
// until that one has begun - see [stallingCloud], which is what makes the
// overlap a property of the fixture rather than a bet on the scheduler. The
// buffer is set to room for every listing but the straggler's own, so that
// backpressure is not what stops the launcher: what is under test is whether
// the stalled CALL stops it.
//
// The assertion is on calls STARTED, not on peak concurrency: peak reads 2
// here whether or not the defect is present, which is exactly why a
// concurrency statistic never showed this.
func TestOneStalledListDoesNotStopTheSweepLaunchingTheRest(t *testing.T) {
	const parallelism = 2

	cloud := newStallingFixture(t, gatedSweepTypes[0])
	run, finished := discoverStalledAsync(t, cloud, parallelism, len(gatedSweepTypes)-1)

	started := cloud.waitForStarts(len(gatedSweepTypes))

	// Release before any assertion, so that a failure reports rather than
	// leaving the sweep wedged behind a call nobody ever answers.
	close(cloud.release)
	<-finished

	if stuck := cloud.stuckOnEntry(); stuck != "" {
		t.Fatalf("the listing of %s gave up waiting for the stalled call to begin, so this run never had a straggler to overlap and asserts nothing", stuck)
	}
	if len(started) != len(gatedSweepTypes) {
		t.Fatalf("while one list call was stalled the sweep started %d of %d: %v\n"+
			"the slot a fetched-but-unconsumed listing holds is still the slot the launcher needs, so one slow call stops the window sliding",
			len(started), len(gatedSweepTypes), started)
	}

	// Two calls were inside the provider at once, by construction rather than
	// by luck: every swept call but the straggler's waits for the straggler
	// to be in, and the straggler does not leave until this test releases it.
	// So this is the in-flight bound reading exactly its own setting -
	// without it, a fix that deleted the bound outright would pass the
	// assertion above.
	if got := cloud.peakInFlight(); got != parallelism {
		t.Errorf("peak concurrent list calls was %d, want %d: with one call held inside the provider and the others waiting on it, that is the SweepParallelism this sweep was given", got, parallelism)
	}

	// And the sweep is the sweep it always was: same rows, same order, same
	// orphans, nothing fetched that nobody consumed.
	assertNoErrors(t, run.diags)
	if got := sweptScanOrder(run.res); strings.Join(got, ",") != strings.Join(gatedSweepTypes, ",") {
		t.Errorf("sweep scan rows read %v\nwant %v (universe order)", got, gatedSweepTypes)
	}
	if len(run.res.Orphans) != len(gatedSweepTypes) {
		t.Errorf("found %d orphans, want %d:\n%s", len(run.res.Orphans), len(gatedSweepTypes), run.res)
	}
	if len(run.res.sweepPrefetchWasted) != 0 || run.res.sweepPrefetchMismatched != 0 {
		t.Errorf("wasted %v and refused %d prefetched listings, want none of either", run.res.sweepPrefetchWasted, run.res.sweepPrefetchMismatched)
	}
}

// sweepBufferSettle is how long the test below waits for a list call the
// bound forbids, before concluding the bound held.
//
// It is the one budget in this file spent on a success, and it is here
// because no channel can carry the news that a launcher is BLOCKED: an absent
// start looks exactly like a start that has not happened yet. What keeps it
// from being a bet is the direction it can fail in. A loaded runner can only
// make this guard miss a violation, never invent one, so it cannot turn a
// correct sweep red - and the margin is not close either way, because the
// list calls this fixture makes are in-process map walks.
const sweepBufferSettle = 500 * time.Millisecond

// TestTheSweepBoundsFetchedButUnconsumedListings is the other half of issue
// #839, and it is what stops the test above being satisfied by deleting the
// backpressure.
//
// The slot the launcher took was never only a concurrency bound: it was also
// the promise that at most SweepParallelism types are ever fetched and not
// yet consumed, so that a sweep over the whole admission table does not hold
// every type's listed objects at once. Splitting the bound in two has to keep
// that promise, and it matters more here than it did for the read pass: a
// listing is every live object of its type, and [scanType] drops those
// objects once it has filed its row, so an unconsumed listing is residency
// the run would not otherwise pay at all.
//
// So: the scan loop is stopped on a stalled first type, the launcher is free,
// and the calls it manages to start are held between two numbers that are
// both the bound.
//
//   - It cannot stop before buffer listings are unconsumed, so at least
//     1 + buffer calls are started. Below that the launcher is not running
//     ahead of the scan loop at all and this test is holding nothing.
//   - It cannot get past buffer + parallelism. The launcher clears the buffer
//     gate and then waits for an in-flight slot, so it is admitted while
//     buffered is at most buffer-1 and may then be joined by the calls
//     already in flight.
//
// Which of the two it lands on is the scheduler's business, and asserting one
// exact value there is how a guard passes on eight processors and fails on
// CI's one.
func TestTheSweepBoundsFetchedButUnconsumedListings(t *testing.T) {
	const parallelism = 2
	const buffer = 3
	const minStarted = 1 + buffer
	const maxStarted = buffer + parallelism

	if len(gatedSweepTypes) <= maxStarted {
		t.Fatalf("the swept universe is %d types and the bound allows %d calls, so an overrun could not happen and this test proves nothing", len(gatedSweepTypes), maxStarted)
	}

	cloud := newStallingFixture(t, gatedSweepTypes[0])
	run, finished := discoverStalledAsync(t, cloud, parallelism, buffer)

	started := cloud.waitForStarts(minStarted)
	// However many the bound allows, one more than that is the violation, and
	// waiting for it is what gives an unbounded launcher its chance to commit
	// one. It returns the moment that call arrives; on a pass it costs the
	// budget and nothing else.
	started = append(started, cloud.waitForStartsWithin(maxStarted+1-len(started), sweepBufferSettle)...)
	overrun := cloud.startedCount()

	close(cloud.release)
	<-finished

	if stuck := cloud.stuckOnEntry(); stuck != "" {
		t.Fatalf("the listing of %s gave up waiting for the stalled call to begin, so the scan loop was never held and this run asserts nothing", stuck)
	}
	if len(started) < minStarted {
		t.Fatalf("with one list call stalled the sweep started %d calls, want at least %d: %v\n"+
			"the launcher is not running ahead of the scan loop at all, so this test is not holding the bound it exists to hold",
			len(started), minStarted, started)
	}
	if overrun > maxStarted {
		t.Errorf("with the scan loop stopped on one stalled list call, %d calls had been started, want at most %d (one stalled, at most %d listings fetched and unconsumed).\n"+
			"Fetched-but-unconsumed listings are unbounded, so a lagging scan loop now holds every type's listed objects at once - the regression the single slot existed to prevent",
			overrun, maxStarted, buffer+parallelism-1)
	}

	// The bound is a pause, not a loss: every type still lists, in order,
	// once the scan loop starts taking.
	assertNoErrors(t, run.diags)
	if got := sweptScanOrder(run.res); strings.Join(got, ",") != strings.Join(gatedSweepTypes, ",") {
		t.Errorf("sweep scan rows read %v\nwant %v (universe order)", got, gatedSweepTypes)
	}
	if len(run.res.Orphans) != len(gatedSweepTypes) {
		t.Errorf("found %d orphans, want %d:\n%s", len(run.res.Orphans), len(gatedSweepTypes), run.res)
	}
	if len(run.res.sweepPrefetchWasted) != 0 || run.res.sweepPrefetchMismatched != 0 {
		t.Errorf("wasted %v and refused %d prefetched listings, want none of either", run.res.sweepPrefetchWasted, run.res.sweepPrefetchMismatched)
	}
}
