// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// checkGCPEstateLabel refuses every google resource whose labels would
// carry the tofu-estate marker when the live block's estate name is not a
// legal GCP label value (GitHub issue #2145). The estate grammar admits
// names up to 128 characters; a GCP label value stops at 63. The node
// stamp refuses each such instance at plan, live-import and live-mv
// (projection's stampedGoogleLabels, SummaryMarkerNotALabel), so without
// this rule live-check passed a configuration none of those could run.
//
// The estate read is the root live block's own estate argument, the value
// the plan stamps with when no -estate flag overrides it. An estate named
// only by a flag, or derived from tofu-estate tags (which no google type
// carries), is not seen here.
//
// With provider schemas, only a type whose labels map carries the marker
// ([substrate.SurfaceOf]) is refused, so an instance template, never
// stamped, is not. Without them every google managed resource is refused;
// such a run refuses every google type as unadmitted-type anyway.
//
// One refusal per block, honouring [Context.Scope]: the plan refuses per
// instance, and a block this run leaves out is never stamped.
func checkGCPEstateLabel(cfg *configs.Config, schemas map[string]providers.Schema, scope identity.Scope, issues *[]Issue) {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil || !cfg.Module.Live.EstateSet {
		return
	}
	estate := cfg.Module.Live.Estate
	if estate == "" || markers.ValidGoogleLabelValue(estate) {
		return
	}
	cfg.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		names := make([]string, 0, len(c.Module.ManagedResources))
		for name := range c.Module.ManagedResources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rc := c.Module.ManagedResources[name]
			if !isGoogleProvider(rc.Provider) {
				continue
			}
			if scopeExcludes(scope, c.Path, rc.Addr()) {
				continue
			}
			if schema, ok := schemas[rc.Type]; ok && schema.Block != nil {
				if _, carries := substrate.SurfaceOf(rc.Provider.Type, schema.Block); !carries {
					continue
				}
			}
			addr := addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}.String()
			*issues = append(*issues, Issue{
				Rule:      RuleGCPEstateLabel,
				Construct: addr,
				Module:    c.Path,
				Detail: fmt.Sprintf(
					"%s carries its ownership marker as a GCP label, and %s. Plan, apply, live-import and live-mv all refuse this block. "+
						"Rename the estate in the live block to at most %d characters, or keep this resource out of a GCP estate.",
					addr, markers.NotAGoogleLabelValue(estate), markers.GoogleLabelMaxValue),
				Subject: rc.DeclRange,
			})
		}
	})
}
