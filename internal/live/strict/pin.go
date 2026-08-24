// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package strict

import (
	"fmt"
	"os"
)

// EnvPin is the environment variable that pins this fork's strict profile
// from OUTSIDE the configuration.
//
// The design note attached to GitHub issue #365's ruling 4 and 5 comment
// (rfc/20260823-foundation-order-ruling.md), itself following
// opentofu/opentofu#3016's configuration-tier idea, is the reason this
// exists at all: a `strict` block lives in the same commit as the resources
// it governs, so an author who wants to relax it can always do so in the
// same change that needs relaxing - the toggle protects nothing a platform
// team cannot be overruled on. EnvPin is read from the process that RUNS a
// plan or apply, never from configuration, so a commit relaxing a pinned
// toggle cannot approve its own relaxation; only whoever controls the
// environment can.
//
// CHOUDOUFU_ rather than TOFU_: internal/command/live_mode.go's
// CHOUDOUFU_NODE_RESOLVE and internal/live/identity/located_test.go's
// CHOUDOUFU_LIVE_SCHEMAS are this fork's own switches, and this is another
// one - a governance lever with no stock equivalent - while the TOFU_
// prefix (internal/command/live_plan.go's cloudControlEnvVar,
// guidedDiscoveryDisableEnvVar) is reserved for levers over behavior stock
// OpenTofu's own environment surface already has an opinion about.
const EnvPin = "CHOUDOUFU_STRICT_PIN"

// Pinned reports whether the execution environment has pinned the strict
// profile: EnvPin set to exactly "1", the same grammar
// internal/command/live_mode.go's CHOUDOUFU_NODE_RESOLVE already uses,
// rather than "any non-empty value", so a CI environment that exports the
// variable empty (a common shell idiom for "unset for this job") reads as
// unset rather than as a typo nobody can see.
func Pinned() bool {
	return os.Getenv(EnvPin) == "1"
}

// PinnedSecrets is what [SecretsFor]-shaped resolution answers for an
// OMITTED secrets argument while [Pinned] - see [SecretsFor]'s own doc
// comment in internal/live/identity for why an explicit, invalid, or
// relaxing argument is not folded into this function and is left to
// [PinRefusal] and the lint rule that calls it instead.
func PinnedSecrets() Secrets { return Refuse }

// PinnedNoSourceCreate is [PinnedSecrets]'s twin for the no_source_create
// toggle. It equals [DefaultNoSourceCreate] today - the default was already
// the safety-first setting before GitHub issue #365's environment pin
// existed - so pinning only ever changes behavior for a configuration that
// explicitly asks for "create", and that case is [PinRefusal]'s, not this
// function's.
func PinnedNoSourceCreate() NoSourceCreate { return NoSourceRefuse }

// SecretsDefault is what internal/live/identity.SecretsFor resolves an
// omitted, absent, or unrecognised secrets argument to: [DefaultSecrets]
// unless [Pinned], in which case [PinnedSecrets] - the pin's silent half.
// The loud half, refusing a configuration that explicitly sets a valid but
// relaxing value while pinned, is [PinRefusal]'s.
func SecretsDefault() Secrets {
	if Pinned() {
		return PinnedSecrets()
	}
	return DefaultSecrets
}

// NoSourceCreateDefault is [SecretsDefault]'s twin for
// internal/live/identity.NoSourceCreateFor.
func NoSourceCreateDefault() NoSourceCreate {
	if Pinned() {
		return PinnedNoSourceCreate()
	}
	return DefaultNoSourceCreate
}

// PinRefusal returns the refusal detail for a `strict.<name>` argument
// whose written value relaxes a [Toggle.Pinnable] toggle away from the
// value [Pinned] forces, or "" when nothing here needs refusing: the pin is
// not active, name is not a toggle this schema defines or is not pinnable,
// or value already equals the toggle's [Toggle.SafeValue] (the safe setting
// written out by hand, GitHub issue #101's own lesson applied here too,
// must lint exactly as leaving the pin to supply it silently does).
//
// It says nothing about a value outside the toggle's vocabulary altogether
// - that is a typo, and internal/live/lint's own checkStrictSecrets and
// checkStrictNoSourceCreate already refuse it before they reach this
// function, under RuleStrictSecrets and RuleStrictNoSourceCreate
// respectively; PinRefusal only has an opinion once a value is confirmed
// valid.
//
// The returned string names both sides on purpose - the pin (EnvPin and
// the value it forces) and the offending line's own construct and setting
// - because a reader of this message may control neither: an author whose
// commit set the argument needs to see the pin exists at all, and an
// operator investigating a refused run needs to see which line to remove.
func PinRefusal(name, value string) string {
	if !Pinned() {
		return ""
	}
	t, ok := toggleNamed(name)
	if !ok || !t.Pinnable || value == t.SafeValue {
		return ""
	}
	return fmt.Sprintf(
		"%s is pinned to %s = %q by the %s environment variable, set outside this configuration so that no "+
			"commit here can switch it off on its own - but this strict block sets %s = %q, which relaxes it. %s "+
			"Remove the %s argument (an omitted one already resolves to the pinned value while %s is set) or run "+
			"this configuration in an environment that does not set %s.",
		EnvPin, name, t.SafeValue, EnvPin, name, value, t.Relaxes, name, EnvPin, EnvPin,
	)
}
