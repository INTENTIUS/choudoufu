// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveOutputs answers `output` and `output -json` for a directory with a live
// block from the record store, which is where an estate's root outputs are
// remembered at the end of every apply (tofu-outputs/<estate>/<output>). A
// fresh checkout has no local state cache, so the stock read finds nothing;
// this read finds what the last apply recorded, which is what a Terragrunt
// `dependency` block (it runs `<binary> output -json`) and an Atmos
// `!terraform.output` need (choudoufu#2213).
//
// handled is false for a directory with no live block, and for a composed
// root, whose outputs belong to several estates; both go on to the stock read.
//
// Which outputs are asked about is bounded by the configuration, because the
// namespace has no listing. What is returned is exactly what the apply
// recorded: a sensitive output and one whose value was not wholly known are
// never recorded, so they are not returned, whatever -show-sensitive says.
// Naming one of them is an error that says why; asking for everything omits
// them and raises a warning naming each. The command never answers a missing
// value with null.
func (c *OutputCommand) liveOutputs(ctx context.Context, name string) (outputs map[string]*states.OutputValue, handled bool, diags tfdiags.Diagnostics) {
	settings, diags := c.liveSettings(ctx, false)
	if diags.HasErrors() {
		return nil, true, diags
	}
	if settings == nil || settings.IsComposed() {
		return nil, false, diags
	}

	mod, modDiags := c.loadSingleModule(ctx, ".", configs.SelectiveLoadAll)
	diags = diags.Append(modDiags)
	if modDiags.HasErrors() {
		return nil, true, diags
	}
	if mod == nil {
		return nil, false, diags
	}

	estate := settings.Estate
	if estate == "" {
		config, cfgDiags := c.loadConfig(ctx, ".")
		diags = diags.Append(cfgDiags)
		if cfgDiags.HasErrors() {
			return nil, true, diags
		}
		var estateDiags tfdiags.Diagnostics
		estate, _, estateDiags = liveEstateFor(ctx, "", config)
		diags = diags.Append(estateDiags)
		if estateDiags.HasErrors() {
			return nil, true, diags
		}
	}
	if estate == "" {
		return nil, true, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No estate named by the live block",
			"`output` reads this estate's recorded outputs by estate name, and nothing in the configuration names one. Set `estate` in the live block or the live sidecar file."))
	}
	if mod.Live == nil || mod.Live.RecordStore == nil {
		return nil, true, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No record store to read outputs from",
			fmt.Sprintf("Under a live block an estate's root outputs are remembered in its record store, and the live block of estate %q configures none, so there is nothing for `output` to read. Add a record_store block, apply, and run `output` again.", estate)))
	}

	storeOpts, err := recordStoreOpenOptions()
	if err != nil {
		return nil, true, diags.Append(recordStoreOpenDiag(mod.Live.RecordStore.Type, err))
	}
	store, err := projection.NewRecordStore(ctx, mod.Live.RecordStore, mod.Live.Retry, estate, ".", storeOpts...)
	if err != nil {
		return nil, true, diags.Append(recordStoreOpenDiag(mod.Live.RecordStore.Type, err))
	}
	rootOutputs := projection.NewRootOutputStore(store, estate)

	var names []string
	if name != "" {
		names = []string{name}
	} else {
		for n := range mod.Outputs {
			names = append(names, n)
		}
		sort.Strings(names)
	}

	outputs = map[string]*states.OutputValue{}
	var absent []string
	for _, n := range names {
		decl := mod.Outputs[n]
		if decl == nil {
			return nil, true, diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("Output %q not found",
				n), fmt.Sprintf("The configuration of estate %q declares no output named %q.", estate, n)))
		}
		val, _, exists, err := rootOutputs.Get(ctx, n)
		if err != nil {
			// A dependency read must never see a guess, so unlike the plan
			// path this does not carry on.
			return nil, true, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a recorded output", err.Error()))
		}
		if !exists {
			if name != "" {
				return nil, true, diags.Append(tfdiags.Sourceless(tfdiags.Error, fmt.Sprintf("Output %q has no recorded value", n), notRecordedDetail(estate, n, decl.Sensitive)))
			}
			absent = append(absent, n)
			continue
		}
		outputs[n] = &states.OutputValue{Value: val, Sensitive: false}
	}
	if len(absent) > 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Some outputs have no recorded value",
			fmt.Sprintf("Estate %q has not recorded: %s. They are left out of this answer. An estate records a root output at the end of each apply; a sensitive output and an output whose value was not wholly known are never recorded.",
				estate, strings.Join(absent, ", "))))
	}
	return outputs, true, diags
}

func notRecordedDetail(estate, name string, sensitive bool) string {
	if sensitive {
		return fmt.Sprintf("Output %q of estate %q is marked sensitive, and a sensitive output is never written to the record store, so `output` has no value to return. Read it from where the apply printed it, or have the consumer read the resource it is built from.", name, estate)
	}
	return fmt.Sprintf("Estate %q has no recorded value for its output %q. Either it has not applied since the output was declared, or the value was not wholly known when the apply finished (such a value is not recorded).", estate, name)
}
