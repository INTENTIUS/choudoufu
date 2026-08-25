// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/providers"
)

// ComponentsFromValue evaluates t.Components against val - a resource
// instance's real, EVALUATED configuration value, exactly what
// NodeAbstractResourceInstance.plan computes into origConfigVal - instead
// of against a static reading of the instance's HCL the way resolveInstance
// (resolve.go) does. This is GitHub issue #388's plan-node seam
// (rfc/20260823-foundation-order-ruling.md, ruling 3): "the identity
// table's Components against real values."
//
// It applies the identical table every ClassConcrete resolution already
// uses, walked in the same order, with the same Literal/Attrs/Block/
// OmitIfAbsent/Default/IdentityAttr/SoleElement rules the static evaluator
// honors for each - the only thing that differs is where a component's
// argument value comes from: read directly off a concrete cty.Value
// instead of resolved through resolveExpr's local-value, variable and
// cross-resource machinery. That is what lets this resolve shapes the
// static evaluator refuses outright: a value assembled by merge()/concat()
// over a local (there is nothing left to fold - the node already
// evaluated it), or an identity argument that reads another resource's
// real, computed attribute rather than one of that resource's declared
// identity attributes (resolve.go's "Not an identity attribute" refusal,
// resolve.go ~line 2916) - by the time the node calls this, that attribute
// is just an ordinary, already-known field of an already-evaluated value.
//
// PerElement and a component naming Cloud are not implemented here: this
// seam has no [CloudContext] (the account and region a plan is running
// against) at the node, and no ratified row this fork resolves at the node
// today needs PerElement's set-canonicalization. A component of either
// shape makes the WHOLE resolution report ok=false - "nothing to say",
// the same answer [ResourceIdentityResolver] gives when found is false -
// never a wrong or a partial identity. So does any value this evaluator is
// not certain of: unknown (not yet known, as opposed to genuinely absent -
// see [Component.OmitIfAbsent]'s own doc comment on telling the two
// apart), or carrying a cty mark (a sensitive argument must never end up in
// an identity string or an import call; that is the marksafe rule applied
// one layer up from an Unmark, not a limitation of this evaluator - see
// internal/live/marksafe).
func ComponentsFromValue(t TypeIdentity, val cty.Value) (importID string, values map[string]string, ok bool) {
	if t.ServerAssigned || t.RecordBacked || len(t.Components) == 0 {
		return "", nil, false
	}
	// Deliberately NOT val.IsWhollyKnown(): that would gate on every
	// attribute in the whole resource, including ones no [Component] ever
	// reads. aws_iam_role_policy is the found case (this file's own
	// TestComponentsFromValueUnrelatedUnknownAttributeIsIgnored and
	// corpus-hongbomiao-labelbox's greenfield stage): role and name (the
	// two components table_generated.go's row actually names) are both
	// known plan-time strings, but policy - jsonencode() over a sibling
	// MODULE's output that itself reads a Computed-only attribute
	// (aws_s3_bucket.id, unknown until create) - is unknown and is not a
	// component at all. componentFromValue below already checks
	// IsWhollyKnown per attribute it actually reads (and per accessed
	// Block value), which is the right scope: an identity-relevant
	// attribute that is not yet known must still refuse (see
	// TestComponentsFromValueUnknownIsNotFound), but an unrelated
	// argument's unknown value must never veto a derivation this row does
	// not need it for.
	if val == cty.NilVal || val.IsNull() || val.IsMarked() {
		return "", nil, false
	}
	if !val.Type().IsObjectType() {
		return "", nil, false
	}

	values = make(map[string]string)
	var sb strings.Builder
	for _, c := range t.Components {
		segment, attrName, rendered, present, hardFail, _ := componentFromValue(c, val)
		if hardFail {
			return "", nil, false
		}
		if !present {
			switch {
			case c.OmitIfAbsent:
				continue
			case c.Default != "":
				segment = c.Literal + c.Default
				if len(c.Attrs) > 0 {
					if a := c.identityAttrFor(c.Attrs[0]); a != "" {
						values[a] = c.Default
					}
				}
			default:
				// Every other absence - including
				// ServerAssignedIfAbsent, which means the provider mints
				// this value at create time and there is nothing in
				// configuration to read - is the same answer the record
				// and marker steps already gave: this evaluator has
				// nothing to add, and the caller falls back exactly as it
				// would for a type this function does not cover at all.
				return "", nil, false
			}
		} else if attrName != "" {
			values[attrName] = rendered
		}
		sb.WriteString(segment)
	}

	importID = sb.String()
	if t.IdentityObjectOnly {
		// Several attributes with no separator between them: there is no
		// such string (GitHub issue #105), so only the identity-object
		// form is offered. See [TypeIdentity.IdentityObjectOnly] and
		// projection's identical convention on [wanted.importID].
		importID = ""
	}
	if importID == "" && len(values) == 0 {
		return "", nil, false
	}
	return importID, values, true
}

// ComponentsUnknown reports whether [ComponentsFromValue] would fail t
// specifically because an attribute one of t.Components needs is not yet
// known, rather than because the value is absent, malformed, marked, or a
// shape this evaluator does not approximate. [ComponentsFromValue] itself
// does not distinguish these - "ok=false" means "nothing to say" for every
// one of those reasons alike, which is right for that function's own
// callers (a wrong guess and no guess must read identically to them). This
// is for a caller with a different question: GitHub issue #388's
// plan-node seam (rfc/20260823-foundation-order-ruling.md, ruling 3, ruling
// 4/#365), deciding whether a config-identified type's own missing source
// is the ambiguous case ruling 4 refuses by default - a real object this
// run simply could not derive the identity of - or a case with no
// ambiguity to refuse over at all: a genuinely new instance whose identity
// argument reads a sibling that has not been applied yet in THIS SAME run
// (a formula over a record-backed resource, [identity.ClassParentDerived]
// in the static evaluator's own terms), so the identity string does not
// exist for anyone - this run, a human, or a duplicate of a real object -
// to have collided with. Stock plans that resource exactly the same way,
// with the same attribute shown "(known after apply)"; there is nothing
// here to check against, so there is nothing to refuse over.
//
// A caller must use this ONLY to decide whether to WITHHOLD the "No
// source" refusal - never as a reason to invent an identity. The instance
// still resolves through the ordinary "found=false, no diagnostic, stock's
// own create behavior applies" path; nothing here writes a marker or an
// import target of its own.
func ComponentsUnknown(t TypeIdentity, val cty.Value) bool {
	if t.ServerAssigned || t.RecordBacked || len(t.Components) == 0 {
		return false
	}
	if val == cty.NilVal || val.IsNull() || val.IsMarked() {
		return false
	}
	if !val.Type().IsObjectType() {
		return false
	}
	for _, c := range t.Components {
		if _, _, _, _, hardFail, unknown := componentFromValue(c, val); hardFail && unknown {
			return true
		}
	}
	return false
}

// ComponentsServerAssignedIfAbsent is [ComponentsUnknown]'s sibling for
// GitHub issue #190's other safe absence: a component whose configuration
// argument is genuinely absent (not merely unknown - see
// [ComponentsUnknown]'s own doc comment on that distinction), AND carries
// [Component.ServerAssignedIfAbsent] - the provider's own Argument
// Reference documenting that IT fills the argument in when configuration
// leaves it blank (aws_iam_role's name, the *_prefix convention). There is
// no configuration value here to have derived a guess from in the first
// place, the identical "no source to be missing" shape a whole-type
// [TypeIdentity.ServerAssigned] row already gets exempted for in
// [projection.NodeResolver.ResolveResourceIdentity] - just discovered one
// component at a time instead of one type at a time.
//
// Unlike [ComponentsUnknown], which may report true from ANY unknown
// component regardless of where [ComponentsFromValue]'s own walk would
// actually have stopped (safe because an unknown value can never collide
// with a real object's identity, whatever else might also be wrong with
// the type), this function mirrors [ComponentsFromValue]'s walk order
// precisely and asks only about the FIRST component the walk would
// actually stop at. A blank ServerAssignedIfAbsent argument earlier in the
// list does not make a later, genuinely ambiguous absence (no
// OmitIfAbsent, no Default, no ServerAssignedIfAbsent) safe to wave
// through - that absence is exactly ruling 4 (#365)'s case, and nothing
// about a DIFFERENT component being provider-assigned changes that.
//
// A caller must use this ONLY to decide whether to WITHHOLD the "No
// source" refusal, the same restriction [ComponentsUnknown]'s own doc
// comment states - never as a reason to invent an identity.
//
// Found via corpus-autoscaling-complete's own greenfield gauntlet stage:
// module.complete.aws_iam_role.this[0] uses the *_prefix convention
// (use_name_prefix defaults to true in the upstream module), so its "name"
// component is a known null, not unknown - [ComponentsUnknown] alone does
// not cover this shape. (An earlier version of this comment also named
// aws_sqs_queue.this here; it is NOT actually reached by this function -
// see [ComponentsCloudPending]'s own doc comment for why: its row's region
// component hard-fails this walk before name is ever reached, regardless
// of name_prefix.)
func ComponentsServerAssignedIfAbsent(t TypeIdentity, val cty.Value) bool {
	if t.ServerAssigned || t.RecordBacked || len(t.Components) == 0 {
		return false
	}
	if val == cty.NilVal || val.IsNull() || val.IsMarked() {
		return false
	}
	if !val.Type().IsObjectType() {
		return false
	}
	for _, c := range t.Components {
		_, _, _, present, hardFail, _ := componentFromValue(c, val)
		if hardFail {
			// Some other reason - marked, PerElement/Cloud, a SoleElement
			// mismatch, or an unknown value ([ComponentsUnknown]'s own
			// business) - is what [ComponentsFromValue]'s walk would
			// actually have stopped on here, whatever a later component in
			// the list might otherwise look like.
			return false
		}
		if !present {
			if c.OmitIfAbsent || c.Default != "" {
				continue
			}
			return c.ServerAssignedIfAbsent
		}
	}
	return false
}

// ComponentsCloudPending is [ComponentsUnknown] and
// [ComponentsServerAssignedIfAbsent]'s third sibling: it reports whether
// [ComponentsFromValue]'s walk stops, for THIS instance, at a component
// naming [CloudContext] (region or account-id) - not because the value is
// wrong, absent, or ambiguous, but because this seam has no CloudContext at
// all ([ComponentsFromValue]'s own doc comment) and never will for a plan
// node: unlike the static evaluator's resolver (resolve.go's
// cloudValueFor), which can answer CloudRegion from a resource's own
// `region` argument or its provider block, and answers CloudAccountID from
// a caller-supplied [CloudContext] that this fork's own pipeline always
// passes as the zero value (see [CloudContext]'s doc comment: "the account
// ID first becomes knowable one phase later still"), this evaluator
// implements neither at all ([componentFromValue] hard-fails on ANY
// Component.Cloud unconditionally, before even looking at val).
//
// That makes a config-identified row naming Cloud structurally unlike every
// other config-identified row: it is not that THIS instance's derivation
// failed unusually (ruling 4 (#365)'s real ambiguity, which the "No source"
// refusal exists for) - no instance of the type could EVER succeed through
// this evaluator, applied or not, real or genuinely new. An already-live
// object of such a type is never at risk from this exemption: its marker
// carries the estate's tag and the discovery sweep - step (b) in
// noderesolver.go, which runs before step (c) ever reaches this function -
// finds it first, exactly the same protection a whole-type ServerAssigned
// row already relies on for the identical reason (EC2's instance ID is also
// never derivable from configuration, at the node or anywhere in this
// evaluator, and that row's brand-new instances are never refused either).
// A genuinely new instance has nothing to have collided with, and this is
// that, not a widened create.
//
// Found via corpus-sqs-basic's own greenfield gauntlet stage:
// aws_sqs_queue's row builds its ARN-shaped url from
// "https://sqs." + {Cloud: "region"} + ".amazonaws.com/" + {Cloud:
// "account-id"} + "/" + name, so EVERY instance - name present, absent, or
// name_prefix's ServerAssignedIfAbsent null alike - hits the region
// component's unconditional Cloud hard-fail before the walk ever reaches
// name. corpus-autoscaling-complete's own sqs queue was never actually
// exempted by [ComponentsServerAssignedIfAbsent] the way its doc comment
// once claimed (that function's own walk stops at the SAME region
// component, before name, regardless of name_prefix) - it happened not to
// regress only because an earlier, less complete aws_sqs_queue row had no
// Cloud component at all; the row grew the two Cloud components as part of
// the schema-first table effort, and this function is what the node-side
// walk needed to still treat the type as "no source to be missing" rather
// than "no source when there should be one."
//
// PerElement is deliberately excluded from this exemption for the same
// reason [ComponentsFromValue]'s own doc comment gives for not implementing
// it here at all: no ratified row this fork resolves at the node today
// needs its set-canonicalization, so there is no evidence yet that a
// PerElement hard-fail is this same "structurally never derivable" shape
// rather than a genuine gap this evaluator should eventually close instead
// of paper over.
//
// A caller must use this ONLY to decide whether to WITHHOLD the "No
// source" refusal, the same restriction [ComponentsUnknown] and
// [ComponentsServerAssignedIfAbsent] both state - never as a reason to
// invent an identity.
func ComponentsCloudPending(t TypeIdentity, val cty.Value) bool {
	if t.ServerAssigned || t.RecordBacked || len(t.Components) == 0 {
		return false
	}
	if val == cty.NilVal || val.IsNull() || val.IsMarked() {
		return false
	}
	if !val.Type().IsObjectType() {
		return false
	}
	for _, c := range t.Components {
		_, _, _, present, hardFail, _ := componentFromValue(c, val)
		if hardFail {
			// Mirrors [ComponentsServerAssignedIfAbsent]'s own precision:
			// the walk stops HERE, at whatever this component turns out to
			// be. Only a Cloud component (never PerElement, see above) is
			// this function's business; every other hard-fail reason
			// (marked, unknown, a SoleElement mismatch, a value that will
			// not convert to string) is a real problem with THIS instance's
			// data, not a structural gap in the evaluator, and must not be
			// waved through.
			return c.Cloud != CloudNone && !c.PerElement
		}
		if !present {
			if c.OmitIfAbsent || c.Default != "" {
				continue
			}
			// A genuinely ambiguous absence reached before any Cloud
			// component - ruling 4's own real case - must not be masked by
			// a Cloud component sitting later in the list.
			return false
		}
	}
	return false
}

// componentFromValue resolves one component's contribution against val,
// which is either the instance's whole configuration value or (when a
// caller nested into a Block) that block's one element.
//
// present is false when the component names Attrs and none of them was set
// in val; segment/attrName/rendered are meaningless in that case; the
// caller decides absence's meaning (OmitIfAbsent, Default, or refuse).
// hardFail is true for a shape this evaluator will not approximate -
// PerElement, Cloud, an unknown or marked value, a SoleElement collection
// with anything but one element - and the caller treats it exactly like
// the static evaluator's own outright refusal: the whole resolution is
// "not found", never a partial or guessed one.
//
// unknown is only meaningful when hardFail is also true, and narrows WHY:
// true means the attribute this component needed is not yet known - it
// will exist once whatever it depends on is applied, which is not this
// resolution's business to guess at - as opposed to every other hardFail
// reason (PerElement/Cloud, a marked value, a SoleElement collection of
// the wrong length, a value that will not convert to string), which mean
// the value IS there and this evaluator still cannot use it. See
// [ComponentsUnknown]'s own doc comment for what the caller does with the
// distinction.
func componentFromValue(c Component, val cty.Value) (segment, attrName, rendered string, present, hardFail, unknown bool) {
	if c.PerElement || c.Cloud != CloudNone {
		return "", "", "", false, true, false
	}
	if len(c.Attrs) == 0 {
		// A pure literal: Literal is the component's whole contribution
		// (see [Component.Literal]), and it supplies no identity
		// attribute of its own.
		return c.Literal, "", "", true, false, false
	}

	source := val
	if c.Block != "" {
		if !source.Type().IsObjectType() || !source.Type().HasAttribute(c.Block) {
			return "", "", "", false, false, false
		}
		blockVal := source.GetAttr(c.Block)
		if blockVal.IsNull() || !blockVal.IsWhollyKnown() || blockVal.IsMarked() {
			return "", "", "", false, false, false
		}
		if !blockVal.CanIterateElements() || blockVal.LengthInt() == 0 {
			// A genuinely optional nested block the configuration left
			// out. Whether that means "absent" or "refuse" is the same
			// OmitIfAbsent/Default/hard-refuse decision every other
			// absent component gets, made by the caller.
			return "", "", "", false, false, false
		}
		source = blockVal.Index(cty.NumberIntVal(0))
		if !source.Type().IsObjectType() {
			return "", "", "", false, true, false
		}
	}

	for _, name := range c.Attrs {
		if !source.Type().IsObjectType() || !source.Type().HasAttribute(name) {
			continue
		}
		attrVal := source.GetAttr(name)
		if attrVal.IsNull() {
			continue
		}
		if !attrVal.IsWhollyKnown() {
			// Not absent - not yet known. Not a fact this evaluator may
			// treat as "the argument was omitted."
			return "", "", "", false, true, true
		}
		// Guards attrVal for every mark-unsafe cty method this function
		// calls on it below - LengthInt/ElementIterator inside the
		// SoleElement branch, and (on the branch that never reassigns
		// attrVal) the convert.Convert/AsString pair at the bottom. See
		// internal/live/marksafe, which proves every call site of a
		// mark-unsafe cty method: a marked value must refuse here, never
		// flow into an identity component or a cloud-facing import call.
		if attrVal.IsMarked() {
			return "", "", "", false, true, false
		}
		if c.SoleElement {
			t := attrVal.Type()
			if t.IsListType() || t.IsSetType() || t.IsTupleType() {
				if attrVal.LengthInt() != 1 {
					return "", "", "", false, true, false
				}
				var only cty.Value
				for it := attrVal.ElementIterator(); it.Next(); {
					_, only = it.Element()
				}
				attrVal = only
				if !attrVal.IsWhollyKnown() {
					return "", "", "", false, true, true
				}
			}
		}
		// attrVal may have just been reassigned to the narrowed element
		// above, which voids the guard at the top of this loop for
		// whatever attrVal now holds - so a second, identical guard, at
		// THIS block's level (not nested inside the SoleElement branch),
		// immediately ahead of the convert.Convert/AsString pair below.
		// See internal/live/marksafe's own doc comment on span rules: a
		// guard nested inside an inner block only proves its subject to
		// the end of THAT block, never past it.
		if attrVal.IsMarked() {
			return "", "", "", false, true, false
		}
		str, err := convert.Convert(attrVal, cty.String)
		if err != nil {
			return "", "", "", false, true, false
		}
		rendered = str.AsString()
		return c.Literal + rendered, c.identityAttrFor(name), rendered, true, false, false
	}
	return "", "", "", false, false, false
}

// SensitiveComponentsAttr names the first attribute t.Components would read
// off a real object that the provider's schema marks Sensitive and does not
// also mark Deprecated, or "" when none is. It is [sensitiveIdentityAttr]'s
// same question - the evidence rule is [credentialMaterial]'s, deliberately:
// Sensitive minus Deprecated, scoped only to the attributes a record would
// actually hold - asked of a ratified [TypeIdentity.Components] chain
// instead of a [LocatedIdentityPlan], for [ComponentsFromValue]'s own
// callers: a record derived straight from the table the static evaluator
// and GitHub issue #388's plan-node seam already use, rather than from
// [LocatedIdentityPlanFor]'s narrower wire-schema/documented-import-ID
// plan, still must never carry a secret.
//
// A component's Attrs are read from the resource's own top-level schema, or
// - when Block names one - from that nested block's own attributes, the
// same two places [componentFromValue] reads the runtime value from. A
// component naming Cloud or PerElement contributes no schema attribute at
// all and is skipped; [ComponentsFromValue] already refuses those shapes
// outright regardless of sensitivity.
func SensitiveComponentsAttr(t TypeIdentity, schema providers.Schema) string {
	if schema.Block == nil {
		return ""
	}
	for _, c := range t.Components {
		if len(c.Attrs) == 0 {
			continue
		}
		block := schema.Block
		if c.Block != "" {
			nb, ok := schema.Block.BlockTypes[c.Block]
			if !ok || nb == nil {
				continue
			}
			block = &nb.Block
		}
		for _, name := range c.Attrs {
			a := block.Attributes[name]
			if a != nil && a.Sensitive && !a.Deprecated {
				return name
			}
		}
	}
	return ""
}
