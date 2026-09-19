// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"errors"
	"fmt"

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
			"%s %s\n\nThe full error, opening the live block's record_store %q: %s.",
			denied.Headline(), denied.Remedy(), storeType, err,
		))
	}
	return tfdiags.Sourceless(tfdiags.Error, "Cannot open the record store", fmt.Sprintf(
		"The live block's record_store %q could not be opened: %s.", storeType, err,
	))
}
