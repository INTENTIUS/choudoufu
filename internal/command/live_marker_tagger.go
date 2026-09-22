// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// GitHub issue #1084: the post-create marker write's two inputs, supplied
// to [projection.NodeResolver] at the same "build early, populate once the
// run knows more" step live_mode.go and live_plan.go populate its other
// fields at.

// markerTagger builds the Resource Groups Tagging API client
// [projection.NodeResolver.WriteAppliedMarkers] writes a withheld marker
// through, for one AWS provider configuration, signed as that
// configuration's own principal exactly as the discovery sweep's clients
// are (GitHub issue #957, [statelessProviders.credentials]). Nil for a
// provider that is not hashicorp/aws.
//
// It is NOT behind [cloudControlTarget]'s opt-in the way the sweep clients
// are. That gate exists because an unconditional sweep client turned every
// offline run into per-type network attempts; this client makes one call,
// only after a real create of one of the ten types whose create call cannot
// carry tags, and the alternative to making it against real AWS is leaving
// a real object unmarked. The endpoint override still applies when one is
// set, so an emulator run lands on the emulator.
func (p *statelessProviders) markerTagger(addr addrs.AbsProviderConfig) projection.MarkerTagger {
	if addr.Provider.Type != "aws" {
		return nil
	}
	ep, _ := cloudControlTarget()
	creds, explicit := p.credentials(addr, ep)
	return cloudcontrol.NewTagging(cloudcontrol.Config{
		Endpoint:             ep,
		Region:               p.region(addr),
		Credentials:          creds,
		SignEndpointOverride: explicit,
	})
}

// markerRoster is the roster the node writer reads tag_on_create from: the
// embedded artifacts, or nil when they do not parse, which the resolver
// reads as "every type takes tags at create" - the pre-#1084 path. The
// parse failure is already reported where the sweep needs the same roster
// (live_plan.go's "Cloud Control fallback unavailable"), so it is not
// repeated here.
func markerRoster() *registry.Roster {
	roster, err := registry.Embedded()
	if err != nil {
		return nil
	}
	return roster
}
