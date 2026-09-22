// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// testBucket is what contractCheckingStore calls itself, so the refusals it
// renders name the same bucket the record_store block does.
const testBucket = "the-bucket"

// contractCheckingStore is a real local store that also answers the bucket
// contract, the same shape internal/live/projection's bucketBackedStore has
// and for the same reason: everything but the bucket's settings is the
// production code, and the settings are the only thing scripted. The words
// are the bucket's own, so what these tests read is what an operator reads.
//
// The runner is built around one of these directly rather than through a
// seam, because BeforeApply reaches the store the way every other caller
// does - staterecord.AsContractChecker over r.rawStore - and a seam
// would be production indirection bought to make a test easier.
type contractCheckingStore struct {
	staterecord.Store
	findings []staterecord.Finding
	err      error
	checks   int
}

func (s *contractCheckingStore) CheckContract(context.Context, staterecord.ContractOptions) ([]staterecord.Finding, error) {
	s.checks++
	return s.findings, s.err
}

func (s *contractCheckingStore) ContractSubject() (string, string) { return "Bucket", testBucket }

func (s *contractCheckingStore) ContractRefusal(f staterecord.Finding) (string, string) {
	return staterecord.BucketContractRefusal(testBucket, f)
}

func (s *contractCheckingStore) ContractCheckFailed(err error) (string, string) {
	return staterecord.BucketContractCheckFailed(testBucket, err)
}

func (s *contractCheckingStore) ContractRefusalClosing([]staterecord.Setting) string { return "" }

func localStoreForTest(t *testing.T) staterecord.Store {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	return store
}

func passingFindings() []staterecord.Finding {
	var out []staterecord.Finding
	for _, setting := range staterecord.BucketSettings {
		out = append(out, staterecord.Finding{Setting: setting, Outcome: staterecord.Passed, Found: "fine"})
	}
	return out
}

// withFinding replaces the finding for f.Setting, leaving the others passing.
func withFinding(f staterecord.Finding) []staterecord.Finding {
	out := passingFindings()
	for i := range out {
		if out[i].Setting == f.Setting {
			out[i] = f
		}
	}
	return out
}

func details(diags tfdiags.Diagnostics, sev tfdiags.Severity) string {
	var b strings.Builder
	for _, d := range diags {
		if d.Severity() == sev {
			b.WriteString(d.Description().Summary)
			b.WriteString("\n")
			b.WriteString(d.Description().Detail)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// TestBeforeApplyAssertsTheBucketContract is GitHub issue #1339's apply half.
// The function had no unit test of its own until #1383, and the audit's
// mutations of it - ignore the waiver, ignore a contract-check error, never
// refuse - all survived.
func TestBeforeApplyAssertsTheBucketContract(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	plain := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}

	runnerOver := func(store staterecord.Store, rs *configs.LiveRecordStore) *statelessRunner {
		return &statelessRunner{rawStore: store, recordStoreCfg: rs, recordEstate: estate}
	}

	t.Run("a bucket that passes says nothing", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: passingFindings()}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if len(diags) != 0 {
			t.Errorf("a correct bucket produced %d diagnostic(s):\n%s", len(diags), diags.Err())
		}
		if store.checks != 1 {
			t.Errorf("the bucket was checked %d time(s), want 1: every apply asserts", store.checks)
		}
	})

	t.Run("a failing setting refuses the apply", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: withFinding(staterecord.Finding{
			Setting: staterecord.BucketVersioning, Found: "versioning is Suspended",
		})}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("a bucket with versioning off was applied to")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"versioning", "the-bucket", "versioning is Suspended", "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("a setting that could not be read is refused like one that failed", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: withFinding(staterecord.Finding{
			Setting: staterecord.BucketPublicAccessBlock, Outcome: staterecord.Unreadable, Found: "s3:GetBucketPublicAccessBlock was denied",
		})}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("a bucket nobody could check was applied to")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"could not be read", "s3:GetBucketPublicAccessBlock was denied", "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("a waived failing setting proceeds, and says what it let through", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", AllowInsecure: []string{"lifecycle"}}
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: withFinding(staterecord.Finding{
			Setting: staterecord.BucketLifecycle, Found: "the bucket has no lifecycle configuration",
		})}
		diags := runnerOver(store, waived).BeforeApply(ctx)
		if diags.HasErrors() {
			t.Fatalf("a waived lifecycle failure refused the apply:\n%s", diags.Err())
		}
		got := summaries(diags, tfdiags.Warning)
		if len(got) != 1 || got[0] != "The waived lifecycle assertion would have refused this apply" {
			t.Fatalf("warning summaries = %q", got)
		}
		// The every-run warning is made from configuration alone and cannot
		// say this; this one read the bucket, so it names what it found.
		detail := details(diags, tfdiags.Warning)
		for _, want := range []string{"the bucket has no lifecycle configuration", "the-bucket", `allow_insecure names "lifecycle"`} {
			if !strings.Contains(detail, want) {
				t.Errorf("the warning does not say %q:\n%s", want, detail)
			}
		}
	})

	t.Run("a waiver reaches only the setting it names", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", AllowInsecure: []string{"lifecycle"}}
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: withFinding(staterecord.Finding{
			Setting: staterecord.BucketVersioning, Found: "versioning is Suspended",
		})}
		diags := runnerOver(store, waived).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("waiving lifecycle also waived versioning")
		}
	})

	// GitHub issue #1387. A lifecycle rule that expires CURRENT objects is
	// the one finding allow_insecure does not reach: the waiver's stated
	// cost is that nothing is KNOWN to expire noncurrent versions, and here
	// something is known and it deletes records on a timer.
	t.Run("a lifecycle that deletes records is refused although lifecycle is waived", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", AllowInsecure: []string{"lifecycle"}}
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: withFinding(staterecord.Finding{
			Setting:    staterecord.BucketLifecycle,
			Unwaivable: true,
			Found:      `rule "expire-everything" expires current objects after 30 day(s), and a record is a current object`,
		})}
		diags := runnerOver(store, waived).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("a rule that deletes records was waived by allow_insecure")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"lifecycle deletes records", "expire-everything", "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
		if w := summaries(diags, tfdiags.Warning); len(w) != 0 {
			t.Errorf("the destructive rule was also reported as waived: %q", w)
		}
	})

	t.Run("a contract check that could not be made stops the apply", func(t *testing.T) {
		store := &contractCheckingStore{Store: localStoreForTest(t), err: errors.New("dial tcp: connect: connection refused")}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("the apply went ahead although the bucket could not be checked")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"Cannot check the record store bucket", "the-bucket", "connection refused", "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("a store with no bucket under it has nothing to assert", func(t *testing.T) {
		diags := runnerOver(localStoreForTest(t), &configs.LiveRecordStore{Type: "local"}).BeforeApply(ctx)
		if len(diags) != 0 {
			t.Errorf("a local record store produced %d diagnostic(s):\n%s", len(diags), diags.Err())
		}
	})

	t.Run("no record store at all", func(t *testing.T) {
		if diags := (&statelessRunner{}).BeforeApply(ctx); len(diags) != 0 {
			t.Errorf("a run with no record store produced %d diagnostic(s)", len(diags))
		}
		store := &contractCheckingStore{Store: localStoreForTest(t), findings: passingFindings()}
		if diags := runnerOver(store, nil).BeforeApply(ctx); len(diags) != 0 {
			t.Errorf("a store with no record_store block produced %d diagnostic(s)", len(diags))
		}
		if store.checks != 0 {
			t.Errorf("the bucket was checked %d time(s) with no record_store block to name it", store.checks)
		}
	})
}

// testNamespace is what clusterContractCheckingStore calls itself.
const testNamespace = "tofu-records-prod"

// clusterContractCheckingStore is the cluster's side of the same shape: a
// real local store answering the CLUSTER contract, with the cluster's own
// words. GitHub issue #1442 made BeforeApply one path over
// [staterecord.ContractChecker] instead of two halves with a `case
// "kubernetes"` between them, and this is the half that had no unit test of
// its own before that.
type clusterContractCheckingStore struct {
	staterecord.Store
	findings []staterecord.Finding
	err      error
	checks   int
}

func (s *clusterContractCheckingStore) CheckContract(context.Context, staterecord.ContractOptions) ([]staterecord.Finding, error) {
	s.checks++
	return s.findings, s.err
}

func (s *clusterContractCheckingStore) ContractSubject() (string, string) {
	return "Namespace", testNamespace
}

func (s *clusterContractCheckingStore) ContractRefusal(f staterecord.Finding) (string, string) {
	return staterecord.ClusterContractRefusal(testNamespace, f)
}

func (s *clusterContractCheckingStore) ContractCheckFailed(err error) (string, string) {
	return staterecord.ClusterContractCheckFailed(err)
}

func (s *clusterContractCheckingStore) ContractRefusalClosing(refused []staterecord.Setting) string {
	return staterecord.ClusterContractRefusalClosing(refused)
}

// TestBeforeApplyAssertsTheClusterContract is GitHub issue #1393's apply half
// over the merged path. The three outcomes a run treats differently all come
// out of one function now, so each is pinned here: a property that was read
// and is wrong refuses, a property nobody could read warns and lets the apply
// through, and a waived failure warns with what it let through.
func TestBeforeApplyAssertsTheClusterContract(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	plain := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true}

	runnerOver := func(store staterecord.Store, rs *configs.LiveRecordStore) *statelessRunner {
		return &statelessRunner{rawStore: store, recordStoreCfg: rs, recordEstate: estate}
	}
	clusterFindings := func(f staterecord.Finding) []staterecord.Finding {
		var out []staterecord.Finding
		for _, setting := range clusterSettingsAlwaysReported() {
			if setting == f.Setting {
				out = append(out, f)
				continue
			}
			out = append(out, staterecord.Finding{Setting: setting, Outcome: staterecord.Passed, Found: "fine"})
		}
		return out
	}

	t.Run("a property that was read and is wrong refuses the apply", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: clusterFindings(staterecord.Finding{
			Setting: staterecord.ClusterEstateBoundary,
			Found:   `no ValidatingAdmissionPolicy named "choudoufu-estate-boundary" is installed`,
		})}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("a cluster with no estate boundary policy was applied to")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"estate_boundary", testNamespace, "is installed", "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("a property nobody could read warns and the apply goes on", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: clusterFindings(staterecord.Finding{
			Setting: staterecord.ClusterEncryptionAtRest,
			Outcome: staterecord.NotChecked,
			Found:   "not readable from here, not checked: no kube-apiserver Pod is visible in kube-system",
		})}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if diags.HasErrors() {
			t.Fatalf("a correctly scoped identity was refused for what it cannot read:\n%s", diags.Err())
		}
		got := details(diags, tfdiags.Warning)
		for _, want := range []string{"could not be checked", "this is not a pass"} {
			if !strings.Contains(got, want) {
				t.Errorf("the warning does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("a waived failure warns with what it let through", func(t *testing.T) {
		waived := &configs.LiveRecordStore{Type: "kubernetes", Namespace: testNamespace, NamespaceSet: true,
			AllowInsecure: []string{"estate_boundary"}}
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), findings: clusterFindings(staterecord.Finding{
			Setting: staterecord.ClusterEstateBoundary,
			Found:   "no ValidatingAdmissionPolicy is installed",
		})}
		diags := runnerOver(store, waived).BeforeApply(ctx)
		if diags.HasErrors() {
			t.Fatalf("a waived estate_boundary failure refused the apply:\n%s", diags.Err())
		}
		got := details(diags, tfdiags.Warning)
		for _, want := range []string{"The waived estate_boundary assertion would have refused this apply",
			`Namespace "` + testNamespace + `"`, `allow_insecure names "estate_boundary"`} {
			if !strings.Contains(got, want) {
				t.Errorf("the warning does not say %q:\n%s", want, got)
			}
		}
	})

	t.Run("a check that could not be made stops the apply in the cluster's words", func(t *testing.T) {
		store := &clusterContractCheckingStore{Store: localStoreForTest(t), err: errors.New("dial tcp: connect: connection refused")}
		diags := runnerOver(store, plain).BeforeApply(ctx)
		if !diags.HasErrors() {
			t.Fatal("the apply went ahead although the cluster could not be checked")
		}
		got := details(diags, tfdiags.Error)
		for _, want := range []string{"Cannot check the record store cluster", "connection refused", "Nothing has been applied."} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "bucket") {
			t.Errorf("a cluster store was refused in the bucket's words:\n%s", got)
		}
	})
}
