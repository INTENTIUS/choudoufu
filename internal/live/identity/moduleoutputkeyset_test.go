// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// #2142: a for_each or count whose key set is a child module's output,
// computed by that module from its own variables. The fixture carries
// terraform-google-modules/iam v8.3.0's helper module verbatim and its
// projects_iam module (modules/projects_iam/main.tf:33 and :52) on an AWS
// type, called the way corpus-k8s-infra-gcp-gcve's iam.tf calls it: the
// helper reshapes its variables into a set of keys and a map of bindings,
// the set drives for_each, and every identity argument reads the map back by
// each.key. Before #2142 every block here refused with "Module output not
// supported in static context". Stock OpenTofu plans it.
//
// The expected identities are asserted per instance, not counted: a key set
// that came back right with every instance bound to one binding would count
// the same and be wrong.
func TestModuleOutputKeySetResolves(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-keyset"), nil)

	result, diags := Resolve(context.Background(), cfg)
	for _, d := range diags {
		t.Errorf("unexpected diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
	}

	want := map[string]string{
		// gcve's own call: one project, so the helper keys every binding by
		// its "default" alias while the binding's name stays the project.
		`module.iam.aws_iam_role_policy.project_iam_authoritative["default--roles/admin"]`:  "k8s-infra-gcp-gcve:roles/admin",
		`module.iam.aws_iam_role_policy.project_iam_authoritative["default--roles/viewer"]`: "k8s-infra-gcp-gcve:roles/viewer",
		// main.tf:52's shape, over two projects.
		`module.iam_additive.aws_iam_role_policy.project_iam_additive["alpha--roles/viewer--user:a@example.com"]`: "alpha:user:a@example.com",
		`module.iam_additive.aws_iam_role_policy.project_iam_additive["beta--roles/viewer--user:a@example.com"]`:  "beta:user:a@example.com",
		// count over a module output.
		`aws_iam_role_policy.counted[0]`: "ops:counted",
		`aws_iam_role_policy.counted[1]`: "dev:counted",
		// A module call's for_each over a sibling module's output.
		`module.consumer["dev--read"].aws_iam_role_policy.this`:  "dev:consumer-read",
		`module.consumer["dev--write"].aws_iam_role_policy.this`: "dev:consumer-write",
		`module.consumer["ops--read"].aws_iam_role_policy.this`:  "ops:consumer-read",
		`module.consumer["ops--write"].aws_iam_role_policy.this`: "ops:consumer-write",
	}
	got := map[string]string{}
	for _, r := range result.All() {
		if r.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", r.Addr, r.Class)
		}
		got[r.Addr.String()] = r.ImportID
	}
	for addr, id := range want {
		if got[addr] != id {
			t.Errorf("%s resolved to %q, want %q", addr, got[addr], id)
		}
	}
	// Each projects_iam call's other mode's set is empty, so that block
	// has no instances at all - neither a refusal nor an invented one.
	for addr := range got {
		if _, ok := want[addr]; !ok {
			t.Errorf("%s resolved, and no instance by that address exists", addr)
		}
	}
}

// The adversarial half. Each block's key set is a module output that is not
// statically knowable, or a reference that is not one output of one
// instance. Asserted as "did not resolve" AND "the refusal stands", because
// a fix that turns a refusal into silence is worse than the refusal.
func TestModuleOutputKeySetRefuses(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-keyset-refused"), nil)

	result, diags := Resolve(context.Background(), cfg)

	resolved := map[string]bool{}
	for _, r := range result.All() {
		resolved[r.Addr.String()] = true
	}
	refusedAt := map[string]bool{}
	for _, d := range diags {
		refusedAt[d.Description().Detail] = true
	}
	refused := func(block string) bool {
		for detail := range refusedAt {
			if strings.Contains(detail, block+" for_each") || strings.Contains(detail, block+" count") ||
				strings.Contains(detail, "for "+block+" cannot") {
				return true
			}
		}
		return false
	}

	for _, tc := range []struct {
		block string
		why   string
	}{
		{"aws_iam_role_policy.from_managed", "the output reads a managed resource's attribute"},
		{"aws_iam_role_policy.count_from_managed", "the count's output reads a managed resource's attribute"},
		{"aws_iam_role_policy.from_data", "the output reads a data source this run has not read"},
		{"aws_iam_role_policy.from_provider_function", "the output calls a provider function"},
		{"aws_iam_role_policy.from_impure", "the output calls uuid()"},
		{"aws_iam_role_policy.sensitive", "the output is declared sensitive, and keys are written into markers in clear"},
		{"aws_iam_role_policy.sensitive_count", "the count's output is declared sensitive"},
		{"aws_iam_role_policy.missing_instance", "module.shard[7] is not an instance of a three-instance call"},
		{"aws_iam_role_policy.missing_instance_count", "module.shard[7] is not an instance of a three-instance call"},
		{"aws_iam_role_policy.unindexed_repeated", "module.shard.keys on a counted call is every instance's output, not one key set"},
		{"aws_iam_role_policy.whole_module", "module.helper is an object of every output"},
		{`module "consumer_managed"`, "a module call ranging over a managed-resource output"},
		{`module "consumer_sensitive"`, "a module call ranging over a sensitive output"},
		{`module "consumer_missing_instance"`, "a module call ranging over an instance that does not exist"},
		{`module "consumer_whole_module"`, "a module call ranging over a whole module"},
	} {
		for addr := range resolved {
			if strings.HasPrefix(addr, tc.block+"[") || strings.HasPrefix(addr, tc.block+".") ||
				strings.HasPrefix(addr, "module."+strings.Trim(strings.TrimPrefix(tc.block, "module "), `"`)+"[") {
				t.Errorf("%s resolved; it must not, because %s", addr, tc.why)
			}
		}
		if !refused(tc.block) {
			t.Errorf("%s carries no refusal; it must, because %s", tc.block, tc.why)
		}
	}

	// The one block here with a static identity of its own still resolves,
	// so the refusals above are not the whole run failing.
	var all []string
	for addr := range resolved {
		all = append(all, addr)
	}
	sort.Strings(all)
	if len(all) != 1 || all[0] != "module.live.aws_iam_role.r" {
		t.Errorf("resolved %v, want only module.live.aws_iam_role.r", all)
	}
}
