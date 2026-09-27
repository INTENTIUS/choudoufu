// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// GitHub issue #1084: the post-create marker write's two inputs, supplied
// to [projection.NodeResolver] at the same "build early, populate once the
// run knows more" step live_mode.go and live_plan.go populate its other
// fields at.

// markerTagger builds the client [projection.NodeResolver.WriteAppliedMarkers]
// writes a withheld marker through, for one provider configuration and the
// post-create write the instance's surface names
// ([substrate.Writes.PostCreate]). Which client is built is chosen by that
// write and the configuration's family ([substrate.Substrate.MarkerWriter]),
// never by the provider type string (GitHub issue #1587): the family is
// looked up the way every other family question is, and a write this build
// has no client for is refused by name.
func (p *statelessProviders) markerTagger(addr addrs.AbsProviderConfig, write substrate.Write) (projection.MarkerTagger, error) {
	sub, known := substrate.ForProvider(addr.Provider.Type)
	return p.markerWriterFor(sub, known, addr, write)
}

// markerWriterFor is [statelessProviders.markerTagger] for a family already
// looked up (known false when no family claims the provider). Every refusal
// names what it could not serve, and [projection.NodeResolver.WriteAppliedMarkers]
// puts it in the "Created object is not marked" error.
func (p *statelessProviders) markerWriterFor(sub substrate.Substrate, known bool, addr addrs.AbsProviderConfig, write substrate.Write) (projection.MarkerTagger, error) {
	if !known {
		return nil, fmt.Errorf("provider %s belongs to no provider family this fork can write a marker through after a create", addr.Provider)
	}
	have := sub.MarkerWriter(addr)
	if have != write {
		return nil, fmt.Errorf("the %q post-create marker write was asked of provider family %s, whose provider configurations write with %q", write, sub.Name(), have)
	}
	build, ok := markerWriters[have]
	if !ok {
		return nil, fmt.Errorf("provider family %s declares the %q post-create marker write and this build has no writer for it", sub.Name(), have)
	}
	return build(p, addr), nil
}

// markerWriters is every post-create marker write this build can make a
// client for (GitHub issue #1587). TestEveryPostCreateWriteHasAWriter holds
// it to [substrate.All]; a family whose write has no entry here is refused
// by name at the create it could not mark.
var markerWriters = map[substrate.Write]func(*statelessProviders, addrs.AbsProviderConfig) projection.MarkerTagger{
	// The Resource Groups Tagging API client, signed as the provider
	// configuration's own principal exactly as the discovery sweep's
	// clients are (GitHub issue #957, [statelessProviders.credentials]).
	//
	// It is NOT behind [cloudControlTarget]'s opt-in the way the sweep
	// clients are. That gate exists because an unconditional sweep client
	// turned every offline run into per-type network attempts; this client
	// makes one call, only after a real create of one of the ten types whose
	// create call cannot carry tags, and the alternative to making it
	// against real AWS is leaving a real object unmarked. The endpoint
	// override still applies when one is set, so an emulator run lands on
	// the emulator.
	substrate.WriteTaggingAPI: func(p *statelessProviders, addr addrs.AbsProviderConfig) projection.MarkerTagger {
		ep, _ := cloudControlTarget()
		creds, explicit := p.credentials(addr, ep)
		return cloudcontrol.NewTagging(cloudcontrol.Config{
			Endpoint:             ep,
			Region:               p.region(addr),
			Credentials:          creds,
			SignEndpointOverride: explicit,
		})
	},
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
