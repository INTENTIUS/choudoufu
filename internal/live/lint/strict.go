// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/strict"
)

// checkLiveStrict validates the optional strict block nested in a live block,
// GitHub issue #365's profile toggles.
//
// One argument today, marker_repair, and two ways it can be wrong. A value
// outside internal/live/strict's vocabulary is a typo. A value inside it
// whose mechanism this build does not have is the more interesting case, and
// it is refused rather than accepted for the reason [strict.Implemented]
// spells out: "never" and "report" both say "leave the tags alone", and
// accepting one while the ordinary plan carried on writing markers would
// tell an operator their estate was safe from this tool when it was not.
// A refusal is loud and reversible (HANDOFF.md, "The safety rule"); a
// setting that does nothing is silent and misleading.
//
// The default is untouched by all of this. An omitted argument - and an
// absent strict block, which is every configuration written before it
// existed - resolves to [strict.DefaultMarkerRepair], today's behavior, and
// is not checked at all. So is `marker_repair = "repair"` written out by
// hand, which must mean exactly the same thing as omitting it; #101 is the
// standing lesson there, where writing a documented default by hand was a
// lint error while omitting it was clean.
//
// This rule follows checkLivePolicy's module handling rather than inventing
// its own: a live block does decode in a child module, nothing outside the
// root's block is acted on, and the conservative direction is to fire on
// whatever is found. The silent half is tracked with the other child-module
// silent-ignores on issue #104.
func checkLiveStrict(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if mod.Live == nil || mod.Live.Strict == nil {
		return
	}
	st := mod.Live.Strict

	if !st.MarkerRepairSet {
		// An omitted argument resolves to strict.DefaultMarkerRepair, which
		// is today's behavior by construction. Nothing to check.
		return
	}

	repair := strict.MarkerRepair(st.MarkerRepair)
	switch {
	case strict.Implemented(repair):
		// The default, written out. Same run as omitting it.
	case strict.Valid(repair):
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkerRepair,
			Construct: fmt.Sprintf("strict.marker_repair = %q", st.MarkerRepair),
			Module:    path,
			Detail: fmt.Sprintf(
				"%q is a marker_repair setting this fork's schema defines and this build does not implement yet "+
					"(GitHub issue #365). It would mean leaving an ownership marker on a live object alone when it "+
					"disagrees with the marker this configuration declares, and nothing in this build does that: "+
					"markers are repaired by the plan's ordinary tags update, which is not yet suppressible per key. "+
					"Accepting the setting would report an estate as protected from this tool while every plan carried "+
					"on rewriting its markers, so it is refused instead. Settings this build implements: %s.",
				st.MarkerRepair, strict.ImplementedNames(),
			),
			Subject: st.MarkerRepairRange,
		})
	default:
		*issues = append(*issues, Issue{
			Rule:      RuleStrictMarkerRepair,
			Construct: fmt.Sprintf("strict.marker_repair = %q", st.MarkerRepair),
			Module:    path,
			Detail: fmt.Sprintf(
				"%q is not a marker_repair setting. Valid settings: %s, of which this build implements %s. "+
					"Omitting the argument means %q, which is what every configuration written before the strict "+
					"block existed gets.",
				st.MarkerRepair, strict.Names(), strict.ImplementedNames(), strict.DefaultMarkerRepair,
			),
			Subject: st.MarkerRepairRange,
		})
	}
}
