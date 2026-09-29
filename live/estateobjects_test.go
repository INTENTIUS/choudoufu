// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #1271. `resourcegroupstaggingapi get-resources` does not index IAM
// on the pinned emulator, and real AWS does not index several IAM types
// there either (#1134). Three assertions in
// live/e2e/corpus-iam-policy/run.sh counted the estate through it: one
// failed loudly ("expected 2, got 0") and two could not fail at all,
// because a comparison between two numbers that are always 0 holds for any
// behaviour the apply could have.
//
// live/e2e/lib/gauntlet.sh's gauntlet_estate_objects is the replacement,
// and this file is its red proof. Everything here runs the real shell
// helper against a stub AWS CLI; nothing is asserted in prose.
//
// The stub is a RECORDING, not a second implementation. Every response
// shape below was read off ghcr.io/lex00/floci@sha256:0bbeb430 on
// 2026-09-17 with no tofu in the loop: a customer-managed policy, a role
// and an instance profile created carrying tofu-estate=probe-estate all
// returned that tag through iam:ListPolicyTags / ListRoleTags /
// ListInstanceProfileTags, while GetResources filtered to the same tag
// returned only an S3 bucket tagged identically in the same container.

// stubWorld describes the account a stub AWS CLI answers for.
type stubWorld struct {
	estate string
	// policies maps a customer-managed policy ARN to the value of its
	// tofu-estate tag; the empty string means the policy carries no such
	// tag, which is what a genuinely unmarked object looks like.
	policies map[string]string
	roles    map[string]string // role name -> tofu-estate value
	profiles map[string]string // instance-profile name -> tofu-estate value
	// users is the fourth leg, added for #1549. The comment on
	// gauntlet_estate_objects used to say IAM users were NOT covered and
	// that an estate holding one would be undercounted; two estates hold
	// one, and both were.
	users map[string]string // user name -> tofu-estate value
	// rgtaServesIAM is the world lex00/floci#206 / #1152 will create: the
	// Tagging API starts returning IAM objects that the native APIs have
	// always returned. The count must not move when it does.
	rgtaServesIAM bool
	// bucket, when set, is an S3 ARN GetResources returns - the control
	// that proves the Tagging API leg is running at all.
	bucket string
	// broken makes every call fail, which is an unreachable endpoint.
	broken bool
}

func tagsJSON(estate string) string {
	if estate == "" {
		return `{"Tags":[{"Key":"Example","Value":"unrelated"}]}`
	}
	return fmt.Sprintf(`{"Tags":[{"Key":"tofu-estate","Value":%q},{"Key":"tofu-address","Value":"module.x.aws_iam_policy.policy:0"}]}`, estate)
}

func writeAWSStub(t *testing.T, w stubWorld) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n# A recording of floci@0bbeb430's answers (issue #1271).\n")
	if w.broken {
		b.WriteString("printf 'Could not connect to the endpoint URL\\n' >&2\nexit 255\n")
	} else {
		b.WriteString(`svc="$1"; op="$2"; shift 2
argval() { local flag="$1"; shift; local want=0 a; for a in "$@"; do if [ "$want" = 1 ]; then printf '%s' "$a"; return; fi; [ "$a" = "$flag" ] && want=1; done; }
case "$svc $op" in
"resourcegroupstaggingapi get-resources")
`)
		var rgta []string
		if w.bucket != "" {
			rgta = append(rgta, fmt.Sprintf(`{"ResourceARN":%q,"Tags":[{"Key":"tofu-estate","Value":%q}]}`, w.bucket, w.estate))
		}
		if w.rgtaServesIAM {
			for arn, est := range w.policies {
				if est == w.estate {
					rgta = append(rgta, fmt.Sprintf(`{"ResourceARN":%q,"Tags":[{"Key":"tofu-estate","Value":%q}]}`, arn, est))
				}
			}
		}
		fmt.Fprintf(&b, "  cat <<'J'\n{\"ResourceTagMappingList\":[%s]}\nJ\n  ;;\n", strings.Join(rgta, ","))

		var pols []string
		for arn := range w.policies {
			pols = append(pols, fmt.Sprintf(`{"Arn":%q}`, arn))
		}
		fmt.Fprintf(&b, "\"iam list-policies\")\n  cat <<'J'\n{\"Policies\":[%s]}\nJ\n  ;;\n", strings.Join(pols, ","))

		b.WriteString("\"iam list-policy-tags\")\n  case \"$(argval --policy-arn \"$@\")\" in\n")
		for arn, est := range w.policies {
			fmt.Fprintf(&b, "  %q) cat <<'J'\n%s\nJ\n  ;;\n", arn, tagsJSON(est))
		}
		b.WriteString("  *) printf 'NoSuchEntity\\n' >&2; exit 254 ;;\n  esac\n  ;;\n")

		var roles []string
		for name := range w.roles {
			roles = append(roles, fmt.Sprintf(`{"RoleName":%q,"Arn":"arn:aws:iam::000000000000:role/%s"}`, name, name))
		}
		fmt.Fprintf(&b, "\"iam list-roles\")\n  cat <<'J'\n{\"Roles\":[%s]}\nJ\n  ;;\n", strings.Join(roles, ","))
		b.WriteString("\"iam list-role-tags\")\n  case \"$(argval --role-name \"$@\")\" in\n")
		for name, est := range w.roles {
			fmt.Fprintf(&b, "  %q) cat <<'J'\n%s\nJ\n  ;;\n", name, tagsJSON(est))
		}
		b.WriteString("  *) printf 'NoSuchEntity\\n' >&2; exit 254 ;;\n  esac\n  ;;\n")

		var profs []string
		for name := range w.profiles {
			profs = append(profs, fmt.Sprintf(`{"InstanceProfileName":%q,"Arn":"arn:aws:iam::000000000000:instance-profile/%s"}`, name, name))
		}
		fmt.Fprintf(&b, "\"iam list-instance-profiles\")\n  cat <<'J'\n{\"InstanceProfiles\":[%s]}\nJ\n  ;;\n", strings.Join(profs, ","))
		b.WriteString("\"iam list-instance-profile-tags\")\n  case \"$(argval --instance-profile-name \"$@\")\" in\n")
		for name, est := range w.profiles {
			fmt.Fprintf(&b, "  %q) cat <<'J'\n%s\nJ\n  ;;\n", name, tagsJSON(est))
		}
		b.WriteString("  *) printf 'NoSuchEntity\\n' >&2; exit 254 ;;\n  esac\n  ;;\n")

		var users []string
		for name := range w.users {
			users = append(users, fmt.Sprintf(`{"UserName":%q,"Arn":"arn:aws:iam::000000000000:user/hm/%s"}`, name, name))
		}
		fmt.Fprintf(&b, "\"iam list-users\")\n  cat <<'J'\n{\"Users\":[%s]}\nJ\n  ;;\n", strings.Join(users, ","))
		b.WriteString("\"iam list-user-tags\")\n  case \"$(argval --user-name \"$@\")\" in\n")
		for name, est := range w.users {
			fmt.Fprintf(&b, "  %q) cat <<'J'\n%s\nJ\n  ;;\n", name, tagsJSON(est))
		}
		b.WriteString("  *) printf 'NoSuchEntity\\n' >&2; exit 254 ;;\n  esac\n  ;;\n")

		b.WriteString("*) printf 'the helper called an operation this stub does not serve: %s %s\\n' \"$svc\" \"$op\" >&2; exit 253 ;;\nesac\n")
	}
	path := filepath.Join(t.TempDir(), "awsstub")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func gauntletLibPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("e2e", "lib", "gauntlet.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// runEstateObjects sources the real library, runs the real helper against
// the stub, and returns the globals it set - or the shell's exit status if
// the helper refused.
func runEstateObjects(t *testing.T, stub, estate string) (n, rgta, iam, both int, arns string, err error) {
	t.Helper()
	out, runErr := runBashScript(t, fmt.Sprintf(`
set -uo pipefail
source %q
gauntlet_estate_objects %q %q || exit 9
printf 'N=%%s RGTA=%%s IAM=%%s BOTH=%%s\n' "$GAUNTLET_ESTATE_N" "$GAUNTLET_ESTATE_RGTA_N" "$GAUNTLET_ESTATE_IAM_N" "$GAUNTLET_ESTATE_BOTH_N"
printf 'ARNS=%%s\n' "$(tr '\n' ',' <<< "$GAUNTLET_ESTATE_ARNS")"
`, gauntletLibPath(t), estate, stub))
	if runErr != nil {
		return 0, 0, 0, 0, out, fmt.Errorf("%w: %s", runErr, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "N=") {
			if _, e := fmt.Sscanf(line, "N=%d RGTA=%d IAM=%d BOTH=%d", &n, &rgta, &iam, &both); e != nil {
				t.Fatalf("could not parse the helper's own report %q from:\n%s", line, out)
			}
		}
		if strings.HasPrefix(line, "ARNS=") {
			arns = strings.TrimPrefix(line, "ARNS=")
		}
	}
	return n, rgta, iam, both, arns, nil
}

func requireShellTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"bash", "jq", "awk"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on PATH; this proof runs the real shell helper and must not be skipped (a skipping guard is permanently green)", bin)
		}
	}
}

const (
	polA = "arn:aws:iam::000000000000:policy/example_from_data_source"
	polB = "arn:aws:iam::000000000000:policy/example-20260917"
	est  = "iam-policy-crossing"
)

// TestGauntletEstateObjectsSeesWhatGetResourcesCannot is #1271's red proof.
// The BREAK arm runs the idiom being replaced against the same stub and
// watches it report 0 for a fully marked estate: if the stub stopped
// reproducing that, the green arm below would mean nothing.
func TestGauntletEstateObjectsSeesWhatGetResourcesCannot(t *testing.T) {
	requireShellTools(t)
	marked := stubWorld{estate: est, policies: map[string]string{polA: est, polB: est}}

	// BREAK arm: `gauntlet_tagged_count ... resourcegroupstaggingapi
	// get-resources`, the exact call all three sites made, against an
	// estate whose every object IS marked. It reads 0.
	stub := writeAWSStub(t, marked)
	out, err := runBashScript(t, fmt.Sprintf(`
set -uo pipefail
source %q
N="$(gauntlet_tagged_count %q resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=%s")"
printf 'old=%%s\n' "$N"
if [ "$N" = "0" ]; then printf 'verdict=confirmed-unmarked\n'; fi
`, gauntletLibPath(t), stub, est))
	if err != nil {
		t.Fatalf("break arm: %v\n%s", err, out)
	}
	if !strings.Contains(out, "old=0") {
		t.Fatalf("the stub no longer reproduces #1271: GetResources is supposed to return nothing for IAM on this pin, so the old idiom must read 0:\n%s", out)
	}
	if !strings.Contains(out, "verdict=confirmed-unmarked") {
		t.Fatalf("the break arm no longer reaches the conclusion the old script drew (\"0 objects carry the estate tag, good\") on a fully marked estate:\n%s", out)
	}

	// GREEN arm: the helper, same stub, same account. It reads 2 - and
	// says that GetResources contributed none of them.
	n, rgta, iam, both, arns, err := runEstateObjects(t, stub, est)
	if err != nil {
		t.Fatalf("green arm: %v", err)
	}
	if n != 2 || rgta != 0 || iam != 2 || both != 0 {
		t.Errorf("gauntlet_estate_objects reported N=%d RGTA=%d IAM=%d BOTH=%d; want 2/0/2/0 on the current pin (both policies marked, GetResources blind to IAM). ARNs: %s", n, rgta, iam, both, arns)
	}
	for _, want := range []string{polA, polB} {
		if !strings.Contains(arns, want) {
			t.Errorf("gauntlet_estate_objects did not return %s; it returned %s", want, arns)
		}
	}
}

// TestGauntletEstateObjectsIsRedWhenAPolicyIsGenuinelyUnmarked is the arm
// that matters: the count has to move when the world does. The old call
// returned 0 here too, which is why "expected 2, got 0" and "0 before, 0
// after" were the same non-measurement wearing two faces.
func TestGauntletEstateObjectsIsRedWhenAPolicyIsGenuinelyUnmarked(t *testing.T) {
	requireShellTools(t)
	for _, tc := range []struct {
		name  string
		world stubWorld
		wantN int
	}{
		{"both marked", stubWorld{estate: est, policies: map[string]string{polA: est, polB: est}}, 2},
		{"one unmarked", stubWorld{estate: est, policies: map[string]string{polA: est, polB: ""}}, 1},
		{"one marked for another estate", stubWorld{estate: est, policies: map[string]string{polA: est, polB: "someone-elses-estate"}}, 1},
		{"none marked", stubWorld{estate: est, policies: map[string]string{polA: "", polB: ""}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, _, _, arns, err := runEstateObjects(t, writeAWSStub(t, tc.world), est)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if n != tc.wantN {
				t.Errorf("gauntlet_estate_objects counted %d objects, want %d. ARNs: %s", n, tc.wantN, arns)
			}
		})
	}

	// And the zero case really is a zero, not a refusal. The first draft of
	// the helper got this wrong - grep exits 1 when it prints nothing and
	// pipefail turned an empty estate into an error - which would have
	// turned cold_deploy's "nothing is marked yet" assertion, the one case
	// that legitimately expects 0, into a permanent failure.
	n, _, _, _, _, err := runEstateObjects(t, writeAWSStub(t, stubWorld{estate: est, policies: map[string]string{polA: ""}}), "an-estate-nothing-carries")
	if err != nil {
		t.Fatalf("an estate with no objects must report 0, not refuse: %v", err)
	}
	if n != 0 {
		t.Errorf("an estate with no objects reported N=%d, want 0", n)
	}
}

// TestGauntletEstateObjectsCountIsTheSameAfterFloci206 is the caution this
// fix was handed: lex00/floci#206 (#1152) makes GetResources serve
// iam:policy, and an assertion that changes meaning the moment the image is
// repinned is not worth writing. The union is deduplicated by ARN, so the
// total does not move; only the split does, and GAUNTLET_ESTATE_BOTH_N is
// what names which world the run happened in.
func TestGauntletEstateObjectsCountIsTheSameAfterFloci206(t *testing.T) {
	requireShellTools(t)
	pols := map[string]string{polA: est, polB: est}

	before, bRGTA, bIAM, bBoth, _, err := runEstateObjects(t,
		writeAWSStub(t, stubWorld{estate: est, policies: pols}), est)
	if err != nil {
		t.Fatalf("current pin: %v", err)
	}
	after, aRGTA, aIAM, aBoth, arns, err := runEstateObjects(t,
		writeAWSStub(t, stubWorld{estate: est, policies: pols, rgtaServesIAM: true}), est)
	if err != nil {
		t.Fatalf("post-#1152 pin: %v", err)
	}

	if before != after {
		t.Errorf("the count moved when GetResources started serving iam:policy: %d -> %d. The union is supposed to deduplicate by ARN so an assertion written against it means the same thing on both pins. ARNs after: %s", before, after, arns)
	}
	if after != 2 {
		t.Errorf("post-#1152 count is %d, want 2 - the two policies counted once each, not twice", after)
	}
	if bRGTA != 0 || bBoth != 0 {
		t.Errorf("on the current pin GetResources must contribute nothing for an IAM-only estate; got RGTA=%d BOTH=%d", bRGTA, bBoth)
	}
	if aRGTA != 2 || aBoth != 2 {
		t.Errorf("after #1152 both routes must return both policies; got RGTA=%d BOTH=%d", aRGTA, aBoth)
	}
	if bIAM != 2 || aIAM != 2 {
		t.Errorf("IAM's own tag APIs answer the same on both pins; got %d then %d", bIAM, aIAM)
	}
}

// TestGauntletEstateObjectsReadsRolesAndInstanceProfiles covers the other
// two IAM types the native leg claims, so the claim is tested rather than
// documented. Both were probed on the same container as the policies.
func TestGauntletEstateObjectsReadsRolesAndInstanceProfiles(t *testing.T) {
	requireShellTools(t)
	n, rgta, iam, _, arns, err := runEstateObjects(t, writeAWSStub(t, stubWorld{
		estate:   est,
		policies: map[string]string{polA: est},
		roles:    map[string]string{"probe-role": est, "unrelated-role": ""},
		profiles: map[string]string{"probe-ip": est},
		bucket:   "arn:aws:s3:::probe-bucket-1271",
	}), est)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n != 4 || rgta != 1 || iam != 3 {
		t.Errorf("counted N=%d RGTA=%d IAM=%d; want 4/1/3 (one bucket through GetResources; one policy, one role and one instance profile through IAM's own tag APIs, with the untagged role excluded). ARNs: %s", n, rgta, iam, arns)
	}
	for _, want := range []string{"probe-role", "probe-ip", "probe-bucket-1271"} {
		if !strings.Contains(arns, want) {
			t.Errorf("%s is missing from %s", want, arns)
		}
	}
	if strings.Contains(arns, "unrelated-role") {
		t.Errorf("a role carrying no tofu-estate tag was counted: %s", arns)
	}
}

// TestGauntletEstateObjectsReadsIAMUsers is #1549's red proof, and the
// fourth leg's own. gauntlet_estate_objects covered policies, roles and
// instance profiles; its comment said in as many words that an estate
// holding an IAM user "will otherwise be undercounted the same way this
// issue describes". corpus-hongbomiao-harbor holds exactly one, and was.
//
// The world below is a recording of that estate's greenfield container -
// ghcr.io/lex00/floci@sha256:6c3d5c2d, us-west-2, read on 2026-09-22 with
// the AWS CLI and no tofu in the loop. The bucket and the user both carry
// tofu-estate=hongbomiao-harbor-greenfield; `iam list-user-tags` returns
// the pair for the user, and GetResources returns ONLY the bucket -
// filtered on the tag, unfiltered, and under --resource-type-filters iam
// alike.
func TestGauntletEstateObjectsReadsIAMUsers(t *testing.T) {
	requireShellTools(t)
	world := stubWorld{
		estate: est,
		bucket: "arn:aws:s3:::probe-bucket-1549",
		users:  map[string]string{"probe-user": est, "unrelated-user": ""},
	}
	stub := writeAWSStub(t, world)

	// BREAK arm first: the call both greenfield stages made. It reads 1 -
	// the bucket - with the user marked, and would read 1 with it unmarked
	// too, which is why "expected 2, got 1" was a wrong oracle rather than
	// a missing stamp.
	out, err := runBashScript(t, fmt.Sprintf(`
set -uo pipefail
source %q
N="$(gauntlet_tagged_count %q resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=%s")"
printf 'old=%%s\n' "$N"
`, gauntletLibPath(t), stub, est))
	if err != nil {
		t.Fatalf("break arm: %v\n%s", err, out)
	}
	if !strings.Contains(out, "old=1") {
		t.Fatalf("the stub no longer reproduces #1549: GetResources must return the bucket and not the user, so the call the greenfield stages made reads 1:\n%s", out)
	}
	unmarked := writeAWSStub(t, stubWorld{estate: est, bucket: world.bucket,
		users: map[string]string{"probe-user": "", "unrelated-user": ""}})
	out2, err := runBashScript(t, fmt.Sprintf(`
set -uo pipefail
source %q
N="$(gauntlet_tagged_count %q resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=%s")"
printf 'old=%%s\n' "$N"
`, gauntletLibPath(t), unmarked, est))
	if err != nil {
		t.Fatalf("break arm, unmarked: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "old=1") {
		t.Fatalf("the old call is supposed to read the SAME number whether the user carries the marker or not - that is the defect. It moved:\n%s", out2)
	}

	// GREEN arm: the helper sees both, and the unmarked user is excluded.
	n, rgta, iam, both, arns, err := runEstateObjects(t, stub, est)
	if err != nil {
		t.Fatalf("green arm: %v", err)
	}
	if n != 2 || rgta != 1 || iam != 1 || both != 0 {
		t.Errorf("counted N=%d RGTA=%d IAM=%d BOTH=%d; want 2/1/1/0 (the bucket through GetResources, the user through iam:ListUserTags). ARNs: %s", n, rgta, iam, both, arns)
	}
	if !strings.Contains(arns, "probe-user") {
		t.Errorf("the marked IAM user is missing from %s", arns)
	}
	if strings.Contains(arns, "unrelated-user") {
		t.Errorf("a user carrying no tofu-estate tag was counted: %s", arns)
	}

	// And it moves when the world does, which the old call did not.
	n2, _, _, _, arns2, err := runEstateObjects(t, unmarked, est)
	if err != nil {
		t.Fatalf("unmarked arm: %v", err)
	}
	if n2 != 1 {
		t.Errorf("with the user unmarked the helper counted %d, want 1 (the bucket alone). ARNs: %s", n2, arns2)
	}
}

// TestGauntletEstateObjectsRefusesAnUnreachableTarget: the idiom this
// replaces ended in `2>/dev/null || echo 0`, so a target that could not be
// reached read back as "0 objects carry the estate tag" and every one of
// the three assertions was happy with that. A refusal stops a human; a
// silent zero does not.
func TestGauntletEstateObjectsRefusesAnUnreachableTarget(t *testing.T) {
	requireShellTools(t)
	if _, _, _, _, out, err := runEstateObjects(t, writeAWSStub(t, stubWorld{broken: true}), est); err == nil {
		t.Errorf("gauntlet_estate_objects returned success against a target every call fails on; it must refuse:\n%s", out)
	}
}

// TestGauntletEstateObjectsIsDocumentedWhereItLives keeps the two facts a
// caller cannot guess next to the function: that it sets globals rather
// than printing (a command substitution loses them), and that the count is
// pin-independent while the RGTA split is not.
func TestGauntletEstateObjectsIsDocumentedWhereItLives(t *testing.T) {
	src := readScript(t, "e2e/lib/gauntlet.sh")
	if !strings.Contains(src, "gauntlet_estate_objects() {") {
		t.Fatal("live/e2e/lib/gauntlet.sh does not define gauntlet_estate_objects")
	}
	head := src[:strings.Index(src, "gauntlet_estate_objects() {")]
	for _, want := range []string{
		"#1271",
		"#1152",
		"SETS GLOBALS",
		"GAUNTLET_ESTATE_BOTH_N",
		// The boundary the comment draws, which moved with #1549: users
		// are covered now, groups and the provider objects are not. The
		// string this replaced was "NOT IAM users".
		"NOT\n# IAM groups",
		"IAM users",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("gauntlet_estate_objects's comment does not mention %q", want)
		}
	}
}

// TestCorpusIAMPolicyCountsThroughTheIAMRoute pins the fix in the script
// itself. All three sites #1271 names have to be off the Tagging API, and
// each has to keep a control that can make its own count fail - not just
// the comparison wrapped around it, which is how BREAK_GREEN went red for
// years while the number it compared was structurally 0.
func TestCorpusIAMPolicyCountsThroughTheIAMRoute(t *testing.T) {
	full := readScript(t, "e2e/corpus-iam-policy/run.sh")
	// Comments are stripped before the code checks below: the fix's own
	// comments name the call they replace, and a guard that cannot tell a
	// comment from a call would forbid explaining itself. Run once with the
	// stripping removed and every check here goes red on prose alone.
	var code []string
	for _, line := range strings.Split(full, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		code = append(code, line)
	}
	src := strings.Join(code, "\n")
	if strings.Contains(src, "gauntlet_tagged_count") {
		t.Errorf("live/e2e/corpus-iam-policy/run.sh still counts through gauntlet_tagged_count (resourcegroupstaggingapi get-resources), which returns 0 for every aws_iam_policy on this target (#1271)")
	}
	if n := strings.Count(src, "gauntlet_estate_objects "); n < 3 {
		t.Errorf("the script calls gauntlet_estate_objects %d times; #1271 names three call sites (cold_deploy, greenfield PART 6, test_apply)", n)
	}
	for _, control := range []string{"BREAK_UNMARKED", "BREAK_UNMARK", "BREAK_NOOP"} {
		if strings.Count(full, control) < 2 || !strings.Contains(src, control) {
			t.Errorf("the %s control is not both documented and implemented in live/e2e/corpus-iam-policy/run.sh", control)
		}
	}
	// The `|| echo 0` tail turned an unreachable endpoint into a pass.
	if strings.Contains(src, "|| echo 0") {
		t.Errorf("live/e2e/corpus-iam-policy/run.sh still swallows a failed inventory read into a literal 0 (#1271)")
	}
}
