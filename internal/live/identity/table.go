// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "sort"

// TypeIdentity is what this package knows about one resource type's
// identity: whether the identity exists in configuration at all, how to
// build it from configuration arguments if it does, and which of the type's
// attributes carry that identity when another resource refers to it.
type TypeIdentity struct {
	// Type is the resource type name, e.g. "aws_route".
	Type string

	// ServerAssigned is true when the identity is minted by the provider's
	// API at create time and appears nowhere in configuration. Instances of
	// such a type always classify as ClassNeedsDiscovery, whatever their
	// arguments say.
	ServerAssigned bool

	// Reason is the operator-facing explanation attached to every
	// ClassNeedsDiscovery resolution of this type. Required when
	// ServerAssigned, unused otherwise.
	Reason string

	// RecordBacked is true when this type's identity is not observed from
	// the cloud at all: the type is one of GitHub issue #73's
	// RECORD_ADMITTED logical types (internal/live/lint), whose whole
	// existence is a persisted micro-state record. Instances of such a
	// type would always classify [ClassRecordBacked], whatever their
	// arguments say, the same way ServerAssigned instances always classify
	// ClassNeedsDiscovery above.
	//
	// Live since #73 phase (d): ten rows of [DefaultTable] set it
	// (null_resource, terraform_data, the random_* and time_* families),
	// and internal/live/projection consumes ClassRecordBacked to hydrate
	// such an instance from the record store without a cloud read. Lint
	// still refuses these types when no record_store is configured, which
	// is why a configuration without one never reaches a row that sets
	// this. [SynthesizeTypeIdentity] never produces it.
	RecordBacked bool

	// Components build the import identity by concatenation, in order.
	// Required unless ServerAssigned or RecordBacked.
	Components []Component

	// ImportSyntax documents the provider's import-ID grammar for this
	// type, in the provider documentation's own notation (e.g.
	// "ROUTETABLEID_DESTINATION"). Documentation only: Components is what
	// the code follows.
	ImportSyntax string

	// IdentityAttrs are the attribute names whose value equals this type's
	// identity, so that a reference to one of them from another resource
	// can be resolved without a cloud read. A reference to any other
	// attribute of this type is an error rather than a guess.
	//
	// An empty list means no attribute of this type is usable as an
	// identity source, which is the honest answer for types whose "id"
	// attribute is a provider-synthesized value distinct from the import
	// ID.
	IdentityAttrs []string

	// IdentityObjectOnly is true for an entry whose identity has more than
	// one attribute and no separator to join them with, so there is no
	// import-ID string to build. A resolution of such a type carries
	// [Resolution.IdentityValues] and an empty [Resolution.ImportID], and
	// the projection imports it by identity object or not at all.
	//
	// It exists because the alternative is worse than a refusal. Component
	// values are concatenated with nothing between them
	// (internal/live/identity/resolve.go's classify), so a two-attribute
	// composite with cluster="prod" and name="svc" would produce the import
	// ID "prodsvc" - not empty, so every guard that tests for emptiness
	// passes it through, and a run that fell back from the identity object
	// would import "prodsvc" against a real account with a TRACE log to say
	// so. GitHub issue #105 names that as the thing to guard before relaxing
	// the composite refusal at all.
	//
	// Only [SynthesizeTypeIdentity] sets it. A hand-written row states its
	// separator, which is the whole reason those rows are hand-written.
	IdentityObjectOnly bool

	// Synthesized is true when this entry was not written by hand but built
	// from the provider's own identity schema at resolution time. See
	// [SynthesizeTypeIdentity].
	Synthesized bool

	// Admits records what backed a synthesized entry: the schemas alone
	// ([AdmitSchema]) or the schemas plus this configuration
	// ([AdmitConfigSignal]). Empty for a hand-written row, which is asserted
	// rather than derived.
	Admits Admission
}

// Component is one piece of an import identity: a fixed separator (Literal,
// with Attrs empty and Cloud unset), a resource argument (Attrs, first
// present wins), or a property of the cloud the run is against (Cloud).
type Component struct {
	// Literal is a fixed fragment of the import ID, used for separators.
	Literal string

	// Attrs names the resource arguments to read, in preference order. The
	// first one present in configuration supplies the value; if none is
	// present, resolution fails naming all of them - unless Default below
	// says what omission means.
	Attrs []string

	// Default is the literal value this component contributes when none of
	// Attrs is set in the configuration, for the arguments whose omission
	// the provider itself documents as selecting a server-side default: an
	// omitted event_bus_name means the "default" event bus, and the Import
	// section says so in words ("if you omit `event_bus_name`, the
	// `default` event bus will be used" - scraped into import-grammar's
	// omitted_fallbacks field). The empty string keeps the standing rule:
	// an unset identity argument refuses resolution. A component carrying
	// one still supplies its identity attribute - under the first Attrs
	// name, since no argument was chosen per instance - so identity-object
	// import works the same whether the configuration wrote the default or
	// left it out.
	Default string

	// ServerAssignedIfAbsent is true when the provider itself documents
	// that omitting every name in Attrs is not an absence of identity but
	// a request for one: the provider assigns a fresh value - usually a
	// random, unique one - at create time, the same convention
	// [TypeIdentity.ServerAssigned] already gives a type-wide resolution
	// class to. Component narrows that convention to a single argument
	// ("If omitted, Terraform will assign a random, unique name" on an
	// otherwise ordinarily-identified type, not a type whose identity is
	// server-assigned outright), so a component whose Attrs are all unset
	// classifies [ClassNeedsDiscovery] instead of refusing outright - see
	// [resolver.identityArgs]'s attr-nil branch. Unlike Default, which
	// supplies a known literal, this supplies no value at all: the
	// resolver still cannot build the import ID, it can only say why not
	// yet. Derived by tools/row-gen/emit.go from
	// tools/importdocs-gen's Argument Reference scrape
	// (ArgumentRefEntry.ServerAssignedIfAbsent) for every ratified row, the
	// same way every other field in this file is - see #190.
	ServerAssignedIfAbsent bool

	// Cloud names a value that comes from the cloud the run is pointed at
	// rather than from the configuration: the AWS account ID, the region.
	// [CloudNone] - the zero value - means this component is a literal or an
	// argument.
	//
	// A component like this is why [CloudContext] exists. Nothing in a
	// configuration says which account it will be applied to, so an identity
	// that embeds the account is not computable from configuration alone,
	// and this package will not read it from anywhere: it is handed the
	// values or it says it does not have them. See [ResolveIn].
	//
	// Cloud and Attrs are not exclusive, and a component setting both is the
	// ordinary case rather than a contradiction: the provider documents an
	// argument whose omission means "this cloud property" and whose presence
	// means something else entirely ("If omitted, this defaults to the AWS
	// Account ID" on a Glue catalog_id, which is how a configuration points
	// at another account's Data Catalog). Attrs then wins whenever the
	// configuration sets it and Cloud is the fallback, decided per instance
	// by [resolver.cloudComponentAttr]. The field is filled in by
	// tools/row-gen's mergeCloudDefault from the provider's own Argument
	// Reference rather than ratified by hand, the same way
	// ServerAssignedIfAbsent is.
	Cloud CloudValue

	// IdentityAttr names the attribute of the provider's resource identity
	// schema that this component supplies, and is the inference the schemas
	// themselves do not carry: an identity schema says a route is identified
	// by a route_table_id, not that the route_table_id argument is where
	// that value is written.
	//
	// Three values, and the helpers below spell all three:
	//
	//   - [SameNameIdentity], what [attr] sets, is the ordinary case: the
	//     identity attribute is whichever argument supplied the value, under
	//     its own name. It is a rule rather than a name because the argument
	//     is chosen per instance - aws_route reads whichever of three
	//     destination arguments the route uses, and the provider's identity
	//     schema names all three.
	//   - An explicit attribute name, what [inAttr] sets, for the case where
	//     the two differ. Several components may name one attribute, and
	//     their rendered strings then concatenate in order to form it, which
	//     is how an SNS topic's single "arn" is built out of a literal
	//     prefix, the region, the account and the topic's name.
	//   - Empty: this component supplies no identity attribute at all. That
	//     is every separator between two identity attributes - exactly the
	//     character that stops mattering once a run imports by identity
	//     object - and it is what [idlessAttr] says about an argument whose
	//     value is part of the import string and part of no identity.
	//
	// An entry whose components do not supply every attribute the provider
	// requires for import cannot be imported by identity, and its import-ID
	// string is used instead. aws_route_table_association is the archetype:
	// the provider identifies an association by the rtbassoc- ID it assigns,
	// and the table builds the association's documented import string out of
	// a subnet and a route table. Both are right about different things, and
	// only one of them is an identity object.
	IdentityAttr string

	// SoleElement narrows this component's value before every other rule in
	// this struct's comments applies: when the argument's own expression is
	// a static list or set construct, resolution requires it to carry
	// exactly one element and uses that element's value in place of the
	// whole collection. A scalar-typed argument (another name in the same
	// Attrs list may be scalar, e.g. a mutually exclusive sibling) passes
	// through unchanged - the flag only ever narrows a collection, never
	// touches a value that already is not one.
	//
	// It exists for identities the provider documents as one segment per
	// value in a list-typed argument (aws_security_group_rule's import ID
	// appends one token per entry of whichever of cidr_blocks,
	// ipv6_cidr_blocks or prefix_list_ids is set), where cty's own convert
	// package refuses a list-to-string conversion unconditionally,
	// regardless of length (verified: even a one-element
	// cty.ListVal/cty.SetVal fails convert.Convert(..., cty.String) with
	// "string required, but have list/set of string"). The common,
	// documented single-value case - one CIDR, one prefix list ID - is
	// exactly a one-element list, so unwrapping it is not a guess about
	// which element to keep.
	//
	// More than one element refuses outright rather than picking one: the
	// AWS API, not the configuration's own list order, decides how such a
	// rule's multiple sources compose into (or split across) real
	// SecurityGroupRule objects (the provider's own docs: "Not all rule
	// permissions... need to be imported"), so no configuration-only
	// answer for the segment order would be honest. A non-static list (a
	// variable or function call, rather than a literal `[...]` or `[ref]`
	// construct written in configuration) refuses the same way, for the
	// same reason [resolver.isSymbolic] and [resolver.evalStatic] already
	// refuse other non-static identity arguments elsewhere in this
	// package.
	SoleElement bool
}

// SameNameIdentity is [Component.IdentityAttr] for the ordinary case: the
// identity attribute is the argument that supplied the value, under its own
// name. It is not an attribute name and no schema has one - the character is
// not legal in an identifier - because it stands for a rule that is applied
// per instance, once the argument is chosen.
const SameNameIdentity = "*"

// ContentMatchBinding names issue #272's discovery leg for a type this
// package treats as [TypeIdentity.ServerAssigned]: the provider mints the
// identity, so [Resolve] always classifies [ClassNeedsDiscovery], but the
// type carries no tags argument, so no ownership marker can be written on
// it either. Ordinarily that conjunction is what [MarkerlessTypes] vetoes -
// there is no way to find the object again after creating it. A type here
// is the one exception: its identity-bearing argument is proven unique
// within the account by two independent sources agreeing (the provider's
// own Argument Reference and the CloudFormation Registry's property
// description - see tools/row-gen's two-source derivation), so matching a
// live object's own property against that argument's declared value is not
// a guess. It is reading the identity the configuration already states.
//
// internal/live/discovery's content-match leg is the consumer: given a
// binding, it lists CFNType through Cloud Control, reads each candidate's
// PropertyPath, and binds a needs-discovery instance to the one candidate
// whose value equals the instance's own Argument. Zero matches leaves the
// instance unbound, same as an ordinary create. Two or more matches is a
// refusal (internal/live/discovery.ProblemAmbiguousContentMatch), never a
// guess - the one absolute rule this mechanism holds itself to.
//
// [ContentMatchTypes] is the generated roster; nothing here is maintained
// by hand.
type ContentMatchBinding struct {
	// Argument is the resource's own top-level configuration argument to
	// read and match on - "name" for every type admitted through this
	// mechanism today.
	Argument string

	// CFNType is the CloudFormation Registry type Cloud Control lists to
	// find candidates, e.g. "AWS::CloudFront::CachePolicy".
	CFNType string

	// PropertyPath is the listed candidate's own Properties path to the
	// matching value, in Cloud Control's own nesting: a one-element path
	// for a top-level property, a two-element path
	// ["CachePolicyConfig", "Name"] for one wrapped in the resource's
	// single mutable-config object - the shape every type admitted through
	// this mechanism today has.
	PropertyPath []string
}

// CloudValue names one property of the cloud a run is against, for a
// [Component] whose value the configuration does not carry.
//
// The set is deliberately tiny and closed. It covers the two substitutions
// the AWS provider's account-embedded import identities need - an SQS queue
// URL (https://sqs.REGION.amazonaws.com/ACCOUNT/NAME) and an SNS topic ARN
// (arn:aws:sns:REGION:ACCOUNT:NAME) - and nothing else. A third kind is a
// decision to make deliberately, not a slot to fill: the partition ("aws",
// "aws-cn", "aws-us-gov") is written as a literal in the templates below
// because every one of those identities is partition-specific in more places
// than the partition segment, and a run against a non-commercial partition
// needs a review rather than a substitution.
type CloudValue string

const (
	// CloudNone is the zero value: this component reads no cloud property.
	CloudNone CloudValue = ""

	// CloudAccountID is the AWS account ID the run is against, twelve
	// digits, no hyphens.
	CloudAccountID CloudValue = "account-id"

	// CloudRegion is the region the run is against, e.g. "us-east-1".
	CloudRegion CloudValue = "region"
)

// describe renders a cloud value for an operator-facing sentence.
func (c CloudValue) describe() string {
	switch c {
	case CloudAccountID:
		return "AWS account ID"
	case CloudRegion:
		return "region"
	default:
		return string(c)
	}
}

// CloudContext is the set of cloud properties a caller can supply so that
// identities embedding them become computable. Both fields are optional; an
// empty one means "not known", never "empty string".
//
// This is the whole of the escape hatch, and its shape is what keeps this
// package cloud-free and process-free (see the package documentation).
// Nothing here is discovered: the values arrive from a caller that already
// has them, and a caller that has none passes the zero value and gets
// [ClassNeedsDiscovery] for the types that need them rather than an error.
//
// Where the values come from in this fork, when they come from anywhere:
// the region is the provider configuration's own, which
// [internal/live/discovery.Request.Region] already carries and which
// every signed request the list clients make must have. The account ID first
// becomes knowable one phase later still - the AWS provider puts an
// account_id attribute in the resource identity objects it attaches to list
// results, which [internal/live/discovery.TypeScan.AccountID] records
// as the scan runs. Both are therefore behind the provider, and identity
// resolution runs in front of it, which is why the pipeline as it stands
// passes the zero value and the two account-derived types below reach their
// live resources through their markers instead.
type CloudContext struct {
	// AccountID is the AWS account ID the run is against.
	AccountID string

	// Region is the region the run is against.
	Region string
}

// value returns the context's value for one cloud property, and whether the
// caller supplied it.
func (c CloudContext) value(which CloudValue) (string, bool) {
	var v string
	switch which {
	case CloudAccountID:
		v = c.AccountID
	case CloudRegion:
		v = c.Region
	default:
		return "", false
	}
	return v, v != ""
}

// attr reads the first of these arguments the configuration sets, and
// supplies the identity attribute of that same name. That is the whole of the
// inference for all but three of the entries below.
func attr(names ...string) Component {
	return Component{Attrs: names, IdentityAttr: SameNameIdentity}
}

// inAttr says a component supplies one named identity attribute rather than
// the one its own name implies. Several components wrapped in the same name
// concatenate into it, in order.
func inAttr(identityAttr string, c Component) Component {
	c.IdentityAttr = identityAttr
	return c
}

// LookupType returns the identity knowledge for a resource type, and
// whether the type is in the table at all.
func LookupType(typeName string) (TypeIdentity, bool) {
	e, ok := DefaultTable[typeName]
	return e, ok
}

// identityAttrFor is the identity attribute one component supplies for one
// instance, given which of its alternative arguments the configuration
// actually set. The empty string means it supplies none.
func (c Component) identityAttrFor(argName string) string {
	if c.IdentityAttr != SameNameIdentity {
		return c.IdentityAttr
	}
	// The rule only means anything for a component that read an argument;
	// a literal or a cloud value has no name of its own to take.
	return argName
}

// SuppliesIdentityAttr reports whether some component of this entry can
// supply the named identity attribute, taking the same-name rule over each
// component's whole alternative list.
//
// It is the type-level question, so it is answered over every alternative:
// aws_route can supply destination_cidr_block or destination_ipv6_cidr_block
// or destination_prefix_list_id, and which one a given route supplies is a
// per-instance answer [Resolve] makes.
func (t TypeIdentity) SuppliesIdentityAttr(name string) bool {
	for _, c := range t.Components {
		if c.IdentityAttr == "" {
			continue
		}
		if c.IdentityAttr != SameNameIdentity {
			if c.IdentityAttr == name {
				return true
			}
			continue
		}
		for _, a := range c.Attrs {
			if a == name {
				return true
			}
		}
	}
	return false
}

// ComposesIdentity reports whether this entry can build a whole identity
// object: every attribute the provider requires for import is supplied by
// some component.
//
// This is the bar for importing by identity rather than by string. It is
// deliberately about the *required* attributes only - the optional ones are
// context the provider fills in itself (account_id, region) or alternatives
// it needs no particular one of - and it is deliberately checked against a
// real identity schema rather than asserted here, because the whole point of
// the exercise is that the provider is the authority on what identifies its
// own resources.
func (t TypeIdentity) ComposesIdentity(required []string) bool {
	if t.ServerAssigned || len(required) == 0 {
		return false
	}
	for _, name := range required {
		if !t.SuppliesIdentityAttr(name) {
			return false
		}
	}
	return true
}

// AdmittedTypes lists every resource type the v0 table covers, sorted.
func AdmittedTypes() []string {
	out := make([]string, 0, len(DefaultTable))
	for t := range DefaultTable {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (t TypeIdentity) hasIdentityAttr(name string) bool {
	for _, a := range t.IdentityAttrs {
		if a == name {
			return true
		}
	}
	return false
}
