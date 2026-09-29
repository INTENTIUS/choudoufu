// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tf

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// EstateOutputsTypeName is GitHub issue #1371's data source: one estate
// reading root output values another estate recorded. It is this fork's,
// not upstream's, and it works only under a live block, whose record store
// is where the values are.
//
//	data "terraform_estate_outputs" "network" {
//	  estate = "network"
//	  names  = ["cluster_services_namespace"]
//	}
//
// The block is the declaration live/OUTPUTS.md asks for: it names the other
// estate, as render-policy.sh's --reads-outputs-of does on the IAM side, so a
// refused read can say which grant is missing.
const EstateOutputsTypeName = "terraform_estate_outputs"

// EstateOutputReader answers a terraform_estate_outputs read. The live run
// supplies one; see internal/command's liveEstateOutputs.
type EstateOutputReader interface {
	ReadEstateOutputs(ctx context.Context, estate string, names []string) (map[string]cty.Value, tfdiags.Diagnostics)
}

func dataSourceEstateOutputsGetSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"estate": {
					Type:            cty.String,
					Description:     "The estate whose recorded root outputs to read, as its live block names it.",
					DescriptionKind: configschema.StringMarkdown,
					Required:        true,
				},
				"names": {
					Type:            cty.Set(cty.String),
					Description:     "The root outputs to read. Each must be one the other estate has recorded: non-sensitive, and wholly known at its last apply.",
					DescriptionKind: configschema.StringMarkdown,
					Required:        true,
				},
				"values": {
					Type:            cty.DynamicPseudoType,
					Description:     "An object with one attribute per name, holding the value the other estate recorded at its last apply.",
					DescriptionKind: configschema.StringMarkdown,
					Computed:        true,
				},
			},
		},
	}
}

func dataSourceEstateOutputsValidate(cfg cty.Value) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if estate := cfg.GetAttr("estate"); estate.IsKnown() && !estate.IsNull() {
		if name := estate.AsString(); !markers.ValidEstateName(name) {
			diags = diags.Append(tfdiags.AttributeValue(tfdiags.Error, "Invalid estate name",
				fmt.Sprintf("%q does not match the tofu-estate marker grammar in live/MARKERS.md: a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters.", name),
				cty.GetAttrPath("estate")))
		}
	}
	if names := cfg.GetAttr("names"); names.IsWhollyKnown() && !names.IsNull() && names.LengthInt() == 0 {
		diags = diags.Append(tfdiags.AttributeValue(tfdiags.Error, "No outputs named",
			"Name at least one of the other estate's root outputs. Nothing is listed: the read is bounded by the names written here.",
			cty.GetAttrPath("names")))
	}
	return diags
}

func dataSourceEstateOutputsRead(ctx context.Context, reader EstateOutputReader, cfg cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if reader == nil {
		return cty.NilVal, diags.Append(EstateOutputsNeedLiveBlock())
	}
	estate := cfg.GetAttr("estate").AsString()
	var names []string
	for it := cfg.GetAttr("names").ElementIterator(); it.Next(); {
		_, v := it.Element()
		names = append(names, v.AsString())
	}
	sort.Strings(names)

	values, readDiags := reader.ReadEstateOutputs(ctx, estate, names)
	diags = diags.Append(readDiags)
	if readDiags.HasErrors() {
		return cty.NilVal, diags
	}
	attrs := make(map[string]cty.Value, len(values))
	for name, v := range values {
		attrs[name] = v
	}
	return cty.ObjectVal(map[string]cty.Value{
		"estate": cfg.GetAttr("estate"),
		"names":  cfg.GetAttr("names"),
		"values": cty.ObjectVal(attrs),
	}), diags
}

// EstateOutputsNeedLiveBlock is the refusal for a terraform_estate_outputs
// read outside a live run.
func EstateOutputsNeedLiveBlock() tfdiags.Diagnostic {
	return tfdiags.Sourceless(tfdiags.Error, "Estate outputs need a live block",
		"data \"terraform_estate_outputs\" reads another estate's recorded root outputs from the record store, which only a run under a live block has. Add a live block to this configuration, or read the value some other way.")
}
