// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestMarkersSpanMatchesTheArtifact is the freshness guard for issue #142's
// roster.
//
// The span in live/MARKERS.md is rendered by this generator, and the
// generator runs when a person runs it. Refreshing live/iam-reference.json
// without re-rendering, or hand-editing the table because a number looked
// wrong, leaves a document that states which services AWS will evaluate
// aws:ResourceTag on while the artifact it claims to be generated from says
// something else. That is the precise failure the section warns its reader
// about, so it should not be reachable in this repository's own document.
//
// This compares the rendering against the SHIPPED artifact rather than
// re-fetching, so it is offline and it fails on either half drifting.
func TestMarkersSpanMatchesTheArtifact(t *testing.T) {
	root := repoRootForTest(t)

	raw, err := os.ReadFile(filepath.Join(root, "live", "iam-reference.json"))
	if err != nil {
		t.Fatalf("reading the artifact: %v", err)
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}

	doc, err := os.ReadFile(filepath.Join(root, markersMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", markersMDRel, err)
	}

	shipped, ok := spanBody(string(doc), spanResourceTag)
	if !ok {
		t.Fatalf("%s no longer carries the %q span. It is generated content; "+
			"if the section moved, the markers move with it.", markersMDRel, spanResourceTag)
	}

	want := strings.TrimSpace(resourceTagSpanBody(art.Rows))
	if strings.TrimSpace(shipped) != want {
		t.Errorf("%s's %q span is stale.\n\nshipped:\n%s\n\nre-rendered from live/iam-reference.json:\n%s\n\n"+
			"Run `just iamref` rather than editing the table.",
			markersMDRel, spanResourceTag, shipped, want)
	}
}

// TestRosterIsNeverPresentedAsItsComplement guards the one sentence this
// section cannot lose. #152 nearly shipped the inverse reading of this same
// artifact - "142 of 160 services unscopable by aws:ResourceTag" - which
// measured the reference's sparseness and not AWS's behaviour. The roster is
// only sound in one direction, and the document has to keep saying so next to
// the table rather than somewhere further up the page.
func TestRosterIsNeverPresentedAsItsComplement(t *testing.T) {
	root := repoRootForTest(t)
	doc, err := os.ReadFile(filepath.Join(root, markersMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", markersMDRel, err)
	}
	body, ok := spanBody(string(doc), spanResourceTag)
	if !ok {
		t.Fatalf("%s no longer carries the %q span", markersMDRel, spanResourceTag)
	}

	for _, phrase := range []string{"unmeasured, not disproven", "never as its complement"} {
		if !strings.Contains(body, phrase) {
			t.Errorf("the %q span no longer says %q.\n"+
				"The reference is authoritative about the keys it names and silent about the ones it "+
				"omits, so a service's absence from this table is not evidence the key does not work "+
				"there. Without that caveat beside the table, the table reads as its own complement.",
				spanResourceTag, phrase)
		}
	}
}

// spanBody pulls one generated region out of a document by its markers.
func spanBody(doc, name string) (string, bool) {
	open := "<!-- iamref-gen:begin " + name + " -->"
	close := "<!-- iamref-gen:end " + name + " -->"
	i := strings.Index(doc, open)
	if i < 0 {
		return "", false
	}
	rest := doc[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// repoRootForTest walks up from the test's directory to the checkout root.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// TestSCPActionsSpanMatchesTheArtifact is the freshness guard for issue
// #152's SCP action list, and matters more than the roster's.
//
// This list goes into a Deny. A stale entry is a service whose removal verb
// changed name and is no longer denied, and nothing about that looks like a
// failure: the policy still applies, still validates, and silently protects
// one service fewer. live/MARKERS.md's own warning is that a statement which
// looks correct and does nothing is worse than no policy.
func TestSCPActionsSpanMatchesTheArtifact(t *testing.T) {
	root := repoRootForTest(t)

	raw, err := os.ReadFile(filepath.Join(root, "live", "iam-reference.json"))
	if err != nil {
		t.Fatalf("reading the artifact: %v", err)
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}
	doc, err := os.ReadFile(filepath.Join(root, markersMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", markersMDRel, err)
	}
	shipped, ok := spanBody(string(doc), spanSCPActions)
	if !ok {
		t.Fatalf("%s no longer carries the %q span", markersMDRel, spanSCPActions)
	}
	if want := strings.TrimSpace(scpActionsSpanBody(art.Rows)); strings.TrimSpace(shipped) != want {
		t.Errorf("%s's %q span is stale. Run `just iamref` rather than editing the list.\n\nshipped:\n%s\n\nre-rendered:\n%s",
			markersMDRel, spanSCPActions, shipped, want)
	}
}

// TestSCPSpanSeparatesCheckedFromAssumed keeps the two lists apart.
//
// Merging them is the tempting simplification - one action list is easier to
// paste - and it is the one thing this span must not do. An action the
// reference does not name aws:TagKeys on, sitting in the same list as the
// ones it does, reads as protection that was checked. Two of the estate's
// services are in that position, and one of them, route53, is an action the
// surrounding prose independently flags as worth verifying.
func TestSCPSpanSeparatesCheckedFromAssumed(t *testing.T) {
	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "live", "iam-reference.json"))
	if err != nil {
		t.Fatalf("reading the artifact: %v", err)
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}

	var unlisted []string
	for _, r := range art.Rows {
		if r.IAMPrefix == "" || r.UntagAction == "" || !r.UntagActionFound {
			continue
		}
		if !r.UntagListsTagKeys {
			unlisted = append(unlisted, r.IAMPrefix+":"+r.UntagAction)
		}
	}
	if len(unlisted) == 0 {
		t.Skip("every untag action names aws:TagKeys; nothing to separate")
	}

	body := scpActionsSpanBody(art.Rows)
	pasteable := body
	if i := strings.Index(body, "</details>"); i >= 0 {
		pasteable = body[:i]
	}
	for _, a := range unlisted {
		if strings.Contains(pasteable, `"`+a+`"`) {
			t.Errorf("%s does not name aws:TagKeys, but appears in the pasteable Action list.\n"+
				"It belongs in the separate list below it. In the same list it reads as a Deny that "+
				"was checked, which is the failure this section warns about.", a)
		}
		if !strings.Contains(body, a) {
			t.Errorf("%s does not name aws:TagKeys and is missing from the span entirely; "+
				"dropping it silently narrows the policy without saying so", a)
		}
	}
}

// TestProseClaimsAboutUnlistedActionsAreTrue checks the hand-written
// sentences that make claims about the artifact.
//
// The span is generated and cannot drift. The prose around it is not, and it
// says things only the artifact knows: the route53 bullet asserts that
// ChangeTagsForResource is "one of the two actions above the reference does
// not name aws:TagKeys on". If AWS populates that key tomorrow, the span
// silently drops route53 from the unlisted list and the bullet keeps
// asserting it, which is worse than either alone - a caveat pointing at
// nothing reads as a caveat that was checked.
//
// Deliberately narrow: it checks the claims that name a specific action and
// a specific count, not the prose in general.
func TestProseClaimsAboutUnlistedActionsAreTrue(t *testing.T) {
	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "live", "iam-reference.json"))
	if err != nil {
		t.Fatalf("reading the artifact: %v", err)
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}
	doc, err := os.ReadFile(filepath.Join(root, markersMDRel))
	if err != nil {
		t.Fatalf("reading %s: %v", markersMDRel, err)
	}

	unlisted := map[string]bool{}
	for _, r := range art.Rows {
		if r.IAMPrefix != "" && r.UntagAction != "" && r.UntagActionFound && !r.UntagListsTagKeys {
			unlisted[r.IAMPrefix+":"+r.UntagAction] = true
		}
	}

	// The prose singles out route53's combined call. That sentence is only
	// true while the artifact agrees.
	const singledOut = "route53:ChangeTagsForResource"
	body := string(doc)
	claims := strings.Contains(body, "one of the two actions above the reference does not")
	if claims && !unlisted[singledOut] {
		t.Errorf("%s still says %s is one of the actions the reference does not name aws:TagKeys on, "+
			"but the artifact no longer agrees.\n"+
			"Either AWS populated the key, or the untag verb resolved differently. Update the bullet: "+
			"a caveat that points at nothing reads as a caveat that was checked.",
			markersMDRel, singledOut)
	}
	if claims && len(unlisted) != 2 {
		t.Errorf("%s says %q while the artifact now has %d such action(s): %v.\n"+
			"The wording names a count; change it with the measurement.",
			markersMDRel, "one of the two actions", len(unlisted), keysOf(unlisted))
	}
}

// keysOf is a sorted key list, for a legible failure message.
func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
