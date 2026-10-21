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
