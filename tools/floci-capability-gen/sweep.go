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
// uses), and for each one asks the question a discovery leg actually needs
// answered: not "does ListResources return without erroring" but "does
// ListResources come back carrying an object that exists".
//
// It answers that by round trip: create one resource of the type through
// Cloud Control, wait for the request to settle, then list and look for the
// identifier the create just named. Nothing else can tell the two apart,
// because an emulator whose list handler is a stub answers an empty
// ResourceDescriptions with no error at all - the shape that used to be
// recorded "implemented" and is what this sweep exists to stop claiming.
//
// checked is how many types this run actually had a listable CFN type for,
// so the caller can report it against len(rows) - the two differ only when
// a call errored in a way this function does not classify (a transport
// failure, not an API-shaped response), which it skips rather than guessing
// about.
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
	seeder := newSeeder(endpoint)

	for _, tfType := range identity.AdmittedTypes() {
		cfnType, ok := roster.EnumerationSource(tfType)
		if !ok {
			continue
		}
		checked++

		row, err := classifyListResources(ctx, cc, seeder, tfType, cfnType)
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

// listResourcesSource is the source string every cloudcontrol-list row
// cites. It names the round trip rather than the tool alone, so a row read
// out of the committed manifest carries what kind of evidence it is.
const listResourcesSource = "live probe (tools/floci-capability-gen -mode=cloudcontrol, create/list round trip)"

// classifyListResources decides one type's cloudcontrol-list row. The
// returned error is only ever a transport-level failure the caller should
// skip past; every API-shaped outcome always produces a row.
//
// The five verdicts, and exactly what each one is evidence of:
//
//   - implemented: a resource was created through Cloud Control and the
//     following ListResources named it, carrying a model. The service
//     answers. This is the only path to "implemented" - a bare successful
//     call never reaches it.
//   - partial: the created resource came back in the list with an empty
//     Properties model. Enumeration works; there is nothing in it for a
//     discovery leg to match a configured resource against.
//   - unimplemented: either the router refuses ListResources outright, or
//     the created resource was not in the list it returned. Both leave a
//     discovery leg unable to find what exists, which is the same outcome
//     whichever half is missing, and the evidence text says which.
//   - broken: a response the client cannot parse as Cloud Control's
//     ordinary error shape (the HTML-error-page signature the databases and
//     stragglers cohort READMEs both document for a router-recognized but
//     crashing handler).
//   - unverified: the calls reached a real handler but nothing established
//     that a list answers - most often because no resource of this type
//     could be created to look for. This is the honest verdict for "we do
//     not know", and callers must read it the same way they read an absent
//     row: not a clearance.
//
// What this sweep does NOT see, and no row under mechanism="cloudcontrol-
// list" should be read as covering: whether the API the AWS provider itself
// calls for this type works. aws_cloudwatch_query_definition and
// aws_athena_named_query both answer Cloud Control normally while their own
// native operations return UnsupportedOperation - a gap this probe cannot
// reach, because it never makes the provider's call. Those belong under
// mechanism="" and are hand data (#278). Nor does it cover the types whose
// list handler needs scoping input, which registry.Roster.EnumerationSource
// excludes by construction and internal/live/discovery reaches through
// EnumerationSourceScoped instead (#277).
func classifyListResources(ctx context.Context, cc *cloudcontrol.Client, seeder *ccSeeder, tfType, cfnType string) (typeRow, error) {
	row := func(status, evidence string) typeRow {
		return typeRow{
			Type:      tfType,
			Mechanism: "cloudcontrol-list",
			Status:    status,
			Evidence:  evidence,
			Source:    listResourcesSource,
		}
	}

	before, callErr := cc.ListResources(ctx, cfnType)
	if callErr != nil {
		var apiErr *cloudcontrol.APIError
		if !errors.As(callErr, &apiErr) {
			// Not even an API-shaped error (no HTTP round trip completed) - a
			// transport problem, not a finding.
			return typeRow{}, callErr
		}
		switch {
		case apiErr.Code == cloudcontrol.CodeUnsupportedOperation || apiErr.Code == "UnknownOperationException":
			return row("unimplemented", fmt.Sprintf("ListResources(%s) returns %s: %s", cfnType, apiErr.Code, apiErr.Message)), nil
		case apiErr.Code == "":
			return row("broken", fmt.Sprintf("ListResources(%s) returned HTTP %d with no parseable AWS error body: %s", cfnType, apiErr.StatusCode, apiErr.Message)), nil
		default:
			// A named API error reached a real handler, but it enumerated
			// nothing, so it is no evidence that a discovery leg could find
			// anything through it.
			return row("unverified", fmt.Sprintf("ListResources(%s) reached a handler that answered %s: %s, so it enumerated nothing; no round trip was attempted", cfnType, apiErr.Code, apiErr.Message)), nil
		}
	}

	seed, err := seeder.seed(ctx, cfnType)
	if err != nil {
		return typeRow{}, err
	}
	if !seed.ok() {
		return row("unverified", fmt.Sprintf(
			"ListResources(%s) returned %d resources without erroring, but nothing could be created to prove it answers: %s",
			cfnType, len(before), seed.describe(cfnType))), nil
	}

	after, callErr := cc.ListResources(ctx, cfnType)
	if callErr != nil {
		var apiErr *cloudcontrol.APIError
		if !errors.As(callErr, &apiErr) {
			return typeRow{}, callErr
		}
		return row("unverified", fmt.Sprintf(
			"CreateResource(%s, {}) made a resource, but the ListResources that would have looked for it failed with %s: %s",
			cfnType, apiErr.Code, apiErr.Message)), nil
	}

	// Neither the identifier the emulator generated nor, on the found path,
	// how many other resources the list happened to carry goes into the
	// evidence: both change every run, and this artifact is committed. A
	// re-probe of the same image must produce the same file or the diff
	// stops meaning anything - see the reproducibility note in main.go's
	// package doc, and TestEvidenceDoesNotVaryWithTheGeneratedIdentifier.
	// The count DOES go into the not-found evidence, where zero-versus-many
	// is the difference between a list handler that is a stub and one that
	// answers about some other resource.
	for _, desc := range after {
		if desc.Identifier != seed.identifier {
			continue
		}
		if len(desc.Properties) == 0 {
			// Enumerated, but the description carries no model at all. A
			// discovery leg gets an identifier and nothing to match a
			// configured resource against, which is the Cloud Control form
			// of the blanked-field shape cloudfront's own list-public-keys
			// has (every item back with Name unset, readable only one
			// get-public-key at a time). The list answers; it does not
			// answer enough.
			return row("partial", fmt.Sprintf(
				"CreateResource(%s, {}) made a resource and the following ListResources(%s) enumerated it, but with an empty Properties model - a discovery leg gets the identifier and no attributes to match on",
				cfnType, cfnType)), nil
		}
		return row("implemented", fmt.Sprintf(
			"CreateResource(%s, {}) made a resource and the following ListResources(%s) enumerated it, carrying %d properties - the round trip closed",
			cfnType, cfnType, len(desc.Properties))), nil
	}
	return row("unimplemented", fmt.Sprintf(
		"CreateResource(%s, {}) made a resource but the following ListResources(%s) returned %d resources, none of them the identifier the create had just named - the call succeeds without enumerating what exists",
		cfnType, cfnType, len(after))), nil
}
