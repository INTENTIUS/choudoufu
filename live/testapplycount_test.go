// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"strings"
	"testing"
)

// Issue #1552. corpus-hongbomiao-harbor's and corpus-hongbomiao-labelbox's
// test_apply stage counted the estate's marked objects before and after a
// no-op apply through `gauntlet_tagged_count ... resourcegroupstaggingapi
// get-resources` and compared the two. The tagging API does not index the
// IAM user (harbor) or IAM role (labelbox) in us-west-2, so both counts read
// 1 where each estate has 2 taggable objects, and an apply that stripped the
// IAM object's tofu-estate marker left both counts equal: the comparison
// could not fail for the one object it was blind to.
//
// testApplyCountProblems reads the test_apply section of a script and says
// what is wrong with how it counts. It is written from what the stage
// promises - that both reads see every taggable object, and that a control
// exists which moves the ACTUAL count - not from the fix's own wording.
func testApplyCountProblems(script string) []string {
	start := strings.Index(script, "gauntlet_begin_stage test_apply")
	end := strings.Index(script, "gauntlet_stage test_apply pass")
	if start < 0 || end < start {
		return []string{"no test_apply section (gauntlet_begin_stage test_apply ... gauntlet_stage test_apply pass)"}
	}
	section := script[start:end]

	// Code only, with the control block removed: the control deliberately
	// prints the old call's number beside the new one, and the fix's own
	// comments name the call they replace.
	var code []string
	inControl := false
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, `if [ "${BREAK_APPLY_COUNT:-}" = "1" ]`) {
			inControl = true
			continue
		}
		if inControl {
			if trimmed == "fi" {
				inControl = false
			}
			continue
		}
		code = append(code, line)
	}
	src := strings.Join(code, "\n")

	var problems []string
	if strings.Contains(src, "gauntlet_tagged_count") {
		problems = append(problems, "test_apply still counts through gauntlet_tagged_count (resourcegroupstaggingapi get-resources), which does not see this estate's IAM object in us-west-2")
	}
	if n := strings.Count(src, "gauntlet_estate_objects "); n < 2 {
		problems = append(problems, "test_apply calls gauntlet_estate_objects fewer than twice outside its control; the before AND the after read both have to include the IAM object")
	}
	if strings.Contains(src, "|| echo 0") {
		problems = append(problems, "test_apply swallows a failed inventory read into a literal 0")
	}
	if !strings.Contains(section, `if [ "${BREAK_APPLY_COUNT:-}" = "1" ]`) {
		problems = append(problems, "test_apply has no BREAK_APPLY_COUNT control")
	} else {
		control := section[strings.Index(section, `if [ "${BREAK_APPLY_COUNT:-}" = "1" ]`):]
		// The control has to move the ACTUAL number: it unmarks a live
		// object, and it does so before the after-read, not before the
		// expectation is set.
		if !strings.Contains(control, "--tag-keys tofu-estate") {
			problems = append(problems, "the BREAK_APPLY_COUNT control does not remove a tofu-estate marker from a live object")
		}
		afterRead := strings.LastIndex(section, "gauntlet_estate_objects ")
		apply := strings.Index(section, `"$TOFU" apply`)
		ctl := strings.Index(section, `if [ "${BREAK_APPLY_COUNT:-}" = "1" ]`)
		if !(apply < ctl && ctl < afterRead) {
			problems = append(problems, "the BREAK_APPLY_COUNT control does not sit between the apply and the after-read, so it cannot move the after-count")
		}
	}
	if strings.Count(script, "BREAK_APPLY_COUNT") < 3 || !strings.Contains(script, "#   BREAK_APPLY_COUNT") {
		problems = append(problems, "BREAK_APPLY_COUNT is not documented in the script's header")
	}
	return problems
}

func TestHongbomiaoTestApplyCountsSeeTheIAMObject(t *testing.T) {
	for _, rel := range []string{
		"e2e/corpus-hongbomiao-harbor/run.sh",
		"e2e/corpus-hongbomiao-labelbox/run.sh",
	} {
		for _, p := range testApplyCountProblems(readScript(t, rel)) {
			t.Errorf("live/%s: %s (#1552)", rel, p)
		}
	}
}

// oldHongbomiaoTestApply is the stage as it stood before #1552, verbatim
// from corpus-hongbomiao-harbor/run.sh. The guard above has to be red on it,
// every run, or it proves nothing.
const oldHongbomiaoTestApply = `#   BREAK_GREEN_COUNT
gauntlet_begin_stage test_apply
log "=== STAGE 4: test apply (apply the empty plan; object count unchanged) ==="
BEFORE_N="$(gauntlet_tagged_count awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  2>/dev/null || echo 0)"

APPLY2_OUT="$(cd "$ESTATE" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_N="$(gauntlet_tagged_count awsl resourcegroupstaggingapi get-resources \
  --tag-filters "Key=tofu-estate,Values=$ESTATE_NAME" \
  2>/dev/null || echo 0)"
[ "$AFTER_N" = "$BEFORE_N" ] || fail "object count changed across a no-op apply: $BEFORE_N -> $AFTER_N"
gauntlet_stage test_apply pass "genuine no-op: $BEFORE_N objects before, $AFTER_N after, no state file either time"
`

func TestHongbomiaoTestApplyGuardIsRedOnTheOldStage(t *testing.T) {
	got := strings.Join(testApplyCountProblems(oldHongbomiaoTestApply), "\n")
	for _, want := range []string{
		"gauntlet_tagged_count",
		"fewer than twice",
		"literal 0",
		"no BREAK_APPLY_COUNT control",
		"not documented",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the #1552 guard is not red on the pre-#1552 stage for %q; it reported:\n%s", want, got)
		}
	}

	// A control placed before the apply only lowers the before-read, which
	// moves the expectation rather than the actual after-count. Built from
	// the CURRENT harbor script with its control moved up, so every other
	// check passes and only the placement can make it red.
	cur := readScript(t, "e2e/corpus-hongbomiao-harbor/run.sh")
	const open = `if [ "${BREAK_APPLY_COUNT:-}" = "1" ]; then`
	i := strings.Index(cur, open)
	j := strings.Index(cur[i:], "\nfi\n") + i + len("\nfi\n")
	block := cur[i:j]
	moved := strings.Replace(cur[:i]+cur[j:], "\nAPPLY2_OUT=", "\n"+block+"APPLY2_OUT=", 1)
	if got := testApplyCountProblems(moved); len(got) != 1 || !strings.Contains(got[0], "between the apply and the after-read") {
		t.Errorf("the #1552 guard does not catch a control moved before the apply; it reported %q", got)
	}
}
