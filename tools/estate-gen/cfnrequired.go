// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This file is issue #174's second machine source: CloudFormation's own
// root-level `required` list, extracted per type into
// live/registry-schema-facts.json (issue #155).
//
// The overrides and the failures #174 catalogues exist because the AWS
// provider under-declares requiredness in its wire schema - subnet_id on a
// NAT gateway is Optional there, and the API then rejects the create. CFN's
// registry schema does not share that failure mode: it declares the member
// required at the root. Where the two disagree, the disagreement is
// information (issue #155's landing notes), and this pass acts on exactly
// that disagreement: a member CFN requires, the wire schema leaves
// settable-but-optional, and the generic required-only pass therefore never
// wrote.
//
// # What it will not do
//
// It only ever writes a value the generator already has a real opinion
// about - a reference to a resource this run renders (valueExpr's role,
// parent and sibling tiers) or the deterministic per-type name its own
// naming tier produces. A member whose value would be the generic
// placeholder is skipped: CFN saying "required" tells us the member must be
// PRESENT, not what a valid value looks like, and a blind "placeholder"
// into an enum, an ARN or a CIDR turns a fixture that failed one way into
// one that fails another. Those members stay overrides, and the skip is the
// honest report that this source cannot retire them.
//
// A member the TF schema has no top-level counterpart for is skipped
// silently: CFN's model flattens what the provider nests
// (AWS::CodeGuruReviewer::RepositoryAssociation requires Name and Type;
// the provider nests both under repository's one-of children), so an
// unmappable member is a shape mismatch between the two models, not a
// requiredness disagreement this pass could act on.
//
// Nothing here names a resource type; the members come from the artifact,
// the mapping from live/mapping.json, and the values from the same
// valueExpr every generically-filled argument already goes through.

// loadCFNRequired reads live/registry-schema-facts.json and resolves each
// CFN type's root `required` members onto the TF types live/mapping.json
// joins to it. A missing artifact is not fatal, exactly like the doc-example
// seed: the pass is additive, and a run without it produces what it
// produced before the pass existed.
func loadCFNRequired(root string) (map[string][]string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "live", "registry-schema-facts.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading live/registry-schema-facts.json: %w", err)
	}
	var art struct {
		Types []struct {
			TypeName string   `json:"type_name"`
			Required []string `json:"required"`
		} `json:"types"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		return nil, fmt.Errorf("decoding live/registry-schema-facts.json: %w", err)
	}

	roster, err := registry.Load(filepath.Join(root, "live", "mapping.json"), filepath.Join(root, "live", "registry.json"))
	if err != nil {
		return nil, fmt.Errorf("loading the registry roster: %w", err)
	}

	out := map[string][]string{}
	for _, t := range art.Types {
		if len(t.Required) == 0 {
			continue
		}
		tfTypes := roster.TFTypesForCFNType(t.TypeName)
		if len(tfTypes) != 1 {
			// Zero: nothing maps. More than one: the reverse join is
			// ambiguous, and the Roster's own contract says treat it so
			// rather than picking the first.
			continue
		}
		out[tfTypes[0]] = append([]string(nil), t.Required...)
	}
	return out, nil
}

// applyCFNRequired writes the members CFN's root `required` names that the
// wire schema leaves optional and the body does not set, and reports which
// - the provenance surface issue #155 asks for when two sources disagree.
// Value selection and its gate are described at the top of this file.
func (g *generator) applyCFNRequired(body *hclwrite.Body, block *configschema.Block, addr resourceAddr) []string {
	if _, overridden := typeOverrides[addr.Type]; overridden {
		return nil
	}
	members := g.cfnRequired[addr.Type]
	if len(members) == 0 || block == nil {
		return nil
	}

	sorted := append([]string(nil), members...)
	sort.Strings(sorted)

	var applied []string
	seen := map[string]bool{}
	for _, m := range sorted {
		name, ok := attrForCFNMember(block, m)
		if !ok || seen[name] {
			continue
		}
		attr := block.Attributes[name]
		if attr.Required || !attr.Optional {
			continue // the generic pass already wrote it, or it is computed-only
		}
		if body.GetAttribute(name) != nil {
			continue // the identity pass or the documented example already set it
		}
		expr := g.valueExpr(addr, name, attr.Type, true)
		if expr == genericExprText(attr.Type) {
			continue // presence without a real value fixes nothing
		}
		seen[name] = true
		body.SetAttributeRaw(name, exprTokens(expr))
		applied = append(applied, name)
	}
	return applied
}

// attrForCFNMember finds the top-level schema attribute a CFN member names:
// the attribute whose name with underscores removed equals the member
// case-folded (SubnetId -> subnet_id). Comparing folded spellings sidesteps
// every camel-case acronym convention; scanning sorted names keeps a
// hypothetical double match deterministic.
func attrForCFNMember(block *configschema.Block, member string) (string, bool) {
	want := strings.ToLower(member)
	names := make([]string, 0, len(block.Attributes))
	for name := range block.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.ReplaceAll(name, "_", "") == want {
			return name, true
		}
	}
	return "", false
}
