// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
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
	if val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return "", nil, false
	}
	if !val.Type().IsObjectType() {
		return "", nil, false
	}

	values = make(map[string]string)
	var sb strings.Builder
	for _, c := range t.Components {
		segment, attrName, rendered, present, hardFail := componentFromValue(c, val)
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
func componentFromValue(c Component, val cty.Value) (segment, attrName, rendered string, present, hardFail bool) {
	if c.PerElement || c.Cloud != CloudNone {
		return "", "", "", false, true
	}
	if len(c.Attrs) == 0 {
		// A pure literal: Literal is the component's whole contribution
		// (see [Component.Literal]), and it supplies no identity
		// attribute of its own.
		return c.Literal, "", "", true, false
	}

	source := val
	if c.Block != "" {
		if !source.Type().IsObjectType() || !source.Type().HasAttribute(c.Block) {
			return "", "", "", false, false
		}
		blockVal := source.GetAttr(c.Block)
		if blockVal.IsNull() || !blockVal.IsWhollyKnown() || blockVal.IsMarked() {
			return "", "", "", false, false
		}
		if !blockVal.CanIterateElements() || blockVal.LengthInt() == 0 {
			// A genuinely optional nested block the configuration left
			// out. Whether that means "absent" or "refuse" is the same
			// OmitIfAbsent/Default/hard-refuse decision every other
			// absent component gets, made by the caller.
			return "", "", "", false, false
		}
		source = blockVal.Index(cty.NumberIntVal(0))
		if !source.Type().IsObjectType() {
			return "", "", "", false, true
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
		if !attrVal.IsWhollyKnown() || attrVal.IsMarked() {
			// Not absent - not yet known, or deliberately hidden. Neither
			// is a fact this evaluator may treat as "the argument was
			// omitted."
			return "", "", "", false, true
		}
		if c.SoleElement {
			t := attrVal.Type()
			if t.IsListType() || t.IsSetType() || t.IsTupleType() {
				if attrVal.LengthInt() != 1 {
					return "", "", "", false, true
				}
				var only cty.Value
				for it := attrVal.ElementIterator(); it.Next(); {
					_, only = it.Element()
				}
				attrVal = only
				if !attrVal.IsWhollyKnown() || attrVal.IsMarked() {
					return "", "", "", false, true
				}
			}
		}
		str, err := convert.Convert(attrVal, cty.String)
		if err != nil {
			return "", "", "", false, true
		}
		rendered = str.AsString()
		return c.Literal + rendered, c.identityAttrFor(name), rendered, true, false
	}
	return "", "", "", false, false
}
