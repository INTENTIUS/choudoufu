// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

func TestJointToolFromArgs(t *testing.T) {
	cases := []struct {
		name  string
		argv  []string
		kind  string
		phase string
		none  bool
	}{
		{"terragrunt run --all plan", []string{"/usr/bin/terragrunt", "run", "--all", "plan"}, "terragrunt", "plan", false},
		{"terragrunt run --all apply", []string{"terragrunt", "run", "--all", "apply", "--", "tfplan"}, "terragrunt", "apply", false},
		{"terragrunt run-all", []string{"terragrunt", "run-all", "plan"}, "terragrunt", "plan", false},
		{"atmos affected", []string{"atmos", "terraform", "plan", "--affected"}, "atmos", "plan", false},
		{"atmos all apply", []string{"atmos", "terraform", "apply", "--all"}, "atmos", "apply", false},
		{"one unit", []string{"terragrunt", "plan"}, "", "", true},
		{"one component", []string{"atmos", "terraform", "plan", "network", "-s", "dev"}, "", "", true},
		{"a shell", []string{"-zsh"}, "", "", true},
		{"make", []string{"make", "plan", "--all"}, "", "", true},
		{"nothing", nil, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jointToolFromArgs(4242, "Sat Oct 24 15:00:00 2026", tc.argv)
			if tc.none {
				if got != nil {
					t.Fatalf("got %+v, want none", got)
				}
				return
			}
			if got == nil || got.Kind != tc.kind || got.Phase != tc.phase || got.PID != 4242 {
				t.Fatalf("got %+v, want kind %s phase %s", got, tc.kind, tc.phase)
			}
		})
	}
	a := jointToolFromArgs(1, "a", []string{"terragrunt", "--all"})
	b := jointToolFromArgs(1, "b", []string{"terragrunt", "--all"})
	if a.ID == b.ID {
		t.Errorf("a pid reused at another start time keeps the id %s", a.ID)
	}
}

func TestJointUnitName(t *testing.T) {
	cases := []struct {
		dir  string
		vf   []string
		want string
	}{
		{"/r/live/network/.terragrunt-cache/AbC/dEf", nil, "network"},
		{"/r/components/terraform/vpc", []string{"/r/components/terraform/vpc/dev-vpc.terraform.tfvars.json"}, "dev-vpc"},
		{"/r/components/terraform/vpc", nil, "vpc"},
		{"/r/9lives", nil, "u_9lives"},
	}
	for _, tc := range cases {
		if got := jointUnitName(tc.dir, tc.vf); got != tc.want {
			t.Errorf("jointUnitName(%q, %v) = %q, want %q", tc.dir, tc.vf, got, tc.want)
		}
	}
}

func jointTestUnits() map[string]*jointUnit {
	return map[string]*jointUnit{
		"network": {ID: "network", Outputs: []jointOutputDecl{{Name: "vpc_id"}}},
	}
}

func TestJointStringHCL(t *testing.T) {
	units := jointTestUnits()
	cases := []struct {
		in, want string
		err      string
	}{
		{"plain", `"plain"`, ""},
		{`say "hi" ${x} %{y}`, `"say \"hi\" $${x} %%{y}"`, ""},
		{jointSentinel("network", "vpc_id"), `module.network.vpc_id`, ""},
		{"prefix-" + jointSentinel("network", "vpc_id") + "-suffix", `"prefix-${module.network.vpc_id}-suffix"`, ""},
		{jointSentinel("nowhere", "x"), "", "not part of this run"},
		{jointSentinel("network", "gone"), "", "declares no such output"},
	}
	for _, tc := range cases {
		got, err := jointStringHCL(tc.in, units)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: error %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func jointVarOf(t *testing.T, v cty.Value) jointVar {
	t.Helper()
	ty, _ := v.Type().MarshalJSON()
	b, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		t.Fatal(err)
	}
	return jointVar{Type: ty, Value: b}
}

// Each unit becomes a module call carrying its inputs, an input holding the
// placeholder of another unit's output becomes a reference to it, and every
// declared output is a root output.
func TestRenderJointRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	units := []jointUnit{
		{ID: "network", Dir: filepath.Join(filepath.Dir(root), "net"), Seq: 1,
			Vars:    map[string]jointVar{"cidr": jointVarOf(t, cty.StringVal("10.0.0.0/16")), "n": jointVarOf(t, cty.NumberIntVal(3))},
			Outputs: []jointOutputDecl{{Name: "vpc_id"}, {Name: "key", Sensitive: true}}},
		{ID: "app", Dir: filepath.Join(filepath.Dir(root), "app"), Seq: 2,
			Vars: map[string]jointVar{
				"vpc_id": jointVarOf(t, cty.StringVal(jointSentinel("network", "vpc_id"))),
				"tags": jointVarOf(t, cty.ObjectVal(map[string]cty.Value{
					"vpc": cty.StringVal("v=" + jointSentinel("network", "vpc_id")),
					"env": cty.StringVal("dev"),
				})),
				"azs": jointVarOf(t, cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})),
				"off": jointVarOf(t, cty.NullVal(cty.String)),
			}},
	}
	text, err := renderJointRoot(root, units)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"module \"network\" {\n  source = \"../net\"\n  cidr = \"10.0.0.0/16\"\n  n = 3\n}",
		"module \"app\" {\n  source = \"../app\"\n  azs = [\"a\", \"b\"]\n  off = null\n  tags = {\"env\" = \"dev\", \"vpc\" = \"v=${module.network.vpc_id}\"}\n  vpc_id = module.network.vpc_id\n}",
		"output \"network__vpc_id\" {\n  value = module.network.vpc_id\n}",
		"output \"network__key\" {\n  value = module.network.key\n  sensitive = true\n}",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("root is missing:\n%s\n--- got ---\n%s", want, text)
		}
	}
}

func TestJointSliceDoc(t *testing.T) {
	var doc map[string]any
	err := json.Unmarshal([]byte(`{
	  "format_version": "1.2",
	  "resource_changes": [
	    {"address": "module.network.aws_vpc.main", "module_address": "module.network", "change": {"actions": ["create"]}},
	    {"address": "module.app.aws_s3_bucket.logs", "module_address": "module.app", "change": {"actions": ["delete","create"]}},
	    {"address": "module.app.module.inner.aws_x.y", "module_address": "module.app.module.inner", "change": {"actions": ["update"]}}
	  ],
	  "output_changes": {
	    "network__vpc_id": {"actions": ["create"], "after_unknown": true},
	    "app__bucket": {"actions": ["create"], "after": "logs", "after_unknown": false, "after_sensitive": false}
	  },
	  "planned_values": {"root_module": {"child_modules": [
	    {"address": "module.app", "resources": [{"address": "module.app.aws_s3_bucket.logs"}]},
	    {"address": "module.network", "resources": [{"address": "module.network.aws_vpc.main"}]}
	  ]}},
	  "configuration": {"provider_config": {"module.app:aws": {"name": "aws"}, "module.network:aws": {"name": "aws"}},
	    "root_module": {"module_calls": {"app": {"module": {"outputs": {"bucket": {}}}}}}}
	}`), &doc)
	if err != nil {
		t.Fatal(err)
	}
	s := jointSliceDoc(doc, "app")
	rcs := s["resource_changes"].([]any)
	if len(rcs) != 2 {
		t.Fatalf("app has %d resource changes, want 2: %v", len(rcs), rcs)
	}
	first := rcs[0].(map[string]any)
	if first["address"] != "aws_s3_bucket.logs" {
		t.Errorf("address %v not made relative to the unit", first["address"])
	}
	if _, has := first["module_address"]; has {
		t.Errorf("the unit's own module_address survived: %v", first)
	}
	if second := rcs[1].(map[string]any); second["address"] != "module.inner.aws_x.y" || second["module_address"] != "module.inner" {
		t.Errorf("nested module not made relative: %v", second)
	}
	oc := s["output_changes"].(map[string]any)
	if _, ok := oc["bucket"]; !ok || len(oc) != 1 {
		t.Errorf("output_changes %v, want only bucket", oc)
	}
	pv := s["planned_values"].(map[string]any)
	if v := pv["outputs"].(map[string]any)["bucket"].(map[string]any)["value"]; v != "logs" {
		t.Errorf("planned output bucket = %v", v)
	}
	rm := pv["root_module"].(map[string]any)["resources"].([]any)[0].(map[string]any)
	if rm["address"] != "aws_s3_bucket.logs" {
		t.Errorf("planned_values resource address %v", rm["address"])
	}
	if pc := s["configuration"].(map[string]any)["provider_config"].(map[string]any); len(pc) != 1 || pc["aws"] == nil {
		t.Errorf("provider_config %v", pc)
	}
	if add, chg, del := jointCounts(s); add != 1 || chg != 1 || del != 1 {
		t.Errorf("counts add=%d change=%d destroy=%d, want 1 1 1", add, chg, del)
	}
}

func TestJointUnitBlocks(t *testing.T) {
	text := "OpenTofu will perform the following actions:\n\n" +
		"  # module.app.aws_s3_bucket.logs will be created\n  + resource \"aws_s3_bucket\" \"logs\" {\n      + bucket = \"x\"\n    }\n\n" +
		"  # module.network.aws_vpc.main will be created\n  + resource \"aws_vpc\" \"main\" {\n    }\n\nPlan: 2 to add, 0 to change, 0 to destroy.\n"
	got := jointUnitBlocks(text, "app")
	if !strings.Contains(got, "aws_s3_bucket") || strings.Contains(got, "aws_vpc") || strings.Contains(got, "Plan:") {
		t.Errorf("blocks for app:\n%s", got)
	}
}

func TestJointStubAndSession(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	sess, err := openJointSession("terragrunt-1-abc")
	if err != nil {
		t.Fatal(err)
	}
	// A unit keeps its id and place when it registers again; another with the
	// same name gets a new one.
	a := sess.register(jointUnit{Key: "/a"}, "net")
	b := sess.register(jointUnit{Key: "/b"}, "net")
	a2 := sess.register(jointUnit{Key: "/a", Dir: "/changed"}, "net")
	if a.ID != "net" || b.ID != "net_2" || a2.ID != "net" || a2.Seq != a.Seq || b.Seq != a.Seq+1 {
		t.Errorf("ids/seqs: %+v %+v %+v", a, b, a2)
	}
	if us := sess.units(); len(us) != 2 || us[0].Dir != "/changed" {
		t.Errorf("units: %+v", us)
	}

	stubPath := filepath.Join(t.TempDir(), "tfplan")
	body, _ := json.Marshal(jointStub{Magic: 1, Session: sess.dir, Unit: "net", Serial: 2, Units: []string{"net", "net_2"}})
	os.WriteFile(stubPath, body, 0o644)
	st, ok := readJointStub(stubPath)
	if !ok || st.Unit != "net" || st.Session != sess.dir || st.Serial != 2 {
		t.Errorf("stub: %+v %v", st, ok)
	}
	plain := filepath.Join(t.TempDir(), "real.plan")
	os.WriteFile(plain, []byte("PK\x03\x04 a zip, not a stub"), 0o644)
	if _, ok := readJointStub(plain); ok {
		t.Errorf("a plan file was taken for a stub")
	}

	// The lock is exclusive, and a lock whose holder is gone is taken over.
	unlock, err := sess.lock()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHOUDOUFU_JOINT_TIMEOUT", "300ms")
	if _, err := sess.lock(); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("second lock: %v, want a timeout", err)
	}
	unlock()
	os.WriteFile(sess.path("lock"), []byte("2147483646"), 0o600)
	if u, err := sess.lock(); err != nil {
		t.Errorf("a dead holder's lock was not taken over: %v", err)
	} else {
		u()
	}
}

// Atmos parses the result of !terraform.output as YAML, so the placeholder must
// stay a plain scalar: a colon-space would make it a one-entry map.
func TestJointSentinelIsAPlainYAMLScalar(t *testing.T) {
	s := jointSentinel("dev-network", "subnet_b_id")
	if strings.Contains(s, ": ") || strings.Contains(s, " #") {
		t.Errorf("sentinel %q would parse as more than a string", s)
	}
	if !jointSentinelRE.MatchString(s) {
		t.Errorf("sentinel %q does not match its own pattern", s)
	}
}

func TestJointRerunCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"/usr/local/bin/terragrunt", "run", "--all", "--out-dir", "/w/plans", "--", "apply"}, "terragrunt run --all --out-dir /w/plans -- plan"},
		{[]string{"atmos", "terraform", "deploy", "--affected", "--include-dependents", "--ci"}, "atmos terraform plan --affected --include-dependents --ci"},
		{[]string{"terragrunt", "run", "--all", "--", "apply", "-auto-approve"}, "terragrunt run --all -- plan"},
		{[]string{"terragrunt", "run", "--all", "--", "plan"}, ""},
		{nil, ""},
	} {
		if got := jointRerunCommand(&jointTool{Args: tc.args}); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestJointInputDiff(t *testing.T) {
	v := func(s string) jointVar { return jointVar{Type: json.RawMessage(`"string"`), Value: json.RawMessage(s)} }
	reviewed := jointUnit{Vars: map[string]jointVar{
		"cidr":      v(`"10.0.2.0/24"`),
		"subnet_id": v(`"(known after apply from network.subnet_id)"`),
		"port":      v(`8080`),
	}}
	same := jointUnit{Vars: map[string]jointVar{"cidr": v(`"10.0.2.0/24"`), "subnet_id": v(`"subnet-123"`), "port": v(`8080`)}}
	if d := jointInputDiff(reviewed, same); len(d) != 0 {
		t.Errorf("a placeholder filled in, nothing else moved: %v", d)
	}
	moved := jointUnit{Vars: map[string]jointVar{"cidr": v(`"10.9.9.0/24"`), "subnet_id": v(`"subnet-123"`), "port": v(`8081`)}}
	if d := jointInputDiff(reviewed, moved); strings.Join(d, ",") != "cidr,port" {
		t.Errorf("got %v, want cidr and port", d)
	}
	// Terragrunt gives an apply of a plan file no inputs at all.
	if d := jointInputDiff(reviewed, jointUnit{}); len(d) != 0 {
		t.Errorf("no inputs given: %v", d)
	}
}

func TestJointSliceDiff(t *testing.T) {
	doc := func(after, unknown string, actions string) map[string]any {
		var m map[string]any
		body := `{"resource_changes":[` +
			`{"address":"terraform_data.worker","mode":"managed","change":{"actions":` + actions + `,"before":null,"after":` + after + `,"after_unknown":` + unknown + `}},` +
			`{"address":"data.x.y","mode":"data","change":{"actions":["read"]}},` +
			`{"address":"terraform_data.same","mode":"managed","change":{"actions":["no-op"],"before":{"input":1},"after":{"input":1},"after_unknown":{}}}]}`
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	reviewed := doc(`{"input":{"cidr":"10.0.2.0/24"}}`, `{"id":true,"input":{"subnet":true}}`, `["create"]`)
	filled := doc(`{"input":{"cidr":"10.0.2.0/24","subnet":"subnet-123"}}`, `{"id":true}`, `["create"]`)
	if d := jointSliceDiff(reviewed, filled); len(d) != 0 {
		t.Errorf("a value known after apply in the reviewed plan, known now: %v", d)
	}
	moved := doc(`{"input":{"cidr":"10.9.9.0/24","subnet":"subnet-123"}}`, `{"id":true}`, `["create"]`)
	if d := jointSliceDiff(reviewed, moved); len(d) != 1 || !strings.Contains(d[0], "other values for input") || strings.Contains(d[0], "10.9") {
		t.Errorf("a moved known value: %v", d)
	}
	gone := map[string]any{"resource_changes": []any{}}
	if d := jointSliceDiff(reviewed, gone); len(d) != 1 || !strings.Contains(d[0], "in the reviewed plan, not in this one") {
		t.Errorf("a change this plan no longer makes: %v", d)
	}
	upd := doc(`{"input":{"cidr":"10.0.2.0/24"}}`, `{"id":true,"input":{"subnet":true}}`, `["update"]`)
	if d := jointSliceDiff(reviewed, upd); len(d) != 1 || !strings.Contains(d[0], "create in the reviewed plan, update in this one") {
		t.Errorf("another action: %v", d)
	}
}

func TestJointStubsCarryTheLastPlan(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	sess, err := openJointSession("terragrunt-7-abc")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// Each unit has the directory unitFromCwd always sets: writeStubs renders
	// the composed root again to learn its sensitive inputs (#2270), and a
	// module source is relative to that directory.
	netDir, appDir := filepath.Join(dir, "net"), filepath.Join(dir, "app")
	net := sess.register(jointUnit{Key: "/net", Dir: netDir, Out: filepath.Join(dir, "network.tfplan")}, "network")
	// The output -json path registers again with no plan file; the unit's
	// plan file is kept.
	sess.register(jointUnit{Key: "/net", Dir: netDir}, "network")
	sess.register(jointUnit{Key: "/app", Dir: appDir, Out: filepath.Join(dir, "app.tfplan")}, "app")
	if us := sess.units(); us[0].Out != net.Out {
		t.Fatalf("the plan file of network was lost: %+v", us)
	}
	// A real plan file: writeStubs drops its sensitive planned values
	// (#2277), so it must open as one, and what the stubs carry is the
	// scrubbed copy rather than these bytes.
	plan := jsvPlanFile(t, jsvPlanned)
	os.WriteFile(sess.path("joint.plan"), plan, 0o600)
	os.WriteFile(sess.path("doc-2.json"), []byte(`{"resource_changes":[]}`), 0o600)
	sess.saveState(jointState{Serial: 2, OK: true, Units: []string{"network", "app"}})
	if err := sess.writeStubs(&jointTool{Kind: "terragrunt"}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"network.tfplan", "app.tfplan"} {
		st, ok := readJointStub(filepath.Join(dir, f))
		if !ok || st.Serial != 2 || len(st.Units) != 2 || st.Bundle == nil || st.Run != "terragrunt-7-abc" || len(st.Bundle.Units) != 2 {
			t.Errorf("%s does not carry the last joint plan: ok=%v serial=%d units=%v run=%q", f, ok, st.Serial, st.Units, st.Run)
			continue
		}
		if !st.Bundle.Scrubbed || bytes.Contains(jsvUnzipAll(t, st.Bundle.Plan), []byte(jsvPlanned)) {
			t.Errorf("%s carries the joint plan with its sensitive planned values", f)
		}
	}
	sess.saveState(jointState{Serial: 3, OK: false})
	if err := sess.writeStubs(&jointTool{Kind: "terragrunt"}); err == nil {
		t.Errorf("plan files were written over a failed joint plan")
	}
}
