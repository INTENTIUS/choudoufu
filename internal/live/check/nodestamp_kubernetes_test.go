// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// kubernetesNeedsDiscoveryFixture lives under internal/command/testdata for
// the reason [stampUnmarkedApplyRecordBackedFixture] gives: it must not add
// rows to [TestIdentityGolden].
const kubernetesNeedsDiscoveryFixture = "../../command/testdata/live-k8s-needs-discovery-1565"

// kubernetesNeedsDiscoverySchemas is the fixture's six types: the
// hashicorp/kubernetes object-metadata shape (a label surface), the
// kubernetes_manifest shape (a manifest surface), and two AWS types with a
// tags map.
func kubernetesNeedsDiscoverySchemas() map[string]providers.Schema {
	objectMeta := func() providers.Schema {
		return providers.Schema{Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Optional: true, Computed: true},
				"data": {Type: cty.Map(cty.String), Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":          {Type: cty.String, Optional: true, Computed: true},
					"namespace":     {Type: cty.String, Optional: true},
					"generate_name": {Type: cty.String, Optional: true},
					"labels":        {Type: cty.Map(cty.String), Optional: true},
					"annotations":   {Type: cty.Map(cty.String), Optional: true},
					"uid":           {Type: cty.String, Computed: true},
				}}, Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1},
			},
		}}
	}
	tagged := func(extra map[string]*configschema.Attribute) providers.Schema {
		attrs := map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"arn":  {Type: cty.String, Computed: true},
			"tags": {Type: cty.Map(cty.String), Optional: true},
		}
		for k, v := range extra {
			attrs[k] = v
		}
		return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
	}
	return map[string]providers.Schema{
		"kubernetes_namespace":     objectMeta(),
		"kubernetes_config_map":    objectMeta(),
		"kubernetes_config_map_v1": objectMeta(),
		"kubernetes_manifest": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"manifest": {Type: cty.DynamicPseudoType, Required: true},
			"object":   {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		}}},
		"aws_vpc":       tagged(map[string]*configschema.Attribute{"cidr_block": {Type: cty.String, Optional: true}}),
		"aws_s3_bucket": tagged(map[string]*configschema.Attribute{"bucket": {Type: cty.String, Optional: true, Computed: true}}),
	}
}

// TestNoKubernetesTypeNeedsDiscovery pins the premise of check/nodestamp.go's
// entry in internal/live/markers' surfaceSeamExemptions (GitHub issue
// #1565): [NodeStampUnmarkedApply] asks only [markers.Taggable], and that is
// complete only because it acts on needs-discovery blocks alone and no
// label- or manifest-surface type resolves to one. If a shape here starts
// resolving to NEEDS_DISCOVERY, that exemption no longer holds: the check
// would explain a Kubernetes type as having no tags map. Route its
// predicate through internal/live/substrate, then update this test.
func TestNoKubernetesTypeNeedsDiscovery(t *testing.T) {
	schemas := kubernetesNeedsDiscoverySchemas()
	report := Dir(t.Context(), kubernetesNeedsDiscoveryFixture, Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	result, diags := identity.ResolveWith(t.Context(), report.Load.Config, identity.Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolving the fixture: %s", diags.Err())
	}

	var kubernetes int
	sawControl := false
	for _, r := range result.All() {
		typeName := r.Addr.Resource.Resource.Type
		surface, ok := substrate.SurfaceOf("", schemas[typeName].Block)
		if !ok {
			t.Fatalf("%s: the fixture's schema for %s carries no marker surface", r.Addr, typeName)
		}
		if substrate.For(surface).Name() == "aws" {
			if r.Addr.Resource.Resource.Name == "server_assigned" {
				sawControl = r.Class == identity.ClassNeedsDiscovery
			}
			continue
		}
		kubernetes++
		if r.Class == identity.ClassNeedsDiscovery {
			t.Errorf("%s (%s surface) resolved to %s (cause %s); NodeStampUnmarkedApply would explain it as a type with no tags map", r.Addr, surface, r.Class, r.Cause)
		}
	}
	if !sawControl {
		t.Errorf("aws_vpc.server_assigned, the control, did not resolve to %s; the fixture cannot tell a needs-discovery block from anything else", identity.ClassNeedsDiscovery)
	}
	if kubernetes != 8 {
		t.Errorf("resolved %d Kubernetes instances, want the fixture's 8", kubernetes)
	}

	// And the check itself says nothing about any of them, even with no
	// writable record store (storeWritable false, #1637), where it refuses most.
	for _, d := range NodeStampUnmarkedApply(report.Load.Config, result, schemas, "k8s-1565", nil, nil, false) {
		if strings.Contains(d.Description().Detail, "kubernetes_") {
			t.Errorf("NodeStampUnmarkedApply spoke about a Kubernetes block: %s: %s", d.Description().Summary, d.Description().Detail)
		}
	}
}
