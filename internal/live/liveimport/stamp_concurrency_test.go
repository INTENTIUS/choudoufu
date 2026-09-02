// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #583's own guard set: Approve stamps concurrently
// now, and the three properties concurrency could plausibly have taken away
// are each pinned here.
//
//  1. Report ordering. [StampReport] is documented as one outcome per Entry
//     in ratification order, and a caller reads it without cross-referencing.
//     The fixture below makes the COMPLETION order the exact reverse of the
//     ratification order, so an implementation that appended results as they
//     arrived would produce a report reversed end to end - not a subtly
//     shuffled one that a weak assertion could miss. [reverseGate] is what
//     makes that reversal a property of the fixture rather than a bet on the
//     scheduler; issue #597 is the CI run where the bet lost.
//  2. [notATagsOnlyPlan] gating every resource individually. A plan that
//     would replace one resource must refuse that one resource and write
//     nothing for it, while its neighbours - planned and applied on other
//     goroutines at the same moment - are stamped normally.
//  3. Partial-failure semantics. The sequential loop always continued past a
//     failure and gave every remaining entry its own attempt and its own
//     outcome (Approve's doc comment says so, and [OutcomeFailed]'s does
//     too). A concurrent version that stopped at the first error would be a
//     silent behaviour change to a command that mutates live infrastructure.
//
// Every test here also asserts that the work ACTUALLY overlapped, through
// [stampProvider.peak]. Without that, all three would pass just as happily
// against the sequential loop they exist to protect - a check that cannot
// fail, which this repository has shipped four of.

// stampProvider is a deliberately minimal [providers.Interface]: it embeds a
// nil interface, so any method these tests do not expect to be called panics
// rather than returning a zero value that quietly means something.
//
// It is NOT [tofu.MockProvider], which holds one mutex across the whole of
// PlanResourceChange - including the caller's own Fn - and would therefore
// serialize exactly the concurrency under test.
type stampProvider struct {
	providers.Interface

	// delay is how long one resource's plan takes, by its id. It is what
	// keeps several stamps inside the provider at once for the tests that
	// need a queue to form; it is NOT how the ordering test reverses
	// completion order (see gate, and issue #597 for why it stopped being
	// allowed to be).
	delay func(id string) time.Duration

	// gate, when set, orders the calls this provider receives by
	// construction rather than by timing. See [reverseGate].
	gate *reverseGate

	// replace names the ids whose plan comes back RequiresReplace, which is
	// what [notATagsOnlyPlan] must refuse.
	replace map[string]bool

	// applyFails names the ids whose apply returns a provider error.
	applyFails map[string]bool

	mu       sync.Mutex
	planned  []string
	applied  []string
	inFlight int
	peak     int
}

func newStampProvider() *stampProvider {
	return &stampProvider{replace: map[string]bool{}, applyFails: map[string]bool{}}
}

// enter/leave track how many stamps were in the provider at once, which is
// the only direct evidence in these tests that anything ran concurrently.
func (p *stampProvider) enter() {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
	p.mu.Unlock()
}

func (p *stampProvider) leave() {
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
}

func idOf(v cty.Value) string {
	if v == cty.NilVal || v.IsNull() {
		return ""
	}
	return v.GetAttr("id").AsString()
}

func (p *stampProvider) PlanResourceChange(_ context.Context, r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	id := idOf(r.PriorState)
	p.enter()
	if p.gate != nil {
		p.gate.plan(id)
	}
	if p.delay != nil {
		time.Sleep(p.delay(id))
	}
	p.leave()

	p.mu.Lock()
	p.planned = append(p.planned, id)
	replace := p.replace[id]
	p.mu.Unlock()

	resp := providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	if replace {
		resp.RequiresReplace = []cty.Path{cty.GetAttrPath("id")}
	}
	return resp
}

func (p *stampProvider) ApplyResourceChange(_ context.Context, r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
	id := idOf(r.PlannedState)
	if p.gate != nil {
		// Deferred, not called here: closing this entry's channel is what
		// releases the entry in front of it, and that release must happen
		// after this apply has genuinely finished - otherwise the entry it
		// releases could record its own apply first and the reversal would
		// be a race again.
		defer p.gate.applied(id)
	}
	p.mu.Lock()
	p.applied = append(p.applied, id)
	fails := p.applyFails[id]
	p.mu.Unlock()

	if fails {
		var diags tfdiags.Diagnostics
		diags = diags.Append(fmt.Errorf("the tagging API refused this write"))
		return providers.ApplyResourceChangeResponse{Diagnostics: diags}
	}
	return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
}

// completions is the order the provider FINISHED each call in: the planned
// slice is one id per PlanResourceChange that returned, the applied slice one
// per ApplyResourceChange. Both are read after Approve has returned, so the
// lock is for tidiness rather than for a race - wg.Wait already orders every
// write here before the read.
func (p *stampProvider) completions() (planned, applied []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.planned...), append([]string(nil), p.applied...)
}

func (p *stampProvider) appliedSet() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]bool, len(p.applied))
	for _, id := range p.applied {
		out[id] = true
	}
	return out
}

// stampIDs is the id an entry carries, and the name it is addressed by:
// r000, r001, ... so that ratification order, index order and lexical order
// all agree and a reversal is unmistakable.
func stampID(i int) string { return fmt.Sprintf("r%03d", i) }

// stampIndex is the inverse of [stampID].
func stampIndex(id string) (int, bool) {
	var i int
	if _, err := fmt.Sscanf(id, "r%03d", &i); err != nil {
		return 0, false
	}
	return i, true
}

// reversedIDs is the completion order [reverseGate] produces, written out so
// a failure prints both sequences in full rather than one element of one.
func reversedIDs(n int) []string {
	out := make([]string, 0, n)
	for i := n - 1; i >= 0; i-- {
		out = append(out, stampID(i))
	}
	return out
}

// ---------------------------------------------------------------------------
// The reversal, by construction
// ---------------------------------------------------------------------------

// gateStuckAfter is a hang-breaker, not a timing device, and it is worth
// being precise about the difference because issue #597 is what happens when
// a fixture's correctness rests on a duration.
//
// Nothing in [reverseGate] SUCCEEDS because of this constant. Every wait it
// guards is satisfied by a channel close, and the goroutine doing the closing
// is already running and already past the barrier when the wait begins, so on
// any Approve that runs its entries concurrently the wait costs microseconds
// at any load and at any GOMAXPROCS. It exists for the one case where the
// chain cannot drain at all - an Approve that stops running entries
// concurrently, which would otherwise deadlock the whole chain - so that the
// test fails in half a minute with [reverseGate.stuckAt]'s diagnosis instead
// of hanging until the package's test timeout. A wait that reaches it always
// FAILS the test; it can never become a pass.
const gateStuckAfter = 30 * time.Second

// reverseGate makes the ordering fixture's completion order the exact reverse
// of its ratification order by construction: on every run, at every load, at
// every GOMAXPROCS, with no clock in the success path.
//
// It replaces the per-entry sleeps this fixture used to lean on, which is
// GitHub issue #597. Sleeping (n-i) milliseconds only reverses completion
// order if every goroutine also STARTS within a few milliseconds of the
// others; on a contended CI runner one did not, entry 0 finished first, and
// the vacuity guard correctly refused to assert against a fixture that had
// not set anything up. The guard was right. The fixture was the bet.
//
// Two happens-before edges per entry replace it, and no duration:
//
//  1. Nobody's plan returns until all n entries are inside the provider at
//     once - the arrival barrier. That is also this fixture's overlap
//     evidence, and it upgrades it: peak concurrency is exactly n, not "at
//     least 2 if the scheduler was kind".
//  2. Entry i's plan then does not return until entry i+1's APPLY has
//     already returned. Entry n-1 waits for nobody, so it is the only entry
//     that can move first; its apply releases n-2, whose apply releases
//     n-3, and so on down to 0.
//
// So planned and applied both read r011, r010, ... r000, in that order, in
// every run - and each entry still has its own whole ApplyResourceChange
// left to do after the entry behind it finished, which is the gap an "append
// as they arrive" collector would have to invert to escape the assertion.
//
// It cannot deadlock a correct Approve: Approve pushes its semaphore token
// and spawns each goroutine before pushing the next, so with parallelism = n
// all n goroutines exist before any of them must finish, and a goroutine
// blocked on a channel receive is one the runtime is free to deschedule in
// favour of the one it is waiting for. It CAN deadlock an Approve that stops
// running entries concurrently - see [gateStuckAfter], which turns that into
// a failure rather than a hang.
type reverseGate struct {
	n int

	// arrived is closed once every entry is inside PlanResourceChange.
	arrived chan struct{}

	// done[i] is closed when entry i's ApplyResourceChange returned, and
	// releasing it is what lets entry i-1's plan return.
	done []chan struct{}

	mu      sync.Mutex
	count   int
	closed  []bool
	abandon chan struct{}
	stuck   string
}

func newReverseGate(n int) *reverseGate {
	g := &reverseGate{
		n:       n,
		arrived: make(chan struct{}),
		done:    make([]chan struct{}, n),
		closed:  make([]bool, n),
		abandon: make(chan struct{}),
	}
	for i := range g.done {
		g.done[i] = make(chan struct{})
	}
	return g
}

// plan is called from PlanResourceChange before that call records anything,
// and returns only when it is this entry's turn to be the next to finish.
func (g *reverseGate) plan(id string) {
	i, ok := stampIndex(id)
	if !ok {
		return
	}
	g.mu.Lock()
	g.count++
	if g.count == g.n {
		close(g.arrived)
	}
	g.mu.Unlock()

	g.wait(g.arrived, id)
	if i+1 < g.n {
		g.wait(g.done[i+1], id)
	}
}

// applied is called when ApplyResourceChange returns and releases the entry
// in front of this one.
func (g *reverseGate) applied(id string) {
	i, ok := stampIndex(id)
	if !ok {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed[i] {
		g.closed[i] = true
		close(g.done[i])
	}
}

func (g *reverseGate) wait(ch <-chan struct{}, id string) {
	timer := time.NewTimer(gateStuckAfter)
	defer timer.Stop()
	select {
	case <-ch:
	case <-g.abandon:
		g.giveUp(id)
	case <-timer.C:
		g.giveUp(id)
	}
}

// giveUp records the first entry that could not be released and frees every
// other waiter, so a stuck chain costs one gateStuckAfter for the whole run
// rather than one per entry.
func (g *reverseGate) giveUp(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stuck == "" {
		g.stuck = id
		close(g.abandon)
	}
}

// stuckAt names the first entry whose wait was abandoned, or "" when the
// chain drained as designed. Anything else means Approve did not have every
// entry in flight at once - a change in Approve, not a slow runner - and the
// test reports that instead of an ordering result the fixture never set up.
func (g *reverseGate) stuckAt() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stuck
}

// concurrentRatification builds n eligible aws_vpc instances, all backed by
// the one provider p - which is how a real run looks, since Ratify hands
// every instance of a type the same configured provider connection.
func concurrentRatification(t *testing.T, n int, p providers.Interface) *Ratification {
	t.Helper()
	rat := &Ratification{
		Estate:     "conc-estate",
		eligible:   make(map[string]*eligible),
		recordable: make(map[string]*recordable),
		located:    make(map[string]*located),
		residuable: make(map[string]*residuable),
	}
	for i := 0; i < n; i++ {
		id := stampID(i)
		addr := mustAddr(t, "aws_vpc."+id)
		rat.Entries = append(rat.Entries, Entry{
			Addr:     addr,
			TypeName: "aws_vpc",
			Status:   StatusVerified,
		})
		rat.eligible[addr.String()] = &eligible{residuable{
			provider: p,
			schema:   vpcSchema(),
			typeName: "aws_vpc",
			applied: cty.ObjectVal(map[string]cty.Value{
				"id":   cty.StringVal(id),
				"tags": cty.MapValEmpty(cty.String),
			}),
			identity: cty.NilVal,
		}}
	}
	return rat
}

// renderReport is the byte-for-byte comparison surface for a report: every
// field of every outcome, in the order the report carries them.
func renderReport(rep *StampReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "estate=%s identities=%d\n", rep.Estate, rep.IdentitiesRecorded)
	for i, o := range rep.Outcomes {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%s\n", i, o.Addr, o.TypeName, o.Outcome, o.Detail)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// 1. Ordering
// ---------------------------------------------------------------------------

// TestApproveReportOrderIsRatificationOrderNotCompletionOrder is issue #583's
// ordering acceptance criterion: the same input, stamped sequentially and
// stamped concurrently, must produce a byte-identical StampReport.
//
// The concurrent run's completion order is deliberately the exact reverse of
// its ratification order - [reverseGate] chains entry i's plan behind entry
// i+1's apply, so entry 0 finishes last on every run - and so "collect by
// index" and "append as they arrive" cannot produce the same answer here.
func TestApproveReportOrderIsRatificationOrderNotCompletionOrder(t *testing.T) {
	const n = 12

	seqProv := newStampProvider()
	seqRat := concurrentRatification(t, n, seqProv)
	seqRat.parallelism = 1
	seqRep, seqDiags := seqRat.Approve(context.Background())
	if seqDiags.HasErrors() {
		t.Fatalf("sequential Approve: %s", seqDiags.Err())
	}
	if seqProv.peak != 1 {
		t.Fatalf("parallelism=1 ran %d stamps at once, want exactly 1 - the bound is not being honoured", seqProv.peak)
	}

	concProv := newStampProvider()
	concProv.gate = newReverseGate(n)
	concRat := concurrentRatification(t, n, concProv)
	concRat.parallelism = n
	concRep, concDiags := concRat.Approve(context.Background())
	if concDiags.HasErrors() {
		t.Fatalf("concurrent Approve: %s", concDiags.Err())
	}

	// Evidence the fixture did what it claims: everything overlapped, and
	// the provider finished them in reverse. All three of these are
	// properties of [reverseGate]'s chain, so a failure here is a report
	// about Approve or about the fixture - never about the runner.
	if id := concProv.gate.stuckAt(); id != "" {
		t.Fatalf("the reverse gate gave up waiting at %s: Approve did not have all %d entries inside the provider at once, so the fixture could not reverse completion order and the ordering assertion below would be vacuous", id, n)
	}
	if concProv.peak != n {
		t.Fatalf("peak concurrency was %d, want exactly %d - every entry should be inside the provider at the barrier, so this test proves nothing about ordering under concurrency", concProv.peak, n)
	}
	planned, applied := concProv.completions()
	wantOrder := strings.Join(reversedIDs(n), " ")
	if got := strings.Join(planned, " "); got != wantOrder {
		t.Fatalf("plans COMPLETED in the order %s, want %s - the fixture is not reversing completion order, so the ordering assertion below is vacuous", got, wantOrder)
	}
	if got := strings.Join(applied, " "); got != wantOrder {
		t.Fatalf("applies COMPLETED in the order %s, want %s - the fixture is not reversing completion order, so the ordering assertion below is vacuous", got, wantOrder)
	}

	if got, want := renderReport(concRep), renderReport(seqRep); got != want {
		t.Errorf("the concurrent report is not byte-identical to the sequential one.\nconcurrent:\n%s\nsequential:\n%s", got, want)
	}
	// And, independently of the sequential run, the report reads in
	// ratification order.
	for i, o := range concRep.Outcomes {
		if got, want := o.Addr.String(), "aws_vpc."+stampID(i); got != want {
			t.Errorf("outcome %d is for %s, want %s", i, got, want)
		}
	}
}

// TestApproveHonoursItsParallelismBound pins the other half of the setting:
// it is a ceiling, not a hint, and the default is stock's.
func TestApproveHonoursItsParallelismBound(t *testing.T) {
	for _, par := range []int{1, 2, 5} {
		t.Run(fmt.Sprintf("par=%d", par), func(t *testing.T) {
			p := newStampProvider()
			p.delay = func(string) time.Duration { return 3 * time.Millisecond }
			rat := concurrentRatification(t, 20, p)
			rat.parallelism = par
			if _, diags := rat.Approve(context.Background()); diags.HasErrors() {
				t.Fatalf("Approve: %s", diags.Err())
			}
			if p.peak > par {
				t.Errorf("peak concurrency %d exceeds parallelism %d", p.peak, par)
			}
			if par > 1 && p.peak < 2 {
				t.Errorf("peak concurrency %d at parallelism %d - nothing ran concurrently", p.peak, par)
			}
		})
	}

	// Zero, the zero value a caller that never set Request.Parallelism
	// leaves behind, means the documented default rather than "no
	// concurrency" or "unbounded".
	p := newStampProvider()
	p.delay = func(string) time.Duration { return 3 * time.Millisecond }
	rat := concurrentRatification(t, 40, p)
	rat.parallelism = 0
	if _, diags := rat.Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	if p.peak > DefaultParallelism {
		t.Errorf("peak concurrency %d with parallelism unset, want at most DefaultParallelism (%d)", p.peak, DefaultParallelism)
	}
	if p.peak < 2 {
		t.Errorf("peak concurrency %d with parallelism unset - the zero value is not reaching DefaultParallelism", p.peak)
	}
}

// TestApproveWithNoEntriesDoesNotDeadlock covers the degenerate size the
// semaphore has to be sized around: an empty ratification bounds nothing.
func TestApproveWithNoEntriesDoesNotDeadlock(t *testing.T) {
	rat := concurrentRatification(t, 0, newStampProvider())
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	if len(rep.Outcomes) != 0 {
		t.Errorf("Outcomes = %d, want 0", len(rep.Outcomes))
	}
}

// ---------------------------------------------------------------------------
// 2. The guard, under concurrency
// ---------------------------------------------------------------------------

// TestNotATagsOnlyPlanStillRefusesPerResourceUnderConcurrency is issue #583's
// safety acceptance criterion, and the one that matters most: concurrency
// must not weaken, share or skip [notATagsOnlyPlan].
//
// Every third resource's plan comes back RequiresReplace while its
// neighbours are planned and applied on other goroutines at the same instant.
// Each of those must be refused INDIVIDUALLY - reported FAILED, with the
// replacement wording - and must reach no ApplyResourceChange at all, while
// every other resource is stamped normally.
//
// Proved red before it was trusted green: with the `if why := notATagsOnly
// Plan(...)` branch in approveOne removed, this fails with 7 resources
// applied that must not have been.
func TestNotATagsOnlyPlanStillRefusesPerResourceUnderConcurrency(t *testing.T) {
	const n = 21
	p := newStampProvider()
	// Uneven delays so the refusals and the writes genuinely interleave
	// rather than running in two tidy phases.
	p.delay = func(id string) time.Duration {
		var i int
		if _, err := fmt.Sscanf(id, "r%03d", &i); err != nil {
			return 0
		}
		return time.Duration((i*7)%5+1) * time.Millisecond
	}
	var wantRefused []string
	for i := 0; i < n; i += 3 {
		p.replace[stampID(i)] = true
		wantRefused = append(wantRefused, stampID(i))
	}

	rat := concurrentRatification(t, n, p)
	rat.parallelism = DefaultParallelism
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	if p.peak < 2 {
		t.Fatalf("peak concurrency was %d - the guard was never exercised concurrently, so this test proves nothing", p.peak)
	}

	applied := p.appliedSet()
	for _, id := range wantRefused {
		if applied[id] {
			t.Errorf("%s was APPLIED despite a plan that requires replacing it - notATagsOnlyPlan did not gate it", id)
		}
	}
	if len(rep.Outcomes) != n {
		t.Fatalf("Outcomes = %d, want %d", len(rep.Outcomes), n)
	}
	refused := map[string]bool{}
	for _, id := range wantRefused {
		refused[id] = true
	}
	for i, o := range rep.Outcomes {
		id := stampID(i)
		switch {
		case refused[id]:
			if o.Outcome != OutcomeFailed {
				t.Errorf("%s: Outcome = %s, want %s", id, o.Outcome, OutcomeFailed)
			}
			if !strings.Contains(o.Detail, "would require replacing it") {
				t.Errorf("%s: Detail = %q, want the replacement refusal", id, o.Detail)
			}
		default:
			if o.Outcome != OutcomeStamped {
				t.Errorf("%s: Outcome = %s, want %s (Detail: %s)", id, o.Outcome, OutcomeStamped, o.Detail)
			}
			if !applied[id] {
				t.Errorf("%s: reported %s but no apply reached the provider", id, o.Outcome)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 3. Partial failure
// ---------------------------------------------------------------------------

// TestApproveContinuesPastAFailureUnderConcurrency pins the semantics the
// sequential loop always had, which issue #583 asks to preserve rather than
// re-invent: a per-resource failure - here a provider error from the tagging
// call itself, the mid-list case - does not abandon the run. Every remaining
// entry still gets its own attempt and its own outcome.
//
// This is why Approve uses a plain WaitGroup and not an errgroup: an
// errgroup's first error cancels the rest, which is precisely the behaviour
// change this test refuses.
func TestApproveContinuesPastAFailureUnderConcurrency(t *testing.T) {
	const n = 15
	const failAt = 4

	run := func(par int) *StampReport {
		p := newStampProvider()
		p.delay = func(string) time.Duration { return time.Millisecond }
		p.applyFails[stampID(failAt)] = true
		rat := concurrentRatification(t, n, p)
		rat.parallelism = par
		rep, diags := rat.Approve(context.Background())
		if diags.HasErrors() {
			t.Fatalf("par=%d: Approve: %s", par, diags.Err())
		}
		if par > 1 && p.peak < 2 {
			t.Fatalf("par=%d: peak concurrency was %d - nothing overlapped", par, p.peak)
		}
		if got := len(p.applied); got != n {
			t.Errorf("par=%d: %d applies reached the provider, want %d - a failure stopped the run", par, got, n)
		}
		return rep
	}

	seq := run(1)
	conc := run(DefaultParallelism)

	if got, want := renderReport(conc), renderReport(seq); got != want {
		t.Errorf("partial-failure behaviour differs between sequential and concurrent.\nconcurrent:\n%s\nsequential:\n%s", got, want)
	}
	for i, o := range conc.Outcomes {
		want := OutcomeStamped
		if i == failAt {
			want = OutcomeFailed
		}
		if o.Outcome != want {
			t.Errorf("outcome %d (%s) = %s, want %s", i, o.Addr, o.Outcome, want)
		}
	}
}

// TestApproveIsIdempotentUnderConcurrency is issue #583 item 6: re-running
// after a partial stamp must stay safe. A resource already carrying this
// estate's markers naming this exact address is reported ALREADY_STAMPED and
// reaches no write at all - the same fast path the sequential loop had, and
// still per-resource under concurrency rather than a decision made once for
// the batch.
func TestApproveIsIdempotentUnderConcurrency(t *testing.T) {
	const n = 12
	p := newStampProvider()
	p.delay = func(string) time.Duration { return time.Millisecond }
	rat := concurrentRatification(t, n, p)
	rat.parallelism = DefaultParallelism

	// Half the estate already stamped, as a run interrupted halfway would
	// leave it.
	for i := 0; i < n; i += 2 {
		addr := "aws_vpc." + stampID(i)
		e := rat.eligible[addr]
		e.applied = cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal(stampID(i)),
			"tags": cty.MapVal(map[string]cty.Value{
				discovery.TagEstate:        cty.StringVal("conc-estate"),
				discovery.AddressTagKey(0): cty.StringVal(discovery.EscapeAddress(addr)),
			}),
		})
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve: %s", diags.Err())
	}
	applied := p.appliedSet()
	for i, o := range rep.Outcomes {
		id := stampID(i)
		if i%2 == 0 {
			if o.Outcome != OutcomeAlreadyStamped {
				t.Errorf("%s: Outcome = %s, want %s (Detail: %s)", id, o.Outcome, OutcomeAlreadyStamped, o.Detail)
			}
			if applied[id] {
				t.Errorf("%s: already carried this estate's markers and was written again", id)
			}
			continue
		}
		if o.Outcome != OutcomeStamped {
			t.Errorf("%s: Outcome = %s, want %s (Detail: %s)", id, o.Outcome, OutcomeStamped, o.Detail)
		}
	}
	if p.peak < 2 {
		t.Fatalf("peak concurrency was %d - nothing overlapped", p.peak)
	}
}
