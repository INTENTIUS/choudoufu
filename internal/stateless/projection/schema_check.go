// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"log"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/stateless/identity"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// This file is the seam where the identity package's hand-maintained type
// table meets the provider's own resource identity schemas
// (opentofu#2854's providers.Schema.IdentitySchema).
//
// The table has to exist before any provider does: identity resolution runs
// with no plugin process, which is what lets a configuration be classified
// and unit-tested cloud-free. But by the time a projection is being built,
// a provider is on the line and has already served its schemas, so this is
// the first point in the pipeline where the table's claims can be checked
// against the provider's own account of what identifies each type. The
// check costs one map walk over schemas that were fetched anyway.
//
// What is done with the result is deliberately split by how actionable it
// is:
//
//   - A table entry that builds an identity out of arguments the type does
//     not have becomes an error diagnostic, raised where that type is used.
//     There is no identity to compute and nothing to read, so the run stops
//     rather than proceeding on a guess.
//   - The other breaking finding - the table offers an identity attribute
//     the type does not have - is logged rather than raised. It only harms
//     a run that actually references that attribute from another resource,
//     and such a run already fails, precisely, where the formula is
//     rendered. Failing every run that touches the type instead would turn
//     one stale table row into an outage for configurations it has nothing
//     to do with.
//   - A divergence - the two sides describe one identity differently - is
//     logged, naming both sides. It is not raised to the operator, who
//     cannot act on it: the table's inference layer (which configuration
//     argument supplies an identity attribute) is something no schema
//     carries, so a run against the real AWS provider has a standing
//     divergence on aws_route_table_association, whose provider identity is
//     the rtbassoc- ID it assigns while the table builds the documented
//     import string from subnet and route table. Warning an operator about
//     that on every plan would be noise about somebody else's maintenance
//     work. [identity.Verification.Diagnostics] renders divergences as
//     warning diagnostics for the callers whose job that maintenance is.
//   - The derivable set - types whose identity schema is fully
//     client-assigned, so the schemas admit them with no hand inference -
//     is logged as the candidate list a wiring batch should draw from.
//     See [identity.Derivable].
//
// The check is given one thing besides the schemas: what the configuration
// under projection says about who names each of its resources
// ([identity.ConfigSignal]). It is not a second source of truth about the
// provider, and it decides nothing the schemas decide. It answers the one
// question they cannot - a legacy-SDK schema marks aws_s3_bucket's bucket
// and aws_vpc's id the same way and means opposite things - and it answers
// it by reading the blocks in front of it: this bucket sets a name, that
// VPC does not. Those verdicts are logged apart from the schema-only ones,
// because a type admitted on the evidence of one configuration is a
// different claim from a type admitted on the evidence of a provider.

// verifySchemas checks the identity table against one provider's schemas
// and logs what it found. Called once per provider configuration, from the
// provider cache, right after the schemas arrive.
func verifySchemas(addr addrs.AbsProviderConfig, resourceTypes map[string]providers.Schema, signal *identity.ConfigSignal) identity.Verification {
	v := identity.VerifyTableIn(resourceTypes, signal)

	log.Printf("[DEBUG] projection: identity table checked against %s: %s", addr, v.Summary())

	for _, f := range v.Findings {
		switch {
		case fatal(f):
			// Raised as an error diagnostic when the type is used; see
			// [providerEntry.resourceSchema].
		case f.Breaking:
			log.Printf("[ERROR] projection: %s: %s", addr, f.Detail)
		default:
			log.Printf(
				"[WARN] projection: the identity table and %s disagree about what identifies a %s (%s): the table says %s, the provider's identity schema says %s",
				addr, f.Type, f.Kind, f.TableSide, f.SchemaSide,
			)
		}
	}

	for _, d := range v.Derivable {
		if d.InTable {
			continue
		}
		switch d.Admits {
		case identity.AdmitConfigSignal:
			log.Printf(
				"[TRACE] projection: %s's identity schema for %s leaves %v settable rather than required, and every %s block in this configuration sets them, so the type admits itself here without a table entry",
				addr, d.Type, d.IdentityAttrs, d.Type,
			)
		default:
			log.Printf(
				"[TRACE] projection: %s's identity schema for %s is fully client-assigned (%v), so the type could admit itself without a table entry",
				addr, d.Type, d.IdentityAttrs,
			)
		}
	}

	return v
}

// fatal reports whether a finding leaves this run nothing to do but stop
// for the type it concerns. See the file comment: a table entry composing
// an identity from arguments the type does not have can produce no
// identity at all, which is a different thing from a table entry offering
// an identity source nobody may reference.
func fatal(f identity.Finding) bool {
	return f.Kind == identity.FindingArgumentNotInSchema
}

// fatalDiags returns the error diagnostics for one resource type, or
// nothing.
func (e *providerEntry) fatalDiags(typeName string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, f := range e.verification.Findings {
		if f.Type == typeName && fatal(f) {
			diags = diags.Append(f.Diagnostic())
		}
	}
	return diags
}
