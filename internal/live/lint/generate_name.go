// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// The generate-name rule (GitHub issue #1064, under #1016's ruling).
//
// A Kubernetes object's identity, and since #1064 its admission, is its
// namespace and name read from the metadata block: the name is authored in
// the configuration, so the object can be found again by it and the
// ownership marker needs to carry only the estate (live/MARKERS.md,
// "Kubernetes: one label"). metadata.generate_name hands the name to the
// API server to mint at create time, with the value as a prefix. The name
// is then unknowable before the create, which is exactly
// identity.ClassNeedsDiscovery on AWS - the one shape that needs the
// configuration address on the object, and the one #1016 refuses to bring
// back for Kubernetes. So a metadata block that sets generate_name is
// refused by name, the same way a missing namespace is refused rather than
// defaulted.
//
// The rule is syntactic - a nested block named metadata with an attribute
// named generate_name - and needs no provider schema, because no provider
// other than Kubernetes' has that block, and the refusal is about a name
// the configuration does not state rather than about any one type. A
// resource that sets both name and generate_name is refused too: the
// provider itself rejects that combination, and this rule says why in the
// vocabulary of this fork before a plan ever reaches the provider.
func checkGenerateName(resource *configs.Resource, addr string, path addrs.Module, issues *[]Issue) {
	if resource.Config == nil {
		return
	}
	content, _, diags := resource.Config.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "metadata"}},
	})
	if diags.HasErrors() || content == nil {
		return
	}
	for _, block := range content.Blocks {
		if block.Type != "metadata" {
			continue
		}
		inner, _, innerDiags := block.Body.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "generate_name"}},
		})
		if innerDiags.HasErrors() || inner == nil {
			continue
		}
		attr, set := inner.Attributes["generate_name"]
		if !set {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleGenerateName,
			Construct: addr,
			Module:    path,
			Detail: fmt.Sprintf(
				"%s sets metadata.generate_name, so the API server mints the object's name at create time and nothing in this configuration states it. "+
					"A Kubernetes object is identified by its namespace and name, and its ownership marker is the estate alone (live/MARKERS.md, \"Kubernetes: one label\"), "+
					"so a name this run cannot know before the create is an object no later run could find again. Set metadata.name instead.",
				addr),
			Subject: attr.Range,
		})
	}
}
