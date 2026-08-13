// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/tfdiags"
	residue "github.com/intentius/choudoufu/live"
)

// ReconcileRequest is one scoped account-reconciliation pass: GitHub issue
// #67's undeclared_untagged = "delete" quadrant, aws-nuke semantics.
//
// It is a separate entry point from [Discover] on purpose. Reconciliation
// looks for live resources this estate's ordinary discovery never lists at
// all - types nothing in the configuration declares and the sweep only
// checks for this estate's own markers on - so it needs its own, narrower
// scan, bounded by the policy's scope block rather than by the admission
// table wholesale. Keeping it out of [Discover]'s own scan loop is also
// what keeps every existing caller of [Discover] byte-identical: this only
// runs when a caller asks for it, which only happens when a policy block
// explicitly set undeclared_untagged to "delete".
type ReconcileRequest struct {
	// Estate is this estate's name. A resource carrying ANY tofu-estate
	// value - this estate's or another's - is never a candidate: account
	// reconciliation reaches only for resources no estate has ever claimed.
	Estate string

	// Provider is a configured provider handle, the same shape
	// [Request.Provider] takes.
	Provider any

	// Region is the region to list in, and the region a scope's Regions list
	// is checked against. Empty leaves the provider's own configured region
	// to decide, and a scope naming regions is checked against "" and never
	// matches - a scoped delete with a region list needs a region to run in.
	Region string

	// Policy is the resolved policy. Must carry a non-nil Scope on its
	// UndeclaredUntagged verb - internal/live/lint refuses a delete
	// quadrant with no scope block, and this function refuses defensively
	// too rather than trust that a caller always ran lint first.
	Policy *policy.Policy
}

// ReconcileCandidate is one live resource the scoped reconciliation pass
// would delete: identity evidence, rendered individually, per issue #67's
// "no aggregate N-resources-will-be-deleted without the roster".
type ReconcileCandidate struct {
	TypeName    string
	ImportID    string
	DisplayName string
	Tags        map[string]string
	Identity    cty.Value

	// Addr is the synthetic address this candidate enters the projection
	// at, minted from its type and import ID because it has no
	// configuration and no tofu-address marker to read one from. See
	// [syntheticReconcileAddr].
	Addr addrs.AbsResourceInstance
}

// String renders a candidate on one line.
func (c ReconcileCandidate) String() string {
	return c.TypeName + " " + c.ImportID
}

// ReconcileGap is one scope-selected type the pass could not speak for:
// not enumerable (no native list resource), or a list call that failed.
// Issue #67 is explicit that "delete" must never imply the account is
// clean, so a gap is surfaced with the same weight as a found candidate
// rather than silently narrowing the roster.
type ReconcileGap struct {
	TypeName string
	Reason   string
	Detail   string
}

// ReconcileResult is what one scoped reconciliation pass found.
type ReconcileResult struct {
	// Roster is every deletion candidate, in type and identity order.
	Roster []ReconcileCandidate

	// Gaps is every scope-selected type this pass could not enumerate.
	Gaps []ReconcileGap

	// Threshold is the guard this roster was checked against -
	// [policy.Policy.EffectiveThreshold].
	Threshold int

	// ThresholdExceeded is true when len(Roster) > Threshold: issue #67's
	// first-run protection. The roster is still populated when this is
	// true, so a caller can show it in the same refusal that names the
	// threshold - "review the roster, raise the threshold deliberately" -
	// rather than a blind count.
	ThresholdExceeded bool
}

// Reconcile runs one scoped account-reconciliation pass: every admitted,
// enumerable resource type the policy's scope allows, listed in full (not
// filtered server-side, because the whole point is finding what carries no
// estate marker at all), with anything already claimed by any estate
// excluded and anything carrying the policy's preservation tag excluded -
// the "not in my source, no [policy] tag" half of the maintainer's
// motivating matrix.
//
// It never touches [Result.Orphans] or [Result.Resolutions] itself; the
// caller (internal/command) merges [ReconcileResult.Roster] into the
// resolution list the same way a swept orphan enters it, once the
// threshold guard has been checked.
func Reconcile(ctx context.Context, req ReconcileRequest) (*ReconcileResult, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	res := &ReconcileResult{}

	switch {
	case !ValidEstateName(req.Estate):
		return res, diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("Scoped account reconciliation needs the estate's name, matching the tofu-estate marker grammar. Got %q.", req.Estate)))
	case req.Provider == nil:
		return res, diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"No provider access",
			"Scoped account reconciliation needs a configured provider handle to list live resources with, and none was given."))
	case req.Policy == nil || req.Policy.Scope == nil || !scopeSet(req.Policy.Scope):
		// Defense in depth: internal/live/lint already refuses a delete
		// quadrant with no scope block before a configuration reaches this
		// far. Reaching here anyway must never fall back to an unscoped
		// account-wide purge, so it refuses outright instead.
		return res, diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"Unscoped account reconciliation refused",
			"undeclared_untagged = \"delete\" is scoped account reconciliation and requires a policy scope block naming at least one service, type, or region. internal/live/lint should have refused this configuration before it reached discovery; running with no scope is refused here too rather than falling back to an unscoped purge."))
	}
	res.Threshold = req.Policy.EffectiveThreshold()

	schemas, schemaDiags := listclient.ListSchemas(ctx, req.Provider)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return res, diags
	}

	types := reconcileTypeUniverse(req.Policy.Scope)
	sort.Strings(types)

	for _, typeName := range types {
		if !req.Policy.Scope.Allows(typeName, reconcileService(typeName), req.Region) {
			continue
		}
		candDiags := reconcileType(ctx, req, schemas, typeName, res)
		diags = diags.Append(candDiags)
	}

	sort.Slice(res.Roster, func(i, j int) bool {
		if res.Roster[i].TypeName != res.Roster[j].TypeName {
			return res.Roster[i].TypeName < res.Roster[j].TypeName
		}
		return res.Roster[i].ImportID < res.Roster[j].ImportID
	})
	sort.Slice(res.Gaps, func(i, j int) bool { return res.Gaps[i].TypeName < res.Gaps[j].TypeName })

	res.ThresholdExceeded = len(res.Roster) > res.Threshold
	return res, diags
}

// reconcileTypeUniverse is the candidate type list before the per-type
// Scope.Allows narrowing: the scope's own explicit Types when it named any,
// or every admitted type otherwise (narrowed to the scope's Services and
// Regions in the caller's loop, per type, the same as an explicit Types
// list would be). Admitted is the only universe reconciliation ever
// considers - issue #67's "only types that are BOTH admitted and
// enumerable are deletable" - enumerable is decided per type in
// [reconcileType].
func reconcileTypeUniverse(scope *policy.Scope) []string {
	if len(scope.Types) > 0 {
		out := make([]string, 0, len(scope.Types))
		admitted := make(map[string]bool)
		for _, t := range identity.AdmittedTypes() {
			admitted[t] = true
		}
		for _, t := range scope.Types {
			if admitted[t] {
				out = append(out, t)
			}
		}
		return out
	}
	return append([]string(nil), identity.AdmittedTypes()...)
}

// reconcileService is the provider service namespace one type belongs to,
// for matching against a scope's Services list - the botocore-derived join
// [foreign.adoptionHint] already uses for the same purpose ("what does the
// AWS CLI call this"), reused rather than re-derived. "" when the artifact
// names no service for the type, which never matches a populated Services
// list - see [policy.Scope.Allows].
func reconcileService(typeName string) string {
	tv, ok := residue.TagVerbForType(typeName)
	if !ok {
		return ""
	}
	return tv.CLIService
}

// scopeSet mirrors internal/live/lint's scopeIsSet: a scope block naming no
// service, type or region does not satisfy the delete quadrant's scope
// requirement any more than a missing scope block would.
func scopeSet(s *policy.Scope) bool {
	return s != nil && (len(s.Services) > 0 || len(s.Types) > 0 || len(s.Regions) > 0)
}

// reconcileType lists one scope-selected type in full and files every
// candidate: untagged by any estate, and not protected by the policy's own
// preservation tag.
func reconcileType(ctx context.Context, req ReconcileRequest, schemas listclient.Schemas, typeName string, res *ReconcileResult) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	ts, ok := schemas.Get(typeName)
	if !ok {
		res.Gaps = append(res.Gaps, ReconcileGap{
			TypeName: typeName,
			Reason:   "NOT_ENUMERABLE",
			Detail: fmt.Sprintf(
				"The provider has no native list resource for %s, so this scoped reconciliation pass could not enumerate it. A %s of this type could exist and would not appear on the roster - \"delete\" never implies the account is clean.",
				typeName, typeName),
		})
		return diags
	}
	if !markerCapable(ts) {
		// Untaggable types can never carry the policy tag or any estate
		// marker, so they can never be classified by this quadrant's own
		// rule at all - there is nothing to read to tell "protected" from
		// "not". Reported as a gap for the same reason an unlistable type
		// is: silence here would read as "nothing found" rather than
		// "nothing this pass could look at".
		res.Gaps = append(res.Gaps, ReconcileGap{
			TypeName: typeName,
			Reason:   "NOT_TAGGABLE",
			Detail: fmt.Sprintf("%s carries no tags, so it can carry no ownership or preservation marker and this pass has nothing to judge it by.", typeName),
		})
		return diags
	}

	vals := make(map[string]cty.Value)
	if hasAttr(ts.Config, "region") && req.Region != "" {
		vals["region"] = cty.StringVal(req.Region)
	}
	config, cfgDiags := ts.BuildConfig(vals)
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		res.Gaps = append(res.Gaps, ReconcileGap{
			TypeName: typeName, Reason: "LIST_CONFIG_FAILED",
			Detail: fmt.Sprintf("The list configuration for %s could not be built from the provider's schema.", typeName),
		})
		return diags
	}

	results, listDiags := listclient.List(ctx, req.Provider, typeName, config, true)
	if listDiags.HasErrors() {
		res.Gaps = append(res.Gaps, ReconcileGap{
			TypeName: typeName, Reason: "LIST_FAILED",
			Detail: fmt.Sprintf("Listing %s failed: %s.", typeName, listDiags.Err()),
		})
		return diags
	}
	diags = diags.Append(listDiags)

	for _, r := range results {
		tags, taggable := markers.TagsOf(r.Resource)
		if !taggable {
			continue
		}
		if tags[TagEstate] != "" {
			// Claimed by some estate - this one or another's. Account
			// reconciliation reaches only for resources no estate has ever
			// claimed; an estate's own removal is the sweep's business
			// (undeclared_tagged), never this quadrant's.
			continue
		}
		if req.Policy.TagMatches(tags) {
			// Protected: carries the policy's own preservation tag. This is
			// the maintainer's motivating example's other half - "tagged
			// strangers protected, untagged strangers purged".
			continue
		}
		importID, _, hasID := importIdentity(typeName, r)
		if !hasID {
			continue
		}
		res.Roster = append(res.Roster, ReconcileCandidate{
			TypeName:    typeName,
			ImportID:    importID,
			DisplayName: r.DisplayName,
			Tags:        tags,
			Identity:    r.Identity,
			Addr:        syntheticReconcileAddr(typeName, importID),
		})
	}

	return diags
}

// syntheticReconcileAddr is the address one reconciliation candidate enters
// the projection at, minted the same way [syntheticChildAddr] mints one for
// a parent-read finding: the candidate has no configuration and no
// tofu-address marker (it is untagged by definition), so there is no
// recorded address to recover, only a label to mint. Two candidates of one
// type never collide here because a live import ID is unique within its
// type.
func syntheticReconcileAddr(typeName, importID string) addrs.AbsResourceInstance {
	return addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: typeName,
		Name: "reconcile_" + sanitizeResourceName(importID),
	}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

// sanitizeResourceName turns an arbitrary live import ID into a string that
// is always a legal HCL resource name: a letter or underscore, then letters,
// digits, underscores or hyphens. An import ID can be an ARN, a colon- or
// slash-delimited composite identifier, or anything else a provider chooses
// to hand back, none of which is a resource name's grammar, so every
// character outside it becomes "_" rather than being carried through
// unescaped.
func sanitizeResourceName(id string) string {
	if id == "" {
		return "_"
	}
	out := make([]rune, 0, len(id))
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			out = append(out, r)
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				out = append(out, '_')
			}
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
