// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// bucketBackedStore is a real local store that also answers the bucket
// contract, so the handshake and the sentinel are the production ones and
// only the bucket's settings are scripted.
type bucketBackedStore struct {
	staterecord.Store
	findings   []staterecord.BucketFinding
	checkErr   error
	deleteErr  error
	checks     int
	namespaces []string
}

func (s *bucketBackedStore) CheckBucketContract(_ context.Context, namespaces []string) ([]staterecord.BucketFinding, error) {
	s.checks++
	s.namespaces = namespaces
	return s.findings, s.checkErr
}

func (s *bucketBackedStore) Delete(ctx context.Context, key, expectedVersion string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Store.Delete(ctx, key, expectedVersion)
}

func passing() []staterecord.BucketFinding {
	var out []staterecord.BucketFinding
	for _, setting := range staterecord.BucketSettings {
		out = append(out, staterecord.BucketFinding{Setting: setting, OK: true, Found: "fine"})
	}
	return out
}

// firstContact runs the production handshake followed by the first-contact
// assertion, the way newRecordStore does.
func firstContact(t *testing.T, store *bucketBackedStore, rs *configs.LiveRecordStore, estate string) error {
	t.Helper()
	// The production sequence, not a copy of it: see openBuiltStore.
	_, err := openBuiltStore(context.Background(), store, rs, estate)
	return err
}

// TestABadBucketIsRefusedOnEveryFirstContactUntilItIsFixed is GitHub issue
// #1339's first-contact half. The part that matters is the second run: a
// refusal that left its sentinel behind would make the next plan proceed
// against the bucket the first one refused.
func TestABadBucketIsRefusedOnEveryFirstContactUntilItIsFixed(t *testing.T) {
	ctx := context.Background()
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	bad := passing()
	bad[0] = staterecord.BucketFinding{Setting: staterecord.BucketVersioning, Found: "versioning has never been enabled"}
	store := &bucketBackedStore{Store: localHintStore(t), findings: bad}

	for run := 1; run <= 2; run++ {
		err := firstContact(t, store, rs, estate)
		if err == nil {
			t.Fatalf("run %d: a bucket with versioning off was accepted", run)
		}
		if !strings.Contains(err.Error(), "versioning") || !strings.Contains(err.Error(), "the-bucket") {
			t.Errorf("run %d: the refusal names neither the setting nor the bucket: %s", run, err)
		}
		keys, listErr := store.List(ctx, staterecord.NamespacePrefix(RecordKeyPrefix(estate)))
		if listErr != nil {
			t.Fatal(listErr)
		}
		if slices.Contains(keys, SentinelKey(RecordKeyPrefix(estate))) {
			t.Fatalf("run %d: the refusal left the sentinel behind, so the next run is no longer a first contact and would not be checked", run)
		}
	}
	if store.checks != 2 {
		t.Errorf("the bucket was checked %d times across two refused runs, want 2", store.checks)
	}
	if want := BucketNamespaces(rs, estate); !slices.Equal(store.namespaces, want) {
		t.Errorf("the check was told the store writes under %q, want %q", store.namespaces, want)
	}

	// Fixed. The third run is accepted and leaves its sentinel.
	store.findings = passing()
	if err := firstContact(t, store, rs, estate); err != nil {
		t.Fatalf("a correct bucket was refused: %s", err)
	}
	// And a fourth run is not a first contact: the ruling on #1339 is that
	// the assertions do not run on every plan.
	if err := firstContact(t, store, rs, estate); err != nil {
		t.Fatal(err)
	}
	if store.checks != 3 {
		t.Errorf("the bucket was checked %d times, want 3: two refusals, one acceptance, and nothing once the sentinel exists", store.checks)
	}
}

// TestAStoreWithNoBucketHasNothingToAssert: the local store is not in a
// bucket, and that is not a failure.
func TestAStoreWithNoBucketHasNothingToAssert(t *testing.T) {
	findings, ok, err := BucketContractFindings(context.Background(), staterecord.NewRunCache(localHintStore(t), RecordKeyPrefix("prod")), nil, "prod")
	if err != nil || ok || findings != nil {
		t.Errorf("BucketContractFindings over a local store = (%v, %v, %v), want (nil, false, nil)", findings, ok, err)
	}
}

// TestTheBucketIsFoundThroughTheProductionWrappers: newRecordStore hands the
// runner a RunCache over a (possibly) CountingStore, and BeforeApply asserts
// through that. A wrapper that hid the bucket would make every apply skip the
// check and say nothing.
func TestTheBucketIsFoundThroughTheProductionWrappers(t *testing.T) {
	inner := &bucketBackedStore{Store: localHintStore(t), findings: passing()}
	wrapped := staterecord.NewRunCache(staterecord.NewCountingStore(inner, nil), RecordKeyPrefix("prod"))
	_, ok, err := BucketContractFindings(context.Background(), wrapped, &configs.LiveRecordStore{Type: "s3", Bucket: "b"}, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || inner.checks != 1 {
		t.Errorf("the bucket under the wrappers was not reached: ok=%v checks=%d", ok, inner.checks)
	}
}

// failing returns a full set of findings with setting failed.
func failingSetting(setting staterecord.BucketSetting, found string) []staterecord.BucketFinding {
	out := passing()
	for i := range out {
		if out[i].Setting == setting {
			out[i] = staterecord.BucketFinding{Setting: setting, Found: found}
		}
	}
	return out
}

// sentinelPresent reports whether the store holds the sentinel for the
// namespace rs and estate actually resolve to, which is the point of it: a
// key_prefix override moves the sentinel and a check that looked at the
// default would pass over a sentinel nobody removed.
func sentinelPresent(t *testing.T, store staterecord.Store, rs *configs.LiveRecordStore, estate string) bool {
	t.Helper()
	prefix := RecordStoreKeyPrefix(rs, estate)
	keys, err := store.List(context.Background(), staterecord.NamespacePrefix(prefix))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return slices.Contains(keys, SentinelKey(prefix))
}

// TestFirstContactHonoursTheWaiver is GitHub issue #1340 on the first-contact
// path, and a mutation the #1383 audit made survive: dropping SplitWaived
// here refuses a bucket the operator has explicitly accepted, on the one run
// that has nothing to fall back on. The waiver reaches only the setting it
// names, so the second half of this test is the one that keeps the first
// from being a waiver that waives everything.
func TestFirstContactHonoursTheWaiver(t *testing.T) {
	const estate = "prod"
	waived := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", AllowInsecure: []string{"versioning"}}
	store := &bucketBackedStore{Store: localHintStore(t), findings: failingSetting(staterecord.BucketVersioning, "versioning is Suspended")}

	if err := firstContact(t, store, waived, estate); err != nil {
		t.Fatalf("a waived versioning failure was refused on first contact: %v", err)
	}
	if !sentinelPresent(t, store, waived, estate) {
		t.Error("the run was accepted but its sentinel was taken back out, so every later run is a first contact again")
	}
	if store.checks != 1 {
		t.Errorf("the bucket was checked %d time(s), want 1: a waiver does not skip the read, it decides what the findings mean", store.checks)
	}

	// The same waiver, a different setting failing: still refused.
	other := &bucketBackedStore{Store: localHintStore(t), findings: failingSetting(staterecord.BucketPublicAccessBlock, "public-access block has BlockPublicPolicy off")}
	err := firstContact(t, other, waived, estate)
	if err == nil {
		t.Fatal("waiving versioning also waived public_access_block")
	}
	if !strings.Contains(err.Error(), "public_access_block") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
}

// TestFirstContactRemovesTheSentinelFromTheConfiguredPrefix is the
// key_prefix half of #1339's "a refusal leaves nothing behind". The sentinel
// is written under the namespace [RecordStoreKeyPrefix] resolves to, so a
// refusal that deleted the DEFAULT prefix's key would leave the real one in
// place and the next run would not be a first contact at all.
func TestFirstContactRemovesTheSentinelFromTheConfiguredPrefix(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", KeyPrefix: "team/prod", KeyPrefixSet: true}
	if RecordStoreKeyPrefix(rs, estate) == RecordKeyPrefix(estate) {
		t.Fatal("the override resolves to the default prefix, so this test cannot tell the two apart")
	}
	store := &bucketBackedStore{Store: localHintStore(t), findings: failingSetting(staterecord.BucketVersioning, "versioning is Suspended")}

	err := firstContact(t, store, rs, estate)
	if err == nil {
		t.Fatal("a bucket with versioning off was accepted")
	}
	if sentinelPresent(t, store, rs, estate) {
		t.Errorf("the sentinel is still at %q after a refusal, so the next run is not a first contact and the bucket is never checked again", SentinelKey(RecordStoreKeyPrefix(rs, estate)))
	}
	if strings.Contains(err.Error(), "could not be removed again") {
		t.Errorf("the refusal reports a failed sentinel delete, which means it was deleting a key it never wrote: %v", err)
	}
}

// TestFirstContactRefusesWhenTheContractCannotBeChecked: an error from the
// contract read is not a bucket that passed. Swallowing it lets an estate's
// first run write records into a bucket nobody has looked at, and leaves the
// sentinel behind so no later run looks either.
func TestFirstContactRefusesWhenTheContractCannotBeChecked(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	store := &bucketBackedStore{Store: localHintStore(t), findings: passing(), checkErr: errors.New("dial tcp: connect: connection refused")}

	err := firstContact(t, store, rs, estate)
	if err == nil {
		t.Fatal("the store opened although the bucket contract could not be read")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("the refusal does not carry what went wrong: %v", err)
	}
	if sentinelPresent(t, store, rs, estate) {
		t.Error("the sentinel survived, so the next run is not a first contact and the bucket is never checked")
	}
	// An unreadable bucket is an OUTAGE, not a refusal: it may well read on
	// the next attempt, which is the difference #1376 turns on.
	if IsStoreRefusal(err) {
		t.Errorf("a contract read that failed is reported as a refusal: %v", err)
	}
}

// TestAFailedSentinelDeleteIsReportedWithTheRefusal: the refusal is what the
// operator has to act on, and the leftover sentinel is what makes the next
// run behave differently from this one. Reporting only one of the two leaves
// whoever fixes the bucket wondering why the next plan says nothing.
func TestAFailedSentinelDeleteIsReportedWithTheRefusal(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	store := &bucketBackedStore{
		Store:     localHintStore(t),
		findings:  failingSetting(staterecord.BucketVersioning, "versioning is Suspended"),
		deleteErr: errors.New("s3:DeleteObject was denied"),
	}

	err := firstContact(t, store, rs, estate)
	if err == nil {
		t.Fatal("a bucket with versioning off was accepted")
	}
	for _, want := range []string{"versioning", "the-bucket", "s3:DeleteObject was denied", "The next plan will not repeat this check"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q:\n%s", want, err)
		}
	}
	if !IsStoreRefusal(err) {
		t.Errorf("wrapping the failed delete around the refusal lost its type, so live-plan would treat it as an outage and go on: %v", err)
	}
}

// TestBucketNamespacesCoversAllThree: the lifecycle assertion is only as
// good as the list of namespaces it is told about, and a namespace left out
// is a set of keys a lifecycle rule may expire with nothing here to notice.
// The three are named one by one on purpose; comparing against
// BucketNamespaces itself is a test of nothing.
func TestBucketNamespacesCoversAllThree(t *testing.T) {
	const estate = "prod"
	rs := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket"}
	got := BucketNamespaces(rs, estate)
	for _, want := range []string{RecordKeyPrefix(estate), HintKeyPrefix(estate), RootOutputKeyPrefix(estate)} {
		if !slices.Contains(got, want) {
			t.Errorf("BucketNamespaces = %q, which does not cover %q", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("BucketNamespaces = %q, want exactly the three namespaces one estate writes under", got)
	}

	// A key_prefix override moves the records namespace and nothing else:
	// the hint and the outputs are not under it.
	override := &configs.LiveRecordStore{Type: "s3", Bucket: "the-bucket", KeyPrefix: "team/prod", KeyPrefixSet: true}
	want := []string{staterecord.NamespacePrefix("team/prod"), HintKeyPrefix(estate), RootOutputKeyPrefix(estate)}
	if got := BucketNamespaces(override, estate); !slices.Equal(got, want) {
		t.Errorf("with key_prefix set, BucketNamespaces = %q, want %q", got, want)
	}
}
