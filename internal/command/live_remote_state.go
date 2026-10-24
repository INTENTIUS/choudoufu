// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/backend"
	backendInit "github.com/intentius/choudoufu/internal/backend/init"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// publishOutputsState is choudoufu#2213's answer to `terraform_remote_state`
// and Atmos `!terraform.state`. Both read a state file straight from the
// backend, without the binary, and a live estate has no authoritative state
// file. After an apply has written its records, this writes a state document
// with the estate's recorded root outputs and no resources to the location the
// directory's own backend block names (a hand-written one, a
// Terragrunt-generated backend.tf, an Atmos backend.tf.json), so those readers
// get the outputs they always got.
//
// Everything here is best effort. The returned diagnostics are warnings only:
// an apply never fails because this write did.
//
//   - No backend block, a local backend, or no record store: nothing happens.
//   - The outputs come from the record store, the same read `output` does, so a
//     sensitive output and one not wholly known are absent, and a scoped apply
//     does not blank outputs it never evaluated.
//   - The write takes the backend's own lock and releases it before returning.
//     A lock that cannot be taken is a warning and no write, never a wait on
//     someone else's run.
//   - Lineage and serial come from the backend's state manager: an existing
//     document keeps its lineage and the serial goes up by one; no document
//     gets a new lineage. A document that is unchanged is not rewritten.
//   - A document that holds resources is a stock state somebody still owns.
//     It is never overwritten.
func publishOutputsState(ctx context.Context, config *configs.Config, outputs *projection.RootOutputStore) (diags tfdiags.Diagnostics) {
	if config == nil || config.Module == nil || config.Module.IgnoredBackend == nil || outputs == nil {
		return nil
	}
	be := config.Module.IgnoredBackend
	if be.Type == "local" {
		return nil
	}
	warn := func(detail string) tfdiags.Diagnostics {
		return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Warning,
			"Outputs were not published to the backend",
			fmt.Sprintf("The apply and its records are complete. The estate's outputs could not be published to its %q backend for terraform_remote_state and !terraform.state readers: %s", be.Type, detail)))
	}

	// The apply is done and its records are written. Whatever goes wrong in a
	// backend client from here is a warning.
	defer func() {
		if r := recover(); r != nil {
			diags = warn(fmt.Sprintf("the backend client failed unexpectedly: %v", r))
		}
	}()
	if be.Eval == nil {
		be.Eval = config.Module.StaticEvaluator
	}

	f, _ := backendInit.Backend(be.Type)
	if f == nil {
		return warn(fmt.Sprintf("choudoufu has no backend of type %q.", be.Type))
	}
	b := f(encryption.StateEncryptionDisabled())
	schema := b.ConfigSchema()
	val, hclDiags := be.Decode(ctx, schema.NoneRequired())
	if hclDiags.HasErrors() || !val.IsWhollyKnown() {
		return warn("the backend block is not fully set in the configuration (a backend configured by init -backend-config flags is not known here). " + hclDiags.Error())
	}
	val, prepDiags := b.PrepareConfig(val)
	if prepDiags.HasErrors() {
		return warn(prepDiags.Err().Error())
	}
	if cfgDiags := b.Configure(ctx, val); cfgDiags.HasErrors() {
		return warn(cfgDiags.Err().Error())
	}
	mgr, err := b.StateMgr(ctx, currentWorkspaceName())
	if err != nil {
		return warn(err.Error())
	}

	values, err := recordedOutputsOf(ctx, config.Module, outputs)
	if err != nil {
		return warn(err.Error())
	}
	if werr := writeOutputsState(ctx, mgr, values); werr != nil {
		return warn(werr.Error())
	}
	return nil
}

// recordedOutputsOf reads every declared root output the record store holds.
func recordedOutputsOf(ctx context.Context, mod *configs.Module, outputs *projection.RootOutputStore) (map[string]cty.Value, error) {
	out := map[string]cty.Value{}
	for name := range mod.Outputs {
		val, _, exists, err := outputs.Get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("the recorded output %q could not be read: %w", name, err)
		}
		if exists {
			out[name] = val
		}
	}
	return out, nil
}

// writeOutputsState writes values as an outputs-only state through mgr, under
// the manager's lock.
func writeOutputsState(ctx context.Context, mgr statemgr.Full, values map[string]cty.Value) (err error) {
	info := statemgr.NewLockInfo()
	info.Operation = "choudoufu outputs publish"
	lockID, lerr := mgr.Lock(ctx, info)
	if lerr != nil {
		return fmt.Errorf("the backend lock could not be taken, so nothing was written: %w", lerr)
	}
	defer func() {
		if uerr := mgr.Unlock(ctx, lockID); uerr != nil {
			uerr = fmt.Errorf("the backend lock %s could not be released and may need `force-unlock`: %w", lockID, uerr)
			if err == nil {
				err = uerr
			} else {
				err = fmt.Errorf("%w; %v", err, uerr)
			}
		}
	}()

	if err := mgr.RefreshState(ctx); err != nil {
		return fmt.Errorf("the existing state could not be read: %w", err)
	}
	existing := mgr.State()
	if existing != nil && existing.HasManagedResourceInstanceObjects() {
		return fmt.Errorf("the backend holds a state with resources, which choudoufu does not overwrite")
	}
	if len(values) == 0 && (existing == nil || len(existing.RootModule().OutputValues) == 0) {
		return nil
	}

	st := states.NewState()
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		st.RootModule().SetOutputValue(n, values[n], false, "")
	}
	if err := mgr.WriteState(st); err != nil {
		return fmt.Errorf("the state could not be staged: %w", err)
	}
	if err := mgr.PersistState(ctx, nil); err != nil {
		return fmt.Errorf("the state could not be written: %w", err)
	}
	return nil
}

// currentWorkspaceName is the workspace the stock tool would use here:
// TF_WORKSPACE, else the name init or `workspace select` saved, else default.
func currentWorkspaceName() string {
	if w := os.Getenv(WorkspaceNameEnvVar); w != "" {
		return w
	}
	dir := os.Getenv("TF_DATA_DIR")
	if dir == "" {
		dir = ".terraform"
	}
	if data, err := os.ReadFile(filepath.Join(dir, "environment")); err == nil {
		if w := strings.TrimSpace(string(data)); w != "" {
			return w
		}
	}
	return backend.DefaultStateName
}
