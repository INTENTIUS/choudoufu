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

	// stalled holds one instance's ReadResource inside the provider until the
	// test closes its channel - the straggler issue #683 is about, with the
	// SDK's twenty-six seconds of backoff replaced by a channel a test
	// controls. A stalled read is in flight the whole time it is stalled:
	// [readProvider.leave] runs at the end of ReadResource, after the wait.
	stalled map[string]chan struct{}

	// entered is closed by the stalled read once it is inside the provider,
	// and every OTHER read waits for it before answering. That is what makes
	// the straggler's overlap a property of the fixture rather than of the
	// scheduler: while any other read is inside this provider, the stalled
	// one is inside it too, still holding its in-flight slot.
	//
	// It is not decoration. The first version of these tests asserted peak
	// concurrency and passed on eight processors and failed on CI's one: at
	// GOMAXPROCS=1 the runtime runs the most recently spawned goroutine
	// first, so the launcher's FIRST worker - the stalled one - was scheduled
	// LAST, and the fixture's straggler made its call after every read it was
	// supposed to be overlapping. Same bet, same loss, as issue #597 on the
	// stamping path and [readReverseGate]'s own opening comment.
	//
	// nil until a test registers a stall, so a provider with no straggler
	// behaves exactly as it did.
	entered chan struct{}

	// entryStuck is the first read that gave up waiting for entered, and
	// abandon frees the rest so a broken fixture costs one
	// [readGateStuckAfter] for the whole run rather than one per read. Every
	// test that registers a stall asserts entryStuck is empty; reaching the
	// hang-breaker is never a pass.
	entryStuck string
	abandon    chan struct{}

	// starts carries one import ID per read STARTED, which is the quantity
	// issue #683 is about and the one peak concurrency cannot show: ten reads
	// outstanding reads ten either way, and what the defect changes is
	// whether an eleventh is ever begun. Sends are non-blocking, so a test
	// that never reads it costs nothing.
	starts chan string

	mu       sync.Mutex
	calls    []string
	inFlight int
	peak     int
}

func newReadProvider() *readProvider {
	return &readProvider{
		readErr: map[string]string{},
		absent:  map[string]bool{},
		stalled: map[string]chan struct{}{},
		starts:  make(chan string, readStartsBuffered),
	}
}

// readStartsBuffered is [readProvider.starts]'s depth. Comfortably more than
// any fixture below has instances, so a test that reads it late still sees
// every start rather than a truncated count that would read as the defect.
const readStartsBuffered = 64

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

	if wait, entered := p.stallGate(id); wait != nil {
		close(entered)
		<-wait
	} else if entered != nil {
		p.waitForStallEntry(entered, id)
	}

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
	p.calls = append(p.calls, call)
	p.inFlight++
	if p.inFlight > p.peak {
		p.peak = p.inFlight
	}
	starts := p.starts
	p.mu.Unlock()

	select {
	case starts <- call:
	default:
	}
}

// stallRead makes id's ReadResource block until the returned channel is
// closed, and makes every other read wait until id's has begun. Called before
// the pass starts; neither field is written after that.
func (p *readProvider) stallRead(id string) chan struct{} {
	ch := make(chan struct{})
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stalled[id] = ch
	p.entered = make(chan struct{})
	p.abandon = make(chan struct{})
	return ch
}

// stallGate is what ReadResource does about a straggler: the release channel
// when this read IS the straggler, and otherwise the channel saying the
// straggler has begun - nil when no test registered one.
func (p *readProvider) stallGate(id string) (release, entered chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stalled[id], p.entered
}

// waitForStallEntry parks a read until the straggler is inside the provider.
// [readGateStuckAfter] is the hang-breaker, and reaching it records the read
// that gave up rather than letting the run hang - every caller asserts
// [readProvider.stuckOnEntry] is empty, so it can never become a pass.
func (p *readProvider) waitForStallEntry(entered chan struct{}, id string) {
	timer := time.NewTimer(readGateStuckAfter)
	defer timer.Stop()

	p.mu.Lock()
	abandon := p.abandon
	p.mu.Unlock()

	select {
	case <-entered:
		return
	case <-abandon:
	case <-timer.C:
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entryStuck == "" {
		p.entryStuck = id
		close(p.abandon)
	}
}

// stuckOnEntry is the first read that gave up waiting for the straggler to
// begin, and empty when the fixture did what it exists to do.
func (p *readProvider) stuckOnEntry() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entryStuck
}

// importCount is how many reads have been STARTED - one ImportResourceState
// apiece - whether or not they have landed.
func (p *readProvider) importCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var n int
	for _, c := range p.calls {
		if strings.HasPrefix(c, "import ") {
			n++
		}
	}
	return n
}

// waitForStarts returns once n reads have been started, or everything it saw
// if they never are. It is the caller that decides a short answer is a
// failure, and every caller does; [readGateStuckAfter] is the same
// hang-breaker [readReverseGate] uses, and reaching it is never a pass.
func (p *readProvider) waitForStarts(t *testing.T, n int) []string {
	t.Helper()
	return p.waitForStartsWithin(n, readGateStuckAfter)
}

// waitForStartsWithin is [readProvider.waitForStarts] with a budget of the
// caller's own, for the one caller that is waiting for reads it hopes will
// NOT be started.
func (p *readProvider) waitForStartsWithin(n int, budget time.Duration) []string {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	seen := make([]string, 0, n)
	for len(seen) < n {
		select {
		case call := <-p.starts:
			seen = append(seen, call)
		case <-timer.C:
			return seen
		}
	}
	return seen
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
// 5. One slow read (GitHub issue #683)
// ---------------------------------------------------------------------------

// runReadPassAsync is [runReadPass] with the consuming loop on its own
// goroutine, because the two tests below have to observe the pass while it is
// still running - a stalled read is not visible from the far side of it.
//
// The builder's fields are read only after the returned channel is closed,
// which is the happens-before edge that makes that safe.
func runReadPassAsync(t *testing.T, p *readProvider, opts Options) (*builder, <-chan struct{}) {
	t.Helper()
	cfg := loadConfig(t, "testdata/read-parallel")
	b := newBuilder(context.Background(), cfg, SingleProvider(awsProvider, p), opts)
	res := readParallelResolutions(t)

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		b.run(context.Background(), res)
	}()
	return b, finished
}

// TestOneStalledReadDoesNotStopTheOthersFromLaunching is issue #683 itself.
//
// One aws_route53_record throttled on a real-AWS plan of 745 resources, and
// the SDK's backoff held that one read for 26.20 seconds. For 26 of those
// seconds the whole plan had ZERO requests in flight except the four retries
// themselves: the nine reads launched beside it had landed and were holding
// their slots, because the one slot did double duty as the memory bound and
// the consuming loop - which walks build order - was stopped on the stalled
// instance. The launcher could not start a tenth.
//
// The fixture is that estate in miniature. g0 is the straggler, held inside
// ReadResource until this test releases it, and every other read waits until
// g0's own call has begun - see [readProvider.entered], which is what makes
// the overlap a property of the fixture rather than a bet on the scheduler.
// The assertion is on reads STARTED, not on peak concurrency: peak reads 2
// here and 10 there whether or not the defect is present, which is what let
// this hide through three rounds of investigation.
//
// Nothing in the pass path here is timed. Every wait is satisfied by a
// channel a call closes, and [readGateStuckAfter] exists so that a pass that
// cannot start those calls fails in half a minute naming the count it reached
// instead of hanging until the package's test timeout.
func TestOneStalledReadDoesNotStopTheOthersFromLaunching(t *testing.T) {
	p := newReadProvider()
	release := p.stallRead(readParallelID(0))

	b, finished := runReadPassAsync(t, p, Options{
		ReadParallelism: 2,
		// Room for every answer but the straggler's own, so that the buffer
		// is not what stops the launcher: what is under test is whether the
		// stalled read stops it.
		ReadBuffer: readParallelN - 1,
	})

	started := p.waitForStarts(t, readParallelN)

	// Release before any assertion, so that a failure reports rather than
	// leaving the pass wedged behind a read nobody ever answers.
	close(release)
	<-finished

	if stuck := p.stuckOnEntry(); stuck != "" {
		t.Fatalf("the read of %s gave up waiting for the stalled read to begin, so this run never had a straggler to overlap and asserts nothing", stuck)
	}
	if len(started) != readParallelN {
		t.Fatalf("while one read was stalled the pass started %d of %d reads: %v\n"+
			"the slot a fetched-but-unconsumed answer holds is still the slot the launcher needs, so one slow read stops the window sliding",
			len(started), readParallelN, started)
	}

	// Two calls were inside the provider at once, by construction rather than
	// by luck: every read but the straggler's waits for the straggler to be
	// in, and the straggler does not leave until this test releases it. So
	// this is the in-flight bound reading exactly its own setting - without
	// it, a fix that deleted the bound outright would pass the assertion
	// above.
	if got, want := p.peakConcurrency(), 2; got != want {
		t.Errorf("peak concurrency was %d, want %d: with one read held inside the provider and the others waiting on it, that is the ReadParallelism this pass was given", got, want)
	}

	// And the pass is the pass it always was: same calls, same order, nothing
	// prefetched that nobody consumed.
	if got, want := len(p.callLog()), 2*readParallelN; got != want {
		t.Errorf("made %d calls, want %d: %v", got, want, p.callLog())
	}
	var wantMaterialized []string
	for i := 0; i < readParallelN; i++ {
		wantMaterialized = append(wantMaterialized, fmt.Sprintf("aws_cloudwatch_log_group.g%d", i))
	}
	if got := addrStrings(b.materialized); !slices.Equal(got, wantMaterialized) {
		t.Errorf("materialized in order\n %v\nwant\n %v", got, wantMaterialized)
	}
	if b.diags.HasErrors() {
		t.Errorf("unexpected diagnostics:\n%s", renderDiags(b.diags))
	}
	if len(b.readWasted) != 0 || b.readMismatched != 0 {
		t.Errorf("wasted %v and refused %d prefetched answers, want none of either", b.readWasted, b.readMismatched)
	}
}

// readBufferSettle is how long the test below waits for a read the bound
// forbids, before concluding the bound held.
//
// It is the one budget in this file that is spent on a success, and it is
// here because no channel can carry the news that a launcher is BLOCKED: an
// absent start looks exactly like a start that has not happened yet. What
// keeps it from being a bet is the direction it can fail in. A loaded runner
// can only make this guard miss a violation, never invent one, so it cannot
// turn a correct pass red - and the margin is not close either way, because
// the reads this fixture makes are in-process map lookups and the unbounded
// launcher in the pull request's red run started the remaining instances
// within microseconds of the completions that freed it.
const readBufferSettle = 500 * time.Millisecond

// TestTheReadPassBoundsFetchedButUnconsumedAnswers is the other half of issue
// #683, and it is what stops the first test being satisfied by deleting the
// backpressure.
//
// The slot the launcher takes was never only a concurrency bound: it was also
// the promise that at most ReadParallelism answers are ever fetched and not
// yet consumed, so that a projection over a thousand instances does not hold
// a thousand read objects at once. Splitting the bound in two has to keep
// that promise, and a read-ahead released on completion alone would trade the
// slowness for exactly the memory regression the original design avoided.
//
// So: the consuming loop is stopped on a stalled g0, the launcher is free,
// and the reads it manages to start are held between two numbers that are
// both the bound.
//
//   - It cannot stop before buffer answers are unconsumed, so at least
//     1 + buffer reads are started. Below that the launcher is not running
//     ahead of the loop at all and this test is not holding anything.
//   - It cannot get past buffer + parallelism. The launcher clears the buffer
//     gate and then waits for an in-flight slot, so it is admitted while
//     buffered is at most buffer-1 and may then be joined by the reads
//     already in flight: at most buffer + parallelism - 1 answers are ever
//     fetched and unconsumed, and one further read - the stalled one - is in
//     flight holding no answer at all.
//
// Which of the two it lands on is the scheduler's business, and asserting one
// exact value there is how a guard passes on eight processors and fails on
// CI's one.
func TestTheReadPassBoundsFetchedButUnconsumedAnswers(t *testing.T) {
	const parallelism = 2
	const buffer = 3
	const minStarted = 1 + buffer
	const maxStarted = buffer + parallelism

	p := newReadProvider()
	release := p.stallRead(readParallelID(0))

	b, finished := runReadPassAsync(t, p, Options{ReadParallelism: parallelism, ReadBuffer: buffer})

	started := p.waitForStarts(t, minStarted)
	// However many the bound allows, one more than that is the violation, and
	// waiting for it is what gives an unbounded launcher its chance to commit
	// one. It returns the moment the read arrives; on a pass it costs the
	// budget and nothing else.
	started = append(started, p.waitForStartsWithin(maxStarted+1-len(started), readBufferSettle)...)
	overrun := p.importCount()

	close(release)
	<-finished

	if stuck := p.stuckOnEntry(); stuck != "" {
		t.Fatalf("the read of %s gave up waiting for the stalled read to begin, so the consuming loop was never held and this run asserts nothing", stuck)
	}
	if len(started) < minStarted {
		t.Fatalf("with one read stalled the pass started %d reads, want at least %d: %v\n"+
			"the launcher is not running ahead of the consuming loop at all, so this test is not holding the bound it exists to hold",
			len(started), minStarted, started)
	}
	if overrun > maxStarted {
		t.Errorf("with the consuming loop stopped on one stalled read, %d reads had been started, want at most %d (one stalled, at most %d answers fetched and unconsumed).\n"+
			"Fetched-but-unconsumed answers are unbounded, so a lagging consumer now holds every answer of an estate at once - the regression the single slot existed to prevent",
			overrun, maxStarted, buffer+parallelism-1)
	}

	// The bound is a pause, not a loss: everything still reads, in order,
	// once the loop starts taking.
	if got, want := len(p.callLog()), 2*readParallelN; got != want {
		t.Errorf("made %d calls, want %d: %v", got, want, p.callLog())
	}
	if got, want := len(b.materialized), readParallelN; got != want {
		t.Errorf("materialized %d instances, want %d: %v", got, want, addrStrings(b.materialized))
	}
	if b.diags.HasErrors() {
		t.Errorf("unexpected diagnostics:\n%s", renderDiags(b.diags))
	}
	if len(b.readWasted) != 0 || b.readMismatched != 0 {
		t.Errorf("wasted %v and refused %d prefetched answers, want none of either", b.readWasted, b.readMismatched)
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
