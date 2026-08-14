// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// FindingKind classifies one disagreement between [DefaultTable] and a
// provider's schemas.
type FindingKind string

const (
	// FindingUnnamedIdentityAttr: the provider requires an identity
	// attribute for import that the table's entry for the type names
	// nowhere - not among its components and not among its identity
	// attributes. The table is building some other string than the one the
	// provider calls this type's identity.
	FindingUnnamedIdentityAttr FindingKind = "unnamed-identity-attribute"

	// FindingComponentOutsideIdentity: the table builds part of the
	// identity from arguments that the provider's identity schema does not
	// mention at all. Usually the same divergence as the one above, seen
	// from the table's side.
	FindingComponentOutsideIdentity FindingKind = "component-outside-identity"

	// FindingServerAssignedDisputed: the table says the identity is minted
	// by the provider's API, and the identity schema says every attribute
	// it requires for import is an argument the configuration must set.
	// One of the two is wrong about whether this type needs discovery.
	FindingServerAssignedDisputed FindingKind = "server-assigned-disputed"

	// FindingArgumentNotInSchema: the table builds the identity out of
	// arguments the type does not have. Breaking: no instance of the type
	// can ever resolve, because none of the alternatives can be read.
	FindingArgumentNotInSchema FindingKind = "argument-not-in-schema"

	// FindingAttributeNotInSchema: the table hands out an attribute name as
	// an identity source for other resources to reference, and the type has
	// no such attribute. Breaking: a reference to it resolves against
	// nothing.
	FindingAttributeNotInSchema FindingKind = "attribute-not-in-schema"

	// FindingIdentityAttrNotInSchema: a component claims to supply a named
	// attribute of the type's identity object, and the provider's identity
	// schema has no attribute by that name. The per-attribute half of
	// FindingComponentOutsideIdentity, and the one that decides whether this
	// type can be imported by identity at all: an identity object built from
	// a component like this would carry an attribute the provider will not
	// decode. Not breaking, because the import-ID string the same components
	// build is unaffected and is what the run falls back to.
	FindingIdentityAttrNotInSchema FindingKind = "identity-attribute-not-in-schema"
)

// Finding is one disagreement, with both sides named: what the hand table
// claims and what the provider's schemas say.
type Finding struct {
	// Type is the resource type the disagreement is about.
	Type string

	// Kind classifies it. See the FindingKind constants.
	Kind FindingKind

	// Breaking is true when the disagreement is not a matter of
	// interpretation: the table names something the provider's schema does
	// not have, so following the table cannot work. Everything else is a
	// divergence to look at, because the table encodes an inference the
	// schema does not carry and the two are allowed to describe the same
	// identity differently.
	Breaking bool

	// TableSide and SchemaSide are the two claims, rendered for a human:
	// what the table says identifies this type, and what the provider says.
	TableSide, SchemaSide string

	// Detail is the operator- (or maintainer-) facing sentence naming both
	// sides.
	Detail string
}

// Skipped is one table entry the provider's schemas could not check, with
// the reason.
type Skipped struct {
	Type   string
	Reason string
}

// Verification is the result of checking [DefaultTable] against one
// provider's schemas: which entries the schemas confirm, which they cannot
// speak to, where the two disagree, and which types the schemas would admit
// on their own.
type Verification struct {
	// Agreed lists the table's types whose identity schema is consistent
	// with what the table claims, sorted.
	Agreed []string

	// Skipped lists the table's types the provider does not serve, or
	// serves without an identity schema, sorted by type.
	Skipped []Skipped

	// Findings are the disagreements, sorted by type then kind.
	Findings []Finding

	// Derivable is the self-admission candidate set: every type in this
	// provider's schemas whose identity is fully client-assigned. See
	// [Derivable].
	//
	// When the check was given a configuration's naming signal, the set
	// includes the types only that signal could settle, each carrying
	// [AdmitConfigSignal] and the per-instance evidence. See
	// [DerivableWith].
	Derivable []DerivableType

	// Report is the machine-readable derivability report over the same
	// schemas: the admitted types, and the ones a configuration could admit
	// with the arguments it would have to set. See [DerivabilityReport].
	Report DerivabilityReport

	// IdentityImportable lists the table's types whose components supply
	// every attribute the provider requires to import one, sorted: the types
	// a run asks the provider for by identity object rather than by a
	// separator-joined string. See [TypeIdentity.ComposesIdentity], and
	// internal/live/projection/build.go for what is done with the answer.
	//
	// The complement is not a failure. aws_route_table_association is
	// identified by an ID the configuration does not hold, and a run reaches
	// it by the documented import string, which is the right answer and the
	// only one.
	IdentityImportable []string
}

// VerifyTable checks [DefaultTable] against the resource schemas one
// provider served, and reports the identity schemas' own derivable set
// alongside.
//
// The caller must already have a running provider: these schemas come from
// GetProviderSchema. That is the whole reason this is a separate entry
// point rather than part of [Resolve] - resolution runs before any plugin
// is launched, and the hand table is what stands in for the schemas until
// one is. This function is the seam where the two meet, so that the table's
// claims stop being unfalsifiable the moment a provider is on the line.
//
// The provider's own type set bounds the check: a table entry for a type
// this provider does not serve is skipped rather than reported, because a
// run that wires one provider says nothing about another's types.
func VerifyTable(resourceTypes map[string]providers.Schema) Verification {
	return verifyTable(DefaultTable, resourceTypes, nil)
}

// VerifyTableIn is [VerifyTable] told what the configuration being run
// says about who names each resource, so that the derivable set it reports
// includes the types the schemas alone leave undecided.
//
// The signal comes from [Result.Signal] or [ScanConfig], and it is the only
// thing in this file that is not a property of the provider: a type
// admitted this way is admitted for this configuration, on the evidence of
// the blocks in it. A nil signal makes this exactly [VerifyTable]. See
// [DerivableWith] for why the cohort needs a configuration at all.
func VerifyTableIn(resourceTypes map[string]providers.Schema, signal *ConfigSignal) Verification {
	return verifyTable(DefaultTable, resourceTypes, signal)
}

func verifyTable(table map[string]TypeIdentity, resourceTypes map[string]providers.Schema, signal *ConfigSignal) Verification {
	v := Verification{
		Derivable: DerivableWith(resourceTypes, signal),
		Report:    Report(resourceTypes, signal),
	}

	types := make([]string, 0, len(table))
	for typeName := range table {
		types = append(types, typeName)
	}
	sort.Strings(types)

	for _, typeName := range types {
		entry := table[typeName]
		if entry.Type == "" {
			// The map key is the authority on which type an entry is for,
			// so a finding never has to say "".
			entry.Type = typeName
		}

		schema, served := resourceTypes[typeName]
		if !served {
			v.Skipped = append(v.Skipped, Skipped{
				Type:   typeName,
				Reason: "the provider does not serve this resource type",
			})
			continue
		}

		findings := checkArguments(entry, schema)

		if schema.IdentitySchema == nil {
			v.Skipped = append(v.Skipped, Skipped{
				Type:   typeName,
				Reason: "the provider serves no identity schema for this resource type",
			})
		} else {
			findings = append(findings, checkIdentity(entry, schema)...)
			if len(findings) == 0 {
				v.Agreed = append(v.Agreed, typeName)
			}
			required, _ := identityAttrs(schema.IdentitySchema)
			if entry.ComposesIdentity(required) {
				v.IdentityImportable = append(v.IdentityImportable, typeName)
			}
		}

		v.Findings = append(v.Findings, findings...)
	}

	sort.SliceStable(v.Findings, func(i, j int) bool {
		if v.Findings[i].Type != v.Findings[j].Type {
			return v.Findings[i].Type < v.Findings[j].Type
		}
		return v.Findings[i].Kind < v.Findings[j].Kind
	})
	return v
}

// checkArguments is the half of the check that needs no identity schema:
// everything the table names has to exist on the type. A name that does not
// is breaking whatever the identity schema says, because the table's own
// machinery reads it.
func checkArguments(entry TypeIdentity, schema providers.Schema) []Finding {
	if schema.Block == nil {
		return nil
	}
	var findings []Finding

	for _, c := range entry.Components {
		if len(c.Attrs) == 0 {
			continue
		}
		if hasAny(schema.Block, c.Attrs) {
			continue
		}
		findings = append(findings, Finding{
			Type:       entry.Type,
			Kind:       FindingArgumentNotInSchema,
			Breaking:   true,
			TableSide:  strings.Join(c.Attrs, " or "),
			SchemaSide: "no such argument",
			Detail: fmt.Sprintf(
				"The identity table builds %s's identity from %s, and the provider's schema for %s has no argument by any of those names. No instance of this type can resolve until the table is corrected.",
				entry.Type, joinQuoted(c.Attrs, " or "), entry.Type,
			),
		})
	}

	for _, name := range entry.IdentityAttrs {
		if hasAny(schema.Block, []string{name}) {
			continue
		}
		findings = append(findings, Finding{
			Type:       entry.Type,
			Kind:       FindingAttributeNotInSchema,
			Breaking:   true,
			TableSide:  name,
			SchemaSide: "no such attribute",
			Detail: fmt.Sprintf(
				"The identity table offers %s.%s as an identity source for other resources to reference, and the provider's schema for %s has no such attribute. A reference to it resolves against nothing.",
				entry.Type, name, entry.Type,
			),
		})
	}

	return findings
}

// checkIdentity is the half that needs the identity schema: the table's
// claim about what identifies the type, against the provider's.
//
// Two asymmetries are deliberate. Only the required-for-import attributes
// have to be covered by the table, because the optional ones are what the
// provider fills in itself (account_id, region) or what it accepts as one
// of several alternatives (aws_route's three destination arguments, of
// which the schema requires none and the table picks whichever the config
// uses). And a component is checked against the identity schema's whole
// attribute set, required and optional together, for the same reason.
//
// [SynthesizeTypeIdentity] leans on the same asymmetry from the other
// direction: a hand entry is free to build the rest of the identity from
// optional attributes because a human wrote it and can see the schema, but
// synthesis has to refuse the moment optional-for-import means anything
// other than context, because inferring which alternative applies is
// exactly what it cannot do. aws_route is the type that makes both sides of
// this concrete.
func checkIdentity(entry TypeIdentity, schema providers.Schema) []Finding {
	required, optional := identityAttrs(schema.IdentitySchema)
	all := append(append([]string{}, required...), optional...)

	var findings []Finding

	named := entry.namedAttrs()
	var unnamed []string
	for _, name := range required {
		if !named[name] {
			unnamed = append(unnamed, name)
		}
	}
	if len(unnamed) > 0 {
		findings = append(findings, Finding{
			Type:       entry.Type,
			Kind:       FindingUnnamedIdentityAttr,
			TableSide:  entry.claim(),
			SchemaSide: strings.Join(required, " + "),
			Detail: fmt.Sprintf(
				"The provider requires %s to import a %s, and the identity table's entry for that type never names %s: it identifies the type by %s. The table is building a different string than the one the provider calls this type's identity.",
				joinQuoted(required, " and "), entry.Type, joinQuoted(unnamed, " or "), entry.claim(),
			),
		})
	}

	// The per-attribute half of the check: an entry that says which identity
	// attribute each component supplies is asserting something the schema can
	// answer directly, name by name.
	inSchema := make(map[string]bool, len(all))
	for _, name := range all {
		inSchema[name] = true
	}
	var claimed []string
	seenClaim := make(map[string]bool)
	for _, c := range entry.Components {
		if c.IdentityAttr == "" || c.IdentityAttr == SameNameIdentity {
			// Nothing named, or named by the same-name rule, which the
			// component check below already covers over the whole
			// alternative list.
			continue
		}
		if inSchema[c.IdentityAttr] || seenClaim[c.IdentityAttr] {
			continue
		}
		seenClaim[c.IdentityAttr] = true
		claimed = append(claimed, c.IdentityAttr)
	}
	for _, name := range claimed {
		findings = append(findings, Finding{
			Type:       entry.Type,
			Kind:       FindingIdentityAttrNotInSchema,
			TableSide:  name,
			SchemaSide: strings.Join(all, ", "),
			Detail: fmt.Sprintf(
				"The identity table builds %s's %q identity attribute out of its configuration, and the provider's identity schema for %s has no attribute by that name - it has %s. An identity object built from this entry would carry an attribute the provider will not decode, so the run falls back to the import-ID string the same components build.",
				entry.Type, name, entry.Type, joinQuoted(all, ", "),
			),
		})
	}

	for _, c := range entry.Components {
		if len(c.Attrs) == 0 || containsAny(all, c.Attrs) {
			continue
		}
		findings = append(findings, Finding{
			Type:       entry.Type,
			Kind:       FindingComponentOutsideIdentity,
			TableSide:  strings.Join(c.Attrs, " or "),
			SchemaSide: strings.Join(all, ", "),
			Detail: fmt.Sprintf(
				"The identity table builds part of %s's identity from %s, and the provider's identity schema for it mentions only %s. One of the two is describing something other than this type's identity.",
				entry.Type, joinQuoted(c.Attrs, " or "), joinQuoted(all, ", "),
			),
		})
	}

	if entry.ServerAssigned && len(required) > 0 && schema.Block != nil {
		configSets := true
		for _, name := range required {
			arg, ok := schema.Block.Attributes[name]
			if !ok || !arg.Required {
				configSets = false
				break
			}
		}
		if configSets {
			findings = append(findings, Finding{
				Type:       entry.Type,
				Kind:       FindingServerAssignedDisputed,
				TableSide:  "server-assigned, needs discovery",
				SchemaSide: strings.Join(required, " + "),
				Detail: fmt.Sprintf(
					"The identity table says a %s's identity is assigned by the provider's API and appears nowhere in configuration, and the provider's schemas say it is %s, which the configuration is required to set. If the schemas are right, this type admits itself and does not need marker discovery.",
					entry.Type, joinQuoted(required, " and "),
				),
			})
		}
	}

	return findings
}

// Diagnostics renders the findings the way the issue asks for them: a
// warning per divergence, an error per breaking one, each naming both
// sides.
//
// Divergence is a warning because the two sides are allowed to describe one
// identity differently - the table carries an inference (which argument
// supplies an identity attribute) that no schema contains - so a
// disagreement is a thing to look at, not a thing to stop for. A breaking
// finding is an error because it is not a matter of description: the table
// names an argument or an attribute the type does not have, and following
// it cannot produce anything.
func (v Verification) Diagnostics() tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, f := range v.Findings {
		diags = diags.Append(f.Diagnostic())
	}
	return diags
}

// The two summaries [Finding.Diagnostic] can produce. They are constants
// rather than fmt.Sprintf templates because a Summary is this package's rule
// identity - see refusals.go - and an interpolated one is invisible to
// [TestRefusalsRegistered]'s scanner, which is exactly how both of these
// escaped the registry when it was written. The type name they used to
// interpolate is in every Detail already.
const (
	SummarySchemaDisagreement = "Identity table and provider schema disagree"
	SummarySchemaBreaking     = "The identity table names something the provider does not have"
)

// Diagnostic renders one finding. See [Verification.Diagnostics].
func (f Finding) Diagnostic() tfdiags.Diagnostic {
	severity := tfdiags.Warning
	summary := SummarySchemaDisagreement
	if f.Breaking {
		severity = tfdiags.Error
		summary = SummarySchemaBreaking
	}
	return tfdiags.Sourceless(severity, summary, f.Detail)
}

// Summary is the one-line result, for a log line.
func (v Verification) Summary() string {
	breaking := 0
	for _, f := range v.Findings {
		if f.Breaking {
			breaking++
		}
	}
	newly := 0
	byConfig := 0
	for _, d := range v.Derivable {
		if !d.InTable {
			newly++
		}
		if d.Admits == AdmitConfigSignal {
			byConfig++
		}
	}
	return fmt.Sprintf(
		"%d table entries confirmed, %d unverifiable, %d divergences (%d breaking); %d import by identity object rather than by a joined string; %d types admit themselves (%d of them only because this configuration names them), %d of those not in the table; %d more would admit themselves against a configuration that sets their identity arguments",
		len(v.Agreed), len(v.Skipped), len(v.Findings), breaking,
		len(v.IdentityImportable),
		len(v.Derivable), byConfig, newly, v.Report.Counts.NeedsConfigSignal,
	)
}

// identityAttrs splits an identity schema's attributes into the ones the
// provider requires for import and the ones it accepts optionally, each
// sorted. The optional ones are context the provider can supply itself
// (account_id, region) or alternatives among which it needs no particular
// one, so only the required ones are what configuration has to name.
func identityAttrs(body *configschema.Object) (required, optional []string) {
	if body == nil {
		return nil, nil
	}
	for name, attr := range body.Attributes {
		switch {
		case attr.Required:
			required = append(required, name)
		default:
			optional = append(optional, name)
		}
	}
	sort.Strings(required)
	sort.Strings(optional)
	return required, optional
}

// namedAttrs is every attribute name this entry mentions, on either side:
// the arguments its components read and the attributes it hands out as
// identity sources. It is the set an identity schema's attribute has to
// land in for the entry to be talking about the same identity.
func (t TypeIdentity) namedAttrs() map[string]bool {
	out := make(map[string]bool)
	for _, c := range t.Components {
		for _, a := range c.Attrs {
			out[a] = true
		}
	}
	for _, a := range t.IdentityAttrs {
		out[a] = true
	}
	return out
}

// claim renders what this entry says identifies the type, for a message
// that has to show both sides.
func (t TypeIdentity) claim() string {
	if t.ServerAssigned {
		if len(t.IdentityAttrs) > 0 {
			return fmt.Sprintf("a server-assigned %s", strings.Join(t.IdentityAttrs, "/"))
		}
		return "a server-assigned identity"
	}
	parts := make([]string, 0, len(t.Components))
	for _, c := range t.Components {
		switch {
		case c.Cloud != CloudNone:
			parts = append(parts, "the run's "+c.Cloud.describe())
		case len(c.Attrs) == 0:
			parts = append(parts, fmt.Sprintf("%q", c.Literal))
		default:
			parts = append(parts, strings.Join(c.Attrs, "|"))
		}
	}
	return strings.Join(parts, " + ")
}

func hasAny(block *configschema.Block, names []string) bool {
	for _, name := range names {
		if _, ok := block.Attributes[name]; ok {
			return true
		}
		if _, ok := block.BlockTypes[name]; ok {
			return true
		}
	}
	return false
}

func containsAny(haystack, needles []string) bool {
	for _, n := range needles {
		for _, h := range haystack {
			if h == n {
				return true
			}
		}
	}
	return false
}

func joinQuoted(names []string, sep string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, sep)
}
