// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/providers"
)

// liveCheckOneRefusalSummary is the summary internal/tofu's runtime-decision
// check (#2256 G, check 1) gave before #2256 U6 lifted it. Kept here so a
// run that is still refused says so by name instead of failing on whatever
// assertion comes next.
const liveCheckOneRefusalSummary = "Live mode cannot run on the new runtime yet"

// newRuntimeCycleVPC is the ID the cycle's cloud gives the VPC it creates.
const newRuntimeCycleVPC = "vpc-cycle-1"

// newRuntimeCycleCloud is newLiveTestCloud whose apply keeps what it creates:
// each created object is stored with the tags it was applied with, and the
// VPC, whose identity is server-assigned, is also listed, so the marker sweep
// of a later run finds it the way a real tagging API would. The base fake
// records the apply and returns the planned value unchanged; here every value
// the plan left unknown is given one, the ID the cloud assigns or null, since
// a provider never returns an unknown.
type newRuntimeCycleCloud struct {
	*liveTestCloud
	prov *liveTestProvider

	mu      sync.Mutex
	creates int
}

func newNewRuntimeCycleCloud() *newRuntimeCycleCloud {
	c := &newRuntimeCycleCloud{liveTestCloud: newLiveTestCloud()}
	c.prov = c.liveTestCloud.provider().(*liveTestProvider)
	base := c.prov.ApplyResourceChangeFn
	c.prov.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if req.PlannedState.IsNull() || !req.PriorState.IsNull() {
			return base(req)
		}
		id := newRuntimeCycleVPC
		if req.TypeName == "aws_s3_bucket" {
			id = req.PlannedState.GetAttr("bucket").AsString()
		}
		req.PlannedState, _ = cty.Transform(req.PlannedState, func(path cty.Path, v cty.Value) (cty.Value, error) {
			if v.IsKnown() {
				return v, nil
			}
			if path.Equals(cty.GetAttrPath("id")) {
				return cty.StringVal(id), nil
			}
			return cty.NullVal(v.Type()), nil
		})
		resp := base(req)
		if resp.Diagnostics.HasErrors() {
			return resp
		}

		attrs := map[string]string{}
		for name, v := range resp.NewState.AsValueMap() {
			if name != "tags" && v.Type() == cty.String && !v.IsNull() {
				attrs[name] = v.AsString()
			}
		}
		tags := liveTestTagsOf(resp.NewState)
		c.mu.Lock()
		c.creates++
		c.put(req.TypeName, id, attrs)
		c.liveTestCloud.tags[req.TypeName+"/"+id] = tags
		if req.TypeName == "aws_vpc" {
			c.list("aws_vpc", id, "the estate's VPC", tags, map[string]string{"cidr_block": attrs["cidr_block"]})
		}
		c.mu.Unlock()
		return resp
	}
	return c
}

func (c *newRuntimeCycleCloud) createCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creates
}

func (c *newRuntimeCycleCloud) meta(view *views.View) Meta {
	return Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
		testingOverrides: &testingOverrides{
			Providers: map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("aws"): providers.FactoryFixed(c.prov),
			},
		},
	}
}

// newRuntimeCycleRun runs one live command in the current directory and
// fails the test unless it exited 0 having run on the new runtime. A run
// check 1 refused fails here, by name.
func newRuntimeCycleRun(t *testing.T, step string, run func(Meta) int, meta func(*views.View) Meta) string {
	t.Helper()
	view, done := testView(t)
	code := run(meta(view))
	output := done(t)
	all := output.Stdout() + output.Stderr()
	flat := strings.Join(strings.Fields(all), " ")
	if strings.Contains(flat, liveCheckOneRefusalSummary) {
		t.Fatalf("%s: live mode was refused on the new runtime by #2256 G's check 1 (%q), so the cycle never reached internal/engine; exit %d\n%s", step, liveCheckOneRefusalSummary, code, all)
	}
	if code != 0 {
		t.Fatalf("%s: exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", step, code, output.Stdout(), output.Stderr())
	}
	if !strings.Contains(flat, newruntime.WarningSummary) {
		t.Fatalf("%s: no %q warning, so this run did not plan or apply on internal/engine\n%s", step, newruntime.WarningSummary, all)
	}
	return output.Stdout()
}

// TestLiveModeCreateCycleOnNewRuntime is #2256 U6's done-when (#2342): live
// mode runs the whole create cycle on the new runtime, through the real
// entry points and with no cloud credentials.
//
//  1. apply over an empty cloud creates both resources, and each reaches the
//     cloud carrying this estate's markers;
//  2. a plan over the same working directory shows no changes;
//  3. a plan and an apply from a fresh directory, with no state cache and no
//     record, find both objects (the VPC by its marker alone, since nothing
//     in configuration names it) and adopt them with no create.
//
// Every run must show the new-runtime warning, which only newEnginePlan and
// newEngineApply emit. With #2256 G's check 1 in place step 1 is refused
// before the engine, and the test fails saying so.
func TestLiveModeCreateCycleOnNewRuntime(t *testing.T) {
	t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME", "1")
	t.Setenv("TOFU_X_EXPERIMENTAL_RUNTIME_NEXT", "")

	cloud := newNewRuntimeCycleCloud()
	apply := func(m Meta) int {
		return (&ApplyCommand{Meta: m}).Run([]string{"-no-color", "-auto-approve"})
	}
	plan := func(m Meta) int {
		return (&PlanCommand{Meta: m}).Run([]string{"-no-color"})
	}

	// 1. Create, with markers.
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	stdout := newRuntimeCycleRun(t, "create", apply, cloud.meta)
	if !strings.Contains(stdout, "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.") {
		t.Fatalf("create: the apply did not create both resources:\n%s", stdout)
	}
	if n := cloud.createCount(); n != 2 {
		t.Fatalf("create: the cloud saw %d creates, want 2", n)
	}
	for _, addr := range []string{"aws_s3_bucket.data", "aws_vpc.main"} {
		tags := cloud.applied[addr]
		if tags == nil {
			t.Fatalf("create: %s was never applied with its tofu-address marker; applied: %v", addr, cloud.applied)
		}
		if got := tags["tofu-estate"]; got != "live-unit" {
			t.Errorf("create: %s was created with tofu-estate %q, want %q", addr, got, "live-unit")
		}
		if got := tags["tofu-address"]; got != addr {
			t.Errorf("create: %s was created with tofu-address %q, want %q", addr, got, addr)
		}
	}
	if got := cloud.liveTestCloud.tags["aws_vpc/"+newRuntimeCycleVPC]["tofu-address"]; got != "aws_vpc.main" {
		t.Fatalf("create: the stored VPC carries tofu-address %q, want %q", got, "aws_vpc.main")
	}

	// 2. Re-plan: nothing to do.
	stdout = newRuntimeCycleRun(t, "re-plan", plan, cloud.meta)
	if !strings.Contains(stdout, "No changes.") {
		t.Fatalf("re-plan: the plan after the create is not empty:\n%s", stdout)
	}

	// 3. Adopt by marker: a fresh directory, so no state cache, and the
	// fixture has no record store. The VPC can only be found by its marker.
	fresh := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), fresh)
	t.Chdir(fresh)

	stdout = newRuntimeCycleRun(t, "adopt plan", plan, cloud.meta)
	if !strings.Contains(stdout, "No changes.") {
		t.Fatalf("adopt: a fresh plan over the created estate is not empty:\n%s", stdout)
	}
	if !cloud.imported("aws_vpc", newRuntimeCycleVPC) {
		t.Fatalf("adopt: the VPC was never read by the ID its marker named; imports were %v", cloud.imports)
	}

	stdout = newRuntimeCycleRun(t, "adopt apply", apply, cloud.meta)
	if strings.Contains(stdout, "will be created") {
		t.Errorf("adopt: the apply proposed a create:\n%s", stdout)
	}
	if n := cloud.createCount(); n != 2 {
		t.Errorf("adopt: the cloud saw %d creates in all, want the 2 from step 1", n)
	}
	if !strings.Contains(stdout, "0 added") {
		t.Errorf("adopt: the apply did not report zero added:\n%s", stdout)
	}
}
