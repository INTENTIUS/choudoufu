// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"

	terraformProvider "github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveLsOpenEstateOutputs opens this command's terraform_estate_outputs
// holder for the declared-instance comparison's identity data-read phase
// (GitHub issue #1859), the way live-plan opens its own after the store.
//
// live-ls is the one live command that opens no record store of its own: it
// lists the cloud, and the comparison resolves DIR's identities to say which
// declared instances the listing missed. Since GitHub issue #1575 an identity
// that reads terraform_estate_outputs is read before resolution, through the
// record store, so a comparison over such a configuration needs the store
// that DIR's live block names - and only such a comparison does. A
// configuration declaring no terraform_estate_outputs block opens nothing,
// and the listing touches no store at all, as it never has.
//
// A configuration with no live block opens nothing either, and the builtin
// provider's "Estate outputs need a live block" refusal stands for it: that
// one is true.
//
// The store is opened the lenient way live-plan's is
// ([openRecordStoreAsOneMoreSource]): an outage is a warning and every
// estate-outputs read then refuses naming why, and a refusal is an error,
// which the caller turns into a skipped comparison. Relative paths in a
// "local" store resolve against DIR, not the process's working directory,
// because DIR is the module.
func (c *LiveLsCommand) liveLsOpenEstateOutputs(ctx context.Context, estate, dir string, config *configs.Config) tfdiags.Diagnostics {
	if config == nil || config.Module == nil || config.Module.Live == nil {
		return nil
	}
	if !configReadsEstateOutputs(config) {
		return nil
	}
	rs := config.Module.Live.RecordStore
	openInDir := func(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, _ string, opts ...projection.RecordStoreOption) (staterecord.Store, error) {
		return projection.NewRecordStore(ctx, rs, rt, estate, dir, opts...)
	}
	store, diags := openRecordStoreAsOneMoreSource(ctx, openInDir, rs, config.Module.Live.Retry, estate, "live-ls")
	if diags.HasErrors() {
		return diags
	}
	unavailable := ""
	if store == nil {
		unavailable = "this live-ls could not open the record store (see the warning about it)"
	}
	c.liveEstateOutputs().open(store, rs, estate, unavailable)
	return diags
}

// configReadsEstateOutputs reports whether any module in config declares a
// data "terraform_estate_outputs" block.
func configReadsEstateOutputs(config *configs.Config) bool {
	found := false
	config.DeepEach(func(c *configs.Config) {
		if found || c.Module == nil {
			return
		}
		for _, r := range c.Module.DataResources {
			if r.Type == terraformProvider.EstateOutputsTypeName {
				found = true
				return
			}
		}
	})
	return found
}
