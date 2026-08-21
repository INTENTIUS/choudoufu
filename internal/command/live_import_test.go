// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/liveimport"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/zclconf/go-cty/cty"
)

// The live-import tests drive the whole command - argument parsing, the
// state read, ratification and (with -approve) the stamp write - through a
// mock AWS provider standing in for a cloud, over the committed fixture
// testdata/live-import-basic/import.tfstate.

func TestLiveImport_reportsWithoutWriting(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-data",
		map[string]string{"id": "tofu-import-unit-data", "bucket": "tofu-import-unit-data"},
		map[string]string{})

	c, done := newLiveImportCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-state=import.tfstate", "-estate=stateless-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	report := output.Stdout()
	for _, want := range []string{
		"Ratifying",
		"import.tfstate",
		"stateless-unit",
		"VERIFIED",
		"aws_s3_bucket.data",
		"No tag has been written",
		"Rerun with -approve",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}

	if len(cloud.applied) != 0 {
		t.Errorf("a run without -approve applied to %v; it must write nothing", cloud.applied)
	}
	if got := cloud.tagsOf("aws_s3_bucket", "tofu-import-unit-data")["tofu-estate"]; got != "" {
		t.Errorf("tofu-estate was written without -approve: %q", got)
	}
}

func TestLiveImport_approveStampsAndReportsSuccess(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-data",
		map[string]string{"id": "tofu-import-unit-data", "bucket": "tofu-import-unit-data"},
		map[string]string{})

	c, done := newLiveImportCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-state=import.tfstate", "-estate=stateless-unit", "-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	report := output.Stdout()
	for _, want := range []string{
		"STAMPED",
		"aws_s3_bucket.data",
		"cloud write",
		"was not touched",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}

	tags := cloud.tagsOf("aws_s3_bucket", "tofu-import-unit-data")
	if tags["tofu-estate"] != "stateless-unit" {
		t.Errorf("tofu-estate = %q, want stateless-unit", tags["tofu-estate"])
	}
	if tags["tofu-address"] != "aws_s3_bucket.data" {
		t.Errorf("tofu-address = %q, want aws_s3_bucket.data", tags["tofu-address"])
	}
	if len(cloud.applied) != 1 {
		t.Errorf("applied = %v, want exactly one write", cloud.applied)
	}
}

func TestLiveImport_requiresStateAndEstate(t *testing.T) {
	cloud := importNewCloud()

	c, done := newLiveImportCommand(t, cloud)
	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code == 0 {
		t.Fatalf("exit code 0 with neither -state nor -estate given\nstdout:\n%s", output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "No state file named") {
		t.Errorf("missing -state was not reported:\n%s", stderr)
	}
	if !strings.Contains(stderr, "No estate named") {
		t.Errorf("missing -estate was not reported:\n%s", stderr)
	}
}

func TestLiveImport_secondRunWithApproveIsIdempotent(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-data",
		map[string]string{"id": "tofu-import-unit-data", "bucket": "tofu-import-unit-data"},
		map[string]string{})

	c1, done1 := newLiveImportCommand(t, cloud)
	if code := c1.Run([]string{"-no-color", "-state=import.tfstate", "-estate=stateless-unit", "-approve"}); code != 0 {
		t.Fatalf("first run exit code %d\n%s", code, done1(t).Stdout())
	}
	done1(t)
	if len(cloud.applied) != 1 {
		t.Fatalf("first run applied %v, want exactly one write", cloud.applied)
	}

	c2, done2 := newLiveImportCommand(t, cloud)
	code2 := c2.Run([]string{"-no-color", "-state=import.tfstate", "-estate=stateless-unit", "-approve"})
	output2 := done2(t)
	if code2 != 0 {
		t.Fatalf("second run exit code %d\n%s", code2, output2.Stdout())
	}
	if len(cloud.applied) != 1 {
		t.Errorf("second run applied again: %v", cloud.applied)
	}
	if !strings.Contains(output2.Stdout(), "ALREADY_STAMPED") {
		t.Errorf("the second run does not report ALREADY_STAMPED:\n%s", output2.Stdout())
	}
}

func newLiveImportCommand(t *testing.T, cloud *importCloud) (*LiveImportCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-import-basic"), td)
	t.Chdir(td)

	view, done := testView(t)
	c := &LiveImportCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
			testingOverrides: &testingOverrides{
				Providers: map[addrs.Provider]providers.Factory{
					addrs.NewDefaultProvider("aws"): providers.FactoryFixed(cloud.provider()),
				},
			},
		},
	}
	return c, done
}

// ---------------------------------------------------------------------------
// A mock AWS provider with a mutable cloud behind it
// ---------------------------------------------------------------------------

// importCloud is a map of live objects keyed by type and id, served through
// a provider that speaks the calls live-import makes: schema, read, and the
// plan/apply pair that performs the tag write.
type importCloud struct {
	attrs map[string]map[string]string
	tags  map[string]map[string]string

	applied []string
}

func importNewCloud() *importCloud {
	return &importCloud{
		attrs: make(map[string]map[string]string),
		tags:  make(map[string]map[string]string),
	}
}

func (c *importCloud) put(typeName, id string, attrs, tags map[string]string) {
	c.attrs[typeName+"/"+id] = attrs
	c.tags[typeName+"/"+id] = tags
}

func (c *importCloud) tagsOf(typeName, id string) map[string]string {
	return c.tags[typeName+"/"+id]
}

func importCloudSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		"aws_s3_bucket": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"bucket": {Type: cty.String, Required: true},
			"tags":   {Type: cty.Map(cty.String), Optional: true},
		}}},
	}
}

func (c *importCloud) provider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"region": {Type: cty.String, Optional: true},
				},
			}},
			ResourceTypes: importCloudSchemas(),
		},
	}
	p.ConfigureProviderCalled = true

	toObject := func(typeName, id string) cty.Value {
		attrs := c.attrs[typeName+"/"+id]
		tags := c.tags[typeName+"/"+id]
		tagVals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			tagVals[k] = cty.StringVal(v)
		}
		tagsVal := cty.MapValEmpty(cty.String)
		if len(tagVals) > 0 {
			tagsVal = cty.MapVal(tagVals)
		}
		return cty.ObjectVal(map[string]cty.Value{
			"id":     cty.StringVal(id),
			"bucket": cty.StringVal(attrs["bucket"]),
			"tags":   tagsVal,
		})
	}

	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		id := r.PriorState.GetAttr("id").AsString()
		key := r.TypeName + "/" + id
		if _, ok := c.attrs[key]; !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: toObject(r.TypeName, id)}
	}

	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}

	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		id := r.PlannedState.GetAttr("id").AsString()
		key := r.TypeName + "/" + id
		tags := map[string]string{}
		tagsVal := r.PlannedState.GetAttr("tags")
		if !tagsVal.IsNull() {
			for it := tagsVal.ElementIterator(); it.Next(); {
				k, v := it.Element()
				tags[k.AsString()] = v.AsString()
			}
		}
		c.tags[key] = tags
		c.applied = append(c.applied, key)
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}

	return p
}

// TestEveryStampOutcomeIsRenderedWithAHeadline holds the pair of lists behind
// live-import -approve's report against liveimport's own Outcome constants.
//
// One list decides which outcome groups render and in what order; the other
// carries each group's one-line explanation. An outcome missing from the
// first renders NOWHERE - the resource vanishes from the report rather than
// printing oddly, so an operator is never told it was touched - and one
// missing from the second prints as a bare code with nothing after the dash.
//
// Neither list can see [liveimport.Outcome]: internal/command/views takes
// outcomes as plain strings on purpose, so nothing over there can notice a
// new constant. Nor can live/summary_line_guard_test.go, where the rest of
// this seam's guards live - liveimport reaches internal/live/discovery, which
// imports package live, so importing it from there is a cycle. This package
// is the one place both are in scope AND `just ci`'s fast tier runs
// (./internal/command/, the package itself).
//
// The constants are referenced rather than spelled, so renaming one breaks
// the build here instead of quietly weakening the check.
func TestEveryStampOutcomeIsRenderedWithAHeadline(t *testing.T) {
	all := []liveimport.Outcome{
		liveimport.OutcomeStamped,
		liveimport.OutcomeAlreadyStamped,
		liveimport.OutcomeRecorded,
		liveimport.OutcomeSensitivityRecorded,
		liveimport.OutcomeAlreadyRecorded,
		liveimport.OutcomeSkipped,
		liveimport.OutcomeFailed,
	}

	rep := &liveimport.StampReport{Estate: "guard-estate"}
	for i, outcome := range all {
		rep.Outcomes = append(rep.Outcomes, liveimport.StampOutcome{
			Addr: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: fmt.Sprintf("guard%d", i)}.
				Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
			TypeName: "aws_vpc",
			Outcome:  outcome,
			Detail:   "guard fixture",
		})
	}

	streams, done := terminal.StreamsForTesting(t)
	views.NewStatelessImport(views.NewView(streams)).Stamped(liveImportStampReport(rep))
	out := done(t).Stdout()

	for i, outcome := range all {
		if !strings.Contains(out, fmt.Sprintf("aws_vpc.guard%d", i)) {
			t.Errorf("a resource whose outcome is %s does not appear in the -approve report at all. The view "+
				"renders only the outcomes its order list names, so this one is dropped silently.\n%s", outcome, out)
			continue
		}
		// The group heading is "<OUTCOME> (n) - <headline>:". An outcome with
		// no headline still prints a heading, with nothing after the dash.
		head := fmt.Sprintf("%s (1) - ", outcome)
		idx := strings.Index(out, head)
		if idx < 0 {
			t.Errorf("%s renders no group heading of its own:\n%s", outcome, out)
			continue
		}
		rest := out[idx+len(head):]
		if end := strings.IndexByte(rest, '\n'); end >= 0 {
			rest = rest[:end]
		}
		if strings.TrimSuffix(strings.TrimSpace(rest), ":") == "" {
			t.Errorf("%s's group heading carries no explanation, so it prints as a bare code", outcome)
		}
	}
}
