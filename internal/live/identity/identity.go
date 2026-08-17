// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
)

// Class is the identity classification of a single managed resource
// instance. Every resolved instance has exactly one, and the class decides
// which of [Resolution]'s three payload fields is populated.
type Class string

const (
	// ClassConcrete means the import identity is fully computable from
	// configuration; Resolution.ImportID holds it. Admission path 1.
	ClassConcrete Class = "CONCRETE"

	// ClassParentDerived means the identity is a composition of parent
	// resources' live IDs with configuration data;
	// Resolution.Formula holds the composition. Admission path 3.
	ClassParentDerived Class = "PARENT_DERIVED"

	// ClassNeedsDiscovery means the identity is server-assigned and can
	// only be recovered by marker discovery; Resolution.Reason says why.
	// Admission path 2.
	ClassNeedsDiscovery Class = "NEEDS_DISCOVERY"

	// ClassRecordBacked means the identity IS the record: a logical
	// resource type (GitHub issue #73's RECORD_ADMITTED lint
	// classification, internal/live/lint) whose whole existence is the
	// persisted micro-state record, not a cloud object. There is no cloud
	// observation to make for an instance of this class, ever - not "not
	// yet discovered," the way ClassNeedsDiscovery's marker sweep resolves
	// in time, but structurally absent, the same way [ClassNeedsDiscovery]'s
	// marker path never applies to it either: a marker is a tag on a cloud
	// resource, and a record-backed instance has no cloud resource to tag.
	// Its identity comes from the record store instead (SSM/S3/local file,
	// per #73's design ruling), which this package does not read.
	//
	// Declared here as groundwork only. No resolver in this package
	// produces it yet: a record-admitted type's resources are refused by
	// lint (internal/live/lint's RuleLogicalResource) before resolution
	// ever runs, so a Resolution's Class is never this value today. The
	// projection work #73 stages next is what will start producing it,
	// additively: existing callers that switch on Class need no change
	// until they choose to handle this one.
	ClassRecordBacked Class = "RECORD_BACKED"

	// ClassRecordLocated means the object exists in the cloud, has nowhere
	// to carry a marker, and the estate's record store holds the answer to
	// "which object is this" - GitHub issue #270, and the maintainer ruling
	// of 2026-08-17 that an ID is not a permission.
	//
	// It is the exact complement of [ClassRecordBacked], and confusing the
	// two is the one mistake that matters here. A record-BACKED instance
	// has no cloud object: the record IS the object, deleting the record
	// deletes the thing, and that is why the record namespace may be
	// enumerated to find orphans. A record-LOCATED instance's object is in
	// the cloud and is read from the cloud exactly like any other; the
	// record carries a single import identity and nothing else. Reading a
	// located record as delete authority would turn a lost or stale key
	// into a cloud deletion, which is what
	// internal/live/projection/located.go's separate namespace and
	// enumeration-free API exist to make structurally impossible rather
	// than merely forbidden.
	//
	// Behaviourally this is [ClassConcrete] with the import identity coming
	// from a store lookup instead of from configuration: once
	// internal/live/projection's builder.materializeLocated has read the
	// identity, the instance goes through the ordinary materialize path -
	// same import, same read, same ownership rule, same encoding. What is
	// different is only where the string came from.
	//
	// Resolution.ImportID is deliberately EMPTY for this class. The
	// identity is not knowable to this package: it is a property of the
	// estate's store, which this package does not read, the same way it
	// does not read the record behind [ClassRecordBacked]. A resolution of
	// this class is a statement that the store is where to look.
	//
	// Produced only when a live block declares a record_store and the
	// provider's own schema clears the type - see [LocatedType] for the
	// three conditions and for why an absent schema fails closed.
	ClassRecordLocated Class = "RECORD_LOCATED"
)

// Resolution is what this package knows about one managed resource
// instance's identity.
//
// Exactly one of ImportID, Formula, and Reason is populated, selected by
// Class. The zero value is not meaningful; resolutions are only produced by
// [Resolve].
type Resolution struct {
	// Addr is the resource instance this resolution describes, including
	// its count or for_each key.
	Addr addrs.AbsResourceInstance

	// Class selects which of the three fields below is populated.
	Class Class

	// ImportID is the provider's import identity string, populated only
	// for ClassConcrete. It is ready to hand to the import path as-is.
	ImportID string

	// Identity is the provider's own resource identity object for this
	// instance, when something already had one: a marker sweep's list result
	// carries the identity the provider attached to it, and handing that back
	// unchanged is strictly better than handing back a string read out of one
	// of its attributes. Null (the zero value) whenever nothing served one,
	// which is every resolution [Resolve] produces - a configuration carries
	// arguments, not identity objects.
	//
	// It never replaces ImportID. The string is what every operator-facing
	// line prints, what a marker rewrite records, and what the import path
	// falls back to for a type the provider serves no identity schema for.
	// Both travel together and the projection builder picks per resource; see
	// internal/live/projection/build.go.
	Identity cty.Value

	// IdentityValues is the identity the *configuration* supplies, one string
	// per identity attribute, populated for ClassConcrete alongside ImportID.
	//
	// It is the same information ImportID holds, unjoined: where ImportID is
	// "rolename/arn:aws:iam::aws:policy/ReadOnlyAccess", this is
	// {"role": "rolename", "policy_arn": "arn:…"}. The separator between the
	// two is the part that only exists in the string, and it is the part no
	// provider schema carries - which is why splitting the two apart here is
	// what lets an import ask by identity object rather than by a grammar
	// this package had to know.
	//
	// Nil when the type's entry does not say which identity attribute each
	// component supplies, which is the honest answer for
	// aws_route_table_association: the provider identifies one by the
	// rtbassoc- ID it assigns, and this package builds the documented import
	// string out of a subnet and a route table instead. Whether what is here
	// is a *whole* identity is not decided here either: that is a question
	// about the provider's identity schema, answered in the projection.
	IdentityValues map[string]string

	// Formula is the symbolic identity, populated only for
	// ClassParentDerived. Render it with the parents' live IDs to get an
	// import ID.
	Formula *Formula

	// Reason explains, in one sentence aimed at an operator, why this
	// instance's identity is not in configuration. Populated only for
	// ClassNeedsDiscovery.
	Reason string

	// Cause is Reason's machine-readable half: which of the structurally
	// different situations ClassNeedsDiscovery covers this instance is in.
	// Populated only for ClassNeedsDiscovery, and only by this package's own
	// walk - see [DiscoveryCause] for why a caller-assembled resolution
	// carries [DiscoveryCauseUnspecified] instead.
	Cause DiscoveryCause

	// CauseArgs are the subjects Cause is about, in a cause-specific order
	// documented on each [DiscoveryCause] constant: the missing cloud
	// property, the omitted argument's candidate names, or the base and
	// prefix argument names. Empty for causes that have no subject.
	CauseArgs []string

	// UniqueName is the account-unique name this instance's configuration
	// states, and the only value discovery may match a listed object against
	// without reading an ownership marker off it. Populated only alongside
	// [DiscoveryUniqueName], and empty everywhere else - including on a
	// resolution of a UniqueName-bearing type whose name argument this run
	// could not evaluate, which is what keeps "no name" and "the empty name"
	// from being spelled the same way.
	UniqueName string

	// Undeclared marks a resolution whose resource block is not in the
	// configuration at all: a live resource this estate owns, found by the
	// marker sweep, whose block was deleted. It is never produced by
	// [Resolve], which only ever describes what the configuration declares;
	// marker discovery adds these afterwards.
	//
	// It exists because a projection built from such a resolution has no
	// configuration to read a provider or a dependency set from, and because
	// the alternative - letting the projection builder infer "no block, so
	// this must be a removal" - would turn a genuine mismatch between the
	// resolutions and the configuration, which is a bug worth failing on,
	// into a silent destroy.
	Undeclared bool

	// cloudScope disambiguates two instances that would otherwise resolve to
	// the same import identity string but do not name the same live object,
	// because they are not pointed at the same account and region: a module
	// called twice with a different `providers = { aws = aws.other }`
	// mapping, or a resource that sets AWS provider's own per-resource
	// `region` argument. Two resources named "example" in eu-west-1 and
	// us-east-1 are not a collision - see [resolver.resourceCloudScope] and
	// [resolver.checkCollisions].
	//
	// It carries no meaning outside this package (never rendered, never
	// compared by a caller), and is set only for [ClassConcrete] and
	// [ClassParentDerived], the two classes checkCollisions inspects.
	cloudScope cloudScopeKey
}

// cloudScopeKey is [Resolution.cloudScope]'s own shape: a base component
// that two resources must match exactly (the resolved provider
// configuration - account, module, alias), and a region component that
// only ever RULES OUT a collision, never causes one on its own (#217). See
// [resolver.resourceCloudScope] for how the two are built and
// [regionsDistinguish] for the comparison this shape exists to make
// possible: a region neither side could determine acts as a wildcard, and
// collides with anything sharing the same base, rather than silently
// distinguishing itself from a side that DID determine one.
type cloudScopeKey struct {
	base string

	// region and regionKnown together are an optional string: regionKnown
	// false means "this run could not establish an effective region for
	// this resource" - deliberately distinct from region=="", which is
	// what a resource literally setting `region = ""` would produce and is
	// itself a known (if odd) value to compare.
	region      string
	regionKnown bool
}

// Type returns the resource type name, e.g. "aws_route".
func (r Resolution) Type() string {
	return r.Addr.Resource.Resource.Type
}

// attrParts is one identity attribute of this resolution, as the parts that
// concatenate into its value, for a reference from another resource that reads
// that attribute. It reports false when this resolution says nothing about the
// named attribute, which is every resolution that is not concrete or
// parent-derived and every entry that does not say which component supplies
// which attribute.
//
// It exists because a reference like aws_cloudwatch_event_target.rule =
// aws_cloudwatch_event_rule.x.name asks for one attribute of a parent whose
// import identity is several attributes joined by a separator. The whole
// import ID is the wrong answer to that question in two ways that no guard
// downstream can see:
//
//   - For a parent whose import ID is composite, it is a longer string than
//     the attribute holds. aws_volume_attachment's entry lists device_name,
//     instance_id and volume_id in [TypeIdentity.IdentityAttrs] while its own
//     components say each supplies one third of "DEVICE_NAME:VOLUME_ID:
//     INSTANCE_ID", so a child reading .device_name used to receive all three.
//   - For a parent that is import-by-identity-object only
//     ([TypeIdentity.IdentityObjectOnly]), the import ID is deliberately
//     empty, so a child reading any of its attributes used to receive "".
//
// Both produce a marker that claims ownership of the wrong cloud object, with
// no diagnostic, which is strictly worse than the refusal a caller gets when
// nothing here can answer.
func (r Resolution) attrParts(name string) ([]Part, bool) {
	switch r.Class {
	case ClassConcrete:
		if v, ok := r.IdentityValues[name]; ok {
			return []Part{{Literal: v}}, true
		}
	case ClassParentDerived:
		if r.Formula == nil {
			return nil, false
		}
		for _, a := range r.Formula.Attrs {
			if a.Name == name {
				return append([]Part(nil), a.Parts...), true
			}
		}
	}
	return nil, false
}

// String renders a resolution in a single line, for diagnostics and test
// failure output.
func (r Resolution) String() string {
	switch r.Class {
	case ClassConcrete:
		if r.Undeclared {
			return r.Addr.String() + " CONCRETE " + r.ImportID + " UNDECLARED"
		}
		return r.Addr.String() + " CONCRETE " + r.ImportID
	case ClassParentDerived:
		return r.Addr.String() + " PARENT_DERIVED " + r.Formula.String()
	default:
		return r.Addr.String() + " NEEDS_DISCOVERY " + r.Reason
	}
}

// Formula is a parent-derived identity: the recipe for an import ID that
// cannot be written down until the parents' live IDs are known.
//
// The import ID is the concatenation of Parts in order. Rendering is
// deliberately dumb string concatenation: the AWS provider's import-ID
// syntaxes are all "component, separator, component", and the separators
// live in Parts as literals so that nothing downstream has to know which
// resource type uses "_" and which uses "/".
type Formula struct {
	// Parts are the pieces of the import ID, in order.
	Parts []Part

	// Attrs is the same formula split by identity attribute: the pieces of
	// each attribute of the provider's identity object, in order, for the
	// attributes this type's entry says which component supplies. Nil when
	// the entry says of none of them, which is [Resolution.IdentityValues]'s
	// case for a rendered formula rather than a concrete one.
	//
	// It is not a second formula. Every part in it is a part of Parts too;
	// what is missing from it are the separator characters between two
	// attributes, which belong to the import-ID string and to no identity.
	Attrs []AttrFormula

	// Parents lists every parent instance referenced by Parts, deduplicated
	// and sorted by address. It is the dependency set: P1.3 must know all
	// of these live IDs before this instance's identity exists.
	Parents []addrs.AbsResourceInstance
}

// AttrFormula is one identity attribute's share of a [Formula]: the pieces
// that concatenate into that attribute's value once the parents' live IDs are
// known.
type AttrFormula struct {
	// Name is the identity attribute, as the provider's identity schema
	// names it.
	Name string

	// Parts concatenate, in order, exactly as [Formula.Parts] do.
	Parts []Part
}

// String renders the formula in OpenTofu interpolation syntax, e.g.
// `${aws_route_table.main.id}_0.0.0.0/0`. It is for humans and tests; use
// [Formula.Render] to produce a real import ID.
func (f *Formula) String() string {
	var buf strings.Builder
	for _, p := range f.Parts {
		if p.Parent == nil {
			buf.WriteString(p.Literal)
			continue
		}
		buf.WriteString("${")
		buf.WriteString(p.Parent.Instance.String())
		buf.WriteString(".")
		buf.WriteString(p.Parent.Attr)
		buf.WriteString("}")
	}
	return buf.String()
}

// Render builds the concrete import ID by asking the caller for each
// parent's live value. The lookup receives a parent instance address and
// the identity attribute name being read, and returns the value and
// whether it is known. If any lookup reports the value as unknown, Render
// returns ok == false and the caller must not use the string: a formula
// with a hole in it is not an identity.
func (f *Formula) Render(lookup func(parent addrs.AbsResourceInstance, attr string) (string, bool)) (string, bool) {
	return renderParts(f.Parts, lookup)
}

// RenderAttrs is [Formula.Render] per identity attribute: the identity object
// this instance's configuration supplies, once the parents' live IDs are
// known, in the same form [Resolution.IdentityValues] holds for a concrete
// instance. It returns nil when the formula carries no per-attribute split,
// and ok == false on the same unknown-parent condition Render fails on.
func (f *Formula) RenderAttrs(lookup func(parent addrs.AbsResourceInstance, attr string) (string, bool)) (map[string]string, bool) {
	if len(f.Attrs) == 0 {
		return nil, true
	}
	out := make(map[string]string, len(f.Attrs))
	for _, a := range f.Attrs {
		s, ok := renderParts(a.Parts, lookup)
		if !ok {
			return nil, false
		}
		out[a.Name] = s
	}
	return out, true
}

func renderParts(parts []Part, lookup func(parent addrs.AbsResourceInstance, attr string) (string, bool)) (string, bool) {
	var buf strings.Builder
	for _, p := range parts {
		if p.Parent == nil {
			buf.WriteString(p.Literal)
			continue
		}
		v, ok := lookup(p.Parent.Instance, p.Parent.Attr)
		if !ok {
			return "", false
		}
		buf.WriteString(v)
	}
	return buf.String(), true
}

// Part is one piece of a [Formula]: either a literal fragment of the import
// ID (config data or a separator) or a reference to a parent's live value.
// Exactly one of Literal and Parent is meaningful; Parent == nil selects
// Literal.
type Part struct {
	Literal string
	Parent  *ParentRef
}

// ParentRef is a reference to one identity attribute of one parent resource
// instance: the live value that has to be discovered before a formula can
// be rendered.
type ParentRef struct {
	// Instance is the parent resource instance, with its own expansion key.
	Instance addrs.AbsResourceInstance

	// Attr is the attribute being read, always one of the parent type's
	// IdentityAttrs in the table (in practice "id").
	Attr string
}

// Result is the outcome of resolving a whole configuration: every managed
// resource instance in it, classified.
type Result struct {
	byAddr map[string]Resolution
	order  []string

	// signal is the config-side naming signal collected on the same walk.
	// See [Result.Signal].
	signal *ConfigSignal
}

// Signal is the config-side naming signal for the configuration this result
// was resolved from: which arguments each managed resource instance sets.
//
// It covers every managed resource type in the configuration, not only the
// ones the admission table knows, because its purpose is the question the
// table cannot answer for itself - which types could join it. See
// [ConfigSignal] and [DerivableWith]. A result assembled by hand rather
// than by [Resolve], such as one marker discovery has added to, carries no
// signal and returns nil, which every consumer treats as "nothing to say".
func (res *Result) Signal() *ConfigSignal {
	if res == nil {
		return nil
	}
	return res.signal
}

func newResult() *Result {
	return &Result{byAddr: make(map[string]Resolution)}
}

func (res *Result) add(r Resolution) {
	key := r.Addr.String()
	if _, exists := res.byAddr[key]; !exists {
		res.order = append(res.order, key)
	}
	res.byAddr[key] = r
}

// Get returns the resolution for one instance address.
func (res *Result) Get(addr addrs.AbsResourceInstance) (Resolution, bool) {
	r, ok := res.byAddr[addr.String()]
	return r, ok
}

// Len is the number of resolved instances.
func (res *Result) Len() int {
	return len(res.order)
}

// All returns every resolution, ordered by address.
func (res *Result) All() []Resolution {
	out := make([]Resolution, 0, len(res.order))
	for _, k := range res.order {
		out = append(out, res.byAddr[k])
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr.String() < out[j].Addr.String()
	})
	return out
}

// OfClass returns every resolution in one class, ordered by address.
func (res *Result) OfClass(c Class) []Resolution {
	var out []Resolution
	for _, r := range res.All() {
		if r.Class == c {
			out = append(out, r)
		}
	}
	return out
}

// NeedsDiscovery is the roadmap's needs-discovery list: the instances whose
// identity waits on P2's marker discovery.
func (res *Result) NeedsDiscovery() []Resolution {
	return res.OfClass(ClassNeedsDiscovery)
}

// RecordBacked is GitHub issue #73's list: the instances of a RECORD_ADMITTED
// logical type whose identity is a persisted micro-state record rather than
// a cloud observation. internal/live/projection's hydration work bucket
// reads this instead of the concrete/parent-derived/needs-discovery lists.
func (res *Result) RecordBacked() []Resolution {
	return res.OfClass(ClassRecordBacked)
}

// RecordLocated is GitHub issue #270's list: the instances whose cloud
// object carries no marker and whose import identity the estate's record
// store holds. See [ClassRecordLocated] for why that is a different list
// from [Result.RecordBacked] and not a wider one.
func (res *Result) RecordLocated() []Resolution {
	return res.OfClass(ClassRecordLocated)
}

// InstanceFailure is carried in the Extra field of every error diagnostic
// [ResolveWith] raises while building one resource instance's identity -
// whether the failure is that instance's own (a required argument with no
// value, an unsupported expression shape) or cascades from a parent
// instance whose own identity could not be resolved ([resolver.parentPart]'s
// "Unresolvable identity"). It names which instance the diagnostic is
// about, structurally, so a caller can group every diagnostic belonging to
// one instance without parsing Detail text.
//
// This is GitHub issue #221's fix. Before it, resolveInstance returned at
// the first failing component of a type's identity, so an instance with two
// independently-failing components produced only one diagnostic - and if
// that one happened to be a cascade onto an eligible data read, a caller
// could reclassify the whole instance as fine while a second, wholly
// unrelated failure stayed invisible. resolveInstance now evaluates every
// component and lets every failure raise its own diagnostic; InstanceFailure
// is what lets internal/live/check tell, for one instance, whether ALL of
// its failures trace back to an eligible read or whether at least one does
// not - the fixpoint in internal/live/check/analyze.go only ever
// reclassifies the former.
//
// A diagnostic that already carries other Extra info - a data-source
// reference's [configs.RefusedReference], say - keeps it: InstanceFailure
// wraps rather than replaces it (see [resolver.appendDiags]), reachable
// through the same [tfdiags.ExtraInfo] unwrap chain RefusedReference itself
// documents for [ReferenceCategory].
type InstanceFailure struct {
	// Addr is the resource instance this diagnostic concerns, in
	// [addrs.AbsResourceInstance.String] form.
	Addr string

	// inner is whatever Extra value this diagnostic already carried before
	// InstanceFailure was attached, or nil. Unexported: a caller reads it
	// only indirectly, through [tfdiags.ExtraInfo]'s unwrap chain via
	// UnwrapDiagnosticExtra below, never by naming InstanceFailure.inner
	// itself.
	inner interface{}
}

// UnwrapDiagnosticExtra exposes whatever Extra value this diagnostic
// carried before InstanceFailure was attached, so a caller reading for a
// different type - [configs.RefusedReference], for instance - still finds
// it. See [tfdiags.ExtraInfoNext].
func (f InstanceFailure) UnwrapDiagnosticExtra() interface{} { return f.inner }
