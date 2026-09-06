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
// block all answer [strict.SecretsDefault], which is [strict.DefaultSecrets]
// - what every configuration written before GitHub issue #365 slice 3 gets
// - unless the environment has pinned the strict profile ([strict.Pinned]),
// in which case it is [strict.PinnedSecrets] instead. A configuration that
// explicitly sets a VALID value while pinned is not clamped here: see
// "Why the pin does not clamp an explicit value" below.
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
//
// # Why the pin does not clamp an explicit value
//
// [strict.Pinned] changes what "nothing here" means (see
// [strict.SecretsDefault]), and a configuration that sets no secrets
// argument at all, or one lint has already condemned as a typo, has
// nothing else this function could honor instead. A configuration that
// sets a VALID value the pin disagrees with is different: that value is
// something the operator wrote on purpose, and this function still returns
// it unchanged. Silently overriding it here would mean a caller reading
// this one value cannot tell "the pin applied" from "the operator asked
// for this", which is the opposite of GitHub issue #365's env-pin design
// note ("REFUSES a configuration that relaxes it" - a loud stop, not a
// silent substitution). The loud stop is internal/live/lint's: every real
// entry point runs checkStrictSecrets before this function is ever called
// for a plan, and [strict.PinRefusal] is what it consults.
func SecretsFor(cfg *configs.Config) strict.Secrets {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return strict.SecretsDefault()
	}
	st := cfg.Module.Live.Strict
	if !st.SecretsSet {
		return strict.SecretsDefault()
	}
	v := strict.Secrets(st.Secrets)
	if !strict.SecretsValid(v) {
		return strict.SecretsDefault()
	}
	return v
}

// NoSourceCreateFor is [SecretsFor]'s twin for GitHub issue #365's ruling-4
// toggle: an omitted argument, an absent strict block and an absent live
// block all answer [strict.NoSourceCreateDefault] ("refuse" either way -
// [strict.DefaultNoSourceCreate] was already the safety-first setting
// before the environment pin existed, so pinning changes nothing here
// unless a configuration explicitly asks for "create", which
// [strict.PinRefusal] and RuleStrictNoSourceCreate handle the same way
// [SecretsFor]'s doc comment describes for its own toggle), for the exact
// reasons [SecretsFor]'s own doc comment gives for its setting - an absent
// configuration means today's behavior, and a spelling
// internal/live/strict does not know has already been refused by
// internal/live/lint (RuleStrictNoSourceCreate), so a caller reaching here
// with one has skipped lint and the honest answer is still the default
// rather than a guess at which way the operator meant to move.
func NoSourceCreateFor(cfg *configs.Config) strict.NoSourceCreate {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return strict.NoSourceCreateDefault()
	}
	st := cfg.Module.Live.Strict
	if !st.NoSourceCreateSet {
		return strict.NoSourceCreateDefault()
	}
	v := strict.NoSourceCreate(st.NoSourceCreate)
	if !strict.NoSourceCreateValid(v) {
		return strict.NoSourceCreateDefault()
	}
	return v
}

// ProviderChangeFor is [NoSourceCreateFor]'s twin for GitHub issue #906's
// toggle, resolved the same way and for the same reasons: an absent live
// block, an absent strict block and an omitted argument all answer
// [strict.ProviderChangeDefault], and a spelling internal/live/strict does
// not know has already been refused by internal/live/lint
// (RuleStrictProviderChange), so a caller reaching here with one has
// skipped lint and the honest answer is the default rather than a guess at
// which way the operator meant to move.
func ProviderChangeFor(cfg *configs.Config) strict.ProviderChange {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || cfg.Module.Live.Strict == nil {
		return strict.ProviderChangeDefault()
	}
	st := cfg.Module.Live.Strict
	if !st.ProviderChangeSet {
		return strict.ProviderChangeDefault()
	}
	v := strict.ProviderChange(st.ProviderChange)
	if !strict.ProviderChangeValid(v) {
		return strict.ProviderChangeDefault()
	}
	return v
}
