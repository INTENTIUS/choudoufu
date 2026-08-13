// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// healthResponse mirrors floci's (and upstream LocalStack's) own
// /_localstack/health shape closely enough for this tool's purpose: a flat
// service-name -> status-string map. live/e2e/estates/stragglers/README.md's
// "Floci coverage" section reads this exact endpoint by hand
// ("`/_localstack/health` lists which services it implements at all").
type healthResponse struct {
	Services map[string]string `json:"services"`
}

// probeServices GETs endpoint's /_localstack/health and returns one
// serviceRow per service in watchlist: implemented (citing the reported
// status string) for every one the response names, unimplemented (citing
// its absence) for every one it does not.
func probeServices(ctx context.Context, endpoint string, watchlist map[string]bool) ([]serviceRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/_localstack/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var health healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, fmt.Errorf("decoding the health response: %w", err)
	}

	var names []string
	for name := range watchlist {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]serviceRow, 0, len(names))
	for _, name := range names {
		status, present := health.Services[name]
		if present {
			rows = append(rows, serviceRow{
				Service:  name,
				Status:   "implemented",
				Evidence: fmt.Sprintf("present in /_localstack/health, reported status %q", status),
				Source:   "live probe (tools/floci-capability-gen -mode=services)",
			})
			continue
		}
		rows = append(rows, serviceRow{
			Service:  name,
			Status:   "unimplemented",
			Evidence: "absent from /_localstack/health's service list entirely",
			Source:   "live probe (tools/floci-capability-gen -mode=services)",
		})
	}
	return rows, nil
}
