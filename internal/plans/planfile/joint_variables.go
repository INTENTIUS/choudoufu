// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planfile

import (
	"archive/zip"
	"bytes"
	"fmt"

	"github.com/intentius/choudoufu/internal/plans"
)

// The joint run's per-unit plan files (INTENTIUS/choudoufu#2270, under
// #2213). Each unit's plan file carries the joint plan, and the joint plan
// holds every unit's input variable values. A unit's file leaves out the
// sensitive inputs of the other units, which are put back from their own
// files at apply. These two functions are the plan-file half of that: read
// the variable values a plan file holds, and copy a plan file with its
// variable values edited and every other entry unchanged. Both take the
// unencrypted bytes of a plan file, which is what the joint run writes.

// ReadVariableValues returns the input variable values the plan file in
// raw holds.
func ReadVariableValues(raw []byte) (map[string]plans.DynamicValue, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != tfplanFilename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		plan, err := readPlan(rc)
		if err != nil {
			return nil, err
		}
		return plan.VariableValues, nil
	}
	return nil, fmt.Errorf("the given file is not a valid plan file")
}

// RewriteVariableValues returns a copy of the plan file in raw whose plan
// has its variable values changed by edit. A name edit deletes is dropped
// from the plan's ephemeral set as well. Every other entry of the file is
// copied as it is.
func RewriteVariableValues(raw []byte, edit func(map[string]plans.DynamicValue)) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	found := false
	for _, f := range zr.File {
		if f.Name != tfplanFilename {
			if err := zw.Copy(f); err != nil {
				return nil, err
			}
			continue
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		plan, err := readPlan(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		if plan.VariableValues == nil {
			plan.VariableValues = map[string]plans.DynamicValue{}
		}
		before := make(map[string]bool, len(plan.VariableValues))
		for name := range plan.VariableValues {
			before[name] = true
		}
		edit(plan.VariableValues)
		for name := range before {
			if _, ok := plan.VariableValues[name]; !ok {
				delete(plan.EphemeralVariables, name)
			}
		}
		for name := range plan.VariableValues {
			if _, ok := plan.EphemeralVariables[name]; !ok {
				plan.EphemeralVariables[name] = false
			}
		}
		// writePlan records an ephemeral variable's name only for a name
		// VariableValues holds, and readPlan leaves ephemeral ones out of
		// it, so they are put back here to survive the copy.
		for name, is := range plan.EphemeralVariables {
			if _, ok := plan.VariableValues[name]; is && !ok {
				plan.VariableValues[name] = nil
			}
		}
		if err := writePlanEntry(zw, plan); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, fmt.Errorf("the given file is not a valid plan file")
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
