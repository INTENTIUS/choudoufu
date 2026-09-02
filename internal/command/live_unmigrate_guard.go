// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markerstrip"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// UnmigrateEnvVar is the deliberate-revert escape hatch for
// [statefulMarkerGuard]: the name of the estate whose ownership markers this
// run is allowed to remove, or several separated by commas.
//
// It takes an estate name rather than a bare on/off value on purpose. An
// on/off value set once in a CI environment covers every estate that
// directory ever migrates, including one migrated a year later by someone
// who never saw the setting. A name covers the estate the operator was
// looking at and nothing else.
//
// It is an environment variable rather than a flag because the guard has to
// serve plan, apply and apply-a-saved-plan identically, and a flag on one of
// them is a flag missing from the others.
const UnmigrateEnvVar = "CHOUDOUFU_UNMIGRATE"

// summaryUnmigrateRefused and summaryUnmigrateApproved are the two headlines
// the guard can produce - the refusal, and the warning that replaces it when
// [UnmigrateEnvVar] names the estate.
const (
	summaryUnmigrateRefused  = "Plan would remove this estate's ownership markers"
	summaryUnmigrateApproved = "Removing this estate's ownership markers"
)

// statefulMarkerGuard is the plan guard a STATE-BACKED run installs: the run
// with no live block, where a state file is authoritative and this fork
// behaves as stock does.
//
// GitHub issue #613. Once "choudoufu live-import -approve" has stamped an
// estate, the live resources carry tofu-estate and tofu-address and the
// state file - taken before the stamp - does not. The next state-backed plan
// refreshes, finds tags the configuration does not declare, and proposes
// removing them. Measured against the emulator at two scales: 38 resources
// at 79 instances, 137 at 301. Applying that plan un-migrates the estate,
// and it reads on screen as routine attribute drift.
//
// The guard refuses rather than warning, for the reason
// internal/live/liveimport's notATagsOnlyPlan refuses rather than warning: the
// damaging form of this run is "apply -auto-approve", where there is no
// prompt for a warning to appear before and no operator watching the output.
// A warning would be correct and unread.
//
// It does NOT make a state-backed plan ignore marker tags. The plan is
// computed, rendered in full, and only then refused, so the operator sees
// exactly the drift stock would have shown them and is told what it is.
// Stateful runs stay call-identical to stock: this adds no provider call and
// no request.
//
// Installed only when the configuration has no live block. Under a live
// block the projection supplies the markers on both sides of the comparison,
// so there is nothing here to detect, and installing it there would put a
// new refusal in front of the path the gauntlet measures.
func statefulMarkerGuard() func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics {
	return func(plan *plans.Plan, schemas *tofu.Schemas) tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		if plan == nil || plan.Changes == nil || schemas == nil {
			return diags
		}
		removals := markerstrip.Scan(plan.Changes.Resources, func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema {
			schema, _ := schemas.ResourceTypeConfig(provider, mode, typeName)
			return schema
		})
		if len(removals) > 0 {
			return unmigrateDiagnostics(removals, os.Getenv(UnmigrateEnvVar))
		}
		// GitHub issue #716's breadcrumb: a stock-mode plan building an
		// estate FROM NOTHING whose configured tags already stamp
		// ownership markers. Two situations look exactly like this - a
		// greenfield bootstrap, and a mid-migration directory whose live
		// block is not on yet - and only the second is a trap: applying
		// it builds duplicates beside the live estate. A refusal would
		// break the legitimate bootstrap, so this is a warning, and it
		// fires only when the prior state holds no managed resources at
		// all (adding one tagged resource to a working stock estate stays
		// silent).
		if priorStateHoldsManaged(plan) {
			return diags
		}
		creations := markerstrip.ScanCreates(plan.Changes.Resources, func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema {
			schema, _ := schemas.ResourceTypeConfig(provider, mode, typeName)
			return schema
		})
		if len(creations) == 0 {
			return diags
		}
		return stockCreateWarning(creations)
	}
}

// priorStateHoldsManaged reports whether the plan's prior state contains any
// managed resource instance - the signal that separates "adding resources to
// a working stock estate" (silent) from "building an estate from nothing"
// (the shape that earns [stockCreateWarning]).
func priorStateHoldsManaged(plan *plans.Plan) bool {
	if plan.PriorState == nil {
		return false
	}
	for _, mod := range plan.PriorState.Modules {
		for _, rsc := range mod.Resources {
			if rsc.Addr.Resource.Mode == addrs.ManagedResourceMode && len(rsc.Instances) > 0 {
				return true
			}
		}
	}
	return false
}

// stockCreateWarning is issue #716's operator breadcrumb, one warning per
// estate the planned creates stamp. Wording is a standalone function for the
// same reason [unmigrateDiagnostics] is: testable without a plan, a backend
// or a provider.
func stockCreateWarning(creations []markerstrip.Creation) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	byEstate := make(map[string]int)
	for _, c := range creations {
		byEstate[c.Estate]++
	}
	for _, estate := range markerstrip.CreationEstates(creations) {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"This plan creates resources already stamped with ownership markers",
			fmt.Sprintf(
				"This configuration has no live block, so this is a stock-mode plan - and it proposes creating %s carrying estate %q's ownership markers, from an empty state.\n\n"+
					"If that estate is already live, applying this builds duplicates beside it: the platform refuses the client-named ones, and any server-assigned duplicates surface as named collisions on the next live plan. Mid-migration, the fix is to turn the live block on and re-plan - the estate binds and the plan empties.\n\n"+
					"If this is the estate's first bootstrap, proceed; this warning is a breadcrumb, not a refusal.",
				instancePhrase(byEstate[estate]), estate,
			),
		))
	}
	return diags
}

// unmigrateDiagnostics turns a non-empty set of marker removals into what the
// operator sees, given the raw value of [UnmigrateEnvVar].
//
// It is separate from [statefulMarkerGuard] so that the wording can be tested
// without a plan, a backend or a provider. Every estate the removals name is
// either approved by the variable or refused; an estate the variable does not
// name is refused even when a sibling estate in the same plan is approved,
// because "I meant to revert A" is not consent to revert B.
func unmigrateDiagnostics(removals []markerstrip.Removal, approvedRaw string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	approved := make(map[string]struct{})
	for _, name := range strings.Split(approvedRaw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			approved[name] = struct{}{}
		}
	}

	byEstate := make(map[string][]markerstrip.Removal)
	for _, r := range removals {
		byEstate[r.Estate] = append(byEstate[r.Estate], r)
	}
	estates := markerstrip.Estates(removals)

	for _, estate := range estates {
		group := byEstate[estate]
		if _, ok := approved[estate]; ok {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				summaryUnmigrateApproved,
				fmt.Sprintf(
					"%s names %q, so this run is allowed to remove that estate's ownership markers from %s.\n\n"+
						"After this is applied nothing on those live resources says which configuration owns them, and \"choudoufu live-import -state=PATH -estate=%s -approve\" would have to stamp the estate again to bring it back.",
					UnmigrateEnvVar, estate, instancePhrase(len(group)), estate,
				),
			))
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			summaryUnmigrateRefused,
			unmigrateRefusalDetail(estate, group),
		))
	}
	return diags
}

// unmigrateRefusalDetail is the paragraph the refusal carries. It says what
// the plan does, why the plan itself is not wrong, what applying it costs,
// and the two ways forward - because a refusal that does not name a way
// forward is read as a bug in the tool.
func unmigrateRefusalDetail(estate string, group []markerstrip.Removal) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"This plan removes the %s from %s. Those tags are what \"choudoufu live-import\" wrote when estate %q was migrated onto live resource markers, and they are that estate's whole ownership record - see live/MARKERS.md.\n\n",
		keyPhrase(group), instancePhrase(len(group)), estate)
	b.WriteString(
		"The diff above is correct. This configuration does not declare those tags and the state file has no record of them, so a state-backed run can only read them as drift and can only propose reverting it. Applying it would remove the markers from the live resources: nothing would then say which configuration owns them, a later run under a live block would not find them, and the migration would have to be redone.\n\n")
	b.WriteString(affectedList(group))
	fmt.Fprintf(&b,
		"\nTo keep the estate, run this configuration the way it was migrated - with its live block present, where \"choudoufu plan\" and \"choudoufu apply\" read the markers instead of proposing to remove them.\n\n"+
			"To remove the markers deliberately, set %s=%s and run again. Nothing else distinguishes a deliberate revert from an ordinary plan in a directory whose live block is missing, which is why this run refuses rather than warns.",
		UnmigrateEnvVar, estate)
	return b.String()
}

// affectedList names the instances, capped, because the plan above already
// showed every one of them in full and a refusal that reprints 137 addresses
// buries its own last paragraph.
func affectedList(group []markerstrip.Removal) string {
	const limit = 5
	shown := group
	if len(shown) > limit {
		shown = shown[:limit]
	}
	var b strings.Builder
	if len(group) > len(shown) {
		fmt.Fprintf(&b, "The first %d, by address:\n", len(shown))
	} else {
		b.WriteString("By address:\n")
	}
	for _, r := range shown {
		fmt.Fprintf(&b, "  %s\n", r.Addr.String())
	}
	if len(group) > len(shown) {
		fmt.Fprintf(&b, "  ... and %d more\n", len(group)-len(shown))
	}
	return b.String()
}

// keyPhrase names the marker tag keys the removals actually touch, rather
// than the pair that is usually right. A count instance also carries
// tofu-slot and a long address spans continuation tags, and a refusal that
// named two keys while removing four would be teaching the operator to look
// for the wrong thing.
func keyPhrase(group []markerstrip.Removal) string {
	seen := make(map[string]struct{})
	for _, r := range group {
		for _, k := range r.Keys {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	switch len(keys) {
	case 0:
		return "ownership markers"
	case 1:
		return fmt.Sprintf("%s tag", keys[0])
	case 2:
		return fmt.Sprintf("%s and %s tags", keys[0], keys[1])
	default:
		return fmt.Sprintf("%s and %s tags", strings.Join(keys[:len(keys)-1], ", "), keys[len(keys)-1])
	}
}

// instancePhrase renders a count with its noun, so that the one-resource case
// does not read "1 resource instances".
func instancePhrase(n int) string {
	if n == 1 {
		return "1 resource instance"
	}
	return fmt.Sprintf("%d resource instances", n)
}
