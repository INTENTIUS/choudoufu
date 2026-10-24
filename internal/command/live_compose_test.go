// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

const composeProviders = "terraform {\n  required_providers {\n    aws = {\n      source = \"hashicorp/aws\"\n    }\n  }\n}\n\nprovider \"aws\" {\n  region = \"us-east-1\"\n}\n\n"

// composeRoleProviders is the same provider block with the estate's own role.
func composeRoleProviders(role string) string {
	return "terraform {\n  required_providers {\n    aws = {\n      source = \"hashicorp/aws\"\n    }\n  }\n}\n\nprovider \"aws\" {\n  region = \"us-east-1\"\n  assume_role {\n    role_arn = \"arn:aws:iam::1:role/" + role + "\"\n  }\n}\n\n"
}

// composeFixture writes a composed root over two estates: "net" owns a VPC
// and publishes its id, "app" owns a bucket tagged with that id.
func composeFixture(t *testing.T, dir string, extraRoot string, netCall ...string) {
	t.Helper()
	files := map[string]string{
		"main.tf": "module \"net\" {\n  source = \"./net\"\n" + strings.Join(netCall, "") + "}\n\nmodule \"app\" {\n  source = \"./app\"\n}\n" + extraRoot,
		"net/main.tf": "terraform {\n  live {\n    estate = \"net-unit\"\n  }\n}\n\n" + composeProviders +
			"resource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.42.0.0/16\"\n}\n\noutput \"vpc_id\" {\n  value = aws_vpc.main.id\n}\n",
		"app/main.tf": "terraform {\n  live {\n    estate = \"app-unit\"\n  }\n}\n\n" + composeProviders +
			"data \"terraform_estate_outputs\" \"net\" {\n  estate = \"net-unit\"\n  names  = [\"vpc_id\"]\n}\n\n" +
			"resource \"aws_s3_bucket\" \"logs\" {\n  bucket = \"tofu-app-logs\"\n  tags = {\n    vpc = data.terraform_estate_outputs.net.values.vpc_id\n  }\n}\n",
		".terraform/modules/modules.json": `{"Modules":[{"Key":"","Source":"","Dir":"."},{"Key":"net","Source":"./net","Dir":"net"},{"Key":"app","Source":"./app","Dir":"app"}]}`,
	}
	for path, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// One apply over two estates: each module's resources are created with its
// own estate's markers and the module-relative address, and the bucket in
// "app" is created with the id of the VPC "net" created in the same run.
func TestLiveCompose_oneApplyTwoEstates(t *testing.T) {
	td := t.TempDir()
	composeFixture(t, td, "")
	t.Chdir(td)

	cloud := newLiveTestCloud()
	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}
	wireBuiltinTerraform(&c.Meta)
	// The VPC's id is assigned by the apply, as EC2 does, so the bucket's
	// tag is a value that does not exist until the same run creates it.
	prov := cloud.provider().(*liveTestProvider)
	plan, apply := prov.PlanResourceChangeFn, prov.ApplyResourceChangeFn
	prov.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		resp := plan(req)
		if req.TypeName == "aws_vpc" && req.PriorState.IsNull() {
			vals := resp.PlannedState.AsValueMap()
			vals["id"] = cty.UnknownVal(cty.String)
			resp.PlannedState = cty.ObjectVal(vals)
		}
		return resp
	}
	prov.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if req.TypeName == "aws_vpc" && !req.PlannedState.IsNull() {
			vals := req.PlannedState.AsValueMap()
			vals["id"] = cty.StringVal("vpc-fresh")
			req.PlannedState = cty.ObjectVal(vals)
		}
		return apply(req)
	}
	c.Meta.testingOverrides.Providers[addrs.NewDefaultProvider("aws")] = providers.FactoryFixed(prov)

	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if !strings.Contains(output.Stdout(), "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.") {
		t.Errorf("apply did not create both resources:\n%s", output.Stdout())
	}

	vpc := cloud.applied["aws_vpc.main"]
	if vpc == nil {
		t.Fatalf("aws_vpc.main was never applied; applied: %v", cloud.applied)
	}
	if vpc["tofu-estate"] != "net-unit" || vpc["tofu-address"] != "aws_vpc.main" {
		t.Errorf("the VPC carries %v, want estate net-unit at its own root-relative address", vpc)
	}
	bucket := cloud.applied["aws_s3_bucket.logs"]
	if bucket == nil {
		t.Fatalf("aws_s3_bucket.logs was never applied; applied: %v", cloud.applied)
	}
	if bucket["tofu-estate"] != "app-unit" || bucket["tofu-address"] != "aws_s3_bucket.logs" {
		t.Errorf("the bucket carries %v, want estate app-unit at its own root-relative address", bucket)
	}
	// Each estate's records are where the estate finds them when it runs
	// alone: in its own directory, under its own prefix, at its own
	// root-relative address, and its outputs are recorded for others to read.
	for _, want := range []string{
		"net/.tofu-records/tofu-records/net-unit/aws_vpc",
		"net/.tofu-records/tofu-outputs/net-unit",
	} {
		if _, err := os.Stat(filepath.Join(td, filepath.FromSlash(want))); err != nil {
			t.Errorf("missing record location %s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(td, ".tofu-records")); err == nil {
		t.Errorf("the composed root wrote records of its own")
	}
	if bucket["vpc"] != "vpc-fresh" {
		t.Errorf("the bucket was created without the VPC id the other estate published: %v", bucket)
	}
}

// A composed root holds module calls only.
func TestLiveCompose_rootResourceRefused(t *testing.T) {
	td := t.TempDir()
	composeFixture(t, td, "\nresource \"aws_vpc\" \"stray\" {\n  cidr_block = \"10.9.0.0/16\"\n}\n")
	t.Chdir(td)

	cloud := newLiveTestCloud()
	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}
	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code == 0 || !strings.Contains(output.Stderr()+output.Stdout(), "A composed root holds module calls only") {
		t.Fatalf("want the refusal, exit %d\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a refused run applied %v", cloud.applied)
	}
}

func TestLiveCompose_stateMoves(t *testing.T) {
	addr := func(s string) addrs.AbsResourceInstance {
		return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: s}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	}
	own := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(addr("main"), &states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"id":"v"}`)},
			addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}, addrs.NoKey)
	})
	moved := rehomeState(own, "net")
	rel := addr("main")
	rel.Module = addrs.RootModuleInstance.Child("net", addrs.NoKey)
	ri := moved.ResourceInstance(rel)
	if ri == nil || ri.Current == nil {
		t.Fatalf("the resource is not under module.net")
	}
	if got := moved.Resource(rel.ContainingResource()).ProviderConfig.Module.String(); got != "module.net" {
		t.Errorf("provider configuration module = %q, want module.net", got)
	}
	if moved.RootModule().Resources["aws_vpc.main"] != nil {
		t.Errorf("the resource is still at the root")
	}

	both := states.NewState()
	mergeInto(both, moved)
	back := sliceState(both, "net")
	if back.ResourceInstance(addr("main")) == nil {
		t.Fatalf("slice did not return the resource to its own root address")
	}
	if sliceState(both, "other").ResourceInstance(addr("main")) != nil {
		t.Errorf("another estate's slice holds this estate's resource")
	}
	if got := back.Resource(addr("main").ContainingResource()).ProviderConfig.Module; len(got) != 0 {
		t.Errorf("sliced provider configuration still has a module path: %s", got)
	}
}

func TestLiveCompose_routerStripsTheCall(t *testing.T) {
	net, app := &projection.NodeResolver{}, &projection.NodeResolver{}
	r := &estateRouter{by: map[string]*projection.NodeResolver{"net": net, "app": app}}
	a := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: "main"}.Instance(addrs.NoKey).
		Absolute(addrs.RootModuleInstance.Child("net", addrs.NoKey).Child("inner", addrs.NoKey))
	n, rel, ok := r.route(a)
	if !ok || n != net {
		t.Fatalf("routed to the wrong estate")
	}
	if rel.String() != "module.inner.aws_vpc.main" {
		t.Errorf("address = %s, want module.inner.aws_vpc.main", rel)
	}
	if _, _, ok := r.route(addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)); ok {
		t.Errorf("a root-level address was routed")
	}
}

// An estate module call cannot repeat and cannot be handed a provider. Both
// are refused by the language already, because an estate's provider blocks are
// its own; that is what keeps each estate's role on its own calls.
func TestLiveCompose_callShapeRefused(t *testing.T) {
	for name, tc := range map[string]struct{ call, want string }{
		"count":     {"  count = 1\n", "Module is incompatible with count"},
		"providers": {"  providers = {\n    aws = aws\n  }\n", "Cannot override provider configuration"},
	} {
		t.Run(name, func(t *testing.T) {
			td := t.TempDir()
			extra := ""
			if name == "providers" {
				extra = "\nprovider \"aws\" {\n  region = \"us-east-1\"\n}\n"
			}
			composeFixture(t, td, extra, tc.call)
			t.Chdir(td)
			cloud := newLiveTestCloud()
			view, done := testView(t)
			c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}
			code := c.Run([]string{"-no-color", "-auto-approve"})
			output := done(t)
			if code == 0 || !strings.Contains(output.Stderr()+output.Stdout(), tc.want) {
				t.Fatalf("want %q, exit %d\nstdout:\n%s\nstderr:\n%s", tc.want, code, output.Stdout(), output.Stderr())
			}
			if len(cloud.applied) != 0 {
				t.Errorf("a refused run applied %v", cloud.applied)
			}
		})
	}
}

// Each estate's provider calls are made under that estate's role: the walk
// configures a module's resources through the provider blocks in the
// module's own directory, so the role on the net estate's block is the one
// that creates the VPC and the role on the app estate's block creates the
// bucket. No call is made under a role covering both.
func TestLiveCompose_eachEstateAssumesItsOwnRole(t *testing.T) {
	td := t.TempDir()
	composeFixture(t, td, "")
	for dir, role := range map[string]string{"net": "net-role", "app": "app-role"} {
		path := filepath.Join(td, dir, "main.tf")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		newBody := strings.Replace(string(body), composeProviders, composeRoleProviders(role), 1)
		if newBody == string(body) {
			t.Fatalf("could not give %s a role", dir)
		}
		if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(td)

	cloud := newLiveTestCloud()
	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}
	wireBuiltinTerraform(&c.Meta)

	var mu sync.Mutex
	roleOf := map[string]string{}
	c.Meta.testingOverrides.Providers[addrs.NewDefaultProvider("aws")] = func() (providers.Interface, error) {
		prov := cloud.provider().(*liveTestProvider)
		role := ""
		configure, apply := prov.ConfigureProviderFn, prov.ApplyResourceChangeFn
		prov.ConfigureProviderFn = func(req providers.ConfigureProviderRequest) providers.ConfigureProviderResponse {
			if ar := req.Config.GetAttr("assume_role"); !ar.IsNull() && ar.LengthInt() > 0 {
				role = ar.AsValueSlice()[0].GetAttr("role_arn").AsString()
			}
			return configure(req)
		}
		prov.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
			if !req.PlannedState.IsNull() {
				mu.Lock()
				roleOf[liveTestTagsOf(req.PlannedState)["tofu-address"]] = role
				mu.Unlock()
			}
			return apply(req)
		}
		return prov, nil
	}

	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if got := roleOf["aws_vpc.main"]; got != "arn:aws:iam::1:role/net-role" {
		t.Errorf("the VPC was created under role %q, want the net estate's", got)
	}
	if got := roleOf["aws_s3_bucket.logs"]; got != "arn:aws:iam::1:role/app-role" {
		t.Errorf("the bucket was created under role %q, want the app estate's", got)
	}
}
