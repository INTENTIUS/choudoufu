// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planfile

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
)

// estateOutputsFromPlanFilename is this fork's entry (GitHub issue #2051):
// which other estates' outputs the plan read from their plans rather than
// the record store, and the values it read. Written only when there are
// any, so every other plan file is byte-for-byte what it was. Its content
// is opaque here; internal/command writes and reads it.
const estateOutputsFromPlanFilename = "tfplan-estate-outputs-from-plan.json"

func writeEstateOutputsFromPlanEntry(zw *zip.Writer, src []byte) error {
	if len(src) == 0 {
		return nil
	}
	w, err := zw.CreateHeader(&zip.FileHeader{
		Name:     estateOutputsFromPlanFilename,
		Method:   zip.Deflate,
		Modified: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("failed to create embedded estate outputs record: %w", err)
	}
	if _, err := w.Write(src); err != nil {
		return fmt.Errorf("failed to write embedded estate outputs record: %w", err)
	}
	return nil
}

// ReadEstateOutputsFromPlan returns the #2051 record, or nil when the plan
// read no other estate's outputs from a plan.
func (r *Reader) ReadEstateOutputsFromPlan() ([]byte, error) {
	f, err := r.zip.Open(estateOutputsFromPlanFilename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to extract the estate outputs record from plan file: %w", err)
	}
	defer f.Close()
	return io.ReadAll(f)
}
