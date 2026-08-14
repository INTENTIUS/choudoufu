// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
)

// This file is GitHub issue #106's first half: the repository derives
// resource identity from three independent sources and never cross-checked
// them.
//
// The proven victim is aws_ecs_service. The provider's own identity schema
// says its identity is ["cluster", "name"]; the scraped documentation's
// Identity Schema section says the same; and this tool proposed
//
//	Components: []Component{ attr("cluster"), sep("/"), attr("service_arn") }
//
// service_arn is not an argument of aws_ecs_service at all. Rule 1 declined
// because the grammar row's separator scraped as null, so rule 5 ran and
// matched segments against the CloudFormation Registry's primaryIdentifier
// of ["ServiceArn", "Cluster"]. Two authoritative sources agreed, a third
// derived source disagreed, nothing compared them, and the wrong one won.
//
// # What is checked, and how narrow it had to become
//
// The first version of this check refused any proposal naming an argument
// the scraped Argument Reference does not list. It demoted twenty-two rows
// and turned one that previously MATCHED its ratified row -
// aws_s3control_multi_region_access_point - into a mismatch. The scrape is
// incomplete: that type's Argument Reference has three entries and does not
// include "name", while its Identity Schema says "name" is exactly what
// identifies it. Treating a partial list as a complete one is the
// absence-of-evidence trap, and the first version fell into it while
// claiming in this very comment not to.
//
// So the check fires only when NO source knows the name. All three must
// hold:
//
//  1. The proposal is a composite. A single-argument proposal has already
//     been through resolveArgName, which consults the identity schema
//     first, and second-guessing it here is where most of the false
//     positives came from.
//  2. The type has a scraped Identity Schema. That is the corroborating
//     source; without it there is one incomplete list and nothing to check
//     it against.
//  3. The argument appears in neither the Argument Reference nor the
//     Identity Schema, required or optional.
//
// aws_ecs_service passes all three: 35 documented arguments including
// "cluster" and "name", an identity schema of ["cluster", "name"], and
// "service_arn" in none of them. That is a name no source has heard of,
// which is a different claim from "a name one incomplete list omits".
//
// The weaker comparison - a proposal naming an argument that is real but
// not an identity attribute - is still recorded, as a note rather than a
// veto. A legacy import ID may legitimately be composed from arguments that
// are not identity attributes.

// crossCheckFinding is one disagreement between the sources, recorded on the
// proposal so the rendered block can print it.
type crossCheckFinding struct {
	// Kind is "not-an-argument" for the fatal case, "not-in-identity-schema"
	// for the advisory one.
	Kind string

	// Args are the argument names the finding is about.
	Args []string

	// Detail is the sentence a human reads.
	Detail string
}

// applyIdentitySchemaCrossCheck compares a proposal's own argument names
// against the two authoritative sources and records what disagrees.
//
// A proposal naming an argument the type does not have is demoted: it goes
// back to bucketNeedsHandSeparator, which is where a composite this tool
// cannot resolve belongs, rather than being emitted for someone to paste.
// That is #106's second acceptance criterion - the correction fires from
// the disagreement, not from a hand-written row for this one type.
func applyIdentitySchemaCrossCheck(p *proposal, g importGrammarRow) {
	args := p.CompositeArgs
	if len(args) < 2 {
		// Single-argument proposals are resolveArgName's territory, and it
		// already ranks the identity schema first. See the doc comment.
		return
	}

	documented := documentedArguments(g)
	required := identitySchemaAttrs(g)
	optional := namesOf(g.IdentitySchemaOptional)

	if len(documented) > 0 && len(required) > 0 {
		var unknown []string
		for _, a := range args {
			if documented[a] || required[a] || optional[a] {
				continue
			}
			unknown = append(unknown, a)
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			p.CrossCheck = append(p.CrossCheck, crossCheckFinding{
				Kind: "not-an-argument",
				Args: unknown,
				Detail: fmt.Sprintf(
					"names %s, which neither %s's documented Argument Reference nor its Identity Schema knows. An identity built from an argument the type does not have fails at projection time with FindingArgumentNotInSchema, the one finding schema_check escalates to a hard error. Refusing here rather than proposing it.",
					quoteArgs(unknown), p.TFType),
			})
			// Back to the bucket a composite this tool cannot resolve
			// belongs in. The evidence that made it wrong is on the
			// proposal, so the printed block says why rather than going
			// quiet.
			p.Bucket = bucketNeedsHandSeparator
			p.CompositeArgs = nil
			p.CompositeSep = ""
			return
		}
	}

	// Advisory only. The identity schema is what the provider says
	// identifies the resource; a composite's arguments are what compose its
	// legacy import ID, and those are allowed to differ.
	if len(required) > 0 {
		var outside []string
		for _, a := range args {
			if !required[a] {
				outside = append(outside, a)
			}
		}
		if len(outside) > 0 {
			sort.Strings(outside)
			p.CrossCheck = append(p.CrossCheck, crossCheckFinding{
				Kind: "not-in-identity-schema",
				Args: outside,
				Detail: fmt.Sprintf(
					"names %s, which %s's identity schema does not list among its required attributes (%s). Not wrong on its own - a legacy import ID may be composed from arguments that are not identity attributes - but worth reading before ratifying.",
					quoteArgs(outside), p.TFType, quoteArgs(sortedKeys(required))),
			})
		}
	}
}

// namesOf indexes a scraped name list, nil-safe.
func namesOf(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// documentedArguments is the type's own argument list, from the scraped
// Argument Reference. Empty when the scrape has none, in which case this
// file's fatal check does not run: absence of evidence is not evidence.
func documentedArguments(g importGrammarRow) map[string]bool {
	if len(g.ArgumentReference) == 0 {
		return nil
	}
	out := make(map[string]bool, len(g.ArgumentReference))
	for _, a := range g.ArgumentReference {
		out[a.Name] = true
	}
	return out
}

// identitySchemaAttrs is the scraped Identity Schema's required attributes.
func identitySchemaAttrs(g importGrammarRow) map[string]bool {
	return namesOf(g.IdentitySchemaRequired)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
