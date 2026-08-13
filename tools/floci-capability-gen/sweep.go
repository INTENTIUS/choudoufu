// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// probeCloudControl sweeps every admitted type (internal/live/identity.
// AdmittedTypes) that live/mapping.json + live/registry.json's join says is
// listable through Cloud Control (registry.Roster.EnumerationSource - the
// same join internal/live/discovery's own enumeration-source selection
// uses), calling ListResources for each exactly the way
// tools/cloudcontrol-probe does by hand for one type at a time. checked is
// how many types this run actually had a listable CFN type for, so the
// caller can report it against len(rows) - the two differ only when a call
// errored in a way this function does not classify (a transport failure,
// not an API-shaped response), which it skips rather than guessing about.
func probeCloudControl(ctx context.Context, root, endpoint, region string) (rows []typeRow, checked int, err error) {
	roster, err := registry.Load(
		filepath.Join(root, "live", "mapping.json"),
		filepath.Join(root, "live", "registry.json"),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("loading live/mapping.json and live/registry.json: %w", err)
	}

	cc := cloudcontrol.New(cloudcontrol.Config{
		Endpoint:     endpoint,
		Region:       region,
		RoundTripper: http.DefaultTransport,
	})

	for _, tfType := range identity.AdmittedTypes() {
		cfnType, ok := roster.EnumerationSource(tfType)
		if !ok {
			continue
		}
		checked++

		row, err := classifyListResources(ctx, cc, tfType, cfnType)
		if err != nil {
			// A transport-level failure (floci unreachable mid-sweep, a
			// timeout) says nothing about this type's own support - skip it
			// rather than record a guess, and let the caller's own -endpoint
			// reachability have already been established by whichever probe
			// ran first.
			continue
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Type < rows[j].Type })
	return rows, checked, nil
}

// classifyListResources makes one ListResources call and turns its outcome
// into a typeRow under the "cloudcontrol-list" mechanism. The returned error
// is only ever a transport-level failure the caller should skip past; every
// API-shaped outcome (success, or any *cloudcontrol.APIError) always
// produces a row.
func classifyListResources(ctx context.Context, cc *cloudcontrol.Client, tfType, cfnType string) (typeRow, error) {
	const source = "live probe (tools/floci-capability-gen -mode=cloudcontrol)"

	_, callErr := cc.ListResources(ctx, cfnType)
	if callErr == nil {
		return typeRow{
			Type:      tfType,
			Mechanism: "cloudcontrol-list",
			Status:    "implemented",
			Evidence:  fmt.Sprintf("ListResources(%s) succeeded", cfnType),
			Source:    source,
		}, nil
	}

	var apiErr *cloudcontrol.APIError
	if !errors.As(callErr, &apiErr) {
		// Not even an API-shaped error (no HTTP round trip completed) - a
		// transport problem, not a finding.
		return typeRow{}, callErr
	}

	if apiErr.Code == cloudcontrol.CodeUnsupportedOperation || apiErr.Code == "UnknownOperationException" {
		return typeRow{
			Type:      tfType,
			Mechanism: "cloudcontrol-list",
			Status:    "unimplemented",
			Evidence:  fmt.Sprintf("ListResources(%s) returns %s: %s", cfnType, apiErr.Code, apiErr.Message),
			Source:    source,
		}, nil
	}

	if apiErr.Code == "" {
		// HTTP round-tripped but the body was not the ordinary AWS JSON
		// error shape - the HTML-error-page signature the databases and
		// stragglers cohort READMEs both document for a router-recognized
		// handler that then crashes, rather than one absent outright.
		return typeRow{
			Type:      tfType,
			Mechanism: "cloudcontrol-list",
			Status:    "broken",
			Evidence:  fmt.Sprintf("ListResources(%s) returned HTTP %d with no parseable AWS error body: %s", cfnType, apiErr.StatusCode, apiErr.Message),
			Source:    source,
		}, nil
	}

	// Any other named API error (ValidationException, ResourceNotFoundException,
	// a required-input complaint, ...) reached a real handler that answered
	// in its own ordinary shape - implemented, whatever this particular call
	// needed that a bare ListResources did not supply.
	return typeRow{
		Type:      tfType,
		Mechanism: "cloudcontrol-list",
		Status:    "implemented",
		Evidence:  fmt.Sprintf("ListResources(%s) reached a real handler, answering %s: %s", cfnType, apiErr.Code, apiErr.Message),
		Source:    source,
	}, nil
}
