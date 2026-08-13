// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/policy"
)

// statelessPolicy resolves a live block's optional policy block into a
// fully populated [policy.Policy], for the setup pipeline in live_mode.go,
// live_plan.go and live_mv.go to construct alongside the estate name.
//
// This is GitHub issue #67's config/lint half only: the value this returns
// is not read by anything yet. The quadrant behavior it will eventually
// drive - which verb runs against which resource, in discovery, projection,
// lifecycle and stamp - lands behind #59b and #60, per the issue's
// Sequencing section. Every call site logs the result at TRACE level and
// goes no further, so this function exists purely so that work starts from
// an agreed, already-validated shape instead of parsing the live block's
// policy block a second time.
//
// live may be nil (no live block at all) or have a nil Policy (a live block
// with no policy block); both resolve to [policy.Build]'s default preset,
// which is today's fixed behavior. Every call site calls this only after
// [lint.CheckWith] has returned no issues, so by the time this runs, any
// verb here is already known valid for its quadrant, and a delete quadrant,
// if any, is already known to carry a scope block - see
// internal/live/lint's checkLivePolicy.
func statelessPolicy(live *configs.Live, estate string) *policy.Policy {
	if live == nil || live.Policy == nil {
		return policy.Build(nil, estate)
	}

	lp := live.Policy
	raw := &policy.Raw{
		DeclaredTagged:        lp.DeclaredTagged,
		DeclaredTaggedSet:     lp.DeclaredTaggedSet,
		DeclaredUntagged:      lp.DeclaredUntagged,
		DeclaredUntaggedSet:   lp.DeclaredUntaggedSet,
		UndeclaredTagged:      lp.UndeclaredTagged,
		UndeclaredTaggedSet:   lp.UndeclaredTaggedSet,
		UndeclaredUntagged:    lp.UndeclaredUntagged,
		UndeclaredUntaggedSet: lp.UndeclaredUntaggedSet,
		TagKey:                lp.TagKey,
		TagKeySet:             lp.TagKeySet,
		TagValue:              lp.TagValue,
		TagValueSet:           lp.TagValueSet,
		Threshold:             lp.Threshold,
		ThresholdSet:          lp.ThresholdSet,
	}
	if lp.Scope != nil {
		raw.Scope = &policy.RawScope{
			Services: lp.Scope.Services,
			Types:    lp.Scope.Types,
			Regions:  lp.Scope.Regions,
		}
	}
	return policy.Build(raw, estate)
}
