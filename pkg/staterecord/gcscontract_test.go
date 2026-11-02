// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"strings"
	"testing"
)

// gcsCorrectBucket is a bucket resource that passes all three settings.
func gcsCorrectBucket() map[string]any {
	return map[string]any{
		"versioning": map[string]any{"enabled": true},
		"lifecycle": map[string]any{"rule": []any{
			map[string]any{"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"daysSinceNoncurrentTime": 30}},
		}},
		"iamConfiguration": map[string]any{
			"publicAccessPrevention":   "enforced",
			"uniformBucketLevelAccess": map[string]any{"enabled": true},
		},
	}
}

var gcsTestNamespaces = []string{"tofu-records/prod/", "tofu-hints/prod/", "tofu-outputs/prod/"}

func checkFakeGCSBucket(t *testing.T, attrs map[string]any, namespaces []string) map[Setting]Finding {
	t.Helper()
	server, fake := newFakeGCSServer(t)
	fake.bucketAttrs = attrs
	// This estate's own sentinel, which list_isolation must not count.
	fake.objects["tofu-records/prod/"+SentinelName] = &fakeGCSObject{body: []byte("s"), gen: 1, metadata: map[string]string{"tofu-estate": "prod"}}
	findings, err := CheckGCSBucketContract(context.Background(), newFakeGCSClient(t, server), "test-bucket", namespaces)
	if err != nil {
		t.Fatalf("CheckGCSBucketContract: %v", err)
	}
	if len(findings) != len(GCSSettings) {
		t.Fatalf("got %d findings, want one per setting (%d)", len(findings), len(GCSSettings))
	}
	byName := map[Setting]Finding{}
	for i, f := range findings {
		if f.Setting != GCSSettings[i] {
			t.Errorf("finding %d is %q, want %q: findings come in GCSSettings order", i, f.Setting, GCSSettings[i])
		}
		byName[f.Setting] = f
	}
	return byName
}

func TestGCSContractPassesACorrectBucket(t *testing.T) {
	for _, ns := range [][]string{nil, gcsTestNamespaces} {
		for s, f := range checkFakeGCSBucket(t, gcsCorrectBucket(), ns) {
			if !f.OK() {
				t.Errorf("namespaces %v: %s did not pass: %s", ns, s, f.Found)
			}
		}
	}
}

// TestGCSContractRefusesEachSettingByName: each broken setting fails on its
// own, by its own name, with the other two still passing, and its refusal
// headline names it.
func TestGCSContractRefusesEachSettingByName(t *testing.T) {
	cases := []struct {
		name    string
		setting Setting
		break_  func(m map[string]any)
		found   string
	}{
		{"versioning off", GCSObjectVersioning, func(m map[string]any) { m["versioning"] = map[string]any{"enabled": false} }, "not enabled"},
		{"no lifecycle", GCSNoncurrentLifecycle, func(m map[string]any) { delete(m, "lifecycle") }, "no lifecycle rules"},
		{"only a storage class rule", GCSNoncurrentLifecycle, func(m map[string]any) {
			m["lifecycle"] = map[string]any{"rule": []any{map[string]any{
				"action": map[string]any{"type": "SetStorageClass", "storageClass": "NEARLINE"}, "condition": map[string]any{"daysSinceNoncurrentTime": 30},
			}}}
		}, "none of the bucket's 1"},
		{"noncurrent rule under another prefix", GCSNoncurrentLifecycle, func(m map[string]any) {
			m["lifecycle"] = map[string]any{"rule": []any{map[string]any{
				"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"daysSinceNoncurrentTime": 30, "matchesPrefix": []string{"tofu-records/"}},
			}}}
		}, "tofu-hints/prod/"},
		{"noncurrent rule with no window", GCSNoncurrentLifecycle, func(m map[string]any) {
			m["lifecycle"] = map[string]any{"rule": []any{map[string]any{
				"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"isLive": false},
			}}}
		}, "no recovery window"},
		{"public access prevention inherited", GCSPublicAccessPrevention, func(m map[string]any) {
			m["iamConfiguration"].(map[string]any)["publicAccessPrevention"] = "inherited"
		}, "inherited"},
		{"uniform bucket-level access off", GCSUniformBucketLevelAccess, func(m map[string]any) {
			m["iamConfiguration"].(map[string]any)["uniformBucketLevelAccess"] = map[string]any{"enabled": false}
		}, "object ACLs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := gcsCorrectBucket()
			tc.break_(attrs)
			got := checkFakeGCSBucket(t, attrs, gcsTestNamespaces)
			for s, f := range got {
				if s == tc.setting {
					if f.OK() || f.Outcome != Failed {
						t.Errorf("%s: outcome %v, want Failed", s, f.Outcome)
					}
					if !strings.Contains(f.Found, tc.found) {
						t.Errorf("%s found %q, want it to say %q", s, f.Found, tc.found)
					}
					if f.Unwaivable {
						t.Errorf("%s is unwaivable; only a rule deleting live objects is", s)
					}
					summary, _ := GCSBucketContractRefusal("test-bucket", f)
					if !strings.Contains(summary, string(s)) {
						t.Errorf("refusal headline %q does not name %q", summary, s)
					}
					continue
				}
				if !f.OK() {
					t.Errorf("breaking %s also failed %s: %s", tc.setting, s, f.Found)
				}
			}
		})
	}
}

// TestGCSContractRefusesARuleThatDeletesLiveRecords is #1377's refusal in
// GCS terms. A Delete rule with no noncurrent-only condition removes live
// records on a timer, and allow_insecure does not reach it, even beside a
// correct noncurrent rule.
func TestGCSContractRefusesARuleThatDeletesLiveRecords(t *testing.T) {
	attrs := gcsCorrectBucket()
	attrs["lifecycle"] = map[string]any{"rule": []any{
		map[string]any{"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"daysSinceNoncurrentTime": 30}},
		map[string]any{"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"age": 90}},
	}}
	f := checkFakeGCSBucket(t, attrs, gcsTestNamespaces)[GCSNoncurrentLifecycle]
	if f.OK() || !f.Unwaivable {
		t.Fatalf("a rule deleting live objects: outcome %v, unwaivable %v; want an unwaivable failure", f.Outcome, f.Unwaivable)
	}
	if !strings.Contains(f.Found, "rule 2 deletes live objects 90 day(s) after creation") {
		t.Errorf("found %q, want it to name rule 2 and its age", f.Found)
	}
	refused, _, waived := SplitWaived([]Finding{f}, []string{string(GCSNoncurrentLifecycle)})
	if len(refused) != 1 || len(waived) != 0 {
		t.Errorf("allow_insecure waived a rule that deletes records")
	}

	// The same rule limited to a prefix outside every namespace is not ours.
	attrs["lifecycle"] = map[string]any{"rule": []any{
		map[string]any{"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"daysSinceNoncurrentTime": 30}},
		map[string]any{"action": map[string]any{"type": "Delete"}, "condition": map[string]any{"age": 90, "matchesPrefix": []string{"scratch/"}}},
	}}
	if f := checkFakeGCSBucket(t, attrs, gcsTestNamespaces)[GCSNoncurrentLifecycle]; !f.OK() {
		t.Errorf("a deleting rule under scratch/ refused the bucket: %s", f.Found)
	}
}

// TestGCSContractDeniedIsUnreadableNotAnError: a role without
// storage.buckets.get gets three unreadable findings, which refuse, and not
// an error, which would read as an outage.
func TestGCSContractDeniedIsUnreadableNotAnError(t *testing.T) {
	server, fake := newFakeGCSServer(t)
	fake.forbidden = true
	findings, err := CheckGCSBucketContract(context.Background(), newFakeGCSClient(t, server), "test-bucket", nil)
	if err != nil {
		t.Fatalf("a denied read returned an error: %v", err)
	}
	for _, f := range findings {
		if f.Setting == GCSListIsolation {
			if f.Outcome != NotChecked || !strings.Contains(f.Found, "storage.objects.list") {
				t.Errorf("%s: outcome %v, found %q; want NotChecked naming storage.objects.list", f.Setting, f.Outcome, f.Found)
			}
			continue
		}
		if f.Outcome != Unreadable || !strings.Contains(f.Found, "storage.buckets.get") {
			t.Errorf("%s: outcome %v, found %q; want Unreadable naming storage.buckets.get", f.Setting, f.Outcome, f.Found)
		}
	}

	fake.forbidden = false
	fake.missingBucket = true
	if _, err := CheckGCSBucketContract(context.Background(), newFakeGCSClient(t, server), "test-bucket", nil); err == nil {
		t.Error("a bucket that does not exist was reported on rather than refused with an error")
	}
}

// TestGCSStoreIsAContractChecker: the store is found through the run
// cache's wrapping, so first contact and every apply assert it.
func TestGCSStoreIsAContractChecker(t *testing.T) {
	store, fake := newTestGCSStore(t, "")
	fake.bucketAttrs = gcsCorrectBucket()
	checker, ok := AsContractChecker(NewRunCache(store, "tofu-records/prod/"))
	if !ok {
		t.Fatal("a GCS store under the run cache is not a ContractChecker")
	}
	findings, err := checker.CheckContract(context.Background(), ContractOptions{Namespaces: gcsTestNamespaces})
	if err != nil {
		t.Fatal(err)
	}
	if refused, _, _ := SplitWaived(findings, nil); len(refused) != 0 {
		t.Errorf("a correct bucket was refused: %v", refused)
	}
}

// TestGCSListIsolationWarnsByNameAboutAnotherEstate: GCS cannot fence a
// listing, so a bucket that holds another estate's keys is said out loud by
// name, as a warning a run proceeds past and allow_insecure can silence. The
// other estate is found by its sentinel wherever its key_prefix put it.
func TestGCSListIsolationWarnsByNameAboutAnotherEstate(t *testing.T) {
	server, fake := newFakeGCSServer(t)
	fake.bucketAttrs = gcsCorrectBucket()
	fake.objects["tofu-records/prod/"+SentinelName] = &fakeGCSObject{body: []byte("s"), gen: 1, metadata: map[string]string{"tofu-estate": "prod"}}
	client := newFakeGCSClient(t, server)

	check := func() Finding {
		t.Helper()
		findings, err := CheckGCSBucketContract(context.Background(), client, "test-bucket", gcsTestNamespaces)
		if err != nil {
			t.Fatal(err)
		}
		return findings[len(findings)-1]
	}
	if f := check(); f.Setting != GCSListIsolation || !f.OK() {
		t.Fatalf("a bucket with only this estate's keys: %s %v %q, want list_isolation passing", f.Setting, f.Outcome, f.Found)
	}

	// A neighbour named like a prefix of this estate, under a key_prefix of
	// its own: still another estate.
	fake.objects["team/prod-eu/"+SentinelName] = &fakeGCSObject{body: []byte("s"), gen: 2, metadata: map[string]string{"tofu-estate": "prod-eu"}}
	f := check()
	if f.Outcome != Warned || !strings.Contains(f.Found, "prod-eu") {
		t.Fatalf("list_isolation = %v %q, want Warned naming prod-eu", f.Outcome, f.Found)
	}
	refused, warned, _ := SplitWaived([]Finding{f}, nil)
	if len(refused) != 0 || len(warned) != 1 {
		t.Errorf("a shared listing refused the run; it warns")
	}
	if _, _, waived := SplitWaived([]Finding{f}, []string{string(GCSListIsolation)}); len(waived) != 1 {
		t.Errorf("allow_insecure = [\"list_isolation\"] did not reach the warning")
	}
	if summary, _ := GCSBucketContractRefusal("test-bucket", f); !strings.Contains(summary, "other estates' keys") {
		t.Errorf("warning headline %q", summary)
	}

	// An identity that may not list cannot be asked, and says so.
	fake.listForbidden = true
	if f := check(); f.Outcome != NotChecked {
		t.Errorf("list_isolation with storage.objects.list denied = %v, want NotChecked", f.Outcome)
	}
}
