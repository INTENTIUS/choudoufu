// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// The gcp lane's later stages, checked offline before the next run. Four
// GCP runs each stopped at their first failing stage, so day2_replace,
// day2_crash, day2_teardown, greenfield, no_local_state and strict never
// ran on gcve, and nothing after migrate ever ran on reference-gcp. These
// tests hold the helpers those stages lean on (live/e2e/lib/gcp.sh and the
// estates' own gcp.sh) to reconstructed output in the real format, and the
// two scripts to running every stage after test_plan in a subshell, so a
// failed stage no longer ends the run.

// gcpBash runs body under bash with the gauntlet and gcp libraries sourced.
// The runner's "run" prompt fires only when an estate's run.sh sources the
// library, never for bash -c, so nothing here stands in for a person.
func gcpBash(t *testing.T, env []string, body string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -uo pipefail; source e2e/lib/gauntlet.sh; source e2e/lib/gcp.sh; "+body)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out), code
}

// A script's fail(), as both gcp scripts spell it.
const gcpFail = `CURRENT_STAGE=""
fail() { printf 'FAIL: %s\n' "$*" >&2; if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi; exit 1; }
`

func TestGCPStageIsolationGoesOnPastAFailedStage(t *testing.T) {
	body := gcpFail + `W="$(mktemp -d)"
gcp_fail_flag "$W/failed"
gauntlet_begin
conv() { echo RECONVERGED; }
( gauntlet_begin_stage day2_count; false || fail "the scale-up plan is not exactly one add"; gauntlet_stage day2_count pass unreached ); gcp_stage_done $? day2_count conv
( gauntlet_begin_stage day2_replace; gauntlet_stage day2_replace pass replaced ); gcp_stage_done $? day2_replace conv
gauntlet_end
gcp_exit_status
`
	out, code := gcpBash(t, nil, body)
	for _, want := range []string{
		"GAUNTLET stage=day2_count verdict=fail",
		"RECONVERGED",
		"GAUNTLET stage=day2_replace verdict=pass",
		"GAUNTLET end=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "unreached") {
		t.Errorf("the failed stage ran past its fail():\n%s", out)
	}
	if n := strings.Count(out, "RECONVERGED"); n != 1 {
		t.Errorf("reconverged %d times, want once (after the failed stage only):\n%s", n, out)
	}
	if code != 1 {
		t.Errorf("exit %d, want 1: a stage failed", code)
	}
}

// The same two stages with fail() outside a subshell, as the scripts ran
// before: the run ends at the first failure, which is what four GCP runs
// did. Kept so the test above is seen to measure the difference.
func TestGCPStageWithoutIsolationEndsTheRun(t *testing.T) {
	body := gcpFail + `gauntlet_begin
gauntlet_begin_stage day2_count; false || fail "the scale-up plan is not exactly one add"
gauntlet_begin_stage day2_replace; gauntlet_stage day2_replace pass replaced
`
	out, _ := gcpBash(t, nil, body)
	if strings.Contains(out, "stage=day2_replace") {
		t.Fatalf("without the subshell the run went on, so the isolation test proves nothing:\n%s", out)
	}
}

func TestGCPExitStatusCountsARecordedFail(t *testing.T) {
	for _, tc := range []struct {
		name, stages string
		want         int
	}{
		{"all pass", `gauntlet_stage strict pass ok`, 0},
		// day2_crash and no_local_state record fail and go on without
		// exiting; the run must still end non-zero.
		{"recorded fail, no exit", `( gauntlet_stage day2_crash fail "not the remainder"; true ); gcp_stage_done $? day2_crash; gauntlet_stage strict pass ok`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := gcpBash(t, nil, `W="$(mktemp -d)"; gcp_fail_flag "$W/failed"; gauntlet_begin; `+tc.stages+`; gauntlet_end; gcp_exit_status`)
			if code != tc.want {
				t.Errorf("exit %d, want %d:\n%s", code, tc.want, out)
			}
		})
	}
}

// A stage after the first failure ran on an estate that failure may have
// left broken, so its verdict says which stage failed first; the failing
// stage and the stages before it carry nothing.
func TestGCPStageAfterAFailSaysSo(t *testing.T) {
	out, _ := gcpBash(t, nil, `W="$(mktemp -d)"; gcp_fail_flag "$W/failed"; gauntlet_begin
gauntlet_stage day2_remove pass removed
( gauntlet_stage day2_count fail "the scale-up plan is not exactly one add"; true ); gcp_stage_done $? day2_count
gauntlet_stage day2_replace pass replaced
( gauntlet_stage day2_crash fail "not the remainder"; true ); gcp_stage_done $? day2_crash
gauntlet_stage strict pass
gauntlet_end`)
	for _, c := range []struct{ stage, want string }{
		{"day2_remove", "detail=removed\n"},
		{"day2_count", "detail=the scale-up plan is not exactly one add\n"},
		{"day2_replace", "detail=replaced after_fail=day2_count\n"},
		{"day2_crash", "detail=not the remainder after_fail=day2_count\n"},
		{"strict", "detail=after_fail=day2_count\n"},
	} {
		line := ""
		for _, l := range strings.SplitAfter(out, "\n") {
			if strings.HasPrefix(l, "GAUNTLET stage="+c.stage+" ") {
				line = l
			}
		}
		if !strings.HasSuffix(line, c.want) {
			t.Errorf("%s: verdict line %q, want it to end %q", c.stage, line, c.want)
		}
	}
}

// greenfield names what its apply did not create. The apply lines are
// terraform's own -no-color format, a for_each key's quotes and a module
// path included.
func TestGCPNotCreatedNamesTheMissingAddresses(t *testing.T) {
	dir := t.TempDir()
	expected := filepath.Join(dir, "expected")
	if err := os.WriteFile(expected, []byte(strings.Join([]string{
		`google_compute_network.maintenance-vpc`,
		`google_project_service.project["compute.googleapis.com"]`,
		`google_project_service.project["essentialcontacts.googleapis.com"]`,
		`module.datadog_bucket.google_storage_bucket.bucket`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	apply := `google_compute_network.maintenance-vpc: Creating...
google_compute_network.maintenance-vpc: Creation complete after 1s [id=projects/broadcom-451918/global/networks/maintenance-vpc-network]
google_project_service.project["compute.googleapis.com"]: Creating...
google_project_service.project["compute.googleapis.com"]: Creation complete after 0s [id=broadcom-451918/compute.googleapis.com]
module.datadog_bucket.google_storage_bucket.bucket: Creation complete after 0s [id=k8s-datadog-billing-analysis-broadcom]

Apply complete! Resources: 3 added, 0 changed, 0 destroyed.
`
	out, code := gcpBash(t, []string{"EXP=" + expected, "APPLY=" + apply}, `gcp_not_created "$EXP" <<< "$APPLY"`)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got, want := strings.TrimSpace(out), `google_project_service.project["essentialcontacts.googleapis.com"]`; got != want {
		t.Errorf("not created = %q, want %q", got, want)
	}
}

// serviceUsage serves GET /v1/projects/<p>/services/<svc> the way
// floci-gcp's ServiceUsageController does, with each service's state from
// states (ENABLED when unnamed).
func serviceUsage(t *testing.T, states map[string]string) *httptest.Server {
	t.Helper()
	path := regexp.MustCompile(`^/v1/projects/([^/]+)/services/([^/]+)$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := path.FindStringSubmatch(r.URL.Path)
		if m == nil || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		state := states[m[2]]
		if state == "" {
			state = "ENABLED"
		}
		fmt.Fprintf(w, `{"name":"projects/123456789/services/%s","parent":"projects/123456789","state":%q}`, m[2], state)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gcve's day2_teardown asserted that none of the five services read
// ENABLED after the destroy. Stock's destroy leaves them ENABLED, because
// the root leaves disable_on_destroy unset and hashicorp/google 8.6.0's
// Delete then never calls the API (resource_google_project_service.go:
// 334-338), so the old check failed a destroy that did what stock does. The
// check now compares each service with stock's oracle after both destroys.
func TestGCVETeardownServicesFollowStocksOracle(t *testing.T) {
	const lib = `source e2e/corpus-k8s-infra-gcp-gcve/gcp.sh; export GOOGLE_OAUTH_ACCESS_TOKEN=chdf-test-fake-token; `
	// What both emulators hold after both destroys: every service ENABLED.
	a := serviceUsage(t, nil)
	b := serviceUsage(t, nil)
	env := []string{"EPA=" + a.URL, "EPB=" + b.URL}

	// The old gate, verbatim but for fail(): red on that world.
	oldGate := lib + `svc_a() { gcve_service_state "$EPA" broadcom-451918 "$1"; }
for svc in cloudasset compute essentialcontacts secretmanager vmwareengine; do
  [ "$(svc_a "$svc.googleapis.com")" = "ENABLED" ] && echo "OLD GATE FAILS: $svc.googleapis.com is still ENABLED"
done; true`
	if out, _ := gcpBash(t, env, oldGate); !strings.Contains(out, "OLD GATE FAILS") {
		t.Fatalf("the old gate passed on a world where stock's destroy left every service ENABLED, so this test shows nothing:\n%s", out)
	}

	// The new one passes there.
	out, code := gcpBash(t, env, lib+`printf '[%s]' "$(gcve_service_mismatches "$EPA" "$EPB" broadcom-451918)"`)
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("gcve_service_mismatches with A and B agreeing = %q (exit %d), want []", out, code)
	}

	// And fails when choudoufu's destroy leaves a service other than stock's
	// did.
	bDisabled := serviceUsage(t, map[string]string{"essentialcontacts.googleapis.com": "DISABLED"})
	out, _ = gcpBash(t, []string{"EPA=" + a.URL, "EPB=" + bDisabled.URL}, lib+`gcve_service_mismatches "$EPA" "$EPB" broadcom-451918`)
	if want := "essentialcontacts.googleapis.com (A=ENABLED, B=DISABLED)"; strings.TrimSpace(out) != want {
		t.Errorf("gcve_service_mismatches = %q, want %q", out, want)
	}

	// An unreadable emulator is a mismatch, never agreement.
	out, _ = gcpBash(t, []string{"EPA=http://127.0.0.1:1", "EPB=http://127.0.0.1:1"}, lib+`gcve_service_mismatches "$EPA" "$EPB" broadcom-451918`)
	if !strings.Contains(out, "A=unread, B=unread") {
		t.Errorf("two unreadable emulators read as agreeing: %q", out)
	}
}

// Every stage after test_plan, in both gcp scripts, opens inside a
// subshell its own stage_done closes, so its fail() ends that stage only.
// Red against the scripts at f6af345bb7, where fail() ended the run.
func TestGCPScriptsIsolateEveryLaterStage(t *testing.T) {
	later := []string{"test_apply", "drift_reconverge", "plan_approval", "day2_rename", "day2_remove", "day2_count",
		"day2_replace", "day2_crash", "day2_teardown", "greenfield", "no_local_state", "strict"}
	for _, script := range []string{"e2e/reference-gcp/run.sh", "e2e/corpus-k8s-infra-gcp-gcve/run.sh"} {
		raw, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		for _, id := range later {
			begin, open, closeAt := -1, -1, -1
			for i, l := range lines {
				if l == "gauntlet_begin_stage "+id {
					begin = i
				}
			}
			if begin < 0 {
				t.Errorf("%s: no gauntlet_begin_stage %s", script, id)
				continue
			}
			for i := begin; i >= 0; i-- {
				if lines[i] == "(" {
					open = i
					break
				}
				if strings.HasPrefix(lines[i], "); stage_done ") {
					break
				}
			}
			for i := begin; i < len(lines); i++ {
				if strings.HasPrefix(lines[i], "); stage_done ") {
					if strings.HasPrefix(lines[i], "); stage_done $? "+id) {
						closeAt = i
					}
					break
				}
			}
			if open < 0 || closeAt < 0 {
				t.Errorf("%s: stage %s does not run in its own subshell closed by \"); stage_done $? %s\", so a fail() in it ends the run", script, id, id)
			}
		}
		if !strings.Contains(string(raw), "\ngcp_fail_flag \"$WORK/stage-failed\"\n") || !strings.Contains(string(raw), "\ngcp_exit_status\n") {
			t.Errorf("%s: does not track failed stages (gcp_fail_flag) and exit on them (gcp_exit_status)", script)
		}
	}
}

// no_local_state counts the plan's creates, destroys and replaces. A
// replace's header in terraform's -no-color plan says "must be replaced",
// so the pattern both gcp scripts used, which looked for "will be
// replaced", counted a replace as no churn at all.
func TestGCPChurnCountSeesAReplace(t *testing.T) {
	plan := `OpenTofu will perform the following actions:

  # google_compute_firewall.ssh must be replaced
-/+ resource "google_compute_firewall" "ssh" {
      ~ name = "refgcp-ssh" -> "refgcp-ssh-2" # forces replacement
    }

  # google_pubsub_topic.events will be updated in-place
  ~ resource "google_pubsub_topic" "events" {
    }

  # data.google_project.project will be read during apply
 <= data "google_project" "project" {
    }

Plan: 1 to add, 1 to change, 1 to destroy.
`
	old := `grep -cE '^[[:space:]]*# .* will be (created|destroyed|replaced)' <<< "$PLAN" || true`
	if out, _ := gcpBash(t, []string{"PLAN=" + plan}, old); strings.TrimSpace(out) != "0" {
		t.Fatalf("the old pattern counted %q on a plan with one replace; this test assumes it counted 0", out)
	}
	out, code := gcpBash(t, []string{"PLAN=" + plan}, `gcp_churn_count <<< "$PLAN"`)
	if code != 0 || strings.TrimSpace(out) != "1" {
		t.Errorf("gcp_churn_count = %q (exit %d), want 1: the replace, not the update or the data read", out, code)
	}
	out, _ = gcpBash(t, []string{"PLAN=No changes. Your infrastructure matches the configuration."}, `gcp_churn_count <<< "$PLAN"`)
	if strings.TrimSpace(out) != "0" {
		t.Errorf("gcp_churn_count on an empty plan = %q, want 0", out)
	}
}

// gcveReplaceRemoval is day2_replace's scratch-block removal, cut out of
// the gcve script verbatim: scrub_replace, the trap that runs it, and the
// measured removal up to the stage's pass verdict.
func gcveReplaceRemoval(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("e2e/corpus-k8s-infra-gcp-gcve/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	cut := func(from, to string) string {
		i := strings.Index(src, from)
		if i < 0 {
			t.Fatalf("the gcve script has no %q", from)
		}
		j := strings.Index(src[i:], to)
		if j < 0 {
			t.Fatalf("the gcve script has no %q after %q", to, from)
		}
		return src[i : i+j+len(to)]
	}
	return cut("scrub_replace() {", "\ntrap scrub_replace EXIT\n") +
		cut("# The scratch block removed, as an operator removes one", "\ngauntlet_stage day2_replace pass ") + `"done"` + "\n"
}

// The fifth GCP run: removing day2_replace's scratch block failed on A
// with "Malformed ownership marker", the old cleanup threw the output away
// and left gcve-cbd-b live, and day2_crash, day2_teardown, greenfield and
// no_local_state each failed on that one bucket. The removal now shows
// choudoufu's error, requires the one destroy stock makes on B, requires
// neither scratch bucket on either emulator, and on any failure deletes
// both scratch buckets so no later stage inherits them.
func TestGCVEReplaceRemovalMatchesStockAndNeverLeaks(t *testing.T) {
	removal := gcveReplaceRemoval(t)
	const destroyed = "\nApply complete! Resources: 0 added, 0 changed, 1 destroyed.\n"
	// The run's own output for the removal apply on A.
	const malformed = `
Warning: GCP resource carries this estate's label and no declared address

1 resource(s) carry estate "corpus-k8s-infra-gcp-gcve"'s label and no address
this configuration declares:
  - storage.googleapis.com/Bucket //storage.googleapis.com/broadcom-451918-gcve-cbd-b (Its tofu-address label "gogeyxybcrnndkm3tyds4ju4pr-gogeyxybcrnndkm3tyds4ju4pr" encodes no address this configuration declares, and a GCP address label is matched, never decoded, so nothing here says which block made it. It is reported and left alone; the estate's record for that block, when there is one, is what proposes its removal.)

Error: Malformed ownership marker

A live storage.googleapis.com/Bucket carries estate
"corpus-k8s-infra-gcp-gcve" and the tofu-address value "", which no declared
instance matches and which the escaping rule in live/MARKERS.md cannot turn
back into an address. Nothing is proposed for it: give it a marker that
round-trips, or remove it. Live resources:
//storage.googleapis.com/broadcom-451918-gcve-cbd-b.
`
	run := func(xOut string, xRC int, aLeft, bLeft string) (string, int) {
		t.Helper()
		w := t.TempDir()
		body := gcpFail + `CURRENT_STAGE=day2_replace
W="` + w + `"; ADOPTED="$W/a"; ORACLE="$W/b"; mkdir -p "$ADOPTED" "$ORACLE"
: > "$ADOPTED/day2_replace.tf"; : > "$ORACLE/day2_replace.tf"
CBD_A=broadcom-451918-gcve-cbd-a; CBD_B=broadcom-451918-gcve-cbd-b
log() { printf '%s\n' "$*"; }
bucket_path() { printf 'storage/v1/b/%s' "$1"; }
live() { [ -f "$W/$1.$(basename "$2")" ]; }
for b in $A_LEFT; do : > "$W/A.$b"; done
for b in $B_LEFT; do : > "$W/B.$b"; done
exists_a() { live A "$1"; }
ra() { [ "$1" = DELETE ] && rm -f "$W/A.$(basename "$2")"; }
rb() { case "$1" in GET) live B "$2" ;; DELETE) rm -f "$W/B.$(basename "$2")" ;; esac; }
stock_b() { printf '%s' "$O_OUT"; for b in broadcom-451918-gcve-cbd-a broadcom-451918-gcve-cbd-b; do rm -f "$W/B.$b"; done; }
apply_a() { printf '%s' "$X_OUT"; return "$X_RC"; }
( ` + removal + ` )
echo "rc=$?"
echo "left on A: $(cd "$W" && ls A.* 2>/dev/null | tr '\n' ' ')"
echo "left on B: $(cd "$W" && ls B.* 2>/dev/null | tr '\n' ' ')"
[ -e "$ADOPTED/day2_replace.tf" ] && echo "the scratch block is still in the adopted root"
true`
		return gcpBash(t, []string{"O_OUT=" + destroyed, "X_OUT=" + xOut, fmt.Sprintf("X_RC=%d", xRC), "A_LEFT=" + aLeft, "B_LEFT=" + bLeft}, body)
	}

	// The run's sequence: choudoufu's removal refused, cbd-b still on A.
	out, _ := run(malformed, 1, "broadcom-451918-gcve-cbd-b", "broadcom-451918-gcve-cbd-b")
	for _, want := range []string{
		"FAIL: removing the scratch block from A failed: Error: Malformed ownership marker",
		"rc=1",
		"broadcom-451918-gcve-cbd-b deleted from A with the REST API",
		"left on A: \n",
		"left on B: \n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the run's refusal: output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "done") || strings.Contains(out, "still in the adopted root") {
		t.Errorf("the run's refusal passed, or left the block in the root:\n%s", out)
	}

	// A removal that proposes nothing (the hit only warned about) leaks the
	// bucket as surely, and fails.
	out, _ = run("\nNo changes. Your infrastructure matches the configuration.\n", 0, "broadcom-451918-gcve-cbd-b", "broadcom-451918-gcve-cbd-b")
	if !strings.Contains(out, "did not destroy exactly the one bucket stock destroyed on B") || !strings.Contains(out, "left on A: \n") {
		t.Errorf("a removal destroying nothing: want the fail and the scrub:\n%s", out)
	}

	// A destroy counted while the bucket stays readable is not a removal.
	out, _ = run(destroyed, 0, "broadcom-451918-gcve-cbd-b", "broadcom-451918-gcve-cbd-b")
	if !strings.Contains(out, "broadcom-451918-gcve-cbd-b still exists on A after the scratch block was removed") {
		t.Errorf("a removal leaving cbd-b live on A: want the fail:\n%s", out)
	}

	// What stock does on B, choudoufu does on A: one destroy, nothing left.
	out, _ = run(destroyed, 0, "", "broadcom-451918-gcve-cbd-b")
	if !strings.Contains(out, "done") || !strings.Contains(out, "rc=0") || strings.Contains(out, "FAIL") || strings.Contains(out, "REST API") {
		t.Errorf("a removal matching stock's oracle did not pass cleanly:\n%s", out)
	}
}

// refgcpSA24Plan is reference-gcp's test_plan output in runs 5 and 6, the
// whole diff lex00/floci-gcp#24 on the service account.
const refgcpSA24Plan = `OpenTofu used the selected providers to generate the following execution
plan. Resource actions are indicated with the following symbols:
  ~ update in-place

OpenTofu will perform the following actions:

  # google_service_account.app will be updated in-place
  ~ resource "google_service_account" "app" {
      - display_name    = "refgcp-app" -> null
        id              = "projects/chdf-refgcp/serviceAccounts/refgcp-app@chdf-refgcp.iam.gserviceaccount.com"
        name            = "projects/chdf-refgcp/serviceAccounts/refgcp-app@chdf-refgcp.iam.gserviceaccount.com"
        # (7 unchanged attributes hidden)
    }

Plan: 0 to add, 1 to change, 0 to destroy.
`

func TestRefGCPDisplayNameOnlyDiffIsExactlyFloci24(t *testing.T) {
	cases := map[string]struct {
		plan string
		want bool
	}{
		"run 6's test_plan":           {refgcpSA24Plan, true},
		"a second attribute changes":  {strings.Replace(refgcpSA24Plan, "        id ", "      ~ description     = \"a\" -> \"b\"\n        id ", 1), false},
		"display_name set to a value": {strings.Replace(refgcpSA24Plan, `-> null`, `-> "x"`, 1), false},
		"another resource":            {strings.ReplaceAll(refgcpSA24Plan, "google_service_account.app will", "google_pubsub_topic.events will"), false},
		"a second resource changes": {strings.Replace(strings.Replace(refgcpSA24Plan, "Plan: 0 to add, 1 to change", "Plan: 0 to add, 2 to change", 1),
			"    }\n\nPlan", "    }\n\n  # google_storage_bucket.logs will be updated in-place\n  ~ resource \"google_storage_bucket\" \"logs\" {\n      ~ labels = {}\n    }\n\nPlan", 1), false},
		"no changes": {"No changes. Your infrastructure matches the configuration.\n", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", "source e2e/reference-gcp/gcp.sh; refgcp_display_name_only_diff")
			cmd.Stdin = strings.NewReader(c.plan)
			err := cmd.Run()
			if got := err == nil; got != c.want {
				t.Errorf("refgcp_display_name_only_diff = %v, want %v (err %v)", got, c.want, err)
			}
		})
	}
}

// refgcpTestPlanStage is reference-gcp's test_plan stage as run.sh holds
// it, from its gauntlet_begin_stage to the adoption count after it.
func refgcpTestPlanStage(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("e2e/reference-gcp/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "\ngauntlet_begin_stage test_plan\n")
	j := strings.Index(s, "\n[ \"$(count_a)\" = \"6\" ] || fail")
	if i < 0 || j < i {
		t.Fatal("run.sh: test_plan stage not found")
	}
	return s[i:j]
}

// A test_plan whose only diff is #24 records fail naming #24, pins the
// display_name in both roots, and the run goes on to the later stages,
// which say after_fail=test_plan. Any other diff takes the adopting apply
// as before. Red against 8e4055c205, where #24 sent the run through the
// adopting apply, which the emulator fails, and that ended the run.
func TestRefGCPTestPlanGoesOnPastFloci24(t *testing.T) {
	for name, c := range map[string]struct {
		firstPlan, wantVerdict string
		wantPinned             bool
	}{
		"floci-gcp#24 only": {refgcpSA24Plan, "lex00/floci-gcp#24", true},
		"another diff":      {strings.Replace(refgcpSA24Plan, `-> null`, `-> "x"`, 1), "Plan: 0 to add, 1 to change, 0 to destroy", false},
	} {
		t.Run(name, func(t *testing.T) {
			w := t.TempDir()
			if err := os.WriteFile(filepath.Join(w, "plan1"), []byte(c.firstPlan), 0o600); err != nil {
				t.Fatal(err)
			}
			body := gcpFail + `source e2e/reference-gcp/gcp.sh
W="` + w + `"; ADOPTED="$W"; ORACLE="$W"; EPA=a; EPB=b; PROJECT=p; REGION=r; ZONE=z; BUCKET=bk
gcp_fail_flag "$W/failed"
gauntlet_begin
log() { printf '%s\n' "$*"; }
topic_path() { printf 'v1/projects/%s/topics/%s' "$PROJECT" "$1"; }
exists_a() { return 0; }
count_a() { echo 6; }
write_config() { printf 'write_config %s %s pin=[%s]\n' "$2" "$3" "$SA_DISPLAY_LINE"; touch "$W/rewritten"; }
plan_a() { if [ -e "$W/rewritten" ] || [ -e "$W/applied" ]; then echo "No changes."; else cat "$W/plan1"; fi; }
apply_a() { echo ADOPTING_APPLY >&2; touch "$W/applied"; }
SA_DISPLAY_LINE=""
CURRENT_STAGE=test_plan
` + refgcpTestPlanStage(t) + `
( gauntlet_begin_stage test_apply; gauntlet_stage test_apply pass later ); gcp_stage_done $? test_apply
gauntlet_end
echo REACHED_THE_END
`
			out, _ := gcpBash(t, nil, body)
			if !strings.Contains(out, "REACHED_THE_END") {
				t.Fatalf("the run ended at test_plan:\n%s", out)
			}
			if !regexp.MustCompile(`GAUNTLET stage=test_plan verdict=fail .*` + regexp.QuoteMeta(c.wantVerdict)).MatchString(out) {
				t.Errorf("test_plan's verdict does not record fail with %q:\n%s", c.wantVerdict, out)
			}
			if !strings.Contains(out, "GAUNTLET stage=test_apply verdict=pass") || !strings.Contains(out, "after_fail=test_plan") {
				t.Errorf("the later stage did not run with after_fail=test_plan:\n%s", out)
			}
			pinned := strings.Contains(out, `write_config live a pin=[  display_name = "refgcp-app"]`) && strings.Contains(out, `write_config stock b pin=[  display_name = "refgcp-app"]`)
			if pinned != c.wantPinned {
				t.Errorf("display_name pinned in both roots = %v, want %v:\n%s", pinned, c.wantPinned, out)
			}
			if adopted := strings.Contains(out, "ADOPTING_APPLY"); adopted == c.wantPinned {
				t.Errorf("adopting apply ran = %v, want %v:\n%s", adopted, !c.wantPinned, out)
			}
		})
	}
}

// gcveNoLocalStateRefusal is the shape of no_local_state's plan once the
// record store and the cache are deleted, under option A (#2152): the four
// markerless objects at held names refused in the real format, and the
// services and IAM, whose creates cannot collide, planned with a warning.
func gcveNoLocalStateRefusal(ids []string, extra string) string {
	var b strings.Builder
	for _, w := range []string{"google_project_service.project[\"compute.googleapis.com\"]", "google_project_iam_binding.projects_iam[\"roles/owner\"]"} {
		fmt.Fprintf(&b, "\nWarning: Unrecorded live object at a declared identity\n\nA live object exists at the identity %s declares.\n", w)
	}
	for _, id := range ids {
		fmt.Fprintf(&b, "\nError: Unlabelled live object holds the declared name\n\nA live object already exists at\n%q,\nthe identity it declares.\n", id)
	}
	return b.String() + extra
}

// The sixth GCP run hid no_local_state behind day2_rename. Traced against
// option A it deletes the record store and then planned for "No changes",
// which option A answers with a name-held refusal for every markerless
// object at a held name. The stage now requires exactly those refusals and
// nothing else, so a refused bucket (the stage's Break, its label stripped)
// or a missing firewall fails it.
func TestGCPNameHeldCheckWantsExactlyTheMarkerlessRefusals(t *testing.T) {
	const p = "broadcom-451918"
	ids := []string{
		"projects/" + p + "/global/networks/maintenance-vpc-network",
		"projects/" + p + "/regions/us-central1/subnetworks/maintenance-subnet",
		"projects/" + p + "/global/firewalls/maintenance-firewall-internet",
		"projects/" + p + "/global/firewalls/maintenance-firewall-internal",
	}
	check := func(plan string) string {
		t.Helper()
		f := filepath.Join(t.TempDir(), "cold.plan")
		if err := os.WriteFile(f, []byte(plan), 0o600); err != nil {
			t.Fatal(err)
		}
		args := `"` + strings.Join(ids, `" "`) + `"`
		out, code := gcpBash(t, nil, `gcp_name_held_check "`+f+`" `+args)
		if code != 0 {
			t.Fatalf("gcp_name_held_check exited %d: %s", code, out)
		}
		return out
	}
	if out := check(gcveNoLocalStateRefusal(ids, "")); out != "" {
		t.Errorf("exactly the four refusals: got %q, want nothing", out)
	}
	if out := check(gcveNoLocalStateRefusal(ids[:3], "")); !strings.Contains(out, "3 name-held refusal(s), want 4") || !strings.Contains(out, "no refusal names "+ids[3]) {
		t.Errorf("a missing firewall refusal: got %q", out)
	}
	bucket := "\nError: Unlabelled live object holds the declared name\n\nA live google_storage_bucket already exists at\n\"" + p + "-gcve-bucket\",\nthe identity it declares.\n"
	if out := check(gcveNoLocalStateRefusal(ids, bucket)); !strings.Contains(out, "5 name-held refusal(s), want 4") {
		t.Errorf("the Break's refused bucket: got %q", out)
	}
	other := "\nError: Malformed ownership marker\n\nsomething\n"
	if out := check(gcveNoLocalStateRefusal(ids, other)); !strings.Contains(out, "1 other error(s) (Error: Malformed ownership marker)") {
		t.Errorf("another error beside the refusals: got %q", out)
	}
	raw, err := os.ReadFile("e2e/corpus-k8s-infra-gcp-gcve/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"global/networks/maintenance-vpc-network", "regions/$REGION/subnetworks/maintenance-subnet", "global/firewalls/maintenance-firewall-internet", "global/firewalls/maintenance-firewall-internal"} {
		if !strings.Contains(string(raw), `"projects/$PROJECT/`+id+`"`) {
			t.Errorf("gcve's no_local_state does not require a refusal naming projects/$PROJECT/%s", id)
		}
	}
	if !strings.Contains(string(raw), `gcp_name_held_check "$NLS/cold.plan" "${NLS_HELD[@]}"`) {
		t.Error("gcve's no_local_state does not check the plan with no local state with gcp_name_held_check")
	}
}

// reference-gcp's no_local_state under option A: the plan with no record
// store refuses exactly the four markerless objects the greenfield root
// declares (network, subnetwork, firewall, service account) by the
// identity each refusal quotes. Red against 8e4055c205, where the stage
// expected the plan to be empty.
func TestRefGCPNoLocalStateWantsTheFourMarkerlessRefusals(t *testing.T) {
	raw, err := os.ReadFile("e2e/reference-gcp/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"global/networks/refgcp-net", "regions/$REGION/subnetworks/refgcp-subnet", "global/firewalls/refgcp-ssh", "serviceAccounts/refgcp-app@$PROJECT.iam.gserviceaccount.com"} {
		if !strings.Contains(string(raw), `"projects/$PROJECT/`+id+`"`) {
			t.Errorf("reference-gcp's no_local_state does not require a refusal naming projects/$PROJECT/%s", id)
		}
	}
	if !strings.Contains(string(raw), `gcp_name_held_check "$NLS/cold.plan" "${NLS_HELD[@]}"`) {
		t.Error("reference-gcp's no_local_state does not check the plan with no local state with gcp_name_held_check")
	}
	ids := []string{"projects/chdf-refgcp/global/networks/refgcp-net", "projects/chdf-refgcp/regions/us-central1/subnetworks/refgcp-subnet",
		"projects/chdf-refgcp/global/firewalls/refgcp-ssh", "projects/chdf-refgcp/serviceAccounts/refgcp-app@chdf-refgcp.iam.gserviceaccount.com"}
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "\nError: Unlabelled live object holds the declared name\n\nA live object already exists at\n%q, the identity it declares.\n", id)
	}
	f := filepath.Join(t.TempDir(), "cold.plan")
	if err := os.WriteFile(f, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _ := gcpBash(t, nil, `gcp_name_held_check "`+f+`" "`+strings.Join(ids, `" "`)+`"`); out != "" {
		t.Errorf("the four refusals in the real format do not satisfy the check: %q", out)
	}
}

// no_local_state puts the record store back after a plan with none. That
// plan recreates an empty .tofu-records (the local store makes its
// directory), and a bare `cp -R keep .tofu-records` then nests the copy
// inside it and leaves the store empty: the seventh GCP run failed both
// estates' no_local_state on exactly that. Each estate's own restore lines
// must leave the records at the top of the store.
func TestGCPNoLocalStateRestoresTheRecordsAtTheStoreRoot(t *testing.T) {
	for _, estate := range []string{"corpus-k8s-infra-gcp-gcve", "reference-gcp"} {
		t.Run(estate, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("e2e", estate, "run.sh"))
			if err != nil {
				t.Fatal(err)
			}
			s := string(raw)
			i := strings.Index(s, "# The record store put back and the cache still gone")
			j := strings.Index(s, `( cd "$GREEN" && TF_LOG=debug TF_LOG_PATH="$NLS/records.log"`)
			if i < 0 || j < i {
				t.Fatal("run.sh: no_local_state's record restore not found")
			}
			green, nls := t.TempDir(), t.TempDir()
			// What the record-less plan leaves: an empty store directory.
			if err := os.MkdirAll(filepath.Join(green, ".tofu-records"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(nls, "records.keep", "identity"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nls, "records.keep", "identity", "r.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			script := "fail() { echo \"FAIL: $*\" >&2; exit 1; }\nGREEN=" + green + "\nNLS=" + nls + "\n" + s[i:j]
			if out, err := exec.Command("bash", "-c", script).CombinedOutput(); err != nil {
				t.Fatalf("the restore failed: %v\n%s", err, out)
			}
			if _, err := os.Stat(filepath.Join(green, ".tofu-records", "identity", "r.json")); err != nil {
				t.Errorf("the restored record is not at the store root (%v); a nested copy leaves the store empty", err)
			}
		})
	}
}

// refgcpEndpointsStage is reference-gcp's day2_endpoints stage as run.sh
// holds it, from its gauntlet_begin_stage to the line before the subshell
// closes, and checks the stage runs in a subshell of its own.
func refgcpEndpointsStage(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("e2e/reference-gcp/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "\ngauntlet_begin_stage day2_endpoints\n")
	j := strings.Index(s, "\n); stage_done $? day2_endpoints\n")
	if i < 0 || j < i {
		t.Fatal("run.sh: no day2_endpoints stage closed by \"); stage_done $? day2_endpoints\"")
	}
	if open := strings.LastIndex(s[:i], "\n(\n"); open < 0 || strings.Contains(s[open:i], "); stage_done ") {
		t.Fatal("run.sh: day2_endpoints does not open its own subshell, so a fail() in it ends the run")
	}
	return s[i:j]
}

// TestRefGCPRunShParses: bash -n over the whole script, so a stage added
// to it cannot land with a syntax error the next emulator run finds first.
func TestRefGCPRunShParses(t *testing.T) {
	if out, err := exec.Command("bash", "-n", "e2e/reference-gcp/run.sh").CombinedOutput(); err != nil {
		t.Fatalf("bash -n e2e/reference-gcp/run.sh: %v\n%s", err, out)
	}
}

// day2_endpoints (#2145 item 6), stub-run offline. Every command the stage
// sends to an emulator or to a binary is a stub over files in a temporary
// directory: a NEG is a file per side, A's apply writes each endpoint's
// record, stock's import succeeds only for an id with the empty segment
// (the provider's importer regexes need five segments, one of them empty).
// The pass case passes cleanly; each other case breaks one gate and that
// gate's own verdict must read fail, with nothing left on either side.
func TestRefGCPEndpointsStageGates(t *testing.T) {
	stage := refgcpEndpointsStage(t)
	run := func(t *testing.T, env ...string) string {
		t.Helper()
		w := t.TempDir()
		body := gcpFail + `source e2e/reference-gcp/gcp.sh
W="` + w + `"; WORK="$W"; ADOPTED="$W/a"; ORACLE="$W/b"; PROJECT=p; REGION=r; ESTATE=reference-gcp; EPA=a; EPB=b
# The headline estate's roots: the stage must never touch them.
mkdir -p "$ADOPTED/.tofu-records" "$ORACLE"; echo '{}' > "$ADOPTED/.tofu-records/pre"; echo x > "$ORACLE/main.tf"
log() { printf '%s\n' "$*"; }
versions_block() { echo "# versions $1 $2 estate=$ESTATE"; }
gauntlet_locked_init() { return 0; }
NEGS="refgcp-gneg-ip refgcp-gneg-fqdn refgcp-rneg-ip refgcp-rneg-fqdn"
live() { [ -f "$W/$1.$(basename "$2")" ]; }
want_ep() { case "$1" in
  refgcp-gneg-ip) echo '{"ipAddress":"203.0.113.10","port":443}' ;;
  refgcp-gneg-fqdn) echo '{"fqdn":"backend.example.com","port":443}' ;;
  refgcp-rneg-ip) echo '{"ipAddress":"203.0.113.20","port":8080}' ;;
  refgcp-rneg-fqdn) echo '{"fqdn":"db.example.com","port":5432}' ;;
esac; }
rest() {
  local side="$1"; shift
  case "$1" in
    GET) live "$side" "$2" ;;
    DELETE) rm -f "$W/$side.$(basename "$2")" ;;
    POST) local n; n="$(basename "$(dirname "$2")")"; live "$side" "$n" || return 1
      if [ "${BAD_EP:-}" = "$side" ] && [ "$n" = refgcp-rneg-ip ]; then
        echo '{"items":[{"networkEndpoint":{"ipAddress":"203.0.113.20","port":8081}}]}'
      else
        echo '{"items":[{"networkEndpoint":'"$(want_ep "$n")"'}]}'
      fi ;;
  esac
}
ra() { rest A "$@"; }
rb() { rest B "$@"; }
exists_a() { live A "$1"; }
apply_side() { # $1 side, $2 root
  [ -f "$2/net.tf" ] && : > "$W/$1.refgcp-ep-net"
  if [ -f "$2/day2_endpoints.tf" ]; then
    if [ "$1" = B ] && [ -n "${B_MK_ERR:-}" ]; then printf '\nError: Error creating GlobalNetworkEndpointGroup: googleapi: Error 404: not found\n'; return 1; fi
    for n in $NEGS; do : > "$W/$1.$n"; done
    if [ "$1" = A ]; then
      mkdir -p "$2/.terraform" "$2/.tofu-records"; echo '{"cache":1}' > "$2/.terraform/choudoufu-cache.tfstate"
    fi
    if [ "$1" = A ] && [ -z "${NO_RECORD:-}" ]; then
      i=0
      for pair in "google_compute_global_network_endpoint.ip p/refgcp-gneg-ip/203.0.113.10//443" \
                  "google_compute_global_network_endpoint.fqdn p/refgcp-gneg-fqdn//backend.example.com/443" \
                  "google_compute_region_network_endpoint.ip p/r/refgcp-rneg-ip/203.0.113.20//8080" \
                  "google_compute_region_network_endpoint.fqdn ${RECORD_ID:-p/r/refgcp-rneg-fqdn//db.example.com/5432}"; do
        i=$((i + 1)); set -- $pair
        printf '{"format_version":2,"address":"%s","kind":"identity","identity":{"secondary_id":"%s"}}' "$1" "$2" > "$EP_A/.tofu-records/r$i"
      done
      set -- A
    fi
    local var="MK_N_$1"; printf '\nApply complete! Resources: %s added, 0 changed, 0 destroyed.\n' "${!var:-9}"
  else
    local leave="LEAVE_$1"
    [ -n "${!leave:-}" ] || for n in $NEGS; do rm -f "$W/$1.$n"; done
    local var="RM_N_$1"; printf '\nApply complete! Resources: 0 added, 0 changed, %s destroyed.\n' "${!var:-8}"
  fi
}
plan_side_a() {
  if [ ! -f "$EP_A/.terraform/choudoufu-cache.tfstate" ]; then printf '%s\n' "${NOSTATE_PLAN:-No changes.}"
  elif [ -f "$EP_A/day2_endpoints.tf" ]; then printf '%s\n' "${A_PLAN:-No changes.}"
  else printf '%s\n' "${LAST_PLAN:-No changes.}"; fi
}
tofu_stub() {
  case "$1" in
    init) return 0 ;;
    plan) plan_side_a ;;
    apply) if [ "${2:-}" = "-destroy" ]; then [ -n "${KEEP_NET_A:-}" ] || rm -f "$W/A.refgcp-ep-net"; else apply_side A "$PWD"; fi ;;
  esac
}
TOFU=tofu_stub
terraform() {
  case "$1" in
    init) return 0 ;;
    apply) apply_side B "$PWD" ;;
    destroy) rm -f "$W/B.refgcp-ep-net" ;;
    plan) printf '%s\n' "${B_PLAN:-No changes.}" ;;
    state) return 0 ;;
    import) local id="${*: -1}"; echo "import $id" >> "$W/imports"
      case "$id" in *//*) echo "Import successful!" ;; *) printf '\nError: Cannot import non-existent remote object\n'; return 1 ;; esac ;;
  esac
}
CURRENT_STAGE=""
( ` + stage + `
)
echo "rc=$?"
echo "left: $(cd "$W" && ls A.* B.* 2>/dev/null | tr '\n' ' ')"
for f in "$W"/endpoints-a/day2_endpoints.tf "$W"/endpoints-b/day2_endpoints.tf; do [ -e "$f" ] && [ -n "${PASS_CASE:-}" ] && echo "SCRATCH LEFT"; done
[ "$(ls "$ADOPTED/.tofu-records" | tr '\n' ' ')" = "pre " ] && [ "$(ls "$ADOPTED")" = "" ] && [ "$(cat "$ORACLE/main.tf")" = x ] && [ "$(ls "$ORACLE")" = "main.tf" ] || echo "HEADLINE TOUCHED"
grep -q 'estate=reference-gcp-endpoints' "$W/endpoints-a/versions.tf" 2>/dev/null || echo "NOT ITS OWN ESTATE"
[ -f "$W/imports" ] && cat "$W/imports"
true`
		out, _ := gcpBash(t, env, body)
		return out
	}

	t.Run("pass", func(t *testing.T) {
		out := run(t, "PASS_CASE=1")
		for _, want := range []string{"GAUNTLET stage=day2_endpoints verdict=pass", "rc=0", "left: \n",
			"import p/refgcp-gneg-ip/203.0.113.10//443", "import p/refgcp-gneg-fqdn//backend.example.com/443",
			"import p/r/refgcp-rneg-ip/203.0.113.20//8080", "import p/r/refgcp-rneg-fqdn//db.example.com/5432",
			"destroyed 8 on A and 8 on B"} {
			if !strings.Contains(out, want) {
				t.Errorf("the pass case lacks %q:\n%s", want, out)
			}
		}
		for _, bad := range []string{"FAIL", "verdict=fail", "LEFT", "HEADLINE TOUCHED", "NOT ITS OWN ESTATE"} {
			if strings.Contains(out, bad) {
				t.Errorf("the pass case carries %q:\n%s", bad, out)
			}
		}
	})

	notEmpty := "Plan: 1 to add, 0 to change, 0 to destroy."
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"the emulator lacks the NEGs":     {[]string{"B_MK_ERR=1"}, "lex00/floci-gcp#26): Error: Error creating GlobalNetworkEndpointGroup"},
		"stock creates another count":     {[]string{"MK_N_B=8"}, "stock's create on B did not add exactly the 8 scratch objects and the scratch network"},
		"choudoufu creates another count": {[]string{"MK_N_A=8"}, "choudoufu's create on A did not add exactly the 8 scratch objects and the network"},
		"the scratch network left on A":   {[]string{"KEEP_NET_A=1"}, "refgcp-ep-net still exists on A after the scratch estate's teardown"},
		"a wrong endpoint on A":           {[]string{"BAD_EP=A"}, `refgcp-rneg-ip on A holds endpoint(s) "203.0.113.20||8081", want exactly "203.0.113.20||8080"`},
		"a wrong endpoint on B":           {[]string{"BAD_EP=B"}, `refgcp-rneg-ip on B holds endpoint(s) "203.0.113.20||8081"`},
		"the next plan is not empty":      {[]string{"A_PLAN=" + notEmpty}, "the plan after the create is not empty (Plan: 1 to add"},
		"no record":                       {[]string{"NO_RECORD=1"}, "no record under"},
		"a record naming another id":      {[]string{"RECORD_ID=p/r/refgcp-rneg-fqdn/db.example.com/5432"}, "the record for google_compute_region_network_endpoint.fqdn disagrees"},
		"the plan with no state file":     {[]string{"NOSTATE_PLAN=" + notEmpty}, "the plan with no state file is not empty (exit 0, Plan: 1 to add"},
		"BREAK_ENDPOINTS":                 {[]string{"BREAK_ENDPOINTS=1"}, `with "p/refgcp-gneg-ip/203.0.113.10/443" failed (BREAK_ENDPOINTS=1: the empty segment collapsed`},
		"stock's plan after the imports":  {[]string{"B_PLAN=" + notEmpty}, "stock's plan on B after importing the four endpoints by choudoufu's ids is not empty"},
		"stock removes another count":     {[]string{"RM_N_B=7"}, "stock's removal on B destroyed 7 object(s), want the 8 it created"},
		"choudoufu removes another count": {[]string{"RM_N_A=7"}, "destroyed 7 object(s) on A; stock destroyed 8 on B"},
		"a NEG left on A":                 {[]string{"LEAVE_A=1"}, "refgcp-gneg-ip still exists on A after the scratch endpoints were removed"},
		"a NEG left on B":                 {[]string{"LEAVE_B=1"}, "refgcp-gneg-ip still exists on B after stock removed the scratch endpoints"},
		"the plan after the removal":      {[]string{"LAST_PLAN=" + notEmpty}, "the plan after the removal is not empty (Plan: 1 to add"},
	} {
		t.Run(name, func(t *testing.T) {
			out := run(t, c.env...)
			if !regexp.MustCompile(`GAUNTLET stage=day2_endpoints verdict=fail .*` + regexp.QuoteMeta(c.want)).MatchString(out) {
				t.Errorf("want a fail verdict carrying %q:\n%s", c.want, out)
			}
			if strings.Contains(out, "verdict=pass") || !strings.Contains(out, "rc=1") {
				t.Errorf("the stage did not end failed:\n%s", out)
			}
			if !strings.Contains(out, "left: \n") {
				t.Errorf("a NEG or the scratch network outlived the failed stage on A or B:\n%s", out)
			}
			// The stage runs in a scratch estate of its own: the headline
			// estate's roots and records are never touched, failed or not.
			if strings.Contains(out, "HEADLINE TOUCHED") {
				t.Errorf("the failed stage touched the headline estate's roots:\n%s", out)
			}
		})
	}
}

// refgcp_endpoint_record reads the estate's record for a network endpoint
// whichever shape it was written in: a one-string identity must equal the
// empty-segment id exactly, and identity attrs must carry the declared
// fields with the unset one absent or empty.
func TestRefGCPEndpointRecordCheck(t *testing.T) {
	const addr = "google_compute_global_network_endpoint.ip"
	const id = "p/refgcp-gneg-ip/203.0.113.10//443"
	for name, c := range map[string]struct {
		identity string
		ok       bool
		want     string
	}{
		"secondary_id":            {`{"secondary_id":"` + id + `"}`, true, "secondary_id " + id},
		"import_id":               {`{"import_id":"` + id + `"}`, true, "import_id " + id},
		"attrs, fqdn absent":      {`{"attrs":{"ip_address":"203.0.113.10","port":"443","project":"p"}}`, true, `attrs {"ip_address"`},
		"attrs, fqdn empty":       {`{"attrs":{"ip_address":"203.0.113.10","fqdn":"","port":"443"}}`, true, "attrs"},
		"a collapsed id":          {`{"secondary_id":"p/refgcp-gneg-ip/203.0.113.10/443"}`, false, "disagrees: secondary_id="},
		"attrs naming an fqdn":    {`{"attrs":{"ip_address":"203.0.113.10","fqdn":"x.example.com","port":"443"}}`, false, "attrs.fqdn="},
		"attrs with another port": {`{"attrs":{"ip_address":"203.0.113.10","port":"80"}}`, false, "attrs.port="},
		"no identity":             {`null`, false, "carries no identity"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "identity"), 0o700); err != nil {
				t.Fatal(err)
			}
			rec := `{"format_version":2,"address":"` + addr + `","kind":"identity","identity":` + c.identity + `}`
			for f, b := range map[string]string{"identity/a": rec, "identity/other": `{"address":"google_compute_global_network_endpoint.fqdn","identity":{"secondary_id":"zzz"}}`, "junk": "not json"} {
				if err := os.WriteFile(filepath.Join(dir, f), []byte(b), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, code := gcpBash(t, nil, `source e2e/reference-gcp/gcp.sh; refgcp_endpoint_record "`+dir+`" `+addr+` "`+id+`" 203.0.113.10 "" 443`)
			if (code == 0) != c.ok || !strings.Contains(out, c.want) {
				t.Errorf("exit %d, output %q; want ok=%v and %q", code, out, c.ok, c.want)
			}
		})
	}
	out, code := gcpBash(t, nil, `source e2e/reference-gcp/gcp.sh; refgcp_endpoint_record "`+t.TempDir()+`" `+addr+` "`+id+`" 203.0.113.10 "" 443`)
	if code == 0 || !strings.Contains(out, "no record under") {
		t.Errorf("an empty store: exit %d, %q", code, out)
	}
}

// refgcp_neg_endpoint_lines reads listNetworkEndpoints' real response
// shape, one networkEndpoint per item, an unset field printed empty.
func TestRefGCPNegEndpointLines(t *testing.T) {
	const resp = `{"kind":"compute#networkEndpointGroupsListNetworkEndpoints","items":[
 {"networkEndpoint":{"fqdn":"backend.example.com","port":443}},
 {"networkEndpoint":{"ipAddress":"203.0.113.10","port":443}}]}`
	cmd := exec.Command("bash", "-c", "source e2e/reference-gcp/gcp.sh; refgcp_neg_endpoint_lines")
	cmd.Stdin = strings.NewReader(resp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := string(out), "203.0.113.10||443\n|backend.example.com|443\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// day2_endpoints is not a headline stage, so it must never decide a
// headline verdict: in the ninth GCP run, placed after day2_replace, its
// failure took day2_crash, day2_teardown, greenfield and no_local_state
// down with it. It runs after strict, the last headline stage, in roots of
// its own, never the headline estate's adopted or oracle root.
func TestRefGCPEndpointsRunsLastInItsOwnEstate(t *testing.T) {
	raw, err := os.ReadFile("e2e/reference-gcp/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	strictDone := strings.Index(s, "\n); stage_done $? strict\n")
	begin := strings.Index(s, "\ngauntlet_begin_stage day2_endpoints\n")
	end := strings.Index(s, "\ngauntlet_end\n")
	if strictDone < 0 || begin < 0 || end < 0 {
		t.Fatal("run.sh: strict, day2_endpoints or gauntlet_end not found")
	}
	if begin < strictDone || begin > end {
		t.Errorf("day2_endpoints begins at byte %d, want after strict's stage_done (%d) and before gauntlet_end (%d)", begin, strictDone, end)
	}
	for _, other := range []string{"day2_crash", "day2_teardown", "greenfield", "no_local_state", "strict"} {
		if i := strings.Index(s, "\ngauntlet_begin_stage "+other+"\n"); i > begin {
			t.Errorf("headline stage %s begins after day2_endpoints", other)
		}
	}
	stage := refgcpEndpointsStage(t)
	if m := regexp.MustCompile(`(^|[^_a-z])(plan_a|apply_a|stock_b)\b`).FindString(stage); m != "" {
		t.Errorf("day2_endpoints calls the headline estate's %s", strings.TrimSpace(m))
	}
	if strings.Contains(stage, "$ADOPTED") || strings.Contains(stage, "$ORACLE") {
		t.Error("day2_endpoints names $ADOPTED or $ORACLE, the headline estate's roots")
	}
}

// refgcpGCSStage is reference-gcp's record_store_gcs stage as run.sh holds
// it, from its gauntlet_begin_stage to the line before the subshell closes,
// and checks the stage runs in a subshell of its own, after day2_endpoints
// and before gauntlet_end, never naming the headline estate's roots.
func refgcpGCSStage(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("e2e/reference-gcp/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	i := strings.Index(s, "\ngauntlet_begin_stage record_store_gcs\n")
	j := strings.Index(s, "\n); stage_done $? record_store_gcs\n")
	if i < 0 || j < i {
		t.Fatal("run.sh: no record_store_gcs stage closed by \"); stage_done $? record_store_gcs\"")
	}
	if open := strings.LastIndex(s[:i], "\n(\n"); open < 0 || strings.Contains(s[open:i], "); stage_done ") {
		t.Fatal("run.sh: record_store_gcs does not open its own subshell, so a fail() in it ends the run")
	}
	endpoints := strings.Index(s, "\n); stage_done $? day2_endpoints\n")
	end := strings.Index(s, "\ngauntlet_end\n")
	if endpoints < 0 || end < 0 || i < endpoints || i > end {
		t.Errorf("record_store_gcs begins at byte %d, want after day2_endpoints' stage_done (%d) and before gauntlet_end (%d)", i, endpoints, end)
	}
	stage := s[i:j]
	if m := regexp.MustCompile(`(^|[^_a-z])(plan_a|apply_a|stock_b)\b`).FindString(stage); m != "" {
		t.Errorf("record_store_gcs calls the headline estate's %s", strings.TrimSpace(m))
	}
	if strings.Contains(stage, "$ADOPTED") || strings.Contains(stage, "$ORACLE") || strings.Contains(stage, "$GREEN") {
		t.Error("record_store_gcs names $ADOPTED, $ORACLE or $GREEN, the headline estate's roots")
	}
	return stage
}

// record_store_gcs, stub-run offline. Every command the stage sends to the
// emulator or to a binary is a stub over files in a temporary directory: a
// live object is a file per name, the record bucket a directory of files
// whose content is the tofu-estate metadata, and the record keys are
// refgcp_record_key's own. The two racing applies run as the stage runs
// them, in the background, and are judged by refgcp_gcs_race_verdict. The
// pass case passes cleanly; each other case breaks one gate, and that
// gate's own verdict must read fail with nothing left on A and no bucket.
func TestRefGCPRecordStoreGCSStageGates(t *testing.T) {
	stage := refgcpGCSStage(t)
	run := func(t *testing.T, env ...string) string {
		t.Helper()
		w := t.TempDir()
		body := gcpFail + `source e2e/reference-gcp/gcp.sh
W="` + w + `"; WORK="$W"; ADOPTED="$W/a"; ORACLE="$W/b"; GREEN="$W/g"; PROJECT=p; REGION=r; ZONE=z; ESTATE=reference-gcp; EPA=a; EPB=b
GOOGLE_REQUIRED_PROVIDER='    google = { source = "hashicorp/google", version = "= 8.6.0" }'
export GOOGLE_OAUTH_ACCESS_TOKEN=chdf-test GS_RACE_SECONDS=1
mkdir -p "$ADOPTED/.tofu-records" "$ORACLE" "$W/bucket"; echo '{}' > "$ADOPTED/.tofu-records/pre"; echo x > "$ORACLE/main.tf"
log() { printf '%s\n' "$*"; }
versions_block() { echo "# versions $1 $2"; }
gcp_provider_block() { echo "# provider $1"; }
gauntlet_locked_init() { return 0; }
topic_path() { printf 'v1/projects/%s/topics/%s' "$PROJECT" "$1"; }
live() { [ -f "$W/A.$(basename "$1")" ]; }
exists_a() { live "$1"; }
ra() { case "$1" in
  GET) case "$2" in
    storage/v1/b/*) [ -e "$W/bucket.exists" ] || return 1
      if [ -n "${BAD_BUCKET:-}" ]; then echo '{"versioning":{"enabled":false},"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]},"iamConfiguration":{"publicAccessPrevention":"enforced","uniformBucketLevelAccess":{"enabled":true}}}'
      else echo '{"versioning":{"enabled":true},"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]},"iamConfiguration":{"publicAccessPrevention":"enforced","uniformBucketLevelAccess":{"enabled":true}}}'; fi ;;
    *) live "$2" ;;
    esac ;;
  DELETE) rm -f "$W/A.$(basename "$2")" ;;
esac; }
refgcp_gcs_make_bucket() { [ -z "${MKB_ERR:-}" ] || return 1; mkdir -p "$W/bucket"; : > "$W/bucket.exists"; }
put_record() { printf '%s' "$2" > "$W/bucket/${1//\//%}"; }
has_record() { [ -f "$W/bucket/${1//\//%}" ]; }
refgcp_gcs_records() { local f n; for f in "$W"/bucket/*; do [ -f "$f" ] || continue; n="${f##*/}"; n="${n//\%//}"; case "$n" in "$3"*) echo "$n $(cat "$f")" ;; esac; done | sort; }
refgcp_gcs_delete() { rm -f "$W/bucket/${3//\//%}"; }
refgcp_gcs_scrub() { rm -rf "$W/bucket" "$W/bucket.exists"; }
GS_E=reference-gcp-gcs
NETK="$(refgcp_record_key $GS_E google_compute_network.main)"
TOPK="$(refgcp_record_key $GS_E google_pubsub_topic.events)"
FWK="$(refgcp_record_key $GS_E google_compute_firewall.ssh)"
RACEK="$(refgcp_record_key $GS_E time_sleep.race)"
cache() { echo "$PWD/.terraform/choudoufu-cache.tfstate"; }
plan_stub() {
  local out
  if [ ! -f "$(cache)" ] && ! has_record "$NETK" && [ -z "${IGNORE_RECORDS:-}" ]; then
    printf '\nError: Unlabelled live object holds the declared name\n'; return 1
  fi
  if [ ! -f main.tf ]; then out="${PLAN_LAST:-No changes.}"
  elif [ -f race.tf ]; then if [ "${PWD##*/}" = gcs-a2 ]; then out="${PLAN_RACE_A2:-No changes.}"; else out="${PLAN_RACE_A:-No changes.}"; fi
  elif [ -f firewall.tf ] && [ ! -f "$(cache)" ]; then out="${PLAN_NOCACHE:-No changes.}"
  elif [ -f firewall.tf ]; then out="${PLAN_APPLY:-No changes.}"
  else out="${PLAN_MIGRATE:-No changes.}"; fi
  mkdir -p .terraform; : > "$(cache)"
  printf '%s\n' "$out"
}
apply_stub() {
  if [ ! -f main.tf ]; then
    [ -n "${LEAVE_NET:-}" ] || rm -f "$W/A.refgcp-gcs-net"; rm -f "$W/A.refgcp-gcs-fw" "$W/A.refgcp-gcs-topic"
    printf '\nApply complete! Resources: 0 added, 0 changed, %s destroyed.\n' "${T_N:-4}"; return 0
  fi
  if [ -f race.tf ]; then
    local me=1; [ "${PWD##*/}" = gcs-a2 ] && me=2
    if [ "$me" = 2 ] && [ -n "${NO_OVERLAP:-}" ]; then printf '\nApply complete! Resources: 0 added, 0 changed, 0 destroyed.\n'; return 0; fi
    printf 'time_sleep.race: Creating...\ntime_sleep.race: Creation complete after 1s [id=2026-10-21T00:00:0%sZ]\n' "$me"
    if [ "$me" = 1 ]; then
      [ -n "${NO_RACE_RECORD:-}" ] || put_record "$RACEK" "$GS_E"
      printf '\nApply complete! Resources: 1 added, 0 changed, 0 destroyed.\n'; return 0
    fi
    sleep 0.2
    if [ -n "${RACE_BOTH_OK:-}" ]; then printf '\nApply complete! Resources: 1 added, 0 changed, 0 destroyed.\n'; return 0; fi
    if [ -n "${RACE_OTHER_ERR:-}" ]; then printf '\nError: Cannot persist a record\n'; return 1; fi
    printf '\nError: Record store write conflict\n\nWriting the persisted record for time_sleep.race failed: another writer changed it between this run'"'"'s plan and apply. This run expected version "(absent)" and the store now holds version "1761004800000". Nothing was overwritten. Re-plan and re-apply to reconcile with the other writer'"'"'s change.\n'
    return 1
  fi
  if [ -f firewall.tf ]; then
    : > "$W/A.refgcp-gcs-fw"; [ -n "${NO_FW_RECORD:-}" ] || put_record "$FWK" "$GS_E"
    printf '\nApply complete! Resources: %s added, 0 changed, 0 destroyed.\n' "${FW_N:-1}"; return 0
  fi
}
tofu_stub() {
  case "$1" in
    init) mkdir -p .terraform; return 0 ;;
    plan) plan_stub ;;
    apply) apply_stub ;;
    live-import)
      [ -n "${NO_NET_RECORD:-}" ] || put_record "$NETK" "$GS_E"
      put_record "$TOPK" "$GS_E"
      [ -z "${LOCAL_RECORDS:-}" ] || mkdir -p .tofu-records
      printf 'SKIPPED (1)\n  google_compute_network.main  google_compute_network has no tags argument in the provider'"'"'s schema, so there is nowhere on it to carry an ownership marker.\n\n'
      printf '%s resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, 1 skipped.\n2 identities recorded.\n' "${MIG_STAMPED:-1}"
      if [ -n "${MIG_RC:-}" ]; then printf '\nError: Cannot open the record store\n'; return 1; fi ;;
  esac
}
TOFU=tofu_stub
terraform() {
  case "$1" in
    init) return 0 ;;
    apply) if [ -n "${STOCK_ERR:-}" ]; then printf '\nError: Error creating Network: googleapi: Error 500\n'; return 1; fi
      : > "$W/A.refgcp-gcs-net"; : > "$W/A.refgcp-gcs-topic"
      printf '\nApply complete! Resources: %s added, 0 changed, 0 destroyed.\n' "${STOCK_N:-2}" ;;
  esac
}
CURRENT_STAGE=""
( ` + stage + `
)
echo "rc=$?"
echo "left: $(cd "$W" && ls A.* bucket.exists 2>/dev/null | tr '\n' ' ')"
[ "$(ls "$ADOPTED/.tofu-records" | tr '\n' ' ')" = "pre " ] && [ "$(cat "$ORACLE/main.tf")" = x ] && [ ! -e "$GREEN" ] || echo "HEADLINE TOUCHED"
grep -q 'estate = "reference-gcp-gcs"' "$W/gcs-a/versions.tf" 2>/dev/null || echo "NOT ITS OWN ESTATE"
grep -q 'record_store "gcs"' "$W/gcs-a/versions.tf" 2>/dev/null && grep -q 'storage_custom_endpoint = "a/storage/v1/"' "$W/gcs-a/versions.tf" || echo "NOT THE GCS STORE"
true`
		out, _ := gcpBash(t, env, body)
		return out
	}

	t.Run("pass", func(t *testing.T) {
		out := run(t)
		for _, want := range []string{"GAUNTLET stage=record_store_gcs verdict=pass", "rc=0", "left: \n",
			"apply 1 landed and apply 2 was refused", "wrote 2 record object(s) carrying tofu-estate=reference-gcp-gcs",
			"the network's at tofu-records/reference-gcp-gcs/google_compute_network/"} {
			if !strings.Contains(out, want) {
				t.Errorf("the pass case lacks %q:\n%s", want, out)
			}
		}
		for _, bad := range []string{"FAIL", "verdict=fail", "HEADLINE TOUCHED", "NOT ITS OWN ESTATE", "NOT THE GCS STORE"} {
			if strings.Contains(out, bad) {
				t.Errorf("the pass case carries %q:\n%s", bad, out)
			}
		}
	})

	t.Run("BREAK_GCS_STORE", func(t *testing.T) {
		out := run(t, "BREAK_GCS_STORE=1")
		if !regexp.MustCompile(`GAUNTLET stage=record_store_gcs verdict=pass .*detail=BREAK_GCS_STORE=1 control`).MatchString(out) || !strings.Contains(out, "Unlabelled live object holds the declared name") || !strings.Contains(out, "left: \n") {
			t.Errorf("the Break control did not pass as a control naming the refusal, with nothing left:\n%s", out)
		}
	})

	notEmpty := "Plan: 1 to add, 0 to change, 0 to destroy."
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"the bucket is not created":            {[]string{"MKB_ERR=1"}, "floci-gcp did not create the record bucket p-refgcp-records"},
		"the bucket reads back unversioned":    {[]string{"BAD_BUCKET=1"}, "did not read back with the contract's four settings (versioning is not enabled)"},
		"stock cannot create the shape":        {[]string{"STOCK_ERR=1"}, "stock could not create the gcs scratch shape on A: Error: Error creating Network"},
		"stock creates another count":          {[]string{"STOCK_N=1"}, "did not add exactly the network and the topic"},
		"live-import fails":                    {[]string{"MIG_RC=1"}, "live-import -approve into the gcs record store failed: Error: Cannot open the record store"},
		"live-import stamps nothing":           {[]string{"MIG_STAMPED=0"}, "did not stamp the topic, skip the network"},
		"no network record in the bucket":      {[]string{"NO_NET_RECORD=1"}, "holds no record for google_compute_network.main at tofu-records/reference-gcp-gcs/google_compute_network/"},
		"a local record store appears":         {[]string{"LOCAL_RECORDS=1"}, `a local .tofu-records appeared beside record_store "gcs"`},
		"the plan after the migration":         {[]string{"PLAN_MIGRATE=" + notEmpty}, "the plan after the migration is not empty (Plan: 1 to add"},
		"the apply adds another count":         {[]string{"FW_N=2"}, "the apply adding the firewall did not add exactly one object"},
		"no firewall record":                   {[]string{"NO_FW_RECORD=1"}, "the apply wrote no record for google_compute_firewall.ssh"},
		"the replan after the apply":           {[]string{"PLAN_APPLY=" + notEmpty}, "the replan after the apply is not empty (Plan: 1 to add"},
		"the plan with no state cache":         {[]string{"PLAN_NOCACHE=" + notEmpty}, "the plan with no state cache is not empty (exit 0, Plan: 1 to add"},
		"the applies do not overlap":           {[]string{"NO_OVERLAP=1"}, "the two applies did not overlap: 1 of 2 created time_sleep.race"},
		"both racing applies succeed":          {[]string{"RACE_BOTH_OK=1"}, "the second record write under ifGenerationMatch=0 was not refused"},
		"the loser fails for another reason":   {[]string{"RACE_OTHER_ERR=1"}, "apply 2 failed (exit 1) but not with the store's write conflict on time_sleep.race: Error: Cannot persist a record"},
		"no record lands for the race":         {[]string{"NO_RACE_RECORD=1"}, "after the race the bucket holds no record for time_sleep.race"},
		"the losing root plans after the race": {[]string{"PLAN_RACE_A2=" + notEmpty}, "the plan in gcs-a2 after the race is not empty (Plan: 1 to add"},
		"the removal destroys another count":   {[]string{"T_N=3"}, "removing the scratch blocks did not destroy exactly the network, the topic, the firewall and time_sleep.race"},
		"the network outlives its block":       {[]string{"LEAVE_NET=1"}, "refgcp-gcs-net still exists on A after its block was removed"},
		"the plan after the removal":           {[]string{"PLAN_LAST=" + notEmpty}, "the plan after the removal is not empty (Plan: 1 to add"},
		"the Break control finds no refusal":   {[]string{"BREAK_GCS_STORE=1", "IGNORE_RECORDS=1"}, "BREAK_GCS_STORE=1: with the network's record deleted from the bucket the plan with no state cache is still empty"},
	} {
		t.Run(name, func(t *testing.T) {
			out := run(t, c.env...)
			if !regexp.MustCompile(`GAUNTLET stage=record_store_gcs verdict=fail .*` + regexp.QuoteMeta(c.want)).MatchString(out) {
				t.Errorf("want a fail verdict carrying %q:\n%s", c.want, out)
			}
			if strings.Contains(out, "verdict=pass") || !strings.Contains(out, "rc=1") {
				t.Errorf("the stage did not end failed:\n%s", out)
			}
			if !strings.Contains(out, "left: \n") {
				t.Errorf("a scratch object or the record bucket outlived the failed stage:\n%s", out)
			}
			if strings.Contains(out, "HEADLINE TOUCHED") {
				t.Errorf("the failed stage touched the headline estate's roots:\n%s", out)
			}
		})
	}
}

// refgcp_record_key must name the object choudoufu's own RecordKey writes,
// or every bucket check in record_store_gcs looks in the wrong place. Held
// to projection.RecordKey itself, a module path, an instance key and an
// address long enough to be chunked included.
func TestRefGCPRecordKeyIsRecordKey(t *testing.T) {
	long := `module.a["` + strings.Repeat("k", 300) + `"].time_sleep.race`
	for _, a := range []string{"google_compute_network.main", "time_sleep.race", `google_pubsub_topic.shard[1]`,
		`module.net["eu.west"].module.inner.google_compute_firewall.ssh["x/y"]`, long} {
		addr, diags := addrs.ParseAbsResourceInstanceStr(a)
		if diags.HasErrors() {
			t.Fatalf("%s: %s", a, diags.Err())
		}
		want := projection.RecordKey(projection.RecordKeyPrefix("reference-gcp-gcs"), addr)
		out, err := exec.Command("bash", "-c", `source e2e/reference-gcp/gcp.sh; refgcp_record_key "$0" "$1"`, "reference-gcp-gcs", a).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", a, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s: refgcp_record_key says %q, RecordKey says %q", a, got, want)
		}
	}
}

// refgcp_gcs_bucket_problems names each of the four contract settings a
// storage.buckets.get response lacks, and nothing for a bucket that has
// them. A noncurrent rule without a window, or isLive alone, does not
// count: the contract page says so.
func TestRefGCPGCSBucketProblems(t *testing.T) {
	const good = `{"versioning":{"enabled":true},"lifecycle":{"rule":[{"action":{"type":"Delete"},"condition":{"daysSinceNoncurrentTime":30}}]},"iamConfiguration":{"publicAccessPrevention":"enforced","uniformBucketLevelAccess":{"enabled":true}}}`
	for name, c := range map[string]struct{ in, want string }{
		"all four":      {good, ""},
		"no versioning": {strings.Replace(good, `"versioning":{"enabled":true}`, `"versioning":{}`, 1), "versioning is not enabled"},
		"isLive only":   {strings.Replace(good, `"daysSinceNoncurrentTime":30`, `"isLive":false`, 1), "no Delete rule with daysSinceNoncurrentTime"},
		"PAP inherited": {strings.Replace(good, `"enforced"`, `"inherited"`, 1), "publicAccessPrevention reads 'inherited'"},
		"no uniform":    {strings.Replace(good, `"uniformBucketLevelAccess":{"enabled":true}`, `"uniformBucketLevelAccess":{"enabled":false}`, 1), "uniform bucket-level access is not enabled"},
		"no response":   {"", "unreadable"},
		"an error page": {`<html>404</html>`, "unreadable"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", "source e2e/reference-gcp/gcp.sh; refgcp_gcs_bucket_problems")
			cmd.Stdin = strings.NewReader(c.in)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if (c.want == "" && got != "") || (c.want != "" && !strings.Contains(got, c.want)) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
