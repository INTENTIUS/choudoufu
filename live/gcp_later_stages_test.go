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
