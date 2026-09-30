// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// lockFile renders a .terraform.lock.hcl the way terraform/tofu write one,
// hashes included, for one provider address at one version.
func lockFile(addr, version string) string {
	return fmt.Sprintf(`# This file is maintained automatically by "terraform init".
# Manual edits may be lost in future updates.

provider %q {
  version     = %q
  constraints = ">= 2.38.0"
  hashes = [
    "h1:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG=",
    "zh:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  ]
}
`, addr, version)
}

// TestParseLockVersionsStripsTheRegistryHost: stock terraform and
// choudoufu write different registry hosts for the same provider, and both
// must land on the one type the row records.
func TestParseLockVersionsStripsTheRegistryHost(t *testing.T) {
	src := lockFile("registry.terraform.io/hashicorp/aws", "6.59.0") + lockFile("registry.opentofu.org/hashicorp/random", "3.7.2")
	got, err := parseLockVersions([]byte(src), "x.hcl")
	if err != nil {
		t.Fatal(err)
	}
	if got[ProviderAWS] != "6.59.0" || got["hashicorp/random"] != "3.7.2" || len(got) != 2 {
		t.Errorf("parseLockVersions = %v, want hashicorp/aws=6.59.0 and hashicorp/random=3.7.2", got)
	}
}

// TestReadReportedLocksAgreementConflictAndAbsence: one version when every
// reported lock file agrees; nothing, and a named conflict, when they do
// not; and an empty answer - not an error - for a run that reported none.
func TestReadReportedLocksAgreementConflictAndAbsence(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lock.a", lockFile("registry.terraform.io/hashicorp/aws", "6.59.0"))
	write("lock.b", lockFile("registry.opentofu.org/hashicorp/aws", "6.59.0"))
	got, err := readReportedLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version(ProviderAWS) != "6.59.0" || len(got.Conflicts) != 0 {
		t.Errorf("two agreeing lock files: got %+v, want hashicorp/aws=6.59.0 and no conflict", got)
	}

	write("lock.c", lockFile("registry.opentofu.org/hashicorp/aws", "6.63.0"))
	got, err = readReportedLocks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version(ProviderAWS) != "" {
		t.Errorf("disagreeing lock files recorded hashicorp/aws=%q; a run that resolved two releases has no one version to record", got.Version(ProviderAWS))
	}
	if len(got.Conflicts) != 1 || !strings.Contains(got.Conflicts[0], "6.59.0 and 6.63.0") {
		t.Errorf("conflicts = %v, want one naming 6.59.0 and 6.63.0", got.Conflicts)
	}

	empty, err := readReportedLocks(filepath.Join(dir, "missing"))
	if err != nil || empty.Version(ProviderAWS) != "" {
		t.Errorf("a missing report dir: got %+v, %v; want no version and no error", empty, err)
	}

	write("lock.d", "provider {")
	if _, err := readReportedLocks(dir); err == nil {
		t.Error("an unparseable reported lock file must be an error, not an absence")
	}
}

// TestRunEstatesRecordsResolvedProviderVersionsNotThePin is issue #1739's
// first defect. live/oracle-versions.json pins aws 6.63.0 and kubernetes
// 3.2.1; the scripts here resolve something else (aws 6.59.0, the version
// terralith-gen hard-codes; kubernetes 2.38.0, the lower bound
// corpus-quickpizza's upstream root allows) and report their lock files
// the way gauntlet_report_lock does. The row must carry what resolved. A
// third script reports no lock file at all, and its row must carry
// nothing - which reads stale - rather than the pin.
//
// Red on main (before #1739), where RunEstates stamped the pin:
//
//	aws-estate LastRun.AWSProviderVersion = "6.63.0", want "6.59.0" (what its lock file resolved, not the pin)
func TestRunEstatesRecordsResolvedProviderVersionsNotThePin(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin := `{"aws_provider_version": "6.63.0", "kubernetes_provider_version": "3.2.1"}`
	if err := os.WriteFile(filepath.Join(root, "live", "oracle-versions.json"), []byte(pin), 0o644); err != nil {
		t.Fatal(err)
	}

	writeScript := func(rel, lock string) {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/usr/bin/env bash\n"
		if lock != "" {
			script += "cat > \"$" + LockReportEnv + "/lock.1\" <<'EOF'\n" + lock + "EOF\n"
		}
		script += "printf 'GAUNTLET protocol=1\\n'\n" +
			"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n"
		if err := os.WriteFile(filepath.Join(root, rel), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeScript(filepath.Join("live", "e2e", "aws-estate", "run.sh"), lockFile("registry.terraform.io/hashicorp/aws", "6.59.0"))
	writeScript(filepath.Join("live", "e2e", "reference-k8s", "run.sh"), lockFile("registry.terraform.io/hashicorp/kubernetes", "2.38.0"))
	writeScript(filepath.Join("live", "e2e", "silent", "run.sh"), "")

	m := &Manifest{Estates: []Estate{
		{Name: "aws-estate", Source: "s", Lane: "reference", Set: SetGrowing, Script: filepath.Join("live", "e2e", "aws-estate", "run.sh")},
		{Name: "reference-k8s", Source: "s", Lane: LaneKubernetes, Set: SetGrowing, Script: filepath.Join("live", "e2e", "reference-k8s", "run.sh")},
		{Name: "silent", Source: "s", Lane: "reference", Set: SetGrowing, Script: filepath.Join("live", "e2e", "silent", "run.sh")},
	}}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"aws-estate", "reference-k8s", "silent"}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}

	aws, _ := a.Result("aws-estate")
	if aws.LastRun == nil {
		t.Fatal("aws-estate LastRun is nil")
	}
	if aws.LastRun.AWSProviderVersion != "6.59.0" {
		t.Errorf("aws-estate LastRun.AWSProviderVersion = %q, want %q (what its lock file resolved, not the pin)", aws.LastRun.AWSProviderVersion, "6.59.0")
	}
	if aws.LastRun.KubernetesProviderVersion != "" {
		t.Errorf("aws-estate LastRun.KubernetesProviderVersion = %q, want empty (mutually exclusive with AWS)", aws.LastRun.KubernetesProviderVersion)
	}

	k8s, _ := a.Result("reference-k8s")
	if k8s.LastRun == nil {
		t.Fatal("reference-k8s LastRun is nil")
	}
	if k8s.LastRun.KubernetesProviderVersion != "2.38.0" {
		t.Errorf("reference-k8s LastRun.KubernetesProviderVersion = %q, want %q (what its lock file resolved, not the pin)", k8s.LastRun.KubernetesProviderVersion, "2.38.0")
	}
	if k8s.LastRun.AWSProviderVersion != "" {
		t.Errorf("reference-k8s LastRun.AWSProviderVersion = %q, want empty (mutually exclusive with Kubernetes)", k8s.LastRun.AWSProviderVersion)
	}

	silent, _ := a.Result("silent")
	if silent.LastRun == nil {
		t.Fatal("silent LastRun is nil")
	}
	if silent.LastRun.AWSProviderVersion != "" {
		t.Errorf("silent reported no lock file; LastRun.AWSProviderVersion = %q, want empty (never the pin)", silent.LastRun.AWSProviderVersion)
	}
	if !IsProviderStale(silent, providerVersions(root)) {
		t.Error("a row that recorded no provider version must read as provider-stale (#1253)")
	}
}

// TestLockedInitReportsTheLockFileItWrote runs the real library: a
// successful gauntlet_locked_init copies the lock file from the directory
// it ran in (or the -chdir target) into GAUNTLET_LOCK_REPORT_DIR, a failed
// one copies nothing, and with no report dir set nothing happens at all.
func TestLockedInitReportsTheLockFileItWrote(t *testing.T) {
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	work := t.TempDir()
	report := filepath.Join(work, "report")
	est := filepath.Join(work, "est")
	chdir := filepath.Join(work, "chdir")
	for _, d := range []string{report, est, chdir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A fake init that writes a lock file where terraform would.
	script := fmt.Sprintf(`
set -u
export TF_PLUGIN_CACHE_DIR=%[1]q
source %[2]q
fakeinit() {
  local d=.
  for a in "$@"; do case "$a" in -chdir=*) d="${a#-chdir=}" ;; esac; done
  printf '%%s' "$FAKE_LOCK" > "$d/.terraform.lock.hcl"
  return "${FAKE_RC:-0}"
}
unset %[3]s
( cd %[4]q && FAKE_LOCK=unreported gauntlet_locked_init fakeinit ) || exit 11
export %[3]s=%[5]q
( cd %[4]q && FAKE_LOCK=one gauntlet_locked_init fakeinit ) || exit 12
FAKE_LOCK=two gauntlet_locked_init fakeinit -chdir=%[6]q || exit 13
( cd %[4]q && FAKE_LOCK=failed FAKE_RC=1 gauntlet_locked_init fakeinit ) && exit 14
exit 0
`, filepath.Join(work, "cache"), lib, LockReportEnv, est, report, chdir)
	if out, err := runBash(script); err != nil {
		t.Fatalf("library run failed: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(report)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(report, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
	}
	joined := strings.Join(got, ",")
	if len(got) != 2 || !strings.Contains(joined, "one") || !strings.Contains(joined, "two") {
		t.Errorf("reported lock files = %q, want exactly the two successful inits' (one, two): not the failed init's, not the one run with no report dir", got)
	}
}

// TestEveryEstateScriptReportsItsLockFile: a registered estate script whose
// stock init reports nothing records no provider version, so its row reads
// stale forever. Every script must reach gauntlet_report_lock, through
// gauntlet_locked_init or by calling it directly after its stage-1 init.
func TestEveryEstateScriptReportsItsLockFile(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	reports := regexp.MustCompile(`(?m)^[^#\n]*\b(gauntlet_locked_init|gauntlet_report_lock)\b`)
	for _, e := range m.Estates {
		b, err := os.ReadFile(filepath.Join(root, e.ScriptPath()))
		if err != nil {
			t.Fatal(err)
		}
		if !reports.Match(b) {
			t.Errorf("%s never calls gauntlet_locked_init or gauntlet_report_lock; its row would record no provider version (#1739)", e.ScriptPath())
		}
	}
}

// TestK8sServerVersionReadsTheServerLine is issue #1739's third defect: the
// first v1.x in `kubectl version` is the client's. Red on main's
// `grep -io 'v1\.[0-9.]*' | head -1`, which reads v1.34.0 here.
func TestK8sServerVersionReadsTheServerLine(t *testing.T) {
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	for _, tc := range []struct{ in, want string }{
		{"Client Version: v1.34.0\nKustomize Version: v5.7.1\nServer Version: v1.37.0\n", "v1.37.0"},
		{"Client Version: v1.34.0\nKustomize Version: v5.7.1\n", ""},
	} {
		out, err := runBash(fmt.Sprintf("source %q; printf '%%b' %q | gauntlet_k8s_server_version", lib, tc.in))
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Errorf("gauntlet_k8s_server_version on %q = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, rel := range []string{"corpus-quickpizza", "reference-k8s", "reference-k8s-stateful", "reference-k8s-cert-manager"} {
		b, err := os.ReadFile(filepath.Join(root, "live", "e2e", rel, "run.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if regexp.MustCompile(`version 2>/dev/null \| grep -io 'v1`).Match(b) {
			t.Errorf("live/e2e/%s/run.sh reads a Kubernetes version as the first v1.x of `kubectl version`, which is the client; use gauntlet_k8s_server_version", rel)
		}
	}
}
