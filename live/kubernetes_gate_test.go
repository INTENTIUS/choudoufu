// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The Kubernetes estate boundary (#1066) is two shipped templates,
// live/kubernetes/estate-boundary.yaml (the ValidatingAdmissionPolicy a
// cluster admin installs once) and live/kubernetes/estate-grant.yaml (the
// ClusterRole that grants one estate), inlined verbatim in
// live/MARKERS.md under "Granting a Kubernetes estate" the way the IAM
// grant is, and applied by claim 13's Kubernetes scenario. The policy asks the
// authorizer for one virtual triple, group/resource/verb, and the grant
// allows exactly that triple; nothing but this test holds the two to each
// other, since the resource exists nowhere a cluster could check.
//
// Proving it red: change the verb in estate-grant.yaml to "get", or edit
// the inlined copy in MARKERS.md by hand; each fails a different check
// below.

const (
	k8sBoundaryTemplate = "kubernetes/estate-boundary.yaml"
	k8sGrantTemplate    = "kubernetes/estate-grant.yaml"
	k8sBoundaryScenario = "smoke/scenarios/k8s-the-label-is-the-boundary.sh"
	k8sMarkersDoc       = "MARKERS.md"
)

// virtualEstateCheck is the authorizer call the policy's CEL makes:
// authorizer.group(G).resource(R).name(...).check(V).
var virtualEstateCheck = regexp.MustCompile(`authorizer\.group\('([^']+)'\)\.resource\('([^']+)'\)\.name\([^)]*\)\.check\('([^']+)'\)`)

func yamlDocs(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var docs []map[string]any
	for {
		var d map[string]any
		if err := dec.Decode(&d); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("%s: not valid YAML: %v", path, err)
		}
		docs = append(docs, d)
	}
	return docs
}

func kindsOf(docs []map[string]any) []string {
	var kinds []string
	for _, d := range docs {
		k, _ := d["kind"].(string)
		kinds = append(kinds, k)
	}
	return kinds
}

// TestKubernetesGateTemplatesAgree: the policy and the grant name one
// virtual resource, the policy matches on the estate label, and the grant
// carries the placeholders the scenario and MARKERS.md say it does.
func TestKubernetesGateTemplatesAgree(t *testing.T) {
	policy := yamlDocs(t, k8sBoundaryTemplate)
	if got := kindsOf(policy); strings.Join(got, ",") != "ValidatingAdmissionPolicy,ValidatingAdmissionPolicyBinding" {
		t.Fatalf("%s holds %v, want a ValidatingAdmissionPolicy then its binding", k8sBoundaryTemplate, got)
	}
	raw, _ := os.ReadFile(k8sBoundaryTemplate)
	checks := virtualEstateCheck.FindAllStringSubmatch(string(raw), -1)
	if len(checks) != 2 {
		t.Fatalf("%s makes %d authorizer checks, want 2 (the estate being left and the one being entered)", k8sBoundaryTemplate, len(checks))
	}
	group, resource, verb := checks[0][1], checks[0][2], checks[0][3]
	for _, c := range checks[1:] {
		if c[1] != group || c[2] != resource || c[3] != verb {
			t.Errorf("%s: the two authorizer checks name different triples: %v vs %v", k8sBoundaryTemplate, checks[0][1:], c[1:])
		}
	}
	if !strings.Contains(string(raw), "key: tofu-estate") || !strings.Contains(string(raw), "operator: Exists") {
		t.Errorf("%s does not select objects by the tofu-estate label", k8sBoundaryTemplate)
	}
	if !strings.Contains(string(raw), `operations: ["CREATE", "UPDATE", "DELETE"]`) {
		t.Errorf("%s does not name CREATE, UPDATE and DELETE; the docs say the fence covers exactly those", k8sBoundaryTemplate)
	}

	grant := yamlDocs(t, k8sGrantTemplate)
	if got := kindsOf(grant); strings.Join(got, ",") != "ClusterRole,ClusterRoleBinding" {
		t.Fatalf("%s holds %v, want a ClusterRole then its binding", k8sGrantTemplate, got)
	}
	rules, _ := grant[0]["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("%s: the ClusterRole has %d rules, want exactly one (the virtual estate)", k8sGrantTemplate, len(rules))
	}
	rule, _ := rules[0].(map[string]any)
	want := map[string]string{"apiGroups": group, "resources": resource, "verbs": verb, "resourceNames": "ESTATE"}
	for key, w := range want {
		vals, _ := rule[key].([]any)
		if len(vals) != 1 || vals[0] != w {
			t.Errorf("%s: rule %s = %v, want [%s] (what the policy's authorizer check asks for)", k8sGrantTemplate, key, vals, w)
		}
	}
	graw, _ := os.ReadFile(k8sGrantTemplate)
	for _, ph := range []string{"ESTATE", "PRINCIPAL", "PRINCIPAL_NAMESPACE"} {
		if !strings.Contains(string(graw), ph) {
			t.Errorf("%s carries no %s placeholder", k8sGrantTemplate, ph)
		}
	}
}

// TestKubernetesGateTemplatesAreShipped: MARKERS.md inlines both templates
// byte for byte, and claim 13's Kubernetes scenario applies the shipped files rather
// than a copy of its own.
func TestKubernetesGateTemplatesAreShipped(t *testing.T) {
	markers, err := os.ReadFile(k8sMarkersDoc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markers), "### Granting a Kubernetes estate") {
		t.Fatalf("%s has no \"Granting a Kubernetes estate\" section", k8sMarkersDoc)
	}
	scenario, err := os.ReadFile(k8sBoundaryScenario)
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range []string{k8sBoundaryTemplate, k8sGrantTemplate} {
		raw, err := os.ReadFile(tmpl)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(markers), "```yaml\n"+string(raw)+"```") {
			t.Errorf("%s does not inline live/%s verbatim in a yaml fence; re-copy it (the file is the source, the doc renders it)", k8sMarkersDoc, tmpl)
		}
		if !strings.Contains(string(scenario), `"$ROOT/live/`+tmpl+`"`) {
			t.Errorf("%s does not apply $ROOT/live/%s; the scenario must exercise the shipped file", k8sBoundaryScenario, tmpl)
		}
	}
}

// controlPlaneName is one quoted username inside the policy's
// not-the-control-plane match condition.
var controlPlaneName = regexp.MustCompile(`'([^']+)'`)

// TestKubernetesGateExemptsTheControlPlaneByName: the first match condition
// exempts named identities and nothing wider (#1448, section C). It used to
// exempt every ServiceAccount in kube-system and every username beginning
// system:kube-, which handed every estate to any add-on installed there.
// The controls the list has to keep are on kind, in claim 13 on Kubernetes: a labelled
// Deployment, Service, Job and StatefulSet still roll out and are still
// collected.
//
// Proving it red: put a startsWith back into the expression, or add
// 'system:serviceaccount:kube-system:coredns' to the list.
func TestKubernetesGateExemptsTheControlPlaneByName(t *testing.T) {
	policy := yamlDocs(t, k8sBoundaryTemplate)[0]
	spec, _ := policy["spec"].(map[string]any)
	conds, _ := spec["matchConditions"].([]any)
	expr := ""
	for _, c := range conds {
		cond, _ := c.(map[string]any)
		if cond["name"] == "not-the-control-plane" {
			expr, _ = cond["expression"].(string)
		}
	}
	if expr == "" {
		t.Fatalf("%s declares no match condition named not-the-control-plane", k8sBoundaryTemplate)
	}
	for _, wide := range []string{"startsWith", "endsWith", "matches", "contains"} {
		if strings.Contains(expr, wide+"(") {
			t.Errorf("not-the-control-plane calls %s: the exemption is a list of names, and a pattern exempts identities nobody listed", wide)
		}
	}

	const sa = "system:serviceaccount:kube-system:"
	named := map[string]bool{}
	for _, m := range controlPlaneName.FindAllStringSubmatch(expr, -1) {
		named[m[1]] = true
	}
	// What a labelled workload cannot roll out or be deleted without.
	for _, need := range []string{
		"system:nodes", "system:apiserver", "system:kube-scheduler", "system:kube-controller-manager",
		sa + "deployment-controller", sa + "replicaset-controller", sa + "statefulset-controller",
		sa + "daemon-set-controller", sa + "job-controller", sa + "cronjob-controller",
		sa + "endpointslice-controller", sa + "generic-garbage-collector", sa + "namespace-controller",
		sa + "pvc-protection-controller", sa + "persistent-volume-binder",
	} {
		if !named[need] {
			t.Errorf("not-the-control-plane does not name %s", need)
		}
	}
	// What the ruling takes out: an add-on is judged like everyone else.
	for _, addon := range []string{sa + "default", sa + "coredns", sa + "kube-proxy", sa + "kindnet", "system:kube-proxy"} {
		if named[addon] {
			t.Errorf("not-the-control-plane names %s, which is not one of the control plane's own controllers; it gets a binding from %s instead", addon, k8sGrantTemplate)
		}
	}
	for name := range named {
		if name == "system:nodes" || name == "system:apiserver" || name == "system:kube-scheduler" || name == "system:kube-controller-manager" {
			continue
		}
		if !strings.HasPrefix(name, sa) {
			t.Errorf("not-the-control-plane names %q, which is neither a control-plane username nor a kube-system ServiceAccount", name)
		}
	}
}
