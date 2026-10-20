// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// checkLivePolicy validates the optional policy block nested in a live
// block, GitHub issue #67's configurable ownership-policy matrix: every
// quadrant verb an author wrote has to be one this fork considers safe for
// that quadrant, and undeclared_untagged = "delete" has to carry a scope
// block, so an unscoped account-wide purge is a lint refusal rather than a
// default.
//
// Only that one quadrant. This used to say "a quadrant explicitly assigned
// delete", which was both what the code did and wrong: undeclared_tagged's
// default IS delete, so writing the documented default by hand was a lint
// error while omitting it was clean, and a scope block added to satisfy the
// refusal would have been ignored (reconcile.go is Scope's only consumer,
// and it runs only for undeclared_untagged). See #101.
//
// A live block DOES decode in a child module - configs.Module.Live is set
// per-module (internal/configs/module.go) and child modules are admitted
// since issue #59, so the old claim that RuleChildModule refuses them first
// is false. What is true is that nothing outside the root's block is acted
// on: liveSettings reads the root module's Live only, and
// recordStoreConfiguredIn is documented as read once from the root and
// threaded. So this rule still fires on whatever it finds, which is the
// conservative direction, but a policy block in a child module is enforced
// while its record_store is ignored. The silent half is tracked with the
// other child-module silent-ignores on issue #104.
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

	// <= 0 rather than == 0 defensively only: internal/configs' decoder
	// refuses a negative threshold and leaves ThresholdSet false, so zero is
	// the only value that reaches here. The message says "zero" rather than
	// "zero or below" for that reason - an audit pointed out it was
	// describing a value no configuration can deliver.
	if p.ThresholdSet && p.Threshold <= 0 {
		*issues = append(*issues, Issue{
			Rule:      RulePolicyThreshold,
			Construct: fmt.Sprintf("policy.threshold = %d", p.Threshold),
			Module:    path,
			Detail: "the delete quadrant's first-run threshold must be a positive whole number: it exists " +
				"to be raised deliberately once the roster has been reviewed, and zero reviews nothing",
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

// checkPolicyOverwriteAtKey is GitHub issue #1120's lint half of #1546's
// refusal, for the opposite server. A policy block may assign
// declared_untagged "keep" or "report" ([policy.LeavesCreateStanding]):
// quieter refusals that leave a declared object found without this
// estate's marker out of the estate while the plan still proposes the
// resource's create. On Kubernetes the API server answers that create with
// 409 AlreadyExists, so the softer verbs cost nothing and stay accepted.
// On a substrate whose create at an occupied id updates the object there
// ([substrate.CreateOverwritesAtKey]: Azure, where an ARM create is a PUT)
// the same create is a silent takeover of the unmarked object. So a
// configuration declaring any resource of such a family is refused here,
// before any plan, by name. internal/live/projection refuses the object
// itself under refuse, keep and report, for a run that never linted;
// adopt and converge admit it first, so they never reach that refusal.
//
// Whole configuration: the policy block governs the estate, not one block,
// and the resource it names is the first such one in address order, as
// evidence that the estate has one. cfg is the node being visited; the
// resources are read from its root, because a policy block anywhere governs
// resources everywhere.
func checkPolicyOverwriteAtKey(cfg *configs.Config, path addrs.Module, issues *[]Issue) {
	mod := cfg.Module
	if mod.Live == nil || mod.Live.Policy == nil || !mod.Live.Policy.DeclaredUntaggedSet {
		return
	}
	p := mod.Live.Policy
	verb := policy.Verb(p.DeclaredUntagged)
	if !policy.Valid(policy.DeclaredUntagged, verb) || !policy.LeavesCreateStanding(verb) {
		// An invalid verb is policy-verb's refusal; only a valid one that
		// leaves the create standing is this rule's.
		return
	}
	root := cfg.Root
	if root == nil {
		root = cfg
	}
	addr, providerType, ok := firstOverwritingResource(root)
	if !ok {
		return
	}
	attr := policy.DeclaredUntagged.Attribute()
	*issues = append(*issues, Issue{
		Rule:      RulePolicyOverwriteAtKey,
		Construct: fmt.Sprintf("policy.%s = %q", attr, verb),
		Module:    path,
		Detail: fmt.Sprintf(
			"the %s quadrant is assigned %q, and this configuration declares %s, through provider %s. Creating it is a PUT to "+
				"the id its block declares, and a PUT to an id that already exists updates the object there instead of "+
				"failing. %q leaves a declared object found at that id without this estate's %s tag out of the estate and "+
				"lets the plan propose the create, so the apply would take that object over with nothing to say it was ever "+
				"this estate's. On Kubernetes the API server refuses that create with 409 AlreadyExists, which is why the "+
				"verb is accepted there (GitHub issue #1546); for this resource nothing refuses it (GitHub issue #1120). "+
				"Remove the assignment to keep the default, \"refuse\", which stops such a plan by name; set it to "+
				"\"adopt\" to take such objects over with this estate's markers written in the open; or point the "+
				"resource at a name nobody is using.",
			attr, verb, addr, providerType, verb, markers.TagEstate,
		),
		Subject: p.DeclaredUntaggedRange,
	})
}

// firstOverwritingResource is the first managed resource, in address order
// over the whole tree under root, whose provider's family answers
// [substrate.CreateOverwritesAtKey] for one of its surfaces. Read off the
// family the provider type belongs to, never off a type name.
func firstOverwritingResource(root *configs.Config) (string, string, bool) {
	var found []string
	byAddr := map[string]string{}
	root.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		for _, rc := range c.Module.ManagedResources {
			sub, ok := substrate.ForProvider(rc.Provider.Type)
			if !ok || !familyOverwritesAtKey(sub) {
				continue
			}
			a := rc.Addr().InModule(c.Path).String()
			found = append(found, a)
			byAddr[a] = rc.Provider.Type
		}
	})
	if len(found) == 0 {
		return "", "", false
	}
	sort.Strings(found)
	return found[0], byAddr[found[0]], true
}

// familyOverwritesAtKey reports whether any of sub's surfaces answers
// [substrate.CreateOverwritesAtKey].
func familyOverwritesAtKey(sub substrate.Substrate) bool {
	for _, surface := range sub.Surfaces() {
		if sub.CreateOverwritesAtKey(surface) {
			return true
		}
	}
	return false
}
