// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/pkg/staterecord"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// fakeRecordStore is an in-memory record store that counts what reaches
// it. It holds nothing, so every Get answers "no record".
type fakeRecordStore struct {
	mu    sync.Mutex
	gets  []string
	lists []string
}

func (s *fakeRecordStore) Get(_ context.Context, key string) ([]byte, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, key)
	return nil, "", false, nil
}

func (s *fakeRecordStore) PutIfVersion(context.Context, string, []byte, string) (string, error) {
	return "1", nil
}

func (s *fakeRecordStore) PutIfAbsent(context.Context, string, []byte) (string, error) {
	return "1", nil
}

func (s *fakeRecordStore) Delete(context.Context, string, string) error { return nil }

func (s *fakeRecordStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists = append(s.lists, prefix)
	return nil, nil
}

func (s *fakeRecordStore) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lists)
}

// fakeSweeper stands in for the AWS family's estate-sweep leg and counts
// how often it is built and run.
type fakeSweeper struct {
	mu     sync.Mutex
	built  int
	swept  int
	legKey substrate.Sweep
}

func (f *fakeSweeper) Leg() substrate.Sweep { return f.legKey }

func (f *fakeSweeper) Sweep(context.Context, *discovery.SweepInput) tfdiags.Diagnostics {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.swept++
	return nil
}

func (f *fakeSweeper) counts() (built, swept int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.built, f.swept
}

// installResourcePathFakes points both pipelines' record-store openers at
// store and the AWS family's sweep leg at sweeper, for the test's lifetime.
func installResourcePathFakes(t *testing.T, store *fakeRecordStore, sweeper *fakeSweeper) {
	t.Helper()
	open := func(context.Context, *configs.LiveRecordStore, *configs.LiveRetry, string, string, ...projection.RecordStoreOption) (staterecord.Store, error) {
		return store, nil
	}
	prevPlan, prevResource := livePlanRecordStoreOpener, liveResourceRecordStoreOpener
	livePlanRecordStoreOpener, liveResourceRecordStoreOpener = open, open

	prevLeg := sweepLegBuilders[substrate.SweepTaggingIndex]
	sweeper.legKey = substrate.SweepTaggingIndex
	sweepLegBuilders[substrate.SweepTaggingIndex] = func(context.Context, *projectionProviders, substrate.Substrate, addrs.AbsProviderConfig) (discovery.Sweeper, bool, tfdiags.Diagnostics) {
		sweeper.mu.Lock()
		sweeper.built++
		sweeper.mu.Unlock()
		return sweeper, true, nil
	}
	t.Cleanup(func() {
		livePlanRecordStoreOpener, liveResourceRecordStoreOpener = prevPlan, prevResource
		sweepLegBuilders[substrate.SweepTaggingIndex] = prevLeg
	})
}

// driftLines splits the drift stream into its three line kinds.
func driftLines(t *testing.T, stdout string) (start map[string]any, records []map[string]any, settle map[string]any) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q: %s\nstdout:\n%s", line, err, stdout)
		}
		switch m["type"] {
		case "drift-start":
			start = m
		case "drift-record":
			records = append(records, m)
		case "drift-settle":
			settle = m
		default:
			t.Fatalf("stdout line has type %v, want drift-start, drift-record or drift-settle: %q", m["type"], line)
		}
	}
	if start == nil || settle == nil {
		t.Fatalf("the stream has no drift-start or no drift-settle line:\n%s", stdout)
	}
	return start, records, settle
}

// TestLivePlanResource_readsNoListAndSweepsNothing is GitHub issue #2245's
// guard: live-plan -resource reads the record store by key and never lists
// it, and never runs the estate-wide sweep. The fake store and the fake
// sweep leg count what reaches them.
//
// The control runs first and is the red half: the whole-estate live-plan
// -json over the same fixture, store and leg lists the store and runs the
// sweep, so a fake that could not see either would fail there rather than
// pass below.
func TestLivePlanResource_readsNoListAndSweepsNothing(t *testing.T) {
	t.Run("control: the whole-estate plan lists and sweeps", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block-record-store"), td)
		t.Chdir(td)

		store, sweeper := &fakeRecordStore{}, &fakeSweeper{}
		installResourcePathFakes(t, store, sweeper)

		view, done := testView(t)
		c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
		code := c.Run([]string{"-no-color", "-json"})
		out := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
		}
		if store.listCount() == 0 {
			t.Errorf("the whole-estate plan never listed the fake store, so the fake cannot see a List and the -resource case proves nothing")
		}
		if built, swept := sweeper.counts(); built == 0 || swept == 0 {
			t.Errorf("the whole-estate plan built the fake sweep leg %d time(s) and ran it %d time(s); want both non-zero, or the -resource case proves nothing", built, swept)
		}
	})

	for _, tc := range []struct {
		resource string
		// scanTypes is what the cone's own discovery lists: nothing for a
		// client-named bucket, the VPC's type for a server-assigned VPC.
		scanTypes []string
	}{
		{resource: "aws_s3_bucket.data", scanTypes: []string{}},
		{resource: "aws_vpc.main", scanTypes: []string{"aws_vpc"}},
	} {
		t.Run("-resource="+tc.resource, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block-record-store"), td)
			t.Chdir(td)

			store, sweeper := &fakeRecordStore{}, &fakeSweeper{}
			installResourcePathFakes(t, store, sweeper)

			view, done := testView(t)
			c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
			code := c.Run([]string{"-no-color", "-resource=" + tc.resource})
			out := done(t)
			if code != 0 {
				t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
			}

			if n := store.listCount(); n != 0 {
				t.Errorf("the -resource run listed the record store %d time(s) (prefixes %q); it reads by key only", n, store.lists)
			}
			if built, swept := sweeper.counts(); built != 0 || swept != 0 {
				t.Errorf("the -resource run built the sweep leg %d time(s) and ran it %d time(s); want neither", built, swept)
			}

			start, records, settle := driftLines(t, out.Stdout())
			if start["resource"] != tc.resource || start["engine"] != "choudoufu" || start["drift"] != "v1" {
				t.Errorf("drift-start = %v", start)
			}
			if len(records) != 1 || records[0]["address"] != tc.resource {
				t.Fatalf("records = %v, want exactly one, for %s: the cone holds nothing else", records, tc.resource)
			}
			if got := records[0]["status"]; got != "in-sync" {
				t.Errorf("%s status %v, want in-sync: the cloud holds the estate's own object at the declared value", tc.resource, got)
			}
			if _, ok := records[0]["node"]; !ok || records[0]["node"] != nil {
				t.Errorf("record node = %v, want an explicit null", records[0]["node"])
			}

			reads, _ := settle["reads"].(map[string]any)
			if reads == nil {
				t.Fatalf("drift-settle carries no reads: %v", settle)
			}
			if reads["recordListCalls"] != float64(0) || reads["sweepCalls"] != float64(0) {
				t.Errorf("drift-settle reads = %v, want recordListCalls and sweepCalls 0", reads)
			}
			if got, ok := reads["recordsRead"].(float64); !ok || int(got) != len(store.gets) {
				t.Errorf("drift-settle recordsRead = %v, the fake store saw %d Get(s)", reads["recordsRead"], len(store.gets))
			}
			var scanned []string
			for _, s := range reads["scanTypes"].([]any) {
				scanned = append(scanned, s.(string))
			}
			sort.Strings(scanned)
			if strings.Join(scanned, ",") != strings.Join(tc.scanTypes, ",") {
				t.Errorf("scanTypes = %q, want %q: the cone's discovery lists the cone's own types and nothing else", scanned, tc.scanTypes)
			}
		})
	}
}

// TestLivePlanResource_refusesWhatItCannotHonour: -resource is the target,
// and a drift read saves nothing.
func TestLivePlanResource_refusesWhatItCannotHonour(t *testing.T) {
	for _, flags := range [][]string{
		{"-target=aws_vpc.main"},
		{"-out=plan.tfplan"},
		{"-destroy"},
		{"-cone=HEAD"},
	} {
		args, closer, diags := arguments.ParseLivePlan(append([]string{"-resource=aws_s3_bucket.data"}, flags...))
		closer()
		if diags.HasErrors() {
			t.Fatalf("%v: parse failed: %s", flags, diags.Err())
		}
		if got := liveResourceRejections(args); !got.HasErrors() {
			t.Errorf("%v with -resource was accepted", flags)
		}
	}
}
