// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/policy"
)

// checkLivePolicy validates the optional policy block nested in a live
// block, GitHub issue #67's configurable ownership-policy matrix: every
// quadrant verb an author wrote has to be one this fork considers safe for
// that quadrant, and a quadrant explicitly assigned "delete" has to carry a
// scope block, so an unscoped account-wide purge is a lint refusal rather
// than a default.
//
// Live blocks only ever decode on the root module (RuleChildModule already
// refuses child modules under live resource markers before this rule would
// matter), so mod.Live is nil for every module this reaches except the
// root - the same shape checkStateBackends and the other whole-module
// checks already assume.
func checkLivePolicy(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if mod.Live == nil || mod.Live.Policy == nil {
		return
	}
	p := mod.Live.Policy

	for _, q := range []struct {
		quadrant policy.Quadrant
		verb     string
		set      bool
		rng      hcl.Range
	}{
		{policy.DeclaredTagged, p.DeclaredTagged, p.DeclaredTaggedSet, p.DeclaredTaggedRange},
		{policy.DeclaredUntagged, p.DeclaredUntagged, p.DeclaredUntaggedSet, p.DeclaredUntaggedRange},
		{policy.UndeclaredTagged, p.UndeclaredTagged, p.UndeclaredTaggedSet, p.UndeclaredTaggedRange},
		{policy.UndeclaredUntagged, p.UndeclaredUntagged, p.UndeclaredUntaggedSet, p.UndeclaredUntaggedRange},
	} {
		if !q.set {
			// An omitted quadrant resolves to internal/live/policy.DefaultVerb,
			// today's fixed behavior, which is valid for its quadrant by
			// construction - see issue #67's "existing estates change
			// nothing". Nothing to check, and nothing here that needs a
			// scope block: the preset's own delete quadrant (undeclared+
			// tagged) is today's unscoped sweep, unchanged by this rule.
			continue
		}

		verb := policy.Verb(q.verb)
		if !policy.Valid(q.quadrant, verb) {
			*issues = append(*issues, Issue{
				Rule:      RulePolicyVerb,
				Construct: fmt.Sprintf("policy.%s = %q", q.quadrant.Attribute(), q.verb),
				Module:    path,
				Detail: fmt.Sprintf(
					"%q is not a valid verb for the %s quadrant. Valid verbs there: %s.",
					q.verb, q.quadrant, policy.ValidVerbNames(q.quadrant),
				),
				Subject: q.rng,
			})
			continue // No scope rule to check against a verb this fork does not recognize there.
		}

		// Only undeclared+untagged's delete is account reconciliation, and
		// only it needs a scope. undeclared+tagged's delete is the ordinary
		// orphan sweep: those resources carry this estate's own ownership
		// marker, so the marker is the scope, and DefaultVerb assigns delete
		// there anyway.
		//
		// The omitted-quadrant branch above already said exactly this, and
		// this check used to disagree with it whenever the same verb was
		// written out rather than left implicit - so `undeclared_tagged =
		// "delete"`, which is the documented default, was a hard lint error
		// while omitting it was clean. That contradicted the invariant
		// policy/verb.go's DefaultVerb doc states and
		// TestPolicyOmittedIsByteIdenticalToDefaultVerb enforces downstream:
		// a policy block spelling out the four default verbs must produce
		// the same run as no policy block at all. Fixed under #101.
		if verb == policy.Delete && q.quadrant == policy.UndeclaredUntagged && !scopeIsSet(p.Scope) {
			*issues = append(*issues, Issue{
				Rule:      RulePolicyScope,
				Construct: fmt.Sprintf("policy.%s = \"delete\"", q.quadrant.Attribute()),
				Module:    path,
				Detail: fmt.Sprintf(
					"the %s quadrant is assigned \"delete\", which is scoped account reconciliation with "+
						"aws-nuke semantics: it can reach resources this configuration has never named. An "+
						"unscoped account-wide purge is a lint refusal, not a default: add a \"scope\" block "+
						"to the policy naming at least one service, type, or region it may reach. (The other "+
						"delete quadrant, undeclared_tagged, needs no scope - it only ever removes resources "+
						"already carrying this estate's ownership marker.)",
					q.quadrant,
				),
				Subject: p.DeclRange,
			})
		}
	}

	if p.ThresholdSet && p.Threshold <= 0 {
		*issues = append(*issues, Issue{
			Rule:      RulePolicyThreshold,
			Construct: fmt.Sprintf("policy.threshold = %d", p.Threshold),
			Module:    path,
			Detail: "the delete quadrant's first-run threshold must be a positive whole number: it exists " +
				"to be raised deliberately once the roster has been reviewed, not to be set to zero or below",
			Subject: p.ThresholdRange,
		})
	}
}

// scopeIsSet reports whether s actually narrows anything: a scope block
// naming no service, type or region is indistinguishable from no scope
// block at all (see [configs.LivePolicyScope]'s doc comment), so it does
// not satisfy the delete quadrant's scope requirement any more than a
// missing scope block would.
func scopeIsSet(s *configs.LivePolicyScope) bool {
	return s != nil && (len(s.Services) > 0 || len(s.Types) > 0 || len(s.Regions) > 0)
}
