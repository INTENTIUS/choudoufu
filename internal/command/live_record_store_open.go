// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// recordStoreOpenDiag is the error for a record store that could not be
// opened. A refusal by the bucket's KMS key gets its own summary (GitHub
// issue #1345): it arrives as AccessDenied on an S3 operation, and a summary
// that says only "cannot open the record store" sends the reader to the
// bucket, which is usually fine.
func recordStoreOpenDiag(storeType string, err error) tfdiags.Diagnostic {
	var denied *staterecord.KMSDeniedError
	if errors.As(err, &denied) {
		return tfdiags.Sourceless(tfdiags.Error, "The record store bucket's KMS key refused this run", fmt.Sprintf(
			"%s %s\n\nWhat S3 said, opening the live block's record_store %q: %s.",
			denied.Headline(), denied.Remedy(), storeType, denied.Err,
		))
	}
	return tfdiags.Sourceless(tfdiags.Error, "Cannot open the record store", fmt.Sprintf(
		"The live block's record_store %q could not be opened: %s.", storeType, err,
	))
}

// SummaryRecordStoreNotRead is the warning `live-plan` and `live-mv` raise
// when the record store could not be reached and they went on without it.
const SummaryRecordStoreNotRead = "The record store was not read"

// recordStoreOpener is [projection.NewRecordStore]'s signature, a seam so a
// test can make the store fail in each of the ways that matter.
type recordStoreOpener func(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, moduleDir string, opts ...projection.RecordStoreOption) (staterecord.Store, error)

// openRecordStoreAsOneMoreSource opens the estate's record store for the two
// commands that treat it as one more source and not as a precondition:
// `live-plan`, which previews, and `live-mv`, for which a record is one more
// way to find a resource. `plan`, `apply` and `live-import` do not come
// through here. For them any failure to open is fatal.
//
// The rule, from the maintainer's ruling on GitHub issue #1376:
//
//   - A REFUSAL is fatal here too: a bucket that failed its contract on first
//     contact, a store whose List is broken, a KMS key that refused the run,
//     an invalid TOFU_LIVE_RECORD_READ_PARALLELISM. A refused bucket is one the
//     estate should not be planned against at all, and before this function
//     existed these two commands wrote it to the log and carried on.
//   - An OUTAGE (unreachable, or a role IAM would not let in) is not fatal,
//     and it is not quiet either. The command goes on without records and
//     says so as a Warning a person sees, with what that does to its output.
//     A `live-plan` under a read-only role stays usable that way (#1370).
//   - A waiver is announced on this path like every other (#1340).
//
// A nil store with no error diagnostics means "go on without it".
func openRecordStoreAsOneMoreSource(ctx context.Context, open recordStoreOpener, rs *configs.LiveRecordStore, rt *configs.LiveRetry, estate, command string) (staterecord.Store, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if rs == nil {
		return nil, diags
	}
	storeOpts, err := recordStoreOpenOptions()
	if err != nil {
		// A bad value is the operator's to fix, and going on with a default
		// they did not choose would hide that they had set anything.
		return nil, diags.Append(recordStoreOpenDiag(rs.Type, err))
	}
	store, err := open(ctx, rs, rt, estate, ".", storeOpts...)
	if err != nil {
		if projection.IsStoreRefusal(err) {
			return nil, diags.Append(recordStoreOpenDiag(rs.Type, err))
		}
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryRecordStoreNotRead, fmt.Sprintf(
			"%s could not open the live block's record_store %q and went on without it: %s.\n\nNothing this run shows came from a record. A record-backed resource (one with no cloud object to carry a marker, such as terraform_data or random_pet) is known only by its record, so it may appear here as something to create when it already exists, and guided discovery had no hint to narrow its sweep.\n\nA kubernetes_manifest is affected the other way, and more quietly. Its record holds which labels and annotations this estate declared, which is the only way to tell a key the configuration dropped from one somebody added with kubectl. Without the record, a label or annotation removed from the configuration will not be planned for removal, so this output can read \"No changes\" for a manifest whose live object still carries it. Its field_manager block may also show as a change that is not one.\n\n`plan` and `apply` do not go on without the store; they stop.",
			command, rs.Type, err,
		)))
	}
	return store, diags.Append(bucketWaiverWarnings(rs))
}
