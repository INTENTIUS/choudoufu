// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJointTerragruntWiring(t *testing.T) {
	root := tgCanonical(t.TempDir())
	writeFiles(t, root, map[string]string{
		"root.hcl": `remote_state {
  backend = "s3"
  config = { bucket = "b", key = "${path_relative_to_include()}/tofu.tfstate" }
}
inputs = { region = "us-east-1" }
`,
		"network/terragrunt.hcl": `include "root" { path = find_in_parent_folders("root.hcl") }`,
		"app/terragrunt.hcl": `include "root" {
  path = find_in_parent_folders("root.hcl")
}

dependency "network" {
  config_path = "../network"
  mock_outputs = { subnet_id = "subnet-mock" }
}

locals {
  port = dependency.network.outputs.port
}

inputs = {
  subnet_id = dependency.network.outputs.subnet_id
  allowlist = [dependency.network.outputs.cidr_a, upper(dependency.network.outputs.cidr_b)]
  everything = dependency.network.outputs
  port       = local.port
  named      = "${get_env("X", "x")}-${dependency.network.outputs.name}"
  plain      = 3
}
`,
	})
	app := filepath.Join(root, "app")
	wired, unsure := jointTerragruntWiring(app)
	net := filepath.Join(root, "network")

	want := map[string]struct {
		expr string
		deps []jointDep
	}{
		"subnet_id":  {"__choudoufu_dep_0__", []jointDep{{Unit: net, Output: "subnet_id"}}},
		"allowlist":  {"[__choudoufu_dep_0__, upper(__choudoufu_dep_1__)]", []jointDep{{Unit: net, Output: "cidr_a"}, {Unit: net, Output: "cidr_b"}}},
		"everything": {"__choudoufu_dep_0__", []jointDep{{Unit: net}}},
	}
	if len(wired) != len(want) {
		t.Fatalf("wired %v, want %d inputs", wired, len(want))
	}
	for k, w := range want {
		got, ok := wired[k]
		if !ok || got.Expr != w.expr || len(got.Deps) != len(w.deps) {
			t.Fatalf("input %s: got %+v, want %+v", k, got, w)
		}
		for i := range w.deps {
			if got.Deps[i] != w.deps[i] {
				t.Fatalf("input %s dep %d: got %+v, want %+v", k, i, got.Deps[i], w.deps[i])
			}
		}
	}
	// The local and the input that also calls get_env are reads choudoufu
	// cannot trace; each is unsure, naming the output it reads.
	if len(unsure) != 2 {
		t.Fatalf("unsure %+v, want 2", unsure)
	}
	outs := map[string]bool{}
	for _, u := range unsure {
		if u.Unit != net || len(u.Outputs) != 1 {
			t.Fatalf("unsure %+v", u)
		}
		outs[u.Outputs[0]] = true
	}
	if !outs["port"] || !outs["name"] {
		t.Fatalf("unsure outputs %v, want port and name", outs)
	}

	// A unit whose configuration cannot be read is unsure of every unit.
	_, unsure = jointTerragruntWiring(filepath.Join(root, "nothing-here"))
	if len(unsure) != 1 || unsure[0].Unit != "" {
		t.Fatalf("unreadable unit: unsure %+v, want one read of any unit", unsure)
	}
	if got := jointTerragruntDir("/r/app/.terragrunt-cache/abc/def"); got != "/r/app" {
		t.Errorf("jointTerragruntDir = %s", got)
	}
}

func TestJointResolveRendersWires(t *testing.T) {
	network := jointUnit{ID: "network", Ref: "/r/network", Dir: "/r/network", Vars: map[string]jointVar{}, Outputs: []jointOutputDecl{{Name: "port"}, {Name: "subnet-id"}}}
	app := jointUnit{ID: "app", Ref: "/r/app", Dir: "/r/app",
		Vars: map[string]jointVar{
			"port":      {Type: []byte(`"number"`), Value: []byte(`8080`)},
			"subnet":    {Type: []byte(`"string"`), Value: []byte(`"subnet-mock"`)},
			"elsewhere": {Type: []byte(`"string"`), Value: []byte(`"from-outside"`)},
		},
		Wired: map[string]jointWire{
			"port":      {Expr: "__choudoufu_dep_0__ - 10", Deps: []jointDep{{Unit: "/r/network", Output: "port"}}},
			"subnet":    {Expr: "__choudoufu_dep_0__", Deps: []jointDep{{Unit: "/r/network", Output: "subnet-id"}}},
			"elsewhere": {Expr: "__choudoufu_dep_0__", Deps: []jointDep{{Unit: "/r/other", Output: "x"}}},
		},
	}
	text, err := renderJointRoot("/r/.root", []jointUnit{network, app})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"port = module.network.port - 10",
		"subnet = module.network.subnet-id",
		// Its upstream is not in the run, so the tool's value stands.
		`elsewhere = "from-outside"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("root lacks %q:\n%s", want, text)
		}
	}

	app.Wired["port"] = jointWire{Expr: "__choudoufu_dep_0__", Deps: []jointDep{{Unit: "/r/network", Output: "nope"}}, Where: "app/terragrunt.hcl:3"}
	if _, err := renderJointRoot("/r/.root", []jointUnit{network, app}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("a read of an undeclared output: err %v", err)
	}
}

func TestJointUnsureCheck(t *testing.T) {
	units := []jointUnit{
		{ID: "network", Ref: "/r/network", Outputs: []jointOutputDecl{{Name: "port"}, {Name: "vpc"}}},
		{ID: "app", Ref: "/r/app", Unsure: []jointUnsure{{Unit: "/r/network", Outputs: []string{"port"}, Where: "local.port"}}},
		{ID: "edge", Ref: "/r/edge", Unsure: []jointUnsure{{Where: "read_terragrunt_config"}}},
	}
	doc := func(portAction string) map[string]any {
		return map[string]any{"output_changes": map[string]any{
			"network__port": map[string]any{"actions": []any{portAction}, "after_unknown": false},
			"network__vpc":  map[string]any{"actions": []any{"no-op"}, "after_unknown": false},
		}}
	}
	r, err := jointResolve(units)
	if err != nil {
		t.Fatal(err)
	}
	if got := jointUnsureCheck(units, r.unsure, doc("no-op")); len(got) != 0 {
		t.Fatalf("unchanged outputs: %v", got)
	}
	got := jointUnsureCheck(units, r.unsure, doc("update"))
	if len(got) != 2 || !strings.Contains(got[0], "unit app") || !strings.Contains(got[1], "unit edge") {
		t.Fatalf("a changed output read unsurely: %v", got)
	}

	// A wire partly outside the run falls back to the tool's value, and is
	// held to the planned part not changing.
	units[1].Unsure = nil
	units[1].Vars = map[string]jointVar{"x": {}}
	units[1].Wired = map[string]jointWire{"x": {Expr: "__choudoufu_dep_0__ + __choudoufu_dep_1__", Deps: []jointDep{{Unit: "/r/network", Output: "port"}, {Unit: "/elsewhere", Output: "y"}}, Where: "w"}}
	units[2].Unsure = nil
	r, _ = jointResolve(units)
	if _, wired := r.exprs["app"]["x"]; wired {
		t.Fatal("a wire partly outside the run was rendered")
	}
	if got := jointUnsureCheck(units, r.unsure, doc("update")); len(got) != 1 {
		t.Fatalf("partial wire: %v", got)
	}
}

func TestAtmosRead(t *testing.T) {
	cases := []struct {
		in             string
		unit, out, suf string
		parsed, ok     bool
	}{
		{"!terraform.state network subnet_b_id", atmosRef("dev", "network"), "subnet_b_id", "", true, true},
		{"!terraform.output network vpc_id", atmosRef("dev", "network"), "vpc_id", "", true, true},
		{"!terraform.state network prod vpc_id", atmosRef("prod", "network"), "vpc_id", "", true, true},
		{"!terraform.state vpc .private_subnet_ids[0]", atmosRef("dev", "vpc"), "private_subnet_ids", "[0]", true, true},
		{"!terraform.state vpc .config.name", atmosRef("dev", "vpc"), "config", ".name", true, true},
		{`!terraform.state vpc '.x // "d"'`, atmosRef("dev", "vpc"), "", "", false, true},
		{"!terraform.state {{ .vars.c }} x", "", "", "", false, true},
		{"10.0.0.0/16", "", "", "", false, false},
	}
	for _, tc := range cases {
		dep, suf, parsed, ok := atmosRead(tc.in, "dev")
		if ok != tc.ok || parsed != tc.parsed || dep.Unit != tc.unit || dep.Output != tc.out || suf != tc.suf {
			t.Errorf("%s: got %+v %q parsed=%v ok=%v", tc.in, dep, suf, parsed, ok)
		}
	}
}

func TestJointAtmosWiring(t *testing.T) {
	stacks := map[string]any{"dev": map[string]any{"components": map[string]any{"terraform": map[string]any{
		"network": map[string]any{"vars": map[string]any{"port": 8080}},
		"app": map[string]any{"vars": map[string]any{
			"subnet": "!terraform.state network subnet_b_id",
			"port":   "!terraform.output network port",
			"list":   []any{"!terraform.state network cidr"},
			"secret": "!store ssm /x",
			"stage":  "dev",
		}},
	}}}}
	stack, comp, section, ok := jointAtmosComponent(stacks, []string{"dev-app.terraform.tfvars.json"})
	if !ok || stack != "dev" || comp != "app" {
		t.Fatalf("component: %s %s %v", stack, comp, ok)
	}
	wired, unsure := jointAtmosWiring(stack, comp, section)
	if len(wired) != 2 || wired["subnet"].Deps[0].Output != "subnet_b_id" || wired["port"].Deps[0].Unit != atmosRef("dev", "network") {
		t.Fatalf("wired %+v", wired)
	}
	if len(unsure) != 2 {
		t.Fatalf("unsure %+v, want the nested read and the store", unsure)
	}
	if _, _, _, ok := jointAtmosComponent(stacks, []string{"prod-app.terraform.tfvars.json"}); ok {
		t.Fatal("found a component for another stack's var file")
	}
}

// "output" inside a joint run answers for a unit its plan registered, found
// from another copy of the unit in .terragrunt-cache too, and never
// registers one: the tool gives "output" no inputs (#2231, run 218).
func TestJointRegisteredUnit(t *testing.T) {
	sess := &jointSession{dir: t.TempDir()}
	if err := os.MkdirAll(sess.path("units"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := tgCanonical(t.TempDir())
	plan := filepath.Join(root, "network", ".terragrunt-cache", "a", "b")
	other := filepath.Join(root, "network", ".terragrunt-cache", "c", "d")
	for _, d := range []string{plan, other, filepath.Join(root, "app")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sess.register(jointUnit{Key: plan, Dir: plan, Ref: filepath.Join(root, "network"), Vars: map[string]jointVar{"port": {Value: []byte("8090")}}}, "network")
	tg := &jointTool{Kind: "terragrunt"}
	for _, dir := range []string{plan, other} {
		u, ok := jointRegisteredUnit(sess, tg, dir)
		if !ok || u.ID != "network" || len(u.Vars) != 1 {
			t.Fatalf("%s: got %+v %v", dir, u, ok)
		}
	}
	if _, ok := jointRegisteredUnit(sess, tg, filepath.Join(root, "app")); ok {
		t.Fatal("found a unit that never registered")
	}
	if _, ok := jointRegisteredUnit(sess, &jointTool{Kind: "atmos"}, other); ok {
		t.Fatal("matched an Atmos component by Terragrunt unit directory")
	}
	if n := len(sess.units()); n != 1 {
		t.Fatalf("%d units registered, want 1", n)
	}
}
