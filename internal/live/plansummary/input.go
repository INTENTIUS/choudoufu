// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Plan is the part of stock OpenTofu's machine-readable plan (the object
// `tofu show -json <planfile>` prints) this package reads. Every other field
// is ignored.
type Plan struct {
	FormatVersion   string           `json:"format_version"`
	Errored         bool             `json:"errored"`
	ResourceChanges []ResourceChange `json:"resource_changes"`
}

// ResourceChange is one element of a plan's resource_changes.
type ResourceChange struct {
	Address string `json:"address"`
	Deposed string `json:"deposed,omitempty"`
	Change  Change `json:"change"`
}

// Change is a resource change's "change" object.
type Change struct {
	Actions      []string        `json:"actions"`
	Before       json.RawMessage `json:"before,omitempty"`
	After        json.RawMessage `json:"after,omitempty"`
	AfterUnknown json.RawMessage `json:"after_unknown,omitempty"`
	ReplacePaths json.RawMessage `json:"replace_paths,omitempty"`
}

// SetDocument is the set plan's -json document (GitHub issue #1752): one
// element per estate root. Only these fields are read.
type SetDocument struct {
	Roots []SetRoot `json:"roots"`
}

// SetRoot is one root of a set document.
type SetRoot struct {
	// Root is the root's directory, relative.
	Root string `json:"root"`
	// Estate is the estate name the root plans.
	Estate string `json:"estate"`
	// Status is "planned" or "failed". Anything but "planned" is a failure.
	Status string `json:"status"`
	// Error is why the root failed.
	Error string `json:"error,omitempty"`
	// Plan is the root's stock machine-readable plan.
	Plan *Plan `json:"plan,omitempty"`
}

// Input is a parsed document: exactly one of Set and Plan is non-nil.
type Input struct {
	Set  *SetDocument
	Plan *Plan
}

// Parse reads either a set document (a JSON object with a top-level
// "roots" array) or a bare stock plan (one with "resource_changes" or
// "format_version").
func Parse(data []byte) (Input, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Input{}, fmt.Errorf("not a JSON object: %w", err)
	}
	dec := func(v any) error {
		d := json.NewDecoder(bytes.NewReader(data))
		return d.Decode(v)
	}
	if _, ok := top["roots"]; ok {
		var doc SetDocument
		if err := dec(&doc); err != nil {
			return Input{}, fmt.Errorf("reading the set document: %w", err)
		}
		return Input{Set: &doc}, nil
	}
	_, hasChanges := top["resource_changes"]
	_, hasVersion := top["format_version"]
	if hasChanges || hasVersion {
		var p Plan
		if err := dec(&p); err != nil {
			return Input{}, fmt.Errorf("reading the plan: %w", err)
		}
		return Input{Plan: &p}, nil
	}
	return Input{}, fmt.Errorf(`neither a set document (no top-level "roots") nor a plan (no "resource_changes" or "format_version")`)
}
