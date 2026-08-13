// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
)

// applyOrphanPolicy governs the undeclared_tagged quadrant: what happens to
// an owned-but-undeclared resource discovery already found, once GitHub
// issue #67's policy block has a say in it.
//
// It runs after [classifyOrphans] and [parentReadSweep] have finished
// deciding which orphans and parent-read findings are removals, because
// policy narrows that decision rather than replacing it - an orphan
// [classifyOrphans] already withheld for a possible rename stays withheld
// regardless of policy, and a resource policy leaves alone still has to have
// been a legitimate removal candidate in the first place.
//
// req.Policy nil is today's fixed behavior with nothing to narrow: every
// removal [classifyOrphans] proposed stays proposed, which is
// [policy.DefaultVerb]'s own reading for the undeclared_tagged quadrant
// (Delete) and is what makes "no policy block" byte-identical to today.
func applyOrphanPolicy(req Request, res *Result) {
	if req.Policy == nil {
		return
	}

	kept := make(map[string]bool)
	for i := range res.Orphans {
		o := &res.Orphans[i]
		if !o.Removal {
			continue
		}
		tagged := req.Policy.TagMatches(o.Tags)
		verb := req.Policy.Verb(false, tagged)
		if verb == policy.DefaultVerb[policy.UndeclaredTagged] {
			// Delete, undeclared_tagged's own default: leave PolicyVerb at
			// its zero value too, so that a policy block naming only
			// default verbs is indistinguishable from no policy block at
			// all - see [TestPolicyOmittedIsByteIdenticalToDefaultVerb].
			continue
		}
		o.Removal = false
		o.PolicyVerb = verb
		o.Withheld = policyWithheldMessage(req.Policy, verb, "undeclared_tagged", o.TypeName, o.Normalized)
		kept[o.Addr.String()] = true
	}

	for i := range res.ParentReads {
		f := &res.ParentReads[i]
		if !f.Removal {
			continue
		}
		// A parent-read finding has no tags of its own on the finding
		// itself - identity.SingleParentComponent types are untaggable by
		// definition, which is the whole reason they are found by reading
		// their parent rather than by marker (see live/LIMITATIONS.md). An
		// untaggable child can never carry TagKey=TagValue, so it is always
		// undeclared_untagged under this policy's tag reading, never
		// undeclared_tagged - and undeclared_untagged's default is Keep, not
        // Delete, which would silently narrow today's fixed behavior for
        // every estate that owns one of these children. So a parent-read
        // removal is left exactly as classifyOrphans and parentReadSweep
        // decided it, regardless of policy, until issue #67's follow-up
        // extends the tag reading to untaggable children.
		_ = f
	}

	if len(kept) == 0 {
		return
	}
	filtered := res.Resolutions[:0]
	for _, r := range res.Resolutions {
		if kept[r.Addr.String()] {
			continue
		}
		filtered = append(filtered, r)
	}
	res.Resolutions = filtered
}

// policyWithheldMessage is the one sentence an operator gets in place of a
// destroy, naming the quadrant and verb that withheld it.
//
// The untag case states the management consequence in plain words only
// when pol.TagKey is the estate marker itself - the maintainer's ruling on
// issue #67's "one semantic question": tag_key defaults to the estate
// marker and may name any tag, and only the estate-marker reading has a
// consequence to state, because only it is the tag marker discovery reads
// to find this resource again. A configured preservation tag distinct from
// the estate marker leaves the ownership marker untouched, so there is
// nothing here for a later run to lose track of.
func policyWithheldMessage(pol *policy.Policy, verb policy.Verb, quadrant, typeName, marker string) string {
	switch verb {
	case policy.Keep:
		return fmt.Sprintf(
			"policy.%s = \"keep\": this estate's ownership marker on this %s protects it from the sweep; it is reported but never planned for destruction",
			quadrant, typeName)
	case policy.Report:
		return fmt.Sprintf(
			"policy.%s = \"report\": this %s is surfaced for review and never planned for destruction",
			quadrant, typeName)
	case policy.Untag:
		consequence := ""
		if pol != nil && pol.TagKey == markers.TagEstate {
			consequence = " This is the estate marker itself: releasing it would leave this resource unmanaged - the next run's marker discovery could no longer find it by this marker."
		}
		return fmt.Sprintf(
			"policy.%s = \"untag\": this run would release %s=%q from this %s (marker %s) rather than destroy it; automatic release for a resource with no declared address is not implemented yet, so the tag is left as it is - see GitHub issue #67's follow-up and release it by hand if this estate no longer wants it.%s",
			quadrant, policyTagKeyOf(pol), policyTagValueOf(pol), typeName, marker, consequence)
	default:
		return fmt.Sprintf("policy.%s = %q withheld this %s from the sweep", quadrant, string(verb), typeName)
	}
}

// policyTagKeyOf and policyTagValueOf read pol's tag key/value defensively:
// pol is never nil when this is called from [applyOrphanPolicy] (the verb
// switch above already dereferenced it to get here), but a direct caller -
// a test, or a future one - should not crash on a nil policy for the sake
// of a log line.
func policyTagKeyOf(pol *policy.Policy) string {
	if pol == nil {
		return markers.TagEstate
	}
	return pol.TagKey
}

func policyTagValueOf(pol *policy.Policy) string {
	if pol == nil {
		return ""
	}
	return pol.TagValue
}
