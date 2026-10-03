// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setdigest

import (
	"encoding/json"
	"strings"
	"testing"
)

// setDoc is a set plan document in GitHub issue #1752's shape: three roots,
// one with an update whose "before" carries a value the plan does not
// change, one with a no-op beside its create.
const setDoc = `{
  "format_version": "1",
  "roots": [
    {"root": "estates/e01", "estate": "ls-e01", "status": "planned", "error": "", "plan": {
      "format_version": "1.2", "timestamp": "2026-10-02T10:00:00Z",
      "resource_changes": [
        {"address": "aws_vpc.network", "mode": "managed", "type": "aws_vpc", "name": "network",
         "change": {"actions": ["update"], "before": {"cidr_block": "10.100.0.0/16", "tags": {"a": "1"}}, "after": {"cidr_block": "10.100.0.0/16", "tags": {"a": "2"}}, "after_unknown": {}}},
        {"address": "aws_sqs_queue.q", "mode": "managed", "type": "aws_sqs_queue", "name": "q",
         "change": {"actions": ["create"], "before": null, "after": {"name": "ls-e01-q", "delay_seconds": 0}, "after_unknown": {"arn": true}}}
      ],
      "output_changes": {"vpc": {"actions": ["no-op"], "before": "x", "after": "x"}}}},
    {"root": "estates/e02", "estate": "ls-e02", "status": "planned", "error": "", "plan": {
      "format_version": "1.2",
      "resource_changes": [
        {"address": "aws_subnet.app", "change": {"actions": ["create"], "before": null, "after": {"cidr_block": "10.100.1.0/24"}}},
        {"address": "aws_s3_bucket.untouched", "change": {"actions": ["no-op"], "before": {"bucket": "b", "tags": {"x": "1"}}, "after": {"bucket": "b", "tags": {"x": "1"}}}}
      ]}},
    {"root": "estates/e03", "estate": "ls-e03", "status": "failed", "error": "Error: no valid credential sources", "plan": null}
  ]
}`

func mustDoc(t *testing.T, s string) *SetDocument {
	t.Helper()
	doc, err := ParseSetDocument([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func digests(t *testing.T, doc *SetDocument) (map[string]string, string) {
	t.Helper()
	byRoot, set, err := DocumentDigests(doc)
	if err != nil {
		t.Fatal(err)
	}
	return byRoot, set
}

// TestSetDigestStableUnderReordering is the first half of #1754's set
// digest criterion: every permutation of the roots gives one digest.
func TestSetDigestStableUnderReordering(t *testing.T) {
	doc := mustDoc(t, setDoc)
	_, want := digests(t, doc)
	if !strings.HasPrefix(want, DigestPrefix) || len(want) != len(DigestPrefix)+64 {
		t.Fatalf("digest %q is not %s and 64 hex digits", want, DigestPrefix)
	}
	perms := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, p := range perms {
		re := &SetDocument{}
		for _, i := range p {
			re.Roots = append(re.Roots, doc.Roots[i])
		}
		if _, got := digests(t, re); got != want {
			t.Errorf("roots in order %v: set digest %s, want %s (the order the roots were given in moved the digest)", p, got, want)
		}
	}
}

// TestSetDigestChangesWithAnyOneRoot is the second half: any single root's
// change set moving moves the set digest, and moves that root's digest and
// no other.
func TestSetDigestChangesWithAnyOneRoot(t *testing.T) {
	base := mustDoc(t, setDoc)
	baseRoots, baseSet := digests(t, base)

	cases := []struct {
		name string
		root string
		old  string
		new  string
	}{
		{"an after value of a create", "estates/e02", `"cidr_block": "10.100.1.0/24"`, `"cidr_block": "10.100.2.0/24"`},
		{"an update's new tag value", "estates/e01", `"tags": {"a": "2"}`, `"tags": {"a": "3"}`},
		{"an update's prior value", "estates/e01", `"tags": {"a": "1"}`, `"tags": {"a": "0"}`},
		{"an action", "estates/e02", `"actions": ["create"], "before": null, "after": {"cidr_block"`, `"actions": ["delete", "create"], "before": null, "after": {"cidr_block"`},
		{"a number's value", "estates/e01", `"delay_seconds": 0`, `"delay_seconds": 1`},
		{"what is unknown", "estates/e01", `"after_unknown": {"arn": true}`, `"after_unknown": {"arn": true, "id": true}`},
		{"an output that now changes", "estates/e01", `"vpc": {"actions": ["no-op"], "before": "x", "after": "x"}`, `"vpc": {"actions": ["update"], "before": "x", "after": "y"}`},
		{"a no-op that becomes a change", "estates/e02", `"actions": ["no-op"], "before": {"bucket": "b", "tags": {"x": "1"}}, "after": {"bucket": "b", "tags": {"x": "1"}}`, `"actions": ["update"], "before": {"bucket": "b", "tags": {"x": "1"}}, "after": {"bucket": "b", "tags": {"x": "2"}}`},
		{"a failed root's reason", "estates/e03", `no valid credential sources`, `the provider crashed`},
		{"a failed root that now plans", "estates/e03", `"status": "failed", "error": "Error: no valid credential sources", "plan": null`, `"status": "planned", "error": "", "plan": {"resource_changes": []}`},
		{"an estate", "estates/e02", `"estate": "ls-e02"`, `"estate": "ls-e09"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Count(setDoc, tc.old) != 1 {
				t.Fatalf("the edit %q matches %d places in the fixture; it must match one", tc.old, strings.Count(setDoc, tc.old))
			}
			moved := mustDoc(t, strings.Replace(setDoc, tc.old, tc.new, 1))
			roots, set := digests(t, moved)
			if set == baseSet {
				t.Errorf("set digest did not move when %s changed in %s: %s", tc.name, tc.root, set)
			}
			for r, d := range roots {
				if r == tc.root && d == baseRoots[r] {
					t.Errorf("root %s's digest did not move", r)
				}
				if r != tc.root && d != baseRoots[r] {
					t.Errorf("root %s's digest moved though only %s changed", r, tc.root)
				}
			}
		})
	}
}

// TestSetDigestIgnoresWhatChangesNothing pins what the digest leaves out:
// key order, whitespace, the order of resource_changes, the plan's
// timestamp and a no-op's values. Each of these differs between two plans
// that change the same things.
func TestSetDigestIgnoresWhatChangesNothing(t *testing.T) {
	_, baseSet := digests(t, mustDoc(t, setDoc))

	cases := []struct{ name, old, new string }{
		{"the timestamp", `"timestamp": "2026-10-02T10:00:00Z"`, `"timestamp": "2026-10-03T11:00:00Z"`},
		{"a no-op's drifted values", `"before": {"bucket": "b", "tags": {"x": "1"}}, "after": {"bucket": "b", "tags": {"x": "1"}}`, `"before": {"bucket": "b", "tags": {"x": "9"}}, "after": {"bucket": "b", "tags": {"x": "9"}}`},
		{"key order inside a change", `"before": null, "after": {"name": "ls-e01-q", "delay_seconds": 0}`, `"after": {"delay_seconds": 0, "name": "ls-e01-q"}, "before": null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Count(setDoc, tc.old) != 1 {
				t.Fatalf("the edit %q matches %d places", tc.old, strings.Count(setDoc, tc.old))
			}
			if _, got := digests(t, mustDoc(t, strings.Replace(setDoc, tc.old, tc.new, 1))); got != baseSet {
				t.Errorf("set digest moved when only %s changed", tc.name)
			}
		})
	}

	t.Run("the order of resource_changes and whitespace", func(t *testing.T) {
		doc := mustDoc(t, setDoc)
		var plan map[string]json.RawMessage
		if err := json.Unmarshal(doc.Roots[0].Plan, &plan); err != nil {
			t.Fatal(err)
		}
		var rcs []json.RawMessage
		if err := json.Unmarshal(plan["resource_changes"], &rcs); err != nil {
			t.Fatal(err)
		}
		rcs[0], rcs[1] = rcs[1], rcs[0]
		plan["resource_changes"], _ = json.MarshalIndent(rcs, "", "     ")
		doc.Roots[0].Plan, _ = json.MarshalIndent(plan, "", "\t")
		if _, got := digests(t, doc); got != baseSet {
			t.Errorf("set digest moved when resource_changes were reordered and re-indented")
		}
	})
}

func TestSetDigestRefusesARootTwice(t *testing.T) {
	doc := mustDoc(t, setDoc)
	doc.Roots = append(doc.Roots, doc.Roots[0])
	if _, _, err := DocumentDigests(doc); err == nil || !strings.Contains(err.Error(), "estates/e01 twice") {
		t.Fatalf("a set naming estates/e01 twice: got %v, want a refusal naming it", err)
	}
	if _, err := SetDigest([]RootDigestEntry{{"a", "x"}, {"a", "x"}}); err == nil {
		t.Fatal("SetDigest accepted one root twice")
	}
}

// TestSetDigestFraming: the boundary between a root's name and its digest
// cannot be moved to make two different sets hash alike.
func TestSetDigestFraming(t *testing.T) {
	a, err := SetDigest([]RootDigestEntry{{"ab", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := SetDigest([]RootDigestEntry{{"a", "bc"}})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal(`{"ab","c"} and {"a","bc"} share a set digest`)
	}
	empty, _ := SetDigest(nil)
	one, _ := SetDigest([]RootDigestEntry{{"", ""}})
	if empty == one {
		t.Fatal("an empty set and a set of one empty entry share a digest")
	}
}

func TestParseSetDocumentRefusesAPlan(t *testing.T) {
	if _, err := ParseSetDocument([]byte(`{"format_version":"1.2","resource_changes":[]}`)); err == nil {
		t.Fatal("a bare plan parsed as a set document")
	}
	if _, err := ParseSetDocument([]byte(`{"roots":[{"estate":"x"}]}`)); err == nil {
		t.Fatal(`a root with no "root" parsed`)
	}
}
