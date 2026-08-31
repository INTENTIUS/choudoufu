// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// RecordTripLogEnvVar names a file this process appends one line to for every
// operation that reaches the record store — see [staterecord.CountingStore]
// for the line format and for what a "trip" is and costs.
//
// It exists because the measurement that matters is a real `tofu plan`, which
// runs in its own process: an in-process counter can be handed to a Go test
// but cannot see what the binary does, and the binary is what a user runs.
// The counting proxy that measures provider traffic has the same shape for
// the same reason.
//
// Unset — which is every ordinary run — nothing is wrapped and nothing is
// written. Set to a path that cannot be opened, [NewRecordStore] fails
// loudly rather than proceeding uncounted: the variable is only ever set
// deliberately, and an instrument that silently measures nothing reports
// zero, which reads as a spectacular result.
const RecordTripLogEnvVar = "TF_LIVE_RECORD_TRIPS"

var (
	tripLogOnce sync.Once
	tripLog     io.Writer
	tripLogErr  error
)

// wrapForTripLog wraps store in a counting store writing to
// [RecordTripLogEnvVar]'s file, or returns store untouched when the variable
// is unset.
//
// The file is opened once per process and shared by every store built in it
// (a run can build more than one), appended to rather than truncated so a
// second store does not erase the first one's trips, and never closed: each
// line is written with one Write and O_APPEND, so nothing is buffered and a
// run that dies mid-plan still leaves every trip it had made.
func wrapForTripLog(store staterecord.Store) (staterecord.Store, error) {
	path := os.Getenv(RecordTripLogEnvVar)
	if path == "" {
		return store, nil
	}
	tripLogOnce.Do(func() {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // a path an operator set deliberately for a measurement
		if err != nil {
			tripLogErr = fmt.Errorf("%s=%q could not be opened for the record-store trip log: %w", RecordTripLogEnvVar, path, err)
			return
		}
		tripLog = &lockedWriter{w: f}
	})
	if tripLogErr != nil {
		return nil, tripLogErr
	}
	return staterecord.NewCountingStore(store, tripLog), nil
}

// lockedWriter serializes writes from the several counting stores and the
// several goroutines that share one trip-log file.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
