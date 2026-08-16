// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// analyzeNoProjection is [analyzeFixture] with issue #193's
// managed-argument projection switched off - the other side of the rule,
// and the shape a with/without measurement uses.
func analyzeNoProjection(t *testing.T, name string) *Analysis {
	t.Helper()
	cfg := loadConfig(t, filepath.Join("testdata", name), nil)
	return Analyze(context.Background(), cfg, Options{SkipManagedProjection: true})
}

// TestManagedProjectionAnswersASetArgument: a data source whose argument
// reads a managed resource attribute the block itself sets classifies
// eligible - no cloud read, no state, the value is in the configuration -
// and refuses again with the projection switched off.
func TestManagedProjectionAnswersASetArgument(t *testing.T) {
	off := analyzeNoProjection(t, "managed-projection")
	src, ok := off.SourceFor(addrs.RootModule, dataAddr("aws_subnet", "of_instance"))
	if !ok {
		t.Fatalf("data.aws_subnet.of_instance was not classified with the projection off")
	}
	if src.Eligible {
		t.Fatalf("the projection is off; the managed reference must still refuse")
	}

	on := analyzeFixture(t, "managed-projection")
	src, ok = on.SourceFor(addrs.RootModule, dataAddr("aws_subnet", "of_instance"))
	if !ok {
		t.Fatalf("data.aws_subnet.of_instance was not classified with the projection on")
	}
	if !src.Eligible {
		t.Fatalf("aws_instance.web.subnet_id is set in the configuration, so the read is eligible; refused: %s", src.ReasonDetail)
	}
}

// TestManagedProjectionRefusesAProviderAssignedAttribute: the other
// direction, and the one that matters. private_dns is not in the
// configuration at all, so the projection must not answer it - and it must
// refuse in the same words it refuses in with the projection off, not with
// a raw HCL "this object does not have an attribute named" error leaking
// out of the evaluator.
func TestManagedProjectionRefusesAProviderAssignedAttribute(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    *Analysis
	}{
		{"projection off", analyzeNoProjection(t, "managed-projection")},
		{"projection on", analyzeFixture(t, "managed-projection")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, ok := tc.a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "of_instance"))
			if !ok {
				t.Fatalf("data.aws_route53_zone.of_instance was not classified at all")
			}
			if src.Eligible {
				t.Fatalf("aws_instance.web.private_dns is assigned by the provider; it must not be projected")
			}
			if src.ReasonSummary != SummaryNotReadable {
				t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
			}
			for _, part := range []string{"managed resource", "cannot be read before the plan"} {
				if !strings.Contains(src.ReasonDetail, part) {
					t.Errorf("the wording lacks %q: %s", part, src.ReasonDetail)
				}
			}
			if strings.Contains(src.ReasonDetail, "does not have an attribute named") {
				t.Errorf("an HCL attribute error leaked into the refusal: %s", src.ReasonDetail)
			}
		})
	}
}

// projectionProviderSchema serves the read-side fixtures' data source
// types: the shared ones plus test_subnet, whose id is the argument a
// projection has to fill in.
func projectionProviderSchema() *providers.GetProviderSchemaResponse {
	s := testProviderSchema()
	s.DataSources["test_subnet"] = providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":         {Type: cty.String, Optional: true},
			"cidr_block": {Type: cty.String, Computed: true},
		},
	}}
	return s
}

// TestReadMaterializesAProjectedArgument is the read side proper, and the
// half no offline classification can stand in for: the provider must be
// asked for the subnet the CONFIGURATION names, not for an unknown, and the
// answer must land in the results map resolution reads.
func TestReadMaterializesAProjectedArgument(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-projection-read"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("test_subnet", "of_instance"))
	if !ok {
		t.Fatalf("data.test_subnet.of_instance was not demanded at all")
	}
	if !src.Eligible {
		t.Fatalf("the projection should have made this eligible; refused: %s", src.ReasonDetail)
	}

	var sawIDs []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: projectionProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			id := req.Config.GetAttr("id")
			if id.IsNull() || !id.IsKnown() {
				t.Fatalf("test_subnet was read with an unknown id: %#v", id)
			}
			sawIDs = append(sawIDs, id.AsString())
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"id":         id,
				"cidr_block": cty.StringVal("10.0." + id.AsString() + ".0/24"),
			})}
		},
	}

	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if len(sawIDs) != 1 || sawIDs[0] != "subnet-0abc" {
		t.Fatalf("the provider was read with ids %v, want [subnet-0abc] - the value aws_instance.web's own block sets", sawIDs)
	}
	got, ok := results["data.test_subnet.of_instance"]
	if !ok {
		t.Fatalf("no result under data.test_subnet.of_instance; keys: %v", keysOf(results))
	}
	if cb := got.GetAttr("cidr_block"); cb.AsString() != "10.0.subnet-0abc.0/24" {
		t.Fatalf("cidr_block is %q, want the value the read answered", cb.AsString())
	}
}

// TestReadProjectsThroughAnAlreadyReadDataSource closes the loop the real
// govuk-aws case needs: the managed block's own argument reads a data
// source, so the projection is only answerable AFTER that source has been
// read. The dependency edge has to be recorded during classification for
// the read order to make that true.
func TestReadProjectsThroughAnAlreadyReadDataSource(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-projection-chain"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	var order []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: projectionProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			order = append(order, req.TypeName)
			switch req.TypeName {
			case "test_zone":
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"name":    req.Config.GetAttr("name"),
					"zone_id": cty.StringVal("subnet-fromzone"),
				})}
			case "test_subnet":
				id := req.Config.GetAttr("id")
				if id.IsNull() || !id.IsKnown() || id.AsString() != "subnet-fromzone" {
					t.Errorf("test_subnet was read with id %#v, want the zone_id test_zone answered", id)
				}
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"id":         id,
					"cidr_block": cty.StringVal("10.1.0.0/24"),
				})}
			}
			t.Fatalf("read of unexpected type %q", req.TypeName)
			return providers.ReadDataSourceResponse{}
		},
	}

	if _, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock}); diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if len(order) != 2 || order[0] != "test_zone" || order[1] != "test_subnet" {
		t.Fatalf("read order %v, want [test_zone test_subnet]: the projection's own dependency has to be read first", order)
	}
}

// TestReadProjectsAnExpandedManagedBlock: a count-expanded managed block
// projects as a tuple, so an instance reference reaches THAT instance's own
// argument - with count.index bound while it evaluates. The adversarial
// half is the splat: a flat object would answer a one-element list for a
// three-instance block, which is a wrong value rather than a refusal.
func TestReadProjectsAnExpandedManagedBlock(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-projection-expanded"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("test_subnet", "indexed"))
	if !ok {
		t.Fatalf("data.test_subnet.indexed was not demanded at all")
	}
	if !src.Eligible {
		t.Fatalf("an expanded managed block's own argument is projectable; refused: %s", src.ReasonDetail)
	}

	var sawIDs []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: projectionProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			id := req.Config.GetAttr("id")
			sawIDs = append(sawIDs, id.AsString())
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"id":         id,
				"cidr_block": cty.StringVal("10.0.0.0/24"),
			})}
		},
	}

	if _, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock}); diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	// data.test_subnet.indexed has count = 3 and reads
	// aws_instance.fleet[count.index].subnet_id, so each instance must see
	// its OWN broker instance's argument, not instance 0's three times.
	want := []string{"subnet-0", "subnet-1", "subnet-2"}
	if len(sawIDs) != len(want) {
		t.Fatalf("the provider was read %d times with %v, want %v", len(sawIDs), sawIDs, want)
	}
	for i, w := range want {
		if sawIDs[i] != w {
			t.Fatalf("read %d used id %q, want %q (per-instance projection, not instance 0 repeated)", i, sawIDs[i], w)
		}
	}
}

// TestReadRefusesAWholeObjectUseOfAProjection is the unsoundness
// [unprojectedAttr] exists for. The projection carries only what the block's
// body sets, so jsonencode(aws_instance.web) would otherwise produce a
// perfectly known string describing a truncated object - a wrong value that
// becomes a wrong marker, which is worse than any refusal. The read must
// refuse it, and must not reach the provider at all.
func TestReadRefusesAWholeObjectUseOfAProjection(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-projection-whole"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	reads := 0
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: projectionProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			reads++
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"id":         req.Config.GetAttr("id"),
				"cidr_block": cty.StringVal("10.0.0.0/24"),
			})}
		},
	}

	_, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if !diags.HasErrors() {
		t.Fatalf("the read succeeded; a whole-object use of a partial projection must refuse")
	}
	if reads != 0 {
		t.Errorf("the provider was read %d times with a value derived from a truncated object", reads)
	}
	if !strings.Contains(diags.Err().Error(), "not knowable before the plan") {
		t.Errorf("refused with unexpected wording: %s", diags.Err())
	}
}

// TestManagedProjectionRefusesANestedBlock: a nested block is not an
// attribute, and its cty shape is the provider schema's NestingMode rather
// than anything the body says. Refusing is the honest answer.
func TestManagedProjectionRefusesANestedBlock(t *testing.T) {
	a := analyzeFixture(t, "managed-projection-nested")
	src, ok := a.SourceFor(addrs.RootModule, dataAddr("test_subnet", "nested"))
	if !ok {
		t.Fatalf("data.test_subnet.nested was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a nested block is not projected; the reference must refuse")
	}
}

// TestManagedProjectionRefusesAnUnexpandableBlock: a managed block whose own
// count is not knowable before the plan has no aggregate shape at all, so
// nothing about it is projectable - including an argument that would have
// evaluated on its own.
func TestManagedProjectionRefusesAnUnexpandableBlock(t *testing.T) {
	a := analyzeFixture(t, "managed-projection-unexpandable")
	src, ok := a.SourceFor(addrs.RootModule, dataAddr("test_subnet", "unexpandable"))
	if !ok {
		t.Fatalf("data.test_subnet.unexpandable was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("the managed block's own count is not statically knowable; nothing about it is projectable")
	}
}
