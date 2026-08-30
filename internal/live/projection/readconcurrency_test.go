// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #585's guard set. The read pass overlaps its
// per-instance provider round trips now, and the four properties concurrency
// could plausibly have taken away are each pinned here.
//
//  1. The calls themselves. Exactly the calls the sequential loop made, for
//     exactly the instances it made them for: no extra read, no skipped one,
//     no retry. That is the property the issue accepts on, and it is asserted
//     from both ends - the provider's own call log, and
//     [builder.readWasted]/[builder.readMismatched], which count a prefetched
//     read nobody consumed and a prefetched read a consumer refused.
//  2. Ordering. Everything a projection reports - the materialized list, the
//     omissions, the diagnostics - is produced by the consuming loop in
//     address order. The fixture below makes the cloud's COMPLETION order the
//     exact reverse of that, so an implementation that collected results as
//     they arrived would produce every one of those lists reversed end to
//     end, not subtly shuffled. [readReverseGate] is what makes the reversal
//     a property of the fixture rather than a bet on the scheduler; issue
//     #597 is the CI run where that bet lost, on the stamping path.
//  3. Failure semantics. The sequential loop continued past a failed read and
//     gave every remaining instance its own attempt and its own omission.
//     There is no errgroup here for exactly that reason: an errgroup's first
//     error would cancel reads the sequential pass would have made, turning a
//     one-resource failure into an estate-wide one.
//  4. Determinism at [Options.ReadParallelism] 1. The documented meaning of
//     one is "reproduces the sequential loop exactly", including the order
//     the provider is called in, and that is asserted rather than described.
//
// Every test that claims concurrency also asserts the work ACTUALLY
// overlapped, through [readProvider.peak]. Without that they would pass just
// as happily against the sequential loop they exist to protect - a check that
// cannot fail, which this repository has shipped four of.

// ---------------------------------------------------------------------------
// A provider that does not serialize
// ---------------------------------------------------------------------------

// readProvider is a deliberately minimal [providers.Interface]: it embeds a
// nil interface, so any method these tests do not expect to be called panics
// rather than returning a zero value that quietly means something.
//
// It is NOT [tofu.MockProvider], which holds one mutex across the whole of
// ImportResourceState and ReadResource - including the caller's own Fn - and
// would therefore serialize exactly the concurrency under test. (That is also
// why every other test in this package still passes unchanged: through
// MockProvider the reads run on several goroutines but one at a time.)
type readProvider struct {
	providers.Interface

	// gate, when set, makes completion order the reverse of call order. See
	// [readReverseGate].
	gate *readReverseGate

	// readErr maps an import ID to the error ReadResource answers with, which
	// is how the failure-semantics test makes exactly one instance fail.
	readErr map[string]string

	// absent maps an import ID to "the object does not exist", the other
	// non-fatal outcome the sequential loop had to keep walking past.
	absent map[string]bool

	mu       sync.Mutex
	calls    []string
	inFlight int
	peak     int
}

func newReadProvider() *readProvider {
	return &readProvider{readErr: map[string]string{}, absent: map[string]bool{}}
}

func (p *readProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{ResourceTypes: fakeSchemas()}
}

func (p *readProvider) ImportResourceState(_ context.Context, r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
	p.enter("import " + r.TypeName + "/" + r.Target.ID)
	if p.gate != nil {
		p.gate.arrived(r.Target.ID)
	}
	schema := fakeSchemas()[r.TypeName]
	return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
		TypeName: r.TypeName,
		State:    objectFor(schema, map[string]string{"id": r.Target.ID, "name": r.Target.ID}),
	}}}
}

func (p *readProvider) ReadResource(_ context.Context, r providers.ReadResourceRequest) providers.ReadResourceResponse {
	schema := fakeSchemas()[r.TypeName]
	id := r.PriorState.GetAttr("id").AsString()
	p.record("read " + r.TypeName + "/" + id)

	var resp providers.ReadResourceResponse
	switch {
	case p.readErr[id] != "":
		resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("%s", p.readErr[id]))
	case p.absent[id]:
		resp.NewState = cty.NullVal(schema.Block.ImpliedType())
	default:
		resp.NewState = objectFor(schema, map[string]string{"id": id, "name": id})
	}

	if p.gate != nil {
		p.gate.finished(id)
	}
	p.leave()
	return resp
}

func (p *readProvider) PlanResourceChange(_ context.Context, r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
}

// enter records a call and opens an in-flight window that [readProvider.leave]
// closes at the end of the matching ReadResource, so peak counts whole
// import+read pairs rather than individual RPCs.
func (p *readProvider) enter(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, call)
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
}

func (p *readProvider) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight--
}

func (p *readProvider) record(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, call)
}

func (p *readProvider) callLog() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

func (p *readProvider) peakConcurrency() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

const readParallelN = 6

func readParallelID(i int) string { return "read-parallel-" + strconv.Itoa(i) }

// readParallelIndex is the fixture's instance number for an import ID, and
// false for anything else - a gate must not block on a call it did not set up.
func readParallelIndex(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "read-parallel-")
	if !ok {
		return 0, false
	}
	i, err := strconv.Atoi(rest)
	if err != nil || i < 0 || i >= readParallelN {
		return 0, false
	}
	return i, true
}

// readParallelResolutions is one concrete resolution per fixture block, handed
// in deliberately reversed so that nothing here depends on the caller's own
// order: [orderWork] sorts by address, and address order is what every
// assertion below is written against.
func readParallelResolutions(t *testing.T) []identity.Resolution {
	t.Helper()
	out := make([]identity.Resolution, 0, readParallelN)
	for i := readParallelN - 1; i >= 0; i-- {
		out = append(out, identity.Resolution{
			Addr:     mustAddr(t, fmt.Sprintf("aws_cloudwatch_log_group.g%d", i)),
			Class:    identity.ClassConcrete,
			ImportID: readParallelID(i),
		})
	}
	return out
}

// runReadPass drives the read pass over the fixture directly, through
// [newBuilder] and [builder.run] rather than through [buildFrom], because the
// ordering claims are about the lists as the loop appended them and buildFrom
// sorts several of them before returning.
func runReadPass(t *testing.T, p *readProvider, opts Options) *builder {
	t.Helper()
	cfg := loadConfig(t, "testdata/read-parallel")
	b := newBuilder(context.Background(), cfg, SingleProvider(awsProvider, p), opts)
	b.run(context.Background(), readParallelResolutions(t))
	return b
}

// ---------------------------------------------------------------------------
// 1. The calls
// ---------------------------------------------------------------------------

// TestReadPassIsCallIdenticalToTheSequentialLoop is the property issue #585
// accepts on: overlapping the reads must not change which calls are made.
// Sequential and concurrent runs are compared as multisets, because the ORDER
// calls arrive in is exactly what concurrency is allowed to change (stock's
// own graph walk at -parallelism 10 has no fixed order either) and nothing in
// this fork reads it.
func TestReadPassIsCallIdenticalToTheSequentialLoop(t *testing.T) {
	seqP := newReadProvider()
	seq := runReadPass(t, seqP, Options{ReadParallelism: 1})

	conP := newReadProvider()
	con := runReadPass(t, conP, Options{ReadParallelism: readParallelN})

	seqCalls := slices.Sorted(slices.Values(seqP.callLog()))
	conCalls := slices.Sorted(slices.Values(conP.callLog()))
	if !slices.Equal(seqCalls, conCalls) {
		t.Errorf("concurrent run made different calls\nsequential: %v\nconcurrent: %v", seqCalls, conCalls)
	}
	if got, want := len(seqCalls), 2*readParallelN; got != want {
		t.Errorf("sequential run made %d calls, want %d (one import and one read per instance)", got, want)
	}

	if !slices.Equal(addrStrings(seq.materialized), addrStrings(con.materialized)) {
		t.Errorf("materialized differs\nsequential: %v\nconcurrent: %v", addrStrings(seq.materialized), addrStrings(con.materialized))
	}
	if seq.diags.HasErrors() || con.diags.HasErrors() {
		t.Errorf("unexpected diagnostics\nsequential:\n%s\nconcurrent:\n%s", renderDiags(seq.diags), renderDiags(con.diags))
	}
	if got := con.readWasted; len(got) != 0 {
		t.Errorf("the concurrent run prefetched reads nobody consumed: %v", got)
	}
	if got := con.readMismatched; got != 0 {
		t.Errorf("the concurrent run refused %d prefetched answers; the plan and the loop disagreed about what to read", got)
	}
}

// TestReadParallelismOneCallsInLoopOrder pins the documented meaning of
// [Options.ReadParallelism] = 1: not merely "one at a time", but the same
// sequence the sequential loop produced, so an operator who turns concurrency
// off gets back exactly the run this fork had before issue #585.
func TestReadParallelismOneCallsInLoopOrder(t *testing.T) {
	p := newReadProvider()
	runReadPass(t, p, Options{ReadParallelism: 1})

	var want []string
	for i := 0; i < readParallelN; i++ {
		want = append(want,
			"import aws_cloudwatch_log_group/"+readParallelID(i),
			"read aws_cloudwatch_log_group/"+readParallelID(i),
		)
	}
	if got := p.callLog(); !slices.Equal(got, want) {
		t.Errorf("calls at parallelism 1 were\n %v\nwant\n %v", got, want)
	}
	if got := p.peakConcurrency(); got != 1 {
		t.Errorf("peak concurrency at parallelism 1 was %d, want 1", got)
	}
}

// TestReadPassMakesNoCallForATerminalPrep is the other half of call parity:
// an instance whose head refuses it - here, a resolution for a resource block
// the configuration does not contain - made no provider call in the sequential
// pass and must make none now. It is also the one case the prefetch spends no
// slot on, so an estate that is mostly unreadable does not stall behind it.
func TestReadPassMakesNoCallForATerminalPrep(t *testing.T) {
	p := newReadProvider()
	cfg := loadConfig(t, "testdata/read-parallel")

	res := readParallelResolutions(t)
	res = append(res, identity.Resolution{
		Addr:     mustAddr(t, "aws_cloudwatch_log_group.not_in_the_configuration"),
		Class:    identity.ClassConcrete,
		ImportID: "no-such-block",
	})

	b := newBuilder(context.Background(), cfg, SingleProvider(awsProvider, p), Options{ReadParallelism: readParallelN})
	b.run(context.Background(), res)

	for _, call := range p.callLog() {
		if strings.Contains(call, "no-such-block") {
			t.Errorf("the provider was called for an instance with no resource block: %v", p.callLog())
			break
		}
	}
	if got, want := len(p.callLog()), 2*readParallelN; got != want {
		t.Errorf("made %d calls, want %d: %v", got, want, p.callLog())
	}
	if len(b.readWasted) != 0 {
		t.Errorf("prefetched reads nobody consumed: %v", b.readWasted)
	}
	if !hasDiag(b.diags, "Resolved instance missing from the configuration", "not_in_the_configuration") {
		t.Errorf("the refusal was not reported:\n%s", renderDiags(b.diags))
	}
}

// ---------------------------------------------------------------------------
// 2. Ordering
// ---------------------------------------------------------------------------

// TestReadPassReportsInLoopOrderWhateverOrderTheCloudAnswersIn is the
// ordering claim, against a fixture whose completion order is the exact
// reverse of its loop order by construction.
//
// Three of the six instances are absent, so the run produces both a
// materialized list and an omission list and both have to read in address
// order. A collector that appended results as they arrived would return both
// reversed.
func TestReadPassReportsInLoopOrderWhateverOrderTheCloudAnswersIn(t *testing.T) {
	p := newReadProvider()
	p.gate = newReadReverseGate(t, readParallelN)
	for i := 1; i < readParallelN; i += 2 {
		p.absent[readParallelID(i)] = true
	}

	b := runReadPass(t, p, Options{ReadParallelism: readParallelN})

	if got, want := p.peakConcurrency(), readParallelN; got != want {
		t.Fatalf("peak concurrency was %d, want %d: the fixture never overlapped, so it asserts nothing", got, want)
	}
	p.gate.assertReversed(t, p.callLog())

	wantMaterialized := []string{
		"aws_cloudwatch_log_group.g0",
		"aws_cloudwatch_log_group.g2",
		"aws_cloudwatch_log_group.g4",
	}
	if got := addrStrings(b.materialized); !slices.Equal(got, wantMaterialized) {
		t.Errorf("materialized in order\n %v\nwant\n %v", got, wantMaterialized)
	}

	wantOmitted := []string{
		"aws_cloudwatch_log_group.g1",
		"aws_cloudwatch_log_group.g3",
		"aws_cloudwatch_log_group.g5",
	}
	var gotOmitted []string
	for _, o := range b.omissionList {
		gotOmitted = append(gotOmitted, o.Addr.String())
	}
	if !slices.Equal(gotOmitted, wantOmitted) {
		t.Errorf("omitted in order\n %v\nwant\n %v", gotOmitted, wantOmitted)
	}
}

// ---------------------------------------------------------------------------
// 3. Failure semantics
// ---------------------------------------------------------------------------

// TestReadPassContinuesPastAFailedRead pins what the sequential loop did with
// a provider error on one instance: report it, omit that one instance, and
// give every remaining instance its own attempt and its own outcome. Nothing
// is cancelled and nothing is abandoned.
//
// This is the property an errgroup would have taken away silently, which is
// why [builder.startReadPrefetch] uses a plain [sync.WaitGroup] - the same
// choice, for the same reason, that issue #583 made on the stamping path.
func TestReadPassContinuesPastAFailedRead(t *testing.T) {
	p := newReadProvider()
	p.readErr[readParallelID(2)] = "the cloud is having a bad day"

	b := runReadPass(t, p, Options{ReadParallelism: readParallelN})

	// Every instance was still read, the failing one included.
	if got, want := len(p.callLog()), 2*readParallelN; got != want {
		t.Errorf("made %d calls, want %d - a failure stopped work the sequential loop would have done: %v", got, want, p.callLog())
	}

	wantMaterialized := []string{
		"aws_cloudwatch_log_group.g0",
		"aws_cloudwatch_log_group.g1",
		"aws_cloudwatch_log_group.g3",
		"aws_cloudwatch_log_group.g4",
		"aws_cloudwatch_log_group.g5",
	}
	if got := addrStrings(b.materialized); !slices.Equal(got, wantMaterialized) {
		t.Errorf("materialized\n %v\nwant\n %v", got, wantMaterialized)
	}
	if len(b.omissionList) != 1 || b.omissionList[0].Addr.String() != "aws_cloudwatch_log_group.g2" {
		t.Errorf("omissions were %v, want only g2", b.omissionList)
	}
	if !hasDiag(b.diags, "Cannot read for projection", readParallelID(2)) {
		t.Errorf("the read failure was not reported:\n%s", renderDiags(b.diags))
	}
}

// ---------------------------------------------------------------------------
// The reversal, by construction
// ---------------------------------------------------------------------------

// readGateStuckAfter is a hang-breaker, not a timing device.
//
// Nothing in [readReverseGate] SUCCEEDS because of this constant. Every wait
// it guards is satisfied by a channel close, and the goroutine doing the
// closing is already running and already past the barrier when the wait
// begins, so on any run that reads concurrently the wait costs microseconds at
// any load and at any GOMAXPROCS. It exists for the one case where the chain
// cannot drain at all - a read pass that stops overlapping, which would
// otherwise deadlock the chain - so that the test fails in half a minute with
// a diagnosis instead of hanging until the package's test timeout. A wait that
// reaches it always FAILS the test; it can never become a pass.
const readGateStuckAfter = 30 * time.Second

// readReverseGate makes the read fixture's completion order the exact reverse
// of its loop order by construction: on every run, at every load, at every
// GOMAXPROCS, with no clock in the success path.
//
// Two happens-before edges per instance, and no duration:
//
//  1. Nobody's import returns until all n instances are inside the provider at
//     once - the arrival barrier. That is also this fixture's overlap
//     evidence, and it upgrades it: peak concurrency is exactly n, not "at
//     least 2 if the scheduler was kind".
//  2. Instance i's import then does not return until instance i+1's READ has
//     already returned. Instance n-1 waits for nobody, so it is the only one
//     that can move first; its read releases n-2, whose read releases n-3, and
//     so on down to 0.
//
// It cannot deadlock a correct read pass at ReadParallelism n: the launcher
// pushes its slot and spawns each worker before pushing the next, so all n
// goroutines exist before any of them must finish, and a goroutine blocked on
// a channel receive is one the runtime is free to deschedule in favour of the
// one it is waiting for.
type readReverseGate struct {
	n int

	// barrier is closed once every instance is inside ImportResourceState.
	barrier chan struct{}

	// done[i] is closed when instance i's ReadResource returned, and
	// releasing it is what lets instance i-1's import return.
	done []chan struct{}

	mu      sync.Mutex
	count   int
	closed  []bool
	abandon chan struct{}
	stuck   string
}

func newReadReverseGate(t *testing.T, n int) *readReverseGate {
	t.Helper()
	g := &readReverseGate{
		n:       n,
		barrier: make(chan struct{}),
		done:    make([]chan struct{}, n),
		closed:  make([]bool, n),
		abandon: make(chan struct{}),
	}
	for i := range g.done {
		g.done[i] = make(chan struct{})
	}
	return g
}

// arrived is called from ImportResourceState before it answers, and returns
// only when it is this instance's turn to be the next to finish.
func (g *readReverseGate) arrived(id string) {
	i, ok := readParallelIndex(id)
	if !ok {
		return
	}
	g.mu.Lock()
	g.count++
	if g.count == g.n {
		close(g.barrier)
	}
	g.mu.Unlock()

	g.wait(g.barrier, id)
	if i+1 < g.n {
		g.wait(g.done[i+1], id)
	}
}

// finished is called when ReadResource has its answer and releases the
// instance in front of this one.
func (g *readReverseGate) finished(id string) {
	i, ok := readParallelIndex(id)
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

func (g *readReverseGate) wait(ch <-chan struct{}, id string) {
	timer := time.NewTimer(readGateStuckAfter)
	defer timer.Stop()
	select {
	case <-ch:
	case <-g.abandon:
		g.giveUp(id)
	case <-timer.C:
		g.giveUp(id)
	}
}

// giveUp records the first instance that could not be released and frees every
// other waiter, so a stuck chain costs one readGateStuckAfter for the whole
// run rather than one per instance.
func (g *readReverseGate) giveUp(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stuck == "" {
		g.stuck = id
		close(g.abandon)
	}
}

// assertReversed checks that the fixture did what it exists to do: the reads
// completed in the exact reverse of loop order. A test that skipped this could
// pass against a sequential implementation, since a sequential run would find
// every gate already open in the order it walks.
func (g *readReverseGate) assertReversed(t *testing.T, calls []string) {
	t.Helper()
	g.mu.Lock()
	stuck := g.stuck
	g.mu.Unlock()
	if stuck != "" {
		t.Fatalf("the read gate could not release %s: the read pass is not overlapping its reads", stuck)
	}

	var readOrder []string
	for _, c := range calls {
		if id, ok := strings.CutPrefix(c, "read aws_cloudwatch_log_group/"); ok {
			readOrder = append(readOrder, id)
		}
	}
	var want []string
	for i := g.n - 1; i >= 0; i-- {
		want = append(want, readParallelID(i))
	}
	if !slices.Equal(readOrder, want) {
		t.Fatalf("the cloud answered in order\n %v\nwant the exact reverse of loop order\n %v\n(the fixture did not set up what the ordering assertions test)", readOrder, want)
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

func addrStrings(as []addrs.AbsResourceInstance) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.String())
	}
	return out
}

// TestSameWantedDistinguishesEveryFieldAReadUses is the guard on
// [readPrefetch.take]'s refusal. Every field of a [wanted] that reaches
// [builder.prepareRead] or [importAndRead] must make two wanteds unequal, or a
// prefetched answer could be handed to an instance it was not read for -
// which no call count and no diagnostic would show.
func TestSameWantedDistinguishesEveryFieldAReadUses(t *testing.T) {
	base := wanted{
		addr:       mustAddr(t, "aws_cloudwatch_log_group.g0"),
		importID:   "id-a",
		identity:   cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("a")}),
		values:     map[string]string{"name": "a"},
		undeclared: false,
		dependsOn:  []addrs.AbsResourceInstance{mustAddr(t, "aws_vpc.main")},
		located:    false,
	}
	if !sameWanted(base, base) {
		t.Fatalf("sameWanted says a wanted differs from itself")
	}

	mutations := map[string]func(w *wanted){
		"addr":        func(w *wanted) { w.addr = mustAddr(t, "aws_cloudwatch_log_group.g1") },
		"importID":    func(w *wanted) { w.importID = "id-b" },
		"identity":    func(w *wanted) { w.identity = cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("b")}) },
		"identityNil": func(w *wanted) { w.identity = cty.NilVal },
		"valuesEdit":  func(w *wanted) { w.values = map[string]string{"name": "b"} },
		"valuesDrop":  func(w *wanted) { w.values = nil },
		"undeclared":  func(w *wanted) { w.undeclared = true },
		"dependsOn":   func(w *wanted) { w.dependsOn = nil },
		"located":     func(w *wanted) { w.located = true },
		"recordFirst": func(w *wanted) { w.recordFirst = true },
	}
	names := make([]string, 0, len(mutations))
	for name := range mutations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		other := base
		mutations[name](&other)
		if sameWanted(base, other) {
			t.Errorf("sameWanted ignores %s: a read planned for one instance could be handed to another", name)
		}
	}
}
