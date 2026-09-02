// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"bytes"
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #344's report half.
//
// SeedRecordForInstance was changed from returning a bool to returning a
// [projection.SeedResult] for one stated reason, which its own type doc still
// carries: "the migration report counts these separately ... reporting the
// upgrade as 'newly recorded' would tell an operator something that is not
// true of an estate that has been migrated before."
//
// The enumeration landed and the count did not. recordOne filed a
// SeedMarksAdded under OutcomeRecorded and changed only the per-row Detail
// text, while internal/command/views/live_import.go buckets by Outcome and
// prints len(byOutcome["RECORDED"]) as "N newly recorded" - so re-migrating
// an estate with fifty long-standing, pre-sensitivity records reported fifty
// resources newly recorded. The row underneath said otherwise; the summary
// line, which is what an operator reads, did not.

// archivePlanState is a one-resource tfstate holding local_file.archive_plan,
// with or without the sensitivity the state file records alongside the value.
// The pair is the whole fixture: the same object, recorded before and after
// this fork persisted sensitive paths.
func archivePlanState(sensitive bool) *states.State {
	src := &states.ResourceInstanceObjectSrc{
		AttrsJSON: []byte(`{"id":"d8186d18","filename":"builds/plan.json","content":"a-secret-build-plan"}`),
		Status:    states.ObjectReady,
	}
	if sensitive {
		src.AttrSensitivePaths = []cty.PathValueMarks{
			{Path: cty.GetAttrPath("content"), Marks: cty.NewValueMarks(marks.Sensitive)},
		}
	}
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "local_file", Name: "archive_plan"}.Instance(addrs.NoKey),
		src,
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("local")},
		addrs.NoKey,
	)
	return state
}

// approveArchivePlan runs one migration of local_file.archive_plan against
// store and returns its single outcome.
func approveArchivePlan(t *testing.T, store staterecord.Store, sensitive bool) StampOutcome {
	t.Helper()
	rat, diags := Ratify(context.Background(), Request{
		Estate:      petEstate,
		State:       archivePlanState(sensitive),
		Providers:   newPetProvider(),
		RecordStore: projection.NewRecordEnvelopeStore(store, recordTestPrefix()),
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	return onlyOutcome(t, rep)
}

// TestSensitivityUpgradeIsNotCountedAsNewlyRecorded is the reproduction. It
// migrates the same estate twice: once as a pre-sensitivity choudoufu would
// have (the state carries no AttrSensitivePaths, so the record is written
// without any), then again with the sensitivity the state file records - the
// case [projection.SeedMarksAdded] exists for.
//
// The second run really does write, and it is not a new record. Anything that
// counts it as one is telling an operator that a store which has held these
// values for weeks was just seeded.
//
// Verified to reproduce: with recordOne's OutcomeSensitivityRecorded reverted
// to OutcomeRecorded, this test fails at the first check below - and every
// other test in this package stays green, which is why the miscount survived
// the refactor that was made to end it.
func TestSensitivityUpgradeIsNotCountedAsNewlyRecorded(t *testing.T) {
	store := petStore(t)

	if got := approveArchivePlan(t, store, false); got.Outcome != OutcomeRecorded {
		t.Fatalf("the first migration's outcome = %s, want %s: %s", got.Outcome, OutcomeRecorded, got.Detail)
	}

	second := approveArchivePlan(t, store, true)

	if second.Outcome == OutcomeRecorded {
		t.Fatalf("a sensitivity-only rewrite of an existing record was filed as %s, which the report counts "+
			"as \"newly recorded\". The record was already there; only which of its attributes are sensitive "+
			"moved. Detail: %s", OutcomeRecorded, second.Detail)
	}
	if second.Outcome != OutcomeSensitivityRecorded {
		t.Fatalf("the second migration's outcome = %s, want %s: %s", second.Outcome, OutcomeSensitivityRecorded, second.Detail)
	}

	// The write really happened, so the outcome is not ALREADY_RECORDED
	// either: the record now carries the path, which is the whole point of
	// #344's narrow re-migration rule.
	raw, _, exists, err := store.Get(context.Background(), projection.RecordKey(recordTestPrefix(), mustAddr(t, "local_file.archive_plan")))
	if err != nil || !exists {
		t.Fatalf("reading the record back: exists = %v, err = %v", exists, err)
	}
	if want := `[[{"type":"get_attr","value":"content"}]]`; !bytes.Contains(raw, []byte(want)) {
		t.Errorf("the record does not carry %s after the upgrade, so nothing was actually rewritten: %s", want, raw)
	}
}

// TestARepeatedSensitivityUpgradeIsANoOp is the idempotence half, and it is
// the mutation check on the test above: if the second run above were counted
// wrongly because the seed simply always rewrites, a THIRD run would report
// the same thing again. It reports ALREADY_RECORDED instead, so the upgrade
// is a one-time event and the outcome above is about the upgrade rather than
// about the write path in general.
func TestARepeatedSensitivityUpgradeIsANoOp(t *testing.T) {
	store := petStore(t)

	approveArchivePlan(t, store, false)
	approveArchivePlan(t, store, true)

	third := approveArchivePlan(t, store, true)
	if third.Outcome != OutcomeAlreadyRecorded {
		t.Errorf("the third migration's outcome = %s, want %s: %s", third.Outcome, OutcomeAlreadyRecorded, third.Detail)
	}
}
