// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// recordReadParallelismEnvVar sets how many of the "s3" record store's
// GetObject calls a bulk read of the estate's records has in flight at once.
// Unset means [staterecord.DefaultS3GetAllParallelism], which is 8; 1 is the
// sequential read. GitHub issue #1336.
//
// # Why 8, and why it can be changed
//
// The record namespace is the record-backed slice of an estate only, a small
// fraction of it, so the bound is not load-bearing and 8 was chosen to be
// unremarkable rather than tuned. It can be changed because the estate that
// needs otherwise will know why, and a fixed bound would make them patch the
// binary to find out.
//
// # Why an environment variable, and why here
//
// The maintainer's ruling on #1336, and the convention [readParallelismEnvVar]
// and [sweepParallelismEnvVar] already record. A parallelism bound is a lever
// for a run that is misbehaving - a throttled account, a slow link - and not
// a decision a team checks in and reviews the way estate and record_store
// are, so it does not belong in the record_store block. And it cannot be a
// flag: a configuration carrying a live block runs under plain "plan" and
// "apply", which parse the stock flag set, so a flag registered on live-plan
// would die in the delegate as "flag provided but not defined". One variable
// covers every command that opens the store.
//
// It bounds the BULK read only. A single record's Get, and every write, is
// one request whatever this says.
const recordReadParallelismEnvVar = "TOFU_LIVE_RECORD_READ_PARALLELISM"

// recordStoreOpenOptions is what every caller of [projection.NewRecordStore]
// passes, so the four of them cannot come to read the environment
// differently. An invalid value is an error for the caller to report the way
// it reports a store it could not open: a run that silently fell back to the
// default would be measuring something other than what the operator set.
func recordStoreOpenOptions() ([]projection.RecordStoreOption, error) {
	raw := strings.TrimSpace(os.Getenv(recordReadParallelismEnvVar))
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is how many of the record store's bulk-read requests run at once, so it takes a whole number; %q is not one. Unset it to take the default of %d", recordReadParallelismEnvVar, raw, staterecord.DefaultS3GetAllParallelism)
	}
	if n < 1 {
		return nil, fmt.Errorf("%s must be at least 1, not %d: 1 is the sequential read, and unsetting it takes the default of %d", recordReadParallelismEnvVar, n, staterecord.DefaultS3GetAllParallelism)
	}
	return []projection.RecordStoreOption{projection.WithBulkReadParallelism(n)}, nil
}
