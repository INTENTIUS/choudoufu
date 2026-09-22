// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "fmt"

// applyResourceVocabulary is the proposal-side half of the rule
// serverassignedattrs.go's mergeIdentityAttrs applies to a ratified row: a
// fresh proposal's DerivedIdentityAttrs may only name RESOURCE attributes,
// because that is what [identity.TypeIdentity.IdentityAttrs] is - the
// attribute names another resource may reference - and the rules that fill
// the guess read the provider's identity schema, which is a different
// vocabulary that mostly coincides.
//
// applyIdentitySchemaAttrsCorrection (importprecedence.go's rule 9) is the
// rule that exposes the gap. It corrects a guess to the documented Identity
// Schema's one required attribute precisely when that name is absent from
// Argument Reference, reading the absence as "a read-only identity attribute
// rather than an argument". For aws_osis_pipeline the absence means something
// else: the identity schema's "name" is not an attribute of the resource at
// all, whose argument is pipeline_name. The proposal then claims
// aws_osis_pipeline.name, the ratified row rightly says pipeline_name, and the
// disagreement would need a ruling in annotations.json for a fact the survey
// already states.
//
// So this pass runs last over every proposal and drops any derived name the
// survey records under not_resource_attributes, with a note saying so. A
// proposal left with no names has made no IdentityAttrs claim, which
// comparison.go already treats as "not proposed" rather than "disagrees" -
// the honest position, since nothing in this tool's evidence names the
// resource's own spelling. It never adds a name and never touches a proposal
// the survey has no identity schema for.
func applyResourceVocabulary(proposals []proposal, survey map[string]surveyEntry) {
	for i := range proposals {
		p := &proposals[i]
		if len(p.DerivedIdentityAttrs) == 0 {
			continue
		}
		s, ok := survey[p.TFType]
		if !ok {
			continue
		}
		var kept, dropped []string
		for _, attr := range p.DerivedIdentityAttrs {
			if s.notResourceAttr(attr) {
				dropped = append(dropped, attr)
				continue
			}
			kept = append(kept, attr)
		}
		if len(dropped) == 0 {
			continue
		}
		p.DerivedIdentityAttrs = kept
		p.Notes = append(p.Notes, fmt.Sprintf("identity schema attribute %s is not an attribute of the resource schema (live/survey-full.json not_resource_attributes), so it is not claimed as an IdentityAttrs value, which names resource attributes", quoteList(dropped)))
	}
}
