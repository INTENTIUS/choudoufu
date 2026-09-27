// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// examples/record-store-cluster/rbac, held to the same standard as
// examples/record-store-bucket/iam (GitHub issue #1603, the maintainer's
// 2026-09-26 ruling: "examples/record-store-cluster/rbac/ gets built
// regardless"). iam/render-policy.sh is the single source of the bucket's
// role policy, with a --read-only rendering for a plan identity; this
// mirrors both halves for the cluster store's own two identities.

const (
	rbacRenderer     = "../examples/record-store-cluster/rbac/render-role.sh"
	rbacExamples     = "../examples/record-store-cluster/rbac"
	rbacDocsPage     = "../examples/record-store-cluster/rbac/README.md"
	rbacGrantFile    = "example-apply.yaml"
	rbacReadOnlyFile = "example-plan-read-only.yaml"
)

func renderRBAC(t *testing.T, args ...string) []byte {
	t.Helper()
	for _, bin := range []string{"bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on PATH; this runs the real renderer and must not be skipped (a skipping guard is permanently green)", bin)
		}
	}
	cmd := exec.Command("bash", append([]string{rbacRenderer}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("render-role.sh %v: %v\n%s", args, err, stderr.String())
	}
	return out
}

// TestRBACTemplateHasOneSource is iam's TestIAMTemplateHasOneSource, mirrored:
// the renderer is the single source, the committed examples are its output,
// and the documentation page shows the same YAML a reader would copy.
func TestRBACTemplateHasOneSource(t *testing.T) {
	for file, args := range map[string][]string{
		rbacGrantFile:    {"prod"},
		rbacReadOnlyFile: {"prod", "--read-only"},
	} {
		want, err := os.ReadFile(filepath.Join(rbacExamples, file))
		if err != nil {
			t.Fatal(err)
		}
		if got := renderRBAC(t, args...); !bytes.Equal(got, want) {
			t.Errorf("%s is not what render-role.sh %s prints; re-render it", file, strings.Join(args, " "))
		}
	}

	page, err := os.ReadFile(rbacDocsPage)
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```yaml\n(.*?)\n```").FindAllSubmatch(page, -1)
	if len(blocks) < 2 {
		t.Fatalf("%s shows %d ```yaml block(s), want at least 2 (the grant and the read-only rendering)", rbacDocsPage, len(blocks))
	}
	wantGrant, _ := os.ReadFile(filepath.Join(rbacExamples, rbacGrantFile))
	wantReadOnly, _ := os.ReadFile(filepath.Join(rbacExamples, rbacReadOnlyFile))
	foundGrant, foundReadOnly := false, false
	for _, b := range blocks {
		shown := append(append([]byte{}, b[1]...), '\n')
		if bytes.Equal(shown, wantGrant) {
			foundGrant = true
		}
		if bytes.Equal(shown, wantReadOnly) {
			foundReadOnly = true
		}
	}
	if !foundGrant {
		t.Errorf("%s shows no ```yaml block matching %s byte for byte; the page is what people copy, and it must be the renderer's output", rbacDocsPage, rbacGrantFile)
	}
	if !foundReadOnly {
		t.Errorf("%s shows no ```yaml block matching %s byte for byte", rbacDocsPage, rbacReadOnlyFile)
	}
}

type rbacRenderedDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
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

func decodeRBAC(t *testing.T, raw []byte) []rbacRenderedDoc {
	t.Helper()
	var docs []rbacRenderedDoc
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var d rbacRenderedDoc
		if err := dec.Decode(&d); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("decoding render-role.sh output: %v\n%s", err, raw)
		}
		docs = append(docs, d)
	}
	return docs
}

// TestRBACVerbsMatchTheStoresLists ties the renderer to
// internal/live/staterecord/kubernetescontract.go's own lists, so a Role
// rendered here cannot drift from what the store actually asks of the
// cluster: KubernetesRecordVerbs for the grant, KubernetesPlanVerbs for the
// read-only rendering (GitHub issue #1370's plan-vs-apply distinction, the
// same one iam's --read-only mirrors for the bucket).
func TestRBACVerbsMatchTheStoresLists(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		verbs []string
	}{
		{"the grant", []string{"prod"}, staterecord.KubernetesRecordVerbs},
		{"the read-only rendering", []string{"prod", "--read-only"}, staterecord.KubernetesPlanVerbs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := decodeRBAC(t, renderRBAC(t, tc.args...))
			var role *rbacRenderedDoc
			for i := range docs {
				if docs[i].Kind == "Role" {
					role = &docs[i]
				}
			}
			if role == nil {
				t.Fatalf("render-role.sh %s printed no Role", strings.Join(tc.args, " "))
			}
			if len(role.Rules) != 1 {
				t.Fatalf("the Role has %d rule(s), want exactly one, on secrets", len(role.Rules))
			}
			r := role.Rules[0]
			if strings.Join(r.APIGroups, ",") != "" || strings.Join(r.Resources, ",") != "secrets" {
				t.Errorf("the Role grants %v in groups %v, want secrets in the core group only", r.Resources, r.APIGroups)
			}
			if got, want := strings.Join(r.Verbs, ","), strings.Join(tc.verbs, ","); got != want {
				t.Errorf("render-role.sh %s grants verbs [%s], want the store's list [%s]", strings.Join(tc.args, " "), got, want)
			}
		})
	}
}

// TestRBACRenderingIsNamespacedNeverCluster: the namespace is the read
// boundary (CONTRACT.md, read_isolation) and RBAC cannot condition on a
// label, so every object this renders has to be namespaced. A ClusterRole
// here would grant across every estate's namespace at once.
func TestRBACRenderingIsNamespacedNeverCluster(t *testing.T) {
	for _, args := range [][]string{{"prod"}, {"prod", "--read-only"}} {
		docs := decodeRBAC(t, renderRBAC(t, args...))
		if len(docs) != 3 {
			t.Fatalf("render-role.sh %s printed %d document(s), want a ServiceAccount, a Role and a RoleBinding", strings.Join(args, " "), len(docs))
		}
		for _, d := range docs {
			if strings.HasPrefix(d.Kind, "Cluster") {
				t.Errorf("render-role.sh %s printed a %s; everything here must be namespaced", strings.Join(args, " "), d.Kind)
			}
			if d.Metadata.Namespace == "" {
				t.Errorf("render-role.sh %s printed a %s with no namespace", strings.Join(args, " "), d.Kind)
			}
		}
	}
}

// TestRBACReadOnlyRenderingGrantsFewerVerbsThanTheGrant is iam's
// TestIAMReadOnlyRenderingAllowsNoWrite, mirrored: the read-only rendering
// must not grant create, update or delete, and the grant (without
// --read-only) must.
func TestRBACReadOnlyRenderingGrantsFewerVerbsThanTheGrant(t *testing.T) {
	verbsOf := func(args ...string) map[string]bool {
		docs := decodeRBAC(t, renderRBAC(t, args...))
		out := map[string]bool{}
		for _, d := range docs {
			if d.Kind != "Role" {
				continue
			}
			for _, r := range d.Rules {
				for _, v := range r.Verbs {
					out[v] = true
				}
			}
		}
		return out
	}
	grant := verbsOf("prod")
	readOnly := verbsOf("prod", "--read-only")
	for _, write := range []string{"create", "update", "delete"} {
		if !grant[write] {
			t.Errorf("the grant does not allow %s, so the read-only comparison below proves nothing", write)
		}
		if readOnly[write] {
			t.Errorf("the read-only rendering allows %s; a role that plans changes nothing in the namespace", write)
		}
	}
	for _, read := range []string{"get", "list"} {
		if !readOnly[read] {
			t.Errorf("the read-only rendering does not allow %s, so it cannot plan at all", read)
		}
	}
}

// TestRBACRendererRefusesWhatIsNotAnEstateName is iam's
// TestIAMRendererRefusesWhatIsNotAnEstateName, mirrored: an estate name
// reaches a Kubernetes object name and a namespace name unescaped, so a
// stray character here is a malformed manifest at best.
func TestRBACRendererRefusesWhatIsNotAnEstateName(t *testing.T) {
	for _, bad := range []string{"prod*", "prod/eu", "*", "Prod", ""} {
		out, err := exec.Command("bash", rbacRenderer, bad).CombinedOutput()
		if err == nil {
			t.Errorf("render-role.sh accepted %q:\n%s", bad, out)
		}
	}
}

// TestRBACRendererRefusesABadNamespace: --namespace overrides the default
// tofu-records-<estate>, and a Kubernetes namespace name is a DNS-1123
// label, so anything else would be silently mangled or refused later by the
// API server with no help from this renderer.
func TestRBACRendererRefusesABadNamespace(t *testing.T) {
	for _, bad := range []string{"UPPER", "-bad", "bad-", "has a space", strings.Repeat("a", 64)} {
		out, err := exec.Command("bash", rbacRenderer, "prod", "--namespace", bad).CombinedOutput()
		if err == nil {
			t.Errorf("render-role.sh accepted --namespace %q:\n%s", bad, out)
		}
	}
}

// TestRBACReadmeExplainsWhyThisIsRoleNotClusterRole holds the README to
// stating the reason read_isolation drives the shape of every manifest here,
// the way iam/README.md states why each IAM statement is the shape it is.
func TestRBACReadmeExplainsWhyThisIsRoleNotClusterRole(t *testing.T) {
	page, err := os.ReadFile(rbacDocsPage)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ClusterRole", "namespace", "read_isolation"} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("%s does not mention %q", rbacDocsPage, want)
		}
	}
}
