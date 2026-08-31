// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// GitHub issue #654's guard set.
//
// Issue #585 gave the CONCRETE phase a prefetch. On the estate this fork
// exists to run - a migrated one - the concrete phase is nearly empty: every
// instance an apply or a migration has written a record for is intercepted by
// [builder.applyRecordFirst] first, and since issue #636 made the store one
// GetAll that interception is free, so it catches almost everything (78 of 79
// instances at scale 1 on the terralith the live-cert harness builds). The
// intercept read one instance at a time, so the whole read pass did, and a
// real-AWS plan of 745 resources took 124 seconds where stock took 22-39 for
// the same 1399 calls.
//
// The tests below are the two properties that failure had: the reads must
// actually overlap, and overlapping them must not change which calls are made.
// The first is what a [readProvider.peakConcurrency] of 1 would have caught
// the day issue #585 landed. Nothing asserted it, because every guard #585
// wrote drives the concrete phase, and the concrete phase was never the one a
// migrated estate reads through.

// recordFirstStore seeds a record store with one identity record per fixture
// instance, pointing at the same import ID that instance's own concrete
// resolution carries. That is the shape a migrated estate is in: the record
// and the configuration agree, so [builder.materializeFromRecord] handles
// every instance and the concrete phase gets none of them.
func recordFirstStore(t *testing.T) *RecordStore {
	t.Helper()
	located := newTestLocatedStore(localHintStore(t), "read-parallel-estate")
	for i := 0; i < readParallelN; i++ {
		addr := mustAddr(t, fmt.Sprintf("aws_cloudwatch_log_group.g%d", i))
		if _, err := located.Put(context.Background(), addr, LocatedRecord{ImportID: readParallelID(i)}, ""); err != nil {
			t.Fatalf("seeding the record for %s: %s", addr, err)
		}
	}
	return located.rs
}

// runRecordFirstPass drives the read pass over the same fixture
// [runReadPass] uses, with every instance's identity already in the record
// store, so the reading happens in [builder.applyRecordFirst] rather than in
// the concrete phase.
func runRecordFirstPass(t *testing.T, p *readProvider, par int) *builder {
	t.Helper()
	cfg := loadConfig(t, "testdata/read-parallel")
	b := newBuilder(context.Background(), cfg, SingleProvider(awsProvider, p), Options{
		RecordStore:     recordFirstStore(t),
		ReadParallelism: par,
	})
	b.run(context.Background(), readParallelResolutions(t))
	return b
}

// TestRecordFirstPassOverlapsItsReads is issue #654 itself: the record-first
// intercept must have more than one read in flight.
//
// It asserts the peak through [readProvider.peakConcurrency] rather than a
// duration, because a timing assertion here would be the check that cannot
// fail this repository has shipped four of. Against the code this issue was
// filed on, the peak is exactly 1.
func TestRecordFirstPassOverlapsItsReads(t *testing.T) {
	p := newReadProvider()
	b := runRecordFirstPass(t, p, readParallelN)

	// The premise first: if these instances did not go through the
	// record-first intercept, a peak above one would be measuring the
	// concrete phase and would say nothing about issue #654.
	if got, want := len(b.materialized), readParallelN; got != want {
		t.Fatalf("materialized %d instances, want %d: %v", got, want, addrStrings(b.materialized))
	}
	for _, call := range p.callLog() {
		if _, ok := readParallelIndex(importIDOfCall(call)); !ok {
			t.Fatalf("the provider was called for something other than the fixture: %v", p.callLog())
		}
	}
	if b.diags.HasErrors() {
		t.Fatalf("unexpected diagnostics:\n%s", renderDiags(b.diags))
	}

	if got := p.peakConcurrency(); got < 2 {
		t.Errorf("peak concurrency in the record-first pass was %d, want at least 2: the intercept is reading one instance at a time, which is GitHub issue #654", got)
	}
}

// TestRecordFirstPassIsCallIdenticalToTheSequentialLoop is the property the
// prefetch accepts on, restated for this phase: overlapping the intercept's
// reads must not change which calls are made, which instances materialize, or
// what the run says about them.
func TestRecordFirstPassIsCallIdenticalToTheSequentialLoop(t *testing.T) {
	seqP := newReadProvider()
	seq := runRecordFirstPass(t, seqP, 1)

	conP := newReadProvider()
	con := runRecordFirstPass(t, conP, readParallelN)

	seqCalls := slices.Sorted(slices.Values(seqP.callLog()))
	conCalls := slices.Sorted(slices.Values(conP.callLog()))
	if !slices.Equal(seqCalls, conCalls) {
		t.Errorf("the concurrent record-first pass made different calls\nsequential: %v\nconcurrent: %v", seqCalls, conCalls)
	}
	if got, want := len(seqCalls), 2*readParallelN; got != want {
		t.Errorf("the sequential record-first pass made %d calls, want %d (one import and one read per instance)", got, want)
	}
	if got := seqP.peakConcurrency(); got != 1 {
		t.Errorf("peak concurrency at parallelism 1 was %d, want 1", got)
	}

	if !slices.Equal(addrStrings(seq.materialized), addrStrings(con.materialized)) {
		t.Errorf("materialized differs\nsequential: %v\nconcurrent: %v", addrStrings(seq.materialized), addrStrings(con.materialized))
	}
	if seq.diags.HasErrors() || con.diags.HasErrors() {
		t.Errorf("unexpected diagnostics\nsequential:\n%s\nconcurrent:\n%s", renderDiags(seq.diags), renderDiags(con.diags))
	}
	if got := con.readWasted; len(got) != 0 {
		t.Errorf("the record-first pass prefetched reads nobody consumed: %v", got)
	}
	if got := con.readMismatched; got != 0 {
		t.Errorf("the record-first pass refused %d prefetched answers; the plan and the loop disagreed about what to read", got)
	}
}

// TestRecordFirstPrefetchPlansNothingWithoutARecord is the other side of
// [builder.startRecordFirstPrefetch]'s bound: a resolution the intercept will
// not handle - here, one with no record at all - must not be prefetched, or
// the run would spend a provider round trip the sequential pass never spent
// and land it on the concrete phase's own read a moment later.
func TestRecordFirstPrefetchPlansNothingWithoutARecord(t *testing.T) {
	p := newReadProvider()
	cfg := loadConfig(t, "testdata/read-parallel")

	// One record only, for g0; the other five resolve concretely.
	located := newTestLocatedStore(localHintStore(t), "read-parallel-estate")
	addr0 := mustAddr(t, "aws_cloudwatch_log_group.g0")
	if _, err := located.Put(context.Background(), addr0, LocatedRecord{ImportID: readParallelID(0)}, ""); err != nil {
		t.Fatalf("seeding the record for %s: %s", addr0, err)
	}

	b := newBuilder(context.Background(), cfg, SingleProvider(awsProvider, p), Options{
		RecordStore:     located.rs,
		ReadParallelism: readParallelN,
	})
	b.run(context.Background(), readParallelResolutions(t))

	if got, want := len(p.callLog()), 2*readParallelN; got != want {
		t.Errorf("made %d calls, want %d (one import and one read per instance, whichever phase read it): %v", got, want, p.callLog())
	}
	if len(b.readWasted) != 0 {
		t.Errorf("prefetched reads nobody consumed: %v", b.readWasted)
	}
	if b.readMismatched != 0 {
		t.Errorf("refused %d prefetched answers", b.readMismatched)
	}
	if got, want := len(b.materialized), readParallelN; got != want {
		t.Errorf("materialized %d instances, want %d: %v", got, want, addrStrings(b.materialized))
	}
}

// importIDOfCall pulls the import ID out of a [readProvider] call-log entry,
// which is "<verb> <type>/<id>".
func importIDOfCall(call string) string {
	for i := len(call) - 1; i >= 0; i-- {
		if call[i] == '/' {
			return call[i+1:]
		}
	}
	return call
}
