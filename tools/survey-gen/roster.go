// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"strings"
)

// readRoster parses tools/survey-gen/roster.txt: one resource type name per
// line, blank lines and #-comments ignored. It is strict on purpose - a
// duplicate or a cell that is not a type name is an error rather than a
// silently different roster.
func readRoster(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsAny(line, " \t|`") || !strings.Contains(line, "_") {
			return nil, fmt.Errorf("%s:%d: %q is not a resource type name", path, i+1, line)
		}
		if seen[line] {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", path, i+1, line)
		}
		seen[line] = true
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no resource types", path)
	}
	return out, nil
}
