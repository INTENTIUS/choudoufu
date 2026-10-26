// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/flags"
	"github.com/intentius/choudoufu/internal/command/workdir"
)

// jointModuleBlocks splits a rendered joint root into its module blocks, by
// unit id.
func jointModuleBlocks(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, part := range strings.Split(text, "module \"")[1:] {
		id, _, ok := strings.Cut(part, "\"")
		if !ok {
			t.Fatalf("unreadable module block: %q", part)
		}
		block, _, _ := strings.Cut(part, "\n}\n")
		out[id] = block
	}
	return out
}

// TestRenderJointRootSameNameTwoUnits is #2219's same-name item: two units
// declaring one variable name with different values each get their own,
// because each value is that unit's module call's argument.
func TestRenderJointRootSameNameTwoUnits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	units := []jointUnit{
		{ID: "east", Dir: filepath.Join(filepath.Dir(root), "east"), Seq: 1,
			Vars: map[string]jointVar{"name": jointVarOf(t, cty.StringVal("x-east"))}},
		{ID: "west", Dir: filepath.Join(filepath.Dir(root), "west"), Seq: 2,
			Vars: map[string]jointVar{"name": jointVarOf(t, cty.StringVal("x-west"))}},
	}
	text, err := renderJointRoot(root, units)
	if err != nil {
		t.Fatal(err)
	}
	blocks := jointModuleBlocks(t, text)
	if !strings.Contains(blocks["east"], `name = "x-east"`) || strings.Contains(blocks["east"], "x-west") {
		t.Errorf("east's call:\n%s\nwant name = \"x-east\" and nothing of west's", blocks["east"])
	}
	if !strings.Contains(blocks["west"], `name = "x-west"`) || strings.Contains(blocks["west"], "x-east") {
		t.Errorf("west's call:\n%s\nwant name = \"x-west\" and nothing of east's", blocks["west"])
	}
}

// TestRenderJointRootChangedInputOnlyItsModule is #2219's changed-input
// item: a changed input on a unit no other unit reads changes that unit's
// module call and nothing else in the joint root, so the joint plan can
// only change that module's resources.
func TestRenderJointRootChangedInputOnlyItsModule(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	units := func(port int64) []jointUnit {
		return []jointUnit{
			{ID: "network", Dir: filepath.Join(filepath.Dir(root), "network"), Seq: 1,
				Vars:    map[string]jointVar{"cidr": jointVarOf(t, cty.StringVal("10.0.0.0/16"))},
				Outputs: []jointOutputDecl{{Name: "vpc_id"}}},
			{ID: "app", Dir: filepath.Join(filepath.Dir(root), "app"), Seq: 2,
				Vars:    map[string]jointVar{"vpc_id": jointVarOf(t, cty.StringVal(jointSentinel("network", "vpc_id")))},
				Outputs: []jointOutputDecl{{Name: "url"}}},
			{ID: "edge", Dir: filepath.Join(filepath.Dir(root), "edge"), Seq: 3,
				Vars: map[string]jointVar{"port": jointVarOf(t, cty.NumberIntVal(port))}},
		}
	}
	before, err := renderJointRoot(root, units(8080))
	if err != nil {
		t.Fatal(err)
	}
	after, err := renderJointRoot(root, units(8081))
	if err != nil {
		t.Fatal(err)
	}
	b, a := jointModuleBlocks(t, before), jointModuleBlocks(t, after)
	if b["edge"] == a["edge"] || !strings.Contains(a["edge"], "port = 8081") {
		t.Errorf("edge's call did not take the new port:\n%s", a["edge"])
	}
	for _, id := range []string{"network", "app"} {
		if b[id] != a[id] {
			t.Errorf("%s's call changed with edge's input:\nbefore:\n%s\nafter:\n%s", id, b[id], a[id])
		}
	}
	if strings.Replace(before, "port = 8080", "port = 8081", 1) != after {
		t.Errorf("the root changed beyond edge's port:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestRenderJointRootSensitiveInput is #2219's sensitive item over the
// joint root: an input whose unit declares the variable sensitive = true
// reaches that unit's module wrapped in sensitive(), so the joint plan's
// configuration holds no constant of it, and a plain input is unchanged.
func TestRenderJointRootSensitiveInput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	secret := jointVarOf(t, cty.StringVal("hunter2"))
	secret.Sensitive = true
	units := []jointUnit{{ID: "db", Dir: filepath.Join(filepath.Dir(root), "db"), Seq: 1,
		Vars: map[string]jointVar{"password": secret, "name": jointVarOf(t, cty.StringVal("orders"))}}}
	text, err := renderJointRoot(root, units)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `password = sensitive("hunter2")`) {
		t.Errorf("the sensitive input is not wrapped:\n%s", text)
	}
	if !strings.Contains(text, `name = "orders"`) || strings.Contains(text, `sensitive("orders")`) {
		t.Errorf("the plain input was changed:\n%s", text)
	}
	if strings.Count(text, "hunter2") != 1 {
		t.Errorf("the sensitive value appears outside its own argument:\n%s", text)
	}
}

// writeJointTestUnit writes a unit directory with a live block, the
// variables named in decls (HCL bodies by name), and files.
func writeJointTestUnit(t *testing.T, dir string, decls map[string]string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("terraform {\n  live {\n    estate = \"joint-inputs\"\n  }\n}\n")
	for name, body := range decls {
		b.WriteString("variable \"" + name + "\" {\n" + body + "\n}\n")
	}
	files["main.tf"] = b.String()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// unitVarsAt registers the unit in dir the way a joint plan does, from its
// own directory, environment and flags, and returns its recorded variables.
func unitVarsAt(t *testing.T, dir string, vars arguments.Vars) map[string]jointVar {
	t.Helper()
	t.Chdir(dir)
	c := &jointCommand{name: "plan", meta: Meta{WorkingDir: workdir.NewDir(".")}}
	u, _, ok := c.unitFromCwd(&jointTool{Kind: "terragrunt"}, vars)
	if !ok {
		t.Fatalf("the unit in %s did not register", dir)
	}
	return u.Vars
}

func jointVarString(t *testing.T, v jointVar) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(v.Value, &s); err != nil {
		t.Fatalf("value %s: %v", v.Value, err)
	}
	return s
}

// TestJointUnitVarPrecedence is #2219's precedence item, over the value
// reader registration uses: TF_VAR_x set for the run reaches every unit
// that declares x and no other; a unit's own tfvars file wins over it; and
// a -var on the unit's command line wins over both, as in Terraform. A
// variable the unit declares sensitive is recorded so.
func TestJointUnitVarPrecedence(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TF_VAR_x", "from-env")
	t.Setenv("TF_VAR_secret", "s3cr3t")

	withFile := filepath.Join(base, "with-file")
	writeJointTestUnit(t, withFile, map[string]string{"x": "  type = string", "secret": "  type = string\n  sensitive = true"},
		map[string]string{"x.auto.tfvars": "x = \"from-file\"\n"})
	envOnly := filepath.Join(base, "env-only")
	writeJointTestUnit(t, envOnly, map[string]string{"x": "  type = string"}, map[string]string{})
	other := filepath.Join(base, "other")
	writeJointTestUnit(t, other, map[string]string{"y": "  type = string\n  default = \"y\""}, map[string]string{})

	got := unitVarsAt(t, withFile, nil)
	if s := jointVarString(t, got["x"]); s != "from-file" {
		t.Errorf("with-file: x = %q, want the unit's own tfvars over TF_VAR_x", s)
	}
	if !got["secret"].Sensitive || got["x"].Sensitive {
		t.Errorf("with-file: secret sensitive %v, x sensitive %v; want only secret recorded sensitive", got["secret"].Sensitive, got["x"].Sensitive)
	}

	got = unitVarsAt(t, withFile, arguments.Vars{flags.RawFlag{Name: "-var", Value: "x=from-flag"}})
	if s := jointVarString(t, got["x"]); s != "from-flag" {
		t.Errorf("with-file and -var: x = %q, want the flag over the file", s)
	}

	got = unitVarsAt(t, envOnly, nil)
	if s := jointVarString(t, got["x"]); s != "from-env" {
		t.Errorf("env-only: x = %q, want TF_VAR_x", s)
	}

	got = unitVarsAt(t, other, nil)
	if _, has := got["x"]; has {
		t.Errorf("other: recorded x %s, which it does not declare", got["x"].Value)
	}
}
