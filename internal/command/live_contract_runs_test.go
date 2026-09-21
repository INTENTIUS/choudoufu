// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1448, section D. `live-mv` and `live-import` write records
// and asserted no contract, while the comment on BeforeApply said an apply
// was the only run that writes any. These hold both commands to the apply's
// behaviour, over the same stand-in stores the apply's own tests use, and to
// the other half of the rule: an invocation that writes nothing asks for
// nothing.

func failingBucket() []staterecord.Finding {
	return withFinding(staterecord.Finding{
		Setting: staterecord.BucketVersioning, Found: "versioning is Suspended",
	})
}

func failingCluster() []staterecord.Finding {
	var out []staterecord.Finding
	for _, setting := range staterecord.ClusterSettings {
		f := staterecord.Finding{Setting: setting, Outcome: staterecord.Passed, Found: "fine"}
		if setting == staterecord.ClusterEstateBoundary {
			f = staterecord.Finding{Setting: setting, Found: `no ValidatingAdmissionPolicy named "choudoufu-estate-boundary" is installed`}
		}
		out = append(out, f)
	}
	return out
}

// fencedOpen is an opener refused by the estate boundary policy the way
// provisionStoreSentinel wraps it: the identity may write the Secret as far
// as the authorizer is concerned, and admission refuses it anyway.
func fencedOpen() recordStoreOpener {
	denied := &staterecord.AdmissionDeniedError{
		Namespace: testNamespace,
		Key:       "tofu-records/prod/.store-sentinel",
		Estate:    "prod",
		Verb:      "create",
		Policy:    staterecord.EstateBoundaryPolicyName,
		Err:       errors.New(`secrets "tofu-record-abc" is forbidden: ValidatingAdmissionPolicy 'choudoufu-estate-boundary' denied request`),
	}
	return openerReturning(nil, fmt.Errorf("record_store: provisioning the sentinel at %q: %w", denied.Key, denied))
}

func passingCluster() []staterecord.Finding {
	var out []staterecord.Finding
	for _, setting := range staterecord.ClusterSettings {
		out = append(out, staterecord.Finding{Setting: setting, Outcome: staterecord.Passed, Found: "fine"})
	}
	return out
}

// TestLiveMvAssertsTheStoreContractBeforeItWrites is the rename half. A
// rename moves a record (projection.RecordStore.MoveRecord), so it is a run
// a store with versioning off or no estate boundary can hurt.
func TestLiveMvAssertsTheStoreContractBeforeItWrites(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	bucketCfg := &configs.LiveRecordStore{Type: "s3", Bucket: testBucket}
	clusterCfg := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true}

	t.Run("a bucket that fails refuses the rename", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: failingBucket()}
		_, diags := openRecordStoreForMove(ctx, openerReturning(store, nil), bucketCfg, nil, estate, false)
		if !diags.HasErrors() {
			t.Fatal("a rename wrote a record into a bucket with versioning off")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"versioning", testBucket, "versioning is Suspended", "No marker has been rewritten"} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "Nothing has been applied") {
			t.Errorf("a refused rename says an apply was stopped:\n%s", got)
		}
	})

	t.Run("a cluster that fails refuses the rename", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: failingCluster()}
		_, diags := openRecordStoreForMove(ctx, openerReturning(store, nil), clusterCfg, nil, estate, false)
		if !diags.HasErrors() {
			t.Fatal("a rename wrote a record into a cluster with no estate boundary policy")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"estate_boundary", testNamespace, "No marker has been rewritten"} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("-dry-run writes nothing, so it asks for nothing", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: failingBucket()}
		got, diags := openRecordStoreForMove(ctx, openerReturning(store, nil), bucketCfg, nil, estate, true)
		if diags.HasErrors() {
			t.Fatalf("a preview of a rename was refused for a store it never writes to:\n%s", diags.Err())
		}
		if got == nil {
			t.Error("the preview lost the record store it reads the old address from")
		}
		if store.checks != 0 {
			t.Errorf("the bucket was checked %d time(s) by a run that writes nothing", store.checks)
		}
	})

	t.Run("a store that passes is checked once and says nothing", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: passingFindings()}
		got, diags := openRecordStoreForMove(ctx, openerReturning(store, nil), bucketCfg, nil, estate, false)
		if len(diags) != 0 {
			t.Errorf("a correct bucket produced %d diagnostic(s):\n%s", len(diags), diags.Err())
		}
		if got == nil {
			t.Error("the rename lost its record store")
		}
		if store.checks != 1 {
			t.Errorf("the bucket was checked %d time(s), want 1", store.checks)
		}
	})

	t.Run("a waiver behaves as it does on an apply, in this run's words", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "s3", Bucket: testBucket, AllowInsecure: []string{"versioning"}}
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: failingBucket()}
		_, diags := openRecordStoreForMove(ctx, openerReturning(store, nil), waived, nil, estate, false)
		if diags.HasErrors() {
			t.Fatalf("a waived versioning failure refused the rename:\n%s", diags.Err())
		}
		got := summaries(diags, tfdiags.Warning)
		// One from the configuration alone on every run (#1340), one from
		// what this run read.
		want := []string{
			"The record store bucket's versioning assertion is waived",
			"The waived versioning assertion would have refused this rename",
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("no warning reads %q; got %q", w, got)
			}
		}
		if !strings.Contains(details(diags, tfdiags.Warning), "The rename proceeds because") {
			t.Errorf("the warning does not say what proceeds:\n%s", details(diags, tfdiags.Warning))
		}
	})

	t.Run("a local store has no contract to assert", func(t *testing.T) {
		local := &configs.LiveRecordStore{Type: "local"}
		_, diags := openRecordStoreForMove(ctx, openerReturning(localStoreForTest(t), nil), local, nil, estate, false)
		if len(diags) != 0 {
			t.Errorf("a local record store produced %d diagnostic(s):\n%s", len(diags), diags.Err())
		}
	})
}

// TestLiveImportAssertsTheStoreContractBeforeItStamps is the migration half.
// -approve seeds this estate's records (liveimport's recordOne, locateOne
// and seedIdentityFor); the run without it prints a report and writes
// nothing.
func TestLiveImportAssertsTheStoreContractBeforeItStamps(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	bucketCfg := &configs.LiveRecordStore{Type: "s3", Bucket: testBucket}
	clusterCfg := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true}

	t.Run("-approve into a bucket that fails is refused", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: failingBucket()}
		_, diags := openRecordStoreForImport(ctx, openerReturning(store, nil), bucketCfg, nil, estate, true)
		if !diags.HasErrors() {
			t.Fatal("a migration seeded records into a bucket with versioning off")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"versioning", testBucket, "No marker has been stamped"} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("-approve into a cluster that fails is refused", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: failingCluster()}
		_, diags := openRecordStoreForImport(ctx, openerReturning(store, nil), clusterCfg, nil, estate, true)
		if !diags.HasErrors() {
			t.Fatal("a migration seeded records into a cluster with no estate boundary policy")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"estate_boundary", testNamespace, "No marker has been stamped"} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("the report-only run writes nothing, so it asks for nothing", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: failingCluster()}
		got, diags := openRecordStoreForImport(ctx, openerReturning(store, nil), clusterCfg, nil, estate, false)
		if diags.HasErrors() {
			t.Fatalf("a ratification report was refused for a store it never writes to:\n%s", diags.Err())
		}
		if got == nil {
			t.Error("the report lost the record store it reads from")
		}
		if store.checks != 0 {
			t.Errorf("the cluster was checked %d time(s) by a run that writes nothing", store.checks)
		}
	})

	t.Run("-approve into a store that passes is checked once", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: passingCluster()}
		if _, diags := openRecordStoreForImport(ctx, openerReturning(store, nil), clusterCfg, nil, estate, true); len(diags) != 0 {
			t.Errorf("a correct cluster produced %d diagnostic(s):\n%s", len(diags), diags.Err())
		}
		if store.checks != 1 {
			t.Errorf("the cluster was checked %d time(s), want 1", store.checks)
		}
	})

	t.Run("a waiver is announced on both runs and hides only what it names", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true,
			AllowInsecure: []string{"estate_boundary"}}
		for _, approve := range []bool{false, true} {
			store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: failingCluster()}
			_, diags := openRecordStoreForImport(ctx, openerReturning(store, nil), waived, nil, estate, approve)
			if diags.HasErrors() {
				t.Fatalf("approve=%v: a waived estate_boundary failure refused the migration:\n%s", approve, diags.Err())
			}
			got := summaries(diags, tfdiags.Warning)
			if !slices.Contains(got, "The record store cluster's estate_boundary assertion is waived") {
				t.Errorf("approve=%v: the every-run waiver warning is missing; got %q", approve, got)
			}
			read := slices.Contains(got, "The waived estate_boundary assertion would have refused this migration")
			if read != approve {
				t.Errorf("approve=%v: the warning made from what this run read is %v; got %q", approve, read, got)
			}
		}
	})

	t.Run("no record_store block at all", func(t *testing.T) {
		store, diags := openRecordStoreForImport(ctx, openerReturning(nil, nil), nil, nil, estate, true)
		if store != nil || len(diags) != 0 {
			t.Errorf("store=%v diags=%d, want neither", store, len(diags))
		}
	})
}

// TestAFencedOpenStopsBothWritingCommands crosses this unit with GitHub
// issue #1448 section C, which landed beside it. Both commands reach the
// store through a wrapper of this unit's, so the fence's own headline has to
// survive both wrappers: a rename or a migration that cannot write the
// sentinel must not carry on, and must not be told the store was merely
// unreachable.
//
// The live-mv half is the one that can regress quietly. Its open is the
// lenient path, where anything that is not a refusal becomes a warning and
// the rename proceeds; an admission denial reads as a plain error unless
// projection.IsStoreRefusal names it.
func TestAFencedOpenStopsBothWritingCommands(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true}

	t.Run("live-mv", func(t *testing.T) {
		store, diags := openRecordStoreForMove(ctx, fencedOpen(), rs, nil, estate, false)
		if !diags.HasErrors() {
			t.Fatal("a rename went on past an estate boundary refusal it will hit again on its own record write")
		}
		if store != nil {
			t.Error("the rename kept a store it could not write to")
		}
		if got := summaries(diags, tfdiags.Error); !slices.Contains(got, projection.SummaryEstateBoundaryRefusedTheWrite) {
			t.Errorf("summaries = %q, want the fence's own headline", got)
		}
		if got := summaries(diags, tfdiags.Warning); slices.Contains(got, SummaryRecordStoreNotRead) {
			t.Errorf("the fence was reported as an outage the rename may proceed past: %q", got)
		}
		if !strings.Contains(details(diags, tfdiags.Error), "estate-grant.yaml") {
			t.Errorf("the refusal does not carry the grant that fixes it:\n%s", details(diags, tfdiags.Error))
		}
	})

	t.Run("live-import -approve", func(t *testing.T) {
		store, diags := openRecordStoreForImport(ctx, fencedOpen(), rs, nil, estate, true)
		if !diags.HasErrors() {
			t.Fatal("a migration went on past an estate boundary refusal")
		}
		if store != nil {
			t.Error("the migration kept a store it could not write to")
		}
		if got := summaries(diags, tfdiags.Error); !slices.Contains(got, projection.SummaryEstateBoundaryRefusedTheWrite) {
			t.Errorf("summaries = %q, want the fence's own headline", got)
		}
	})

	// The read-only migration opens the same store and is refused too: the
	// fence is about opening, not about writing, so there is nothing for it
	// to proceed to.
	t.Run("live-import with no -approve", func(t *testing.T) {
		if _, diags := openRecordStoreForImport(ctx, fencedOpen(), rs, nil, estate, false); !diags.HasErrors() {
			t.Fatal("a ratification report was built against a store that refused to open")
		}
	})
}
