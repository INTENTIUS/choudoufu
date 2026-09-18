// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	snapshotRel = "aws-paginating-operations.json"
	baselineRel = "aws-page-query-baseline.json"
)

func loadSnapshot(t *testing.T) *PaginatingOperations {
	t.Helper()
	snap, err := LoadPaginatingOperations(snapshotRel)
	if err != nil {
		t.Fatalf("loading live/%s: %v", snapshotRel, err)
	}
	return snap
}

// TestPaginatingOperationsSnapshotIsCanonical is everything about the
// vendored snapshot that can be verified with no botocore installed, which
// is the only kind of check that runs everywhere.
//
// The live comparison - regenerate from botocore and diff - is
// `go run ./tools/aws-paginators-gen -check`, deliberately a command and
// not a test. A test would have to skip itself where botocore is absent,
// and a skipping guard is permanently green, which is the failure CLAUDE.md
// names by hand. The precedent is TestGauntletCrossingScriptsPinOneAWSProvider
// in pins_drift_test.go, which for the same reason verifies everything
// checkable offline and documents the network half as a command a human
// runs.
//
// What is left here is not nothing. A hand edit to the file - the way a
// snapshot actually rots, because regenerating it is easy and editing it is
// tempting - fails on the canonical-form comparison, and the by-value
// anchors below fail if the file is truncated, reordered, or swapped for
// one taken from a different source.
func TestPaginatingOperationsSnapshotIsCanonical(t *testing.T) {
	raw, err := os.ReadFile(snapshotRel)
	if err != nil {
		t.Fatalf("reading live/%s: %v", snapshotRel, err)
	}
	snap := loadSnapshot(t)

	got, err := snap.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, got) {
		t.Errorf("live/%s is not in canonical form (two-space indent, sorted keys, sorted and deduplicated operation lists, one trailing newline) - it was edited by hand. Regenerate it: go run ./tools/aws-paginators-gen", snapshotRel)
	}

	if snap.BotocoreVersion == "" {
		t.Errorf("live/%s records no botocore_version - a reader cannot tell how old it is", snapshotRel)
	}
	if !strings.Contains(snap.Generated, "aws-paginators-gen") {
		t.Errorf("live/%s's _generated stamp %q does not name its generator", snapshotRel, snap.Generated)
	}

	// Anchors, by value. The four service counts are the ones #1214's
	// ruling measured and quoted; they are asserted rather than logged so
	// that a snapshot regenerated against something that is not botocore -
	// or a botocore whose paginator data changed shape - stops here instead
	// of quietly narrowing the guard.
	for svc, want := range map[string]int{
		"resourcegroupstaggingapi": 5,
		"iam":                      34,
		"s3":                       7,
		"ec2":                      169,
	} {
		if got := len(snap.Services[svc]); got != want {
			t.Errorf("live/%s records %d paginating operations for %s; #1214's ruling measured %d. If botocore really moved, re-run the generator and update this anchor in the same commit so the change is deliberate", snapshotRel, got, svc, want)
		}
	}

	// The two operations both defects actually ran, and the one the issue
	// cites as obviously safe - which botocore marks as paginating and
	// #1214's ruling keeps UNPRUNED on purpose.
	for _, tc := range []struct{ svc, op string }{
		{"iam", "list-policies"},                      // #1206
		{"resourcegroupstaggingapi", "get-resources"}, // #1042
		{"iam", "list-role-tags"},                     // "returns a handful of tags"
	} {
		pages, err := snap.Paginates(tc.svc, tc.op)
		if err != nil {
			t.Errorf("%s %s: %v", tc.svc, tc.op, err)
			continue
		}
		if !pages {
			t.Errorf("live/%s says `aws %s %s` does not paginate; botocore says it does", snapshotRel, tc.svc, tc.op)
		}
	}

	if snap.PaginatorFiles < len(snap.Services) {
		t.Errorf("live/%s read %d paginator files for %d services - fewer files than services is impossible", snapshotRel, snap.PaginatorFiles, len(snap.Services))
	}
}

// TestPaginatesRefusesAnUnknownService: "I do not know this service" and
// "this operation does not page" must never read the same to the guard.
// The one hand-written thing in the whole derivation is the awscli rename
// table, and this is what keeps it honest - a new alias stops a human.
func TestPaginatesRefusesAnUnknownService(t *testing.T) {
	snap := loadSnapshot(t)

	// s3api is awscli's name for botocore's s3 directory.
	pages, err := snap.Paginates("s3api", "list-objects-v2")
	if err != nil {
		t.Fatalf("s3api should resolve to the s3 directory: %v", err)
	}
	if !pages {
		t.Errorf("s3api list-objects-v2 should paginate")
	}

	// A service token no directory serves is an error, not a false.
	if _, err := snap.Paginates("notaservice", "list-things"); err == nil {
		t.Errorf("Paginates returned no error for an unknown service - an unresolved token would be classified as non-paginating, which is the direction that hides defects")
	} else if _, ok := err.(ErrUnknownCLIService); !ok {
		t.Errorf("Paginates returned %T for an unknown service, want ErrUnknownCLIService", err)
	}
}

// TestReducingQueryClassification pins the class boundary by value,
// including the two literal queries the defects ran and - the case that
// decides the boundary - the query #1206's FIX adopted, which filters but
// does not reduce and must stay out of the class.
func TestReducingQueryClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
		want  bool
	}{
		{"#1042's literal", `length(ResourceTagMappingList)`, true},
		{"#1042's class in another service", `length(Tags)`, true},
		{"#1206's literal", "Policies[?starts_with(PolicyName, 'x') == `true`].Arn | [0]", true},
		{"#1206's fix: filter, no reduction", "Policies[?starts_with(PolicyName, 'x') == `true`].Arn", false},
		{"the bare tag idiom", `Tags[?Key=='tofu-address'].Value | [0]`, true},
		{"a direct index", `Tags[0].Value`, true},
		{"a last-element index", `Reservations[-1].Instances`, true},
		{"a slice", `Subnets[0:1].SubnetId`, true},
		{"a sort", `sort(Subnets[].CidrBlock)`, true},
		{"a flatten projection", `ResourceTagMappingList[].ResourceARN`, false},
		{"a wildcard projection", `Buckets[*].Name`, false},
		{"a plain field", `Vpc.VpcId`, false},
		{"a filtered projection", `Roles[?starts_with(RoleName, 'x')].Arn`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reducingQuery.MatchString(tc.query); got != tc.want {
				t.Errorf("reducing(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// measure runs the analyzer over the real population and returns per-script
// counts plus the set of scripts that exist.
func measure(t *testing.T, snap *PaginatingOperations) (counts map[string]int, present map[string]bool) {
	t.Helper()
	sources, err := CrossingScriptSources(".")
	if err != nil {
		t.Fatalf("enumerating crossing scripts: %v", err)
	}
	present = map[string]bool{}
	for rel := range sources {
		present[rel] = true
	}
	counts, err = MeasurePageQueryBaseline(".", snap)
	if err != nil {
		t.Fatalf("measuring: %v", err)
	}
	return counts, present
}

// TestNoScriptAddsAPerPageQuery is issue #1214's guard, and it replaces
// tools/gauntlet's TestNoScriptCountsTaggedObjectsPerPage (issue #1042),
// which matched one regex:
//
//	`--query\s+['"]length\(ResourceTagMappingList`
//
// one service, one result key, one JMESPath function. #1206 was none of
// those three and walked straight past it. The population was narrow too:
// that test scanned only the estates registered in
// live/gauntlet/estates.json, and #1042's exact banned idiom was still
// sitting in live/e2e/corpus-message-queue/run.sh, which is not one of
// them. This guard scans every e2e/*/run.sh and e2e/lib/*.sh instead.
//
// See live/awspagequery_baseline.go for why the guard is a per-script
// ratchet rather than a flat ban, and how to move a number.
func TestNoScriptAddsAPerPageQuery(t *testing.T) {
	snap := loadSnapshot(t)
	base, err := LoadPageQueryBaseline(baselineRel)
	if err != nil {
		t.Fatalf("loading live/%s: %v", baselineRel, err)
	}
	counts, present := measure(t, snap)

	if len(present) == 0 {
		t.Fatal("no scripts enumerated - this guard checked nothing, which is worse than not existing")
	}

	for _, v := range CheckPageQueryRatchet(base.Scripts, counts, present) {
		t.Errorf("%s", v.Message)
		if v.Measured > v.Recorded {
			for _, f := range findingsFor(t, snap, v.Script) {
				t.Logf("  live/%s %s", v.Script, f)
			}
		}
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	t.Logf("scanned %d script(s); %d carry %d call site(s) in the class", len(present), len(counts), total)
}

func findingsFor(t *testing.T, snap *PaginatingOperations, rel string) []AWSPageQueryFinding {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading live/%s: %v", rel, err)
		return nil
	}
	f, _, _ := snap.PageQueryFindings(string(data))
	return f
}

// replaceExactlyOnce is substitution that cannot silently no-op. A proof
// that edits a real script's text has to fail loudly when the text it edits
// has moved, or it goes on "passing" while testing the unedited original.
// Same argument, and the same shape, as mustReplaceOnce in
// pins_drift_test.go.
func replaceExactlyOnce(t *testing.T, rel, src, old, new string) string {
	t.Helper()
	if n := strings.Count(src, old); n != 1 {
		t.Fatalf("expected exactly one occurrence of\n%s\nin live/%s, found %d - this proof edits the real script's text, so it must fail rather than silently test the unedited original", old, rel, n)
	}
	return strings.Replace(src, old, new, 1)
}

func readScript(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("reading live/%s: %v", rel, err)
	}
	return string(data)
}

// TestGuardIsRedOnTheOriginalIAMPolicyQuery is #1214's required red proof,
// run on every `go test` rather than once by hand.
//
// The trap, which both the issue and the ruling call out because a proof
// that falls into it looks like success: restoring `--path-prefix /` in
// place of `--scope Local` does NOT reproduce #1206. The narrowing is what
// made the listing sixteen pages long, but the `| [0]` pipe is what turned
// those pages into fifteen literal "None"s, and it is the pipe the guard
// keys on. The scope-only arm asserts the guard stays GREEN, so that
// warning is an executable fact in this repo rather than a sentence
// someone has to remember.
//
// Three arms, because the script's current shape splits the proof:
//
//  1. the no-op narrowing alone - green, and must stay green;
//  2. the `| [0]` pipe restored inside policy_arn_by_prefix, which invokes
//     the CLI as `"$awsfn"` - red, as ReasonUnattributed. #1206's own call
//     site is one the analyzer cannot attribute, which is precisely why
//     an unattributable reducing --query is counted rather than dropped;
//  3. the whole pre-#1206 inline shape, command word and all, the way the
//     nine copies the function replaced were written - red, attributed to
//     `iam list-policies` and named as paginating.
func TestGuardIsRedOnTheOriginalIAMPolicyQuery(t *testing.T) {
	const rel = "e2e/corpus-iam-policy/run.sh"
	snap := loadSnapshot(t)
	real := readScript(t, rel)

	// The fixed form as it stands: filtered, not reduced, and out of the
	// class. Asserted by value first, because every count below is a delta
	// from it.
	baseFindings, unknown, _ := snap.PageQueryFindings(real)
	if len(unknown) > 0 {
		t.Fatalf("live/%s: unresolved service tokens %v", rel, unknown)
	}
	before := len(baseFindings)

	const fixedCall = "\"$awsfn\" iam list-policies --scope Local \\\n    --query \"Policies[?starts_with(PolicyName, '$prefix') == \\`true\\`].Arn\" --output text"

	// Arm 1, the trap. Put the no-op narrowing back and nothing else.
	scopeOnly := replaceExactlyOnce(t, rel, real,
		`iam list-policies --scope Local `,
		`iam list-policies --path-prefix / `)
	f1, _, _ := snap.PageQueryFindings(scopeOnly)
	if len(f1) != before {
		t.Errorf("restoring --path-prefix / alone moved the guard from %d to %d findings. It must not: the narrowing is not what this guard keys on, and a proof that goes red here would be red for the wrong reason", before, len(f1))
	}

	// Arm 2: restore the `| [0]` pipe, which is what produced the "None"s.
	original := replaceExactlyOnce(t, rel, real, fixedCall,
		"\"$awsfn\" iam list-policies --path-prefix / \\\n    --query \"Policies[?starts_with(PolicyName, '$prefix') == \\`true\\`].Arn | [0]\" --output text")
	f2, _, _ := snap.PageQueryFindings(original)
	if len(f2) != before+1 {
		t.Fatalf("restoring #1206's original query shape moved the guard from %d to %d findings, want %d - the widened guard does not catch the defect it was written for", before, len(f2), before+1)
	}
	restored := findQuery(t, f2, "| [0]", ReasonUnattributed)
	if !strings.Contains(restored.String(), "cannot resolve") {
		t.Errorf("the failure line does not say the call could not be resolved:\n%s", restored)
	}
	t.Logf("red on live/%s (arm 2): %s", rel, restored)

	// Arm 3: the same restoration written the way the nine inline copies
	// #1206 replaced actually were, `awsl` and all, so the paginating
	// classification itself is exercised and named.
	inline := replaceExactlyOnce(t, rel, real, fixedCall,
		"awsl iam list-policies --path-prefix / \\\n    --query \"Policies[?starts_with(PolicyName, '$prefix') == \\`true\\`].Arn | [0]\" --output text")
	f3, _, _ := snap.PageQueryFindings(inline)
	if len(f3) != before+1 {
		t.Fatalf("the inline pre-#1206 shape moved the guard from %d to %d findings, want %d", before, len(f3), before+1)
	}
	named := findQuery(t, f3, "| [0]", ReasonPaginates)
	if named.Operation != "list-policies" {
		t.Fatalf("the extra finding is `aws %s %s`, want iam list-policies", named.Service, named.Operation)
	}
	t.Logf("red on live/%s (arm 3): %s", rel, named)
	for _, want := range []string{"list-policies", "ListPolicies paginates", "one page at a time"} {
		if !strings.Contains(named.String(), want) {
			t.Errorf("the failure line does not mention %q - a reader has to be told which call and why:\n%s", want, named.String())
		}
	}
}

// findQuery returns the one finding whose query contains needle and whose
// reason matches, and fails the test if there is not exactly one.
func findQuery(t *testing.T, findings []AWSPageQueryFinding, needle string, reason FindingReason) AWSPageQueryFinding {
	t.Helper()
	var hits []AWSPageQueryFinding
	for _, f := range findings {
		if f.Reason == reason && strings.Contains(f.Query, needle) && strings.Contains(f.Query, "Policies[?starts_with") {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one %s finding whose query contains %q, got %d", reason, needle, len(hits))
	}
	return hits[0]
}

// TestGuardIsRedOnTheRestored1042Idiom proves the widened guard still
// covers the narrow one it replaces, in a script the narrow one could not
// see. corpus-message-queue is not registered in live/gauntlet/estates.json,
// which is the only population TestNoScriptCountsTaggedObjectsPerPage ever
// scanned - so the exact idiom #1042 banned was still on main here, and
// this unit's first fix was to route it through gauntlet_tagged_count.
func TestGuardIsRedOnTheRestored1042Idiom(t *testing.T) {
	const rel = "e2e/corpus-message-queue/run.sh"
	snap := loadSnapshot(t)
	real := readScript(t, rel)

	before, _, _ := snap.PageQueryFindings(real)

	reverted := replaceExactlyOnce(t, rel, real,
		"MARKED=\"$(gauntlet_tagged_count awsl resourcegroupstaggingapi get-resources \\\n  --tag-filters \"Key=tofu-estate,Values=$ESTATE\" \\\n  2>/dev/null || echo 0)\"",
		"MARKED=\"$(awsl resourcegroupstaggingapi get-resources \\\n  --tag-filters \"Key=tofu-estate,Values=$ESTATE\" \\\n  --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo 0)\"")
	after, _, _ := snap.PageQueryFindings(reverted)
	if len(after) != len(before)+1 {
		t.Fatalf("reverting the #1042 fix in live/%s moved the guard from %d to %d findings, want %d", rel, len(before), len(after), len(before)+1)
	}
	var reintroduced *AWSPageQueryFinding
	for i := range after {
		if strings.Contains(after[i].Query, "ResourceTagMappingList") {
			reintroduced = &after[i]
		}
	}
	if reintroduced == nil {
		t.Fatalf("the extra finding is not the reverted get-resources call: %v", after)
	}
	if reintroduced.Operation != "get-resources" || reintroduced.Reason != ReasonPaginates {
		t.Errorf("the reverted call reads as `aws %s %s` (%s), want resourcegroupstaggingapi get-resources (paginates)", reintroduced.Service, reintroduced.Operation, reintroduced.Reason)
	}
	t.Logf("red on live/%s: %s", rel, reintroduced)
}

// TestTheNULByteScriptIsInThePopulation: live/e2e/corpus-mastino-dns/run.sh
// carries a legitimate embedded NUL byte, and grep reports nothing for it -
// the blind spot that made PR #1157 count 23 scripts out of 24 (#1219).
// This guard reads bytes for that reason, and the script carries call sites
// in the class, so the hole would have been load-bearing rather than
// theoretical. Asserted by value so that a future rewrite of the
// enumeration in terms of grep fails here.
func TestTheNULByteScriptIsInThePopulation(t *testing.T) {
	const rel = "e2e/corpus-mastino-dns/run.sh"
	sources, err := CrossingScriptSources(".")
	if err != nil {
		t.Fatal(err)
	}
	src, ok := sources[rel]
	if !ok {
		t.Fatalf("live/%s is not in the scanned population - the enumeration skipped the one file in this tree that grep cannot read", rel)
	}
	if !strings.ContainsRune(src, 0) {
		t.Fatalf("live/%s no longer contains a NUL byte, so this proof no longer proves anything - check whether #1219's blind spot still has a live example, and retarget or retire this test", rel)
	}
	snap := loadSnapshot(t)
	f, _, _ := snap.PageQueryFindings(src)
	if len(f) == 0 {
		t.Errorf("live/%s carries no findings, so its presence in the population no longer demonstrates anything", rel)
	}
	t.Logf("live/%s: NUL byte present, %d finding(s) a grep-based sweep would have missed", rel, len(f))
}

// TestAWrapperHidingTheCallIsCountedNotDropped: corpus-service-linked-roles
// reads tags through
//
//	tag_of() { local key="$1"; shift; "$@" --query "Tags[?Key=='$key'].Value | [0]" ...; }
//
// There is no `aws` token on that line at all. The analyzer cannot say
// which operation it calls, and "cannot say" must not read as "safe", so
// the reducing --query is counted in the class with ReasonUnattributed.
func TestAWrapperHidingTheCallIsCountedNotDropped(t *testing.T) {
	const rel = "e2e/corpus-service-linked-roles/run.sh"
	snap := loadSnapshot(t)
	f, _, _ := snap.PageQueryFindings(readScript(t, rel))
	var unattributed int
	for _, x := range f {
		if x.Reason == ReasonUnattributed {
			unattributed++
		}
	}
	if unattributed == 0 {
		t.Fatalf("live/%s: the tag_of() wrapper's reducing --query was not counted - a shell indirection is not evidence that the call it hides is safe", rel)
	}
	t.Logf("live/%s: %d unattributed reducing --query call site(s)", rel, unattributed)
}

// TestCommandPositionAnchorIsLoadBearing. A bare \baws\b also matches
// inside `--path-prefix "/aws-service-role/$SERVICE/"`, and the phantom
// invocation it starts owns the rest of the logical line and swallows the
// real call's --query. corpus-service-linked-roles read as unparseable
// lines rather than call sites until the match was anchored to command
// position, so the anchor is asserted here rather than trusted.
func TestCommandPositionAnchorIsLoadBearing(t *testing.T) {
	const src = "ROLE=\"$(awsl iam list-roles --path-prefix \"/aws-service-role/${SVC}/\" \\\n  --query 'Roles[0].RoleName' --output text)\""
	uses, unparsed := AnalyzeAWSPageQueries(src)
	if len(unparsed) != 0 {
		t.Errorf("the path containing \"aws-service-role\" produced %d unattributable --query line(s); the command-position anchor is not holding", len(unparsed))
	}
	if len(uses) != 1 {
		t.Fatalf("got %d uses, want 1: %+v", len(uses), uses)
	}
	if uses[0].Service != "iam" || uses[0].Operation != "list-roles" {
		t.Errorf("read `aws %s %s`, want `aws iam list-roles`", uses[0].Service, uses[0].Operation)
	}
	if !uses[0].Reducing {
		t.Errorf("Roles[0].RoleName is an index into a paginated listing and must read as reducing")
	}
}

// ── the helper (issue #1214 item 3) ──────────────────────────────────────

// writePagingStub writes a stand-in for the AWS CLI that reproduces the one
// behaviour this whole issue is about, and the behaviour is not invented:
//
//   - with --query, the CLI applies it to EACH page before merging and
//     emits one result per page. #1206 recorded what that looked like for
//     `Tags[?Key=='k'].Value | [0]` over a listing whose match was not on
//     the first page: the literal "None", then the value.
//   - with --output json and no --query, the CLI's automatic pagination
//     merges the pages into one document first.
//
// Hardcoding the per-page results rather than evaluating JMESPath keeps the
// stub honest about what it is: a recording of observed CLI behaviour, not
// a second implementation of the thing under test.
func writePagingStub(t *testing.T, dir string, match string) string {
	t.Helper()
	// page 2 holds the match; page 1 does not.
	merged := `{"Tags":[{"Key":"other","Value":"x"}`
	if match != "" {
		merged += `,{"Key":"tofu-address","Value":"` + match + `"}`
	}
	merged += `]}`
	page2 := "None"
	if match != "" {
		page2 = match
	}
	body := fmt.Sprintf(`#!/usr/bin/env bash
# A recording of the AWS CLI's behaviour over a two-page listing (issue #1206).
want=0
query=""
for arg in "$@"; do
  if [ "$want" = "1" ]; then query="$arg"; want=0; continue; fi
  if [ "$arg" = "--query" ]; then want=1; fi
done
if [ -n "$query" ]; then
  # --query is applied to each page before merging: one result per page.
  # Page 1 holds no match, so it prints the literal "None".
  cat <<'PAGES'
None
%s
PAGES
  exit 0
fi
# No --query: automatic pagination merges the pages into one document
# before anything filters it.
cat <<'MERGED'
%s
MERGED
`, page2, merged)
	path := filepath.Join(dir, "awsstub")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func runBashScript(t *testing.T, script string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// TestGauntletFirstMatchMergesPagesBeforeFiltering proves the helper, and
// proves it against a stub that genuinely reproduces the defect first -
// the BREAK arm is run, not asserted in prose. If the stub stopped
// reproducing #1206, the break arm fails and the green arm's meaning would
// be gone with it.
func TestGauntletFirstMatchMergesPagesBeforeFiltering(t *testing.T) {
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on PATH; this proof runs the real shell helper and must not be skipped (a skipping guard is permanently green)", bin)
		}
	}
	const wantARN = "arn:aws:iam::000000000000:policy/example-abc"
	dir := t.TempDir()
	stub := writePagingStub(t, dir, wantARN)
	lib, err := filepath.Abs(filepath.Join("e2e", "lib", "gauntlet.sh"))
	if err != nil {
		t.Fatal(err)
	}

	// BREAK arm: the idiom the helper replaces, against the same stub.
	// Two lines come back, and #1206's guard - which every call site wrote
	// - passes on them, which is the whole mechanism.
	broke, err := runBashScript(t, fmt.Sprintf(`
set -uo pipefail
X="$(%q iam list-policy-tags --query "Tags[?Key=='tofu-address'].Value | [0]" --output text)"
printf 'lines=%%s\n' "$(printf '%%s' "$X" | wc -l | tr -d ' ')"
if [ -n "$X" ] && [ "$X" != "None" ]; then printf 'guard=passed\n'; else printf 'guard=caught\n'; fi
`, stub))
	if err != nil {
		t.Fatalf("break arm: %v\n%s", err, broke)
	}
	if !strings.Contains(broke, "lines=1") {
		t.Fatalf("the stub no longer reproduces #1206's per-page output (expected two lines, \"None\" then the arn):\n%s", broke)
	}
	if !strings.Contains(broke, "guard=passed") {
		t.Fatalf("the stub no longer reproduces #1206's mechanism - the `[ -n ] && [ != None ]` guard is supposed to PASS on the multi-line string:\n%s", broke)
	}

	// GREEN arm: the helper, same stub, same listing.
	got, err := runBashScript(t, fmt.Sprintf(`
set -euo pipefail
source %q
X="$(gauntlet_first_match '.Tags[] | select(.Key == "tofu-address") | .Value' %q iam list-policy-tags --policy-arn whatever)"
printf 'value=%%s\n' "$X"
printf 'lines=%%s\n' "$(printf '%%s' "$X" | wc -l | tr -d ' ')"
`, lib, stub))
	if err != nil {
		t.Fatalf("green arm: %v\n%s", err, got)
	}
	if !strings.Contains(got, "value="+wantARN) {
		t.Errorf("gauntlet_first_match returned the wrong value:\n%s", got)
	}
	if !strings.Contains(got, "lines=0") {
		t.Errorf("gauntlet_first_match returned more than one line - it is still reading page by page:\n%s", got)
	}

	// And no match prints nothing, not the literal "None".
	empty := writePagingStub(t, t.TempDir(), "")
	none, err := runBashScript(t, fmt.Sprintf(`
set -euo pipefail
source %q
X="$(gauntlet_first_match '.Tags[] | select(.Key == "tofu-address") | .Value' %q iam list-policy-tags)"
printf 'empty=%%s\n' "${X:-<nothing>}"
`, lib, empty))
	if err != nil {
		t.Fatalf("no-match arm: %v\n%s", err, none)
	}
	if !strings.Contains(none, "empty=<nothing>") {
		t.Errorf("gauntlet_first_match printed something for a listing with no match; it must print nothing, never the literal \"None\":\n%s", none)
	}
}

// TestGauntletFirstMatchIsDocumentedWhereItLives keeps the helper's
// migration note next to the helper. The "None" difference is the one that
// breaks a mechanical conversion of the idiom, so a caller reading the
// function has to find it there.
func TestGauntletFirstMatchIsDocumentedWhereItLives(t *testing.T) {
	src := readScript(t, "e2e/lib/gauntlet.sh")
	if !strings.Contains(src, "gauntlet_first_match()") {
		t.Fatal("live/e2e/lib/gauntlet.sh does not define gauntlet_first_match")
	}
	head := src[:strings.Index(src, "gauntlet_first_match()")]
	for _, want := range []string{"#1206", "[ -n \"$X\" ]", "Never add --query back"} {
		if !strings.Contains(head, want) {
			t.Errorf("gauntlet_first_match's comment does not mention %q", want)
		}
	}
}

// TestBaselineIsMeasuredNotTyped: the committed baseline has to be exactly
// what the generator produces from the scripts as they stand. Without this,
// a number could be raised by hand to make a failure go away, which is the
// one thing a ratchet must not permit.
func TestBaselineIsMeasuredNotTyped(t *testing.T) {
	raw, err := os.ReadFile(baselineRel)
	if err != nil {
		t.Fatalf("reading live/%s: %v", baselineRel, err)
	}
	base, err := LoadPageQueryBaseline(baselineRel)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := MarshalPageQueryBaseline(base.Scripts)
	if err != nil {
		t.Fatal(err)
	}
	// The _generated stamp carries a date, so compare everything else.
	if strip(raw) != strip(rendered) {
		t.Errorf("live/%s is not in canonical form - regenerate it: go run ./tools/aws-paginators-gen -baseline", baselineRel)
	}
	if base.Comment != pageQueryBaselineComment {
		t.Errorf("live/%s's _comment was edited; it is generated", baselineRel)
	}
	var names []string
	for k := range base.Scripts {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Error("the baseline is empty, so the ratchet compares against nothing")
	}
}

func strip(b []byte) string {
	var keep []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"_generated"`) {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

// TestRatchetFailsInBothDirections proves the ratchet's four arms, because
// a ratchet whose failure paths have never been run is a number in a file.
// Each case is asserted red; the last one is the shape that matters most -
// a NEW script starts at an implicit zero, so it cannot be born dirty.
func TestRatchetFailsInBothDirections(t *testing.T) {
	present := map[string]bool{"a.sh": true, "b.sh": true, "new.sh": true}
	for _, tc := range []struct {
		name     string
		recorded map[string]int
		measured map[string]int
		want     string
	}{
		{
			name:     "a new call site in an already-dirty script",
			recorded: map[string]int{"a.sh": 3},
			measured: map[string]int{"a.sh": 4},
			want:     "A NEW one was added",
		},
		{
			name:     "progress that was not recorded",
			recorded: map[string]int{"a.sh": 3},
			measured: map[string]int{"a.sh": 1},
			want:     "record the progress",
		},
		{
			name:     "a script cleaned all the way to zero",
			recorded: map[string]int{"b.sh": 2},
			measured: map[string]int{},
			want:     "now carries none",
		},
		{
			name:     "dead credit for a script that is gone",
			recorded: map[string]int{"gone.sh": 5},
			measured: map[string]int{},
			want:     "no longer exists",
		},
		{
			name:     "a brand new script born dirty",
			recorded: map[string]int{},
			measured: map[string]int{"new.sh": 1},
			want:     "A NEW one was added",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := CheckPageQueryRatchet(tc.recorded, tc.measured, present)
			if len(v) != 1 {
				t.Fatalf("want exactly one violation, got %d: %v", len(v), v)
			}
			if !strings.Contains(v[0].Message, tc.want) {
				t.Errorf("violation does not say %q:\n%s", tc.want, v[0].Message)
			}
		})
	}

	// And the green arm: agreement is silent.
	if v := CheckPageQueryRatchet(map[string]int{"a.sh": 3}, map[string]int{"a.sh": 3}, present); len(v) != 0 {
		t.Errorf("an exact match produced %d violation(s): %v", len(v), v)
	}
}
