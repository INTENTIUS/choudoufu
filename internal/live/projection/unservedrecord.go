// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SummaryRemovalProviderNotConfigured is GitHub issue #1729's refusal: a
// resource this estate owns has no resource block left, and the provider
// this run would read it through does not serve its type. The usual way to
// get here is removing a provider and its resources from the configuration
// in one edit, so the record store still holds an identity that only the
// removed provider can read or destroy. Skipping the instance would drop a
// removal without a word, so the run refuses and says what to add back.
const SummaryRemovalProviderNotConfigured = "No configured provider serves a removed resource"

// unservedRemoval reports whether an undeclared instance of typeName cannot
// be read through entry because the provider does not serve the type, and
// returns the named refusal for it. False for a declared instance, whose
// block names its own provider, and for a provider that does serve the
// type: those keep [providerEntry.resourceSchema]'s own diagnostics.
func (b *builder) unservedRemoval(ctx context.Context, addr addrs.AbsResourceInstance, typeName string, undeclared bool, providerAddr addrs.AbsProviderConfig, entry *providerEntry) (tfdiags.Diagnostics, bool) {
	if !undeclared {
		return nil, false
	}
	if s, ok := entry.schema.ResourceTypes[typeName]; ok && s.Block != nil {
		return nil, false
	}

	recorded := ""
	if b.opts.RecordStore != nil {
		if env, _, exists, err := b.opts.RecordStore.getRaw(ctx, addr); err == nil && exists {
			recorded = env.Provider
		}
	}
	var detail string
	if recorded != "" {
		detail = fmt.Sprintf(
			"%s (type %q) has no resource block, and the record store holds it under %s. This run reads it through %s, which does not serve that type, so its removal cannot be planned. Add that provider's configuration back so the removal can be planned.",
			addr, typeName, recorded, providerAddr,
		)
	} else {
		detail = fmt.Sprintf(
			"%s (type %q) has no resource block, and no recorded provider for it was found. This run reads it through %s, which does not serve that type, so its removal cannot be planned. Add the configuration of the provider that serves %q back so the removal can be planned.",
			addr, typeName, providerAddr, typeName,
		)
	}
	return tfdiags.Diagnostics(nil).Append(tfdiags.Sourceless(tfdiags.Error, SummaryRemovalProviderNotConfigured, detail)), true
}
