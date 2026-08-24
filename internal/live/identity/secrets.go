// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/strict"
)

// SecretsFor is the root module's `strict { secrets = ... }` setting,
// resolved: an omitted argument, an absent strict block and an absent live
// block all answer [strict.DefaultSecrets], which is what every
// configuration written before GitHub issue #365 slice 3 gets.
//
// It lives here rather than in internal/live/strict for [SelectionFor]'s
// reason, spelled out on that function: the setting is read from the
// configuration by every layer that acts on it - internal/live/lint,
// internal/live/projection's residue read and write sides,
// internal/live/liveimport - rather than threaded from one, so that no two
// layers can disagree about what the operator asked for. internal/live/strict
// deliberately does not depend on internal/configs, and this package already
// does.
//
// Only the ROOT module's live block is read, for the same reason
// [SelectionFor] reads only the root's: a live block in a child module is
// refused outright by internal/live/lint's RuleChildLiveConfig.
//
// # Why an absent configuration resolves to the default rather than to the
// strict answer, unlike [SelectionFor]
//
// A nil cfg means "no live block was given", and it resolves to
// [strict.DefaultSecrets] the same way an omitted argument does. That is the
// opposite direction from [SelectionFor]'s nil, which selects nothing, and
// the two are not inconsistent because they are not the same kind of
// question.
//
// A selection WITHHOLDS an ownership marker. Getting it wrong in the
// permissive direction creates a live object with neither a marker nor a
// record, which no later run can find - the failure HANDOFF.md's safety rule
// exists to prevent - so a layer that cannot prove an instance was selected
// must mark it.
//
// This setting withholds nothing from the cloud and moves no identity.
// Getting it wrong in the permissive direction records a value stock's own
// state file would have held anyway, in a store this estate already owns;
// getting it wrong in the strict direction leaves the estate proposing an
// update to that value on every run, forever. Neither is a wrong marker, and
// the first is the one an operator who wrote no configuration about it asked
// for by writing none.
//
// # Why an unrecognised spelling resolves to the default rather than to the
// strict answer
//
// A run reaching here with a spelling internal/live/strict does not know has
// already been refused by internal/live/lint (RuleStrictSecrets), so the
// value cannot reach a plan. What is left is a caller that skipped lint -
// tools/estate-plan and the offline probes - and for those the honest answer
// is "this configuration says nothing this package understands", which is
// the default. Reading a typo as [strict.Refuse] would be worse in the one
// way that matters: it would silently produce a DIFFERENT plan from the one
// the operator's configuration describes, rather than the same plan lint is
// about to refuse.
func SecretsFor(cfg *configs.Config) strict.Secrets {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return strict.DefaultSecrets
	}
	st := cfg.Module.Live.Strict
	if !st.SecretsSet {
		return strict.DefaultSecrets
	}
	v := strict.Secrets(st.Secrets)
	if !strict.SecretsValid(v) {
		return strict.DefaultSecrets
	}
	return v
}

// NoSourceCreateFor is [SecretsFor]'s twin for GitHub issue #365's ruling-4
// toggle: an omitted argument, an absent strict block and an absent live
// block all answer [strict.DefaultNoSourceCreate] ("refuse"), for the exact
// reasons [SecretsFor]'s own doc comment gives for its setting - an absent
// configuration means today's behavior, and a spelling
// internal/live/strict does not know has already been refused by
// internal/live/lint (RuleStrictNoSourceCreate), so a caller reaching here
// with one has skipped lint and the honest answer is still the default
// rather than a guess at which way the operator meant to move.
func NoSourceCreateFor(cfg *configs.Config) strict.NoSourceCreate {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return strict.DefaultNoSourceCreate
	}
	st := cfg.Module.Live.Strict
	if !st.NoSourceCreateSet {
		return strict.DefaultNoSourceCreate
	}
	v := strict.NoSourceCreate(st.NoSourceCreate)
	if !strict.NoSourceCreateValid(v) {
		return strict.DefaultNoSourceCreate
	}
	return v
}
