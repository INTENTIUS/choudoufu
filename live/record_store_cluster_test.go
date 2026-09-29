// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// examples/record-store-cluster, held to the store it is an example for.
// GitHub issue #1442, the cluster half of record_store_bucket_test.go.
//
// Two of these read the shipped manifests and workflow; the other two RUN
// the shipped recipes with a stub `kubectl` and a stub `choudoufu` first on
// PATH, for the same reason the bucket's tests do: #1379's audit changed a
// `verify` to `... || true` and deleted an `exit 1` from a `down`, and every
// string a grep-only test looked for was still in the file. The selftest that
// applies the example to a kind cluster (examples/record-store-cluster/selftest.sh)
// runs in .github/workflows/k8s-smoke.yml, because the ordinary gate has no
// cluster.

const recordStoreClusterProject = "../examples/record-store-cluster"

// rbacDoc is what this test reads off one document of records.yaml.
type rbacDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []struct {
		APIGroups []string `yaml:"apiGroups"`
		Resources []string `yaml:"resources"`
		Verbs     []string `yaml:"verbs"`
	} `yaml:"rules"`
	RoleRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
}

func recordStoreClusterManifests(t *testing.T) []rbacDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(recordStoreClusterProject, "manifests", "records.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var docs []rbacDoc
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	for {
		var d rbacDoc
		if err := dec.Decode(&d); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decoding records.yaml: %v", err)
		}
		docs = append(docs, d)
	}
	if len(docs) < 7 {
		t.Fatalf("records.yaml has %d documents, want the namespace, two ServiceAccounts, two Roles and two RoleBindings", len(docs))
	}
	return docs
}

// TestRecordStoreClusterRolesCarryTheStoresVerbs holds the two Roles to the
// store's own lists: KubernetesPlanVerbs for the plan Role and
// KubernetesRecordVerbs for the apply Role, on Secrets in the core group and
// nothing else, as Roles and never ClusterRoles. An example whose verbs
// drifted from the store's would be one the cluster contract refuses on
// first contact, or one that grants a verb the store never uses.
//
// Proving it red: add "patch" to the apply Role, or change either kind to
// ClusterRole.
func TestRecordStoreClusterRolesCarryTheStoresVerbs(t *testing.T) {
	docs := recordStoreClusterManifests(t)
	want := map[string][]string{
		"choudoufu-records-plan":  staterecord.KubernetesPlanVerbs,
		"choudoufu-records-apply": staterecord.KubernetesRecordVerbs,
	}
	seen := map[string]bool{}
	for _, d := range docs {
		if d.Kind == "ClusterRole" || d.Kind == "ClusterRoleBinding" {
			t.Errorf("records.yaml carries a %s (%s); the namespace is the read boundary and everything here must be namespaced", d.Kind, d.Metadata.Name)
		}
		if d.Kind != "Role" {
			continue
		}
		verbs, ok := want[d.Metadata.Name]
		if !ok {
			t.Errorf("records.yaml carries a Role %q this test does not know; the example has exactly a plan Role and an apply Role", d.Metadata.Name)
			continue
		}
		seen[d.Metadata.Name] = true
		if d.Metadata.Namespace != "tofu-records-ESTATE" {
			t.Errorf("Role %s is in namespace %q, want tofu-records-ESTATE (the store's default, with the placeholder `just up` fills)", d.Metadata.Name, d.Metadata.Namespace)
		}
		if len(d.Rules) != 1 {
			t.Errorf("Role %s has %d rules, want exactly one, on secrets", d.Metadata.Name, len(d.Rules))
			continue
		}
		r := d.Rules[0]
		if strings.Join(r.APIGroups, ",") != "" || strings.Join(r.Resources, ",") != "secrets" {
			t.Errorf("Role %s grants %v in groups %v, want secrets in the core group only", d.Metadata.Name, r.Resources, r.APIGroups)
		}
		if got, w := strings.Join(r.Verbs, ","), strings.Join(verbs, ","); got != w {
			t.Errorf("Role %s grants verbs [%s]; the store's list is [%s]", d.Metadata.Name, got, w)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("records.yaml has no Role %q", name)
		}
	}
	// And each binding points at a Role by that name, not at a ClusterRole.
	bound := 0
	for _, d := range docs {
		if d.Kind != "RoleBinding" {
			continue
		}
		bound++
		if d.RoleRef.Kind != "Role" {
			t.Errorf("RoleBinding %s binds a %s; a ClusterRole bound here would still grant only in the namespace, but the example would then be one edit from a cluster-wide read", d.Metadata.Name, d.RoleRef.Kind)
		}
		if _, ok := want[d.RoleRef.Name]; !ok {
			t.Errorf("RoleBinding %s binds %q, which is not one of the example's Roles", d.Metadata.Name, d.RoleRef.Name)
		}
	}
	if bound != 2 {
		t.Errorf("records.yaml has %d RoleBindings, want two", bound)
	}
}

// recordStoreClusterEnv is the environment for a recipe run: the stub
// directory first on PATH and none of the variables that change what the
// recipes do inherited from whoever runs the tests. KUBECONFIG is pointed at
// nothing so a real kubectl, if the stub were skipped, could not reach a
// cluster.
func recordStoreClusterEnv(t *testing.T, stubDir string, extra ...string) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "PATH", "CHOUDOUFU_BIN", "KUBECONFIG", "STUB_LOG":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KUBECONFIG=/dev/null",
		"STUB_LOG="+filepath.Join(stubDir, "calls.log"),
	)
	return append(env, extra...)
}

func recordStoreClusterRun(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	return recordStoreRunIn(t, recordStoreClusterProject, env, args...)
}

// TestRecordStoreClusterVerifyAsksTheBinary runs `just verify` with a stub
// choudoufu and reads what it was asked and whether its exit code came
// through. The recipe exists to ask `choudoufu live-cluster` about the
// estate's namespace; with `|| true` after the call it would report every
// cluster correct, and a recipe that read the settings itself would be a
// second implementation of a check the binary owns.
func TestRecordStoreClusterVerifyAsksTheBinary(t *testing.T) {
	recordStoreBins(t)
	raw, err := os.ReadFile(filepath.Join(recordStoreClusterProject, "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	verify := justRecipeWithParams(t, string(raw), "verify")
	for _, reimplementation := range []string{"auth can-i", "selfsubjectaccessreview", "validatingadmissionpolic", "encryption-provider-config"} {
		if strings.Contains(strings.ToLower(verify), reimplementation) {
			t.Errorf("`just verify` reads %s itself: that is a second implementation of a check the binary owns", reimplementation)
		}
	}
	for _, tc := range []struct {
		name    string
		binExit int
	}{{"the binary is happy", 0}, {"the binary refuses the cluster", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "kubectl", `echo "kubectl $*" >> "$STUB_LOG"; exit 0`)
			recordStoreStub(t, stubDir, "choudoufu", fmt.Sprintf(`
echo "choudoufu $*" >> "$STUB_LOG"
echo "stub choudoufu: live-cluster says %d"
exit %d
`, tc.binExit, tc.binExit))
			env := recordStoreClusterEnv(t, stubDir, "CHOUDOUFU_BIN="+filepath.Join(stubDir, "choudoufu"))
			code, out := recordStoreClusterRun(t, env, "verify", "chdf-guard", "-plan-identity")
			calls := recordStoreCalls(t, stubDir)
			if !strings.Contains(calls, "choudoufu live-cluster -namespace=tofu-records-chdf-guard -estate=chdf-guard -plan-identity") {
				t.Errorf("`just verify chdf-guard -plan-identity` never ran `choudoufu live-cluster -namespace=tofu-records-chdf-guard -estate=chdf-guard -plan-identity`. It ran:\n%s\noutput:\n%s", calls, out)
			}
			if strings.Contains(calls, "kubectl ") {
				t.Errorf("`just verify` called kubectl itself:\n%s", calls)
			}
			if tc.binExit == 0 && code != 0 {
				t.Errorf("the binary exited 0 and `just verify` exited %d:\n%s", code, out)
			}
			if tc.binExit != 0 && code == 0 {
				t.Errorf("the binary exited %d and `just verify` still exited 0, so the recipe reports every cluster correct:\n%s", tc.binExit, out)
			}
		})
	}
}

// recordStoreClusterKubectlStub answers the calls `down` makes. HELD is what
// `get secrets -o name` prints; GET_EXIT is its exit code, so a read that
// failed can be measured apart from a read that found nothing. Everything
// else is logged and answered with exit 0, and an unexpected verb fails.
const recordStoreClusterKubectlStub = `
echo "kubectl $*" >> "$STUB_LOG"
case "$*" in
  *"get secrets"*)
    if [ -n "${HELD:-}" ]; then printf '%s\n' "$HELD"; fi
    if [ -n "${GET_STDERR:-}" ]; then printf '%s\n' "$GET_STDERR" >&2; fi
    exit "${GET_EXIT:-0}" ;;
  *"delete namespace"*|*"delete clusterrolebinding"*|*"delete clusterrole"*)
    exit 0 ;;
esac
echo "stub kubectl: the recipe made a call this test did not expect: $*" >&2
exit 1
`

// TestRecordStoreClusterDownRefusesWhileRecordsAreHeld runs `just down`. For
// a record-backed resource the record is the only copy of the estate's
// identity for it, and deleting the namespace deletes every record in it,
// so the refusal is the whole point of the recipe: it has to exit non-zero,
// say so, and never reach `kubectl delete namespace`. A count that could
// not be read is not zero (#1448): a kubectl that could not reach the
// server prints nothing, and a recipe that read that as an empty namespace
// would delete it.
func TestRecordStoreClusterDownRefusesWhileRecordsAreHeld(t *testing.T) {
	recordStoreBins(t)
	for _, tc := range []struct {
		name        string
		held        string
		getExit     string
		getStderr   string
		wantRefusal bool
		wantSaid    []string
	}{
		{
			name:        "one record is held",
			held:        "secret/tofu-record-0a1b2c",
			wantRefusal: true,
			wantSaid:    []string{"REFUSING", "1 record Secret(s)", "secret/tofu-record-0a1b2c"},
		},
		{
			name:        "many records are held",
			held:        strings.Repeat("secret/tofu-record-x\n", 7),
			wantRefusal: true,
			wantSaid:    []string{"REFUSING", "7 record Secret(s)"},
		},
		{
			name:        "the count could not be read",
			getExit:     "1",
			getStderr:   "Unable to connect to the server: dial tcp 127.0.0.1:6443: connect: connection refused",
			wantRefusal: true,
			wantSaid:    []string{"REFUSING", "could not be counted", "connection refused"},
		},
		{
			name:        "nothing is held",
			wantRefusal: false,
		},
		{
			// A namespace that is already gone is not a refusal: the grant
			// still comes down.
			name:        "the namespace is already gone",
			getExit:     "1",
			getStderr:   `Error from server (NotFound): namespaces "tofu-records-chdf-guard" not found`,
			wantRefusal: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "kubectl", recordStoreClusterKubectlStub)
			env := recordStoreClusterEnv(t, stubDir, "HELD="+tc.held, "GET_EXIT="+tc.getExit, "GET_STDERR="+tc.getStderr)
			code, out := recordStoreClusterRun(t, env, "down", "chdf-guard")
			calls := recordStoreCalls(t, stubDir)
			deletedNS := strings.Contains(calls, "delete namespace tofu-records-chdf-guard")
			deletedGrant := strings.Contains(calls, "delete clusterrole choudoufu-estate-chdf-guard")

			if tc.wantRefusal {
				if code == 0 {
					t.Errorf("`just down` exited 0; a refusal that does not exit non-zero stops nothing:\n%s", out)
				}
				if deletedNS || deletedGrant {
					t.Errorf("`just down` refused and deleted anyway. It ran:\n%s\noutput:\n%s", calls, out)
				}
				for _, said := range tc.wantSaid {
					if !strings.Contains(out, said) {
						t.Errorf("`just down`'s refusal does not say %q, so it does not say what it found:\n%s", said, out)
					}
				}
				return
			}
			if code != 0 {
				t.Errorf("`just down` exited %d over an empty namespace:\n%s", code, out)
			}
			if !deletedGrant {
				t.Errorf("`just down` never deleted the estate grant. It ran:\n%s\noutput:\n%s", calls, out)
			}
			if tc.getStderr == "" && !deletedNS {
				t.Errorf("`just down` over an empty namespace never called `kubectl delete namespace`, so the cases above prove nothing about the refusal. It ran:\n%s\noutput:\n%s", calls, out)
			}
			if strings.Contains(out, "REFUSING") {
				t.Errorf("`just down` said REFUSING and exited 0:\n%s", out)
			}
		})
	}
}

// TestRecordStoreClusterSelftestRunsInCI holds the kind selftest to the
// workflow that has a cluster: k8s-smoke.yml must run
// examples/record-store-cluster/selftest.sh, read its PASS verdict line
// rather than its exit code, run the BREAK=1 control and read its "-> caught"
// line, and be triggered by a change under the example. A selftest nothing
// runs is a README.
//
// Proving it red: delete the job, or drop the grep for the PASS line.
func TestRecordStoreClusterSelftestRunsInCI(t *testing.T) {
	wf, err := os.ReadFile(k8sSmokeWorkflow)
	if err != nil {
		t.Fatalf("read %s: %v", k8sSmokeWorkflow, err)
	}
	text := string(wf)
	for _, want := range []string{
		`- "examples/record-store-cluster/**"`,
		"bash examples/record-store-cluster/selftest.sh",
		"'^PASS: record-store-cluster selftest - '",
		"'^PASS: record-store-cluster selftest (BREAK=1) - '",
		"'^  -> caught'",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not contain %q, so the record-store-cluster selftest is not run, not triggered, or passed on an exit code", k8sSmokeWorkflow, want)
		}
	}
	selftest, err := os.ReadFile(filepath.Join(recordStoreClusterProject, "selftest.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// The verdict lines the workflow greps for are the ones the script
	// prints; a rename on one side is a job that passes on nothing.
	for _, want := range []string{
		`"PASS: record-store-cluster selftest - `,
		`"PASS: record-store-cluster selftest (BREAK=1) - `,
		`"  -> caught`,
	} {
		if !strings.Contains(string(selftest), want) {
			t.Errorf("selftest.sh does not print %s, which the workflow reads for its verdict", want)
		}
	}
}
