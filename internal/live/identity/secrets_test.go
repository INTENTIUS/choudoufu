// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/strict"
)

// cfgWithStrict builds the shape [SecretsFor] reads: a root module whose
// live block carries the given strict block, or none.
//
// It is a struct literal rather than a parsed fixture because what is under
// test here is the resolution of an ABSENT value, and the four ways a value
// can be absent - no live block, no strict block, no argument, an argument
// that failed to decode - are four distinct struct shapes and one parse.
func cfgWithStrict(st *configs.LiveStrict, live bool) *configs.Config {
	mod := &configs.Module{}
	if live {
		mod.Live = &configs.Live{Strict: st}
	}
	return &configs.Config{Module: mod}
}

// TestSecretsFor pins every way an omitted setting can be spelled, because
// they must all mean the same thing and each is a different line of code.
//
// GitHub issue #101 is the standing lesson and it is the reason the "written
// out by hand" case is here: a documented default that behaves differently
// from omitting it punishes an operator for saying what they mean.
func TestSecretsFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *configs.Config
		want strict.Secrets
	}{
		{"nil configuration", nil, strict.DefaultSecrets},
		{"no live block", cfgWithStrict(nil, false), strict.DefaultSecrets},
		{"live block, no strict block", cfgWithStrict(nil, true), strict.DefaultSecrets},
		{"strict block, argument omitted", cfgWithStrict(&configs.LiveStrict{}, true), strict.DefaultSecrets},
		{
			// The decoder leaves SecretsSet false when the expression did
			// not yield a literal string, and that has to read as omitted
			// rather than as the empty string - which strict.StoresSecrets
			// answers false for.
			"argument present but undecodable",
			cfgWithStrict(&configs.LiveStrict{Secrets: "", SecretsSet: false}, true),
			strict.DefaultSecrets,
		},
		{
			"the default written out by hand",
			cfgWithStrict(&configs.LiveStrict{Secrets: "store", SecretsSet: true}, true),
			strict.Store,
		},
		{
			"the principle turned on",
			cfgWithStrict(&configs.LiveStrict{Secrets: "refuse", SecretsSet: true}, true),
			strict.Refuse,
		},
		{
			// A spelling internal/live/lint has already refused. It reads as
			// the default rather than as the strict answer: this layer's job
			// is to say what the configuration means, and a typo means
			// nothing, so the run that lint is about to stop is the run the
			// configuration otherwise describes. Reading it as Refuse would
			// silently produce a DIFFERENT plan from the refused one.
			"a spelling outside the vocabulary",
			cfgWithStrict(&configs.LiveStrict{Secrets: "none", SecretsSet: true}, true),
			strict.DefaultSecrets,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SecretsFor(tc.cfg); got != tc.want {
				t.Errorf("SecretsFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSecretsForReadsOnlyTheRootModule is [SelectionFor]'s own rule applied
// to this setting: a live block in a child module is refused outright by
// internal/live/lint's RuleChildLiveConfig, and every other consumer of a
// live block reads the root's only. A child block that could turn this off
// for part of an estate would be a second, unrefused way to configure it.
func TestSecretsForReadsOnlyTheRootModule(t *testing.T) {
	root := cfgWithStrict(nil, true)
	root.Children = map[string]*configs.Config{
		"child": cfgWithStrict(&configs.LiveStrict{Secrets: "refuse", SecretsSet: true}, true),
	}
	if got, want := SecretsFor(root), strict.DefaultSecrets; got != want {
		t.Errorf("SecretsFor read a child module's live block: got %q, want %q", got, want)
	}
}
