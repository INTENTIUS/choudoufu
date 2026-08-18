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

// probeCloudControlScoped is probeCloudControl's counterpart for the
// parent-scoped Cloud Control leg (issue #277): every admitted type whose
// registry row makes Cloud Control's list handler require scoping input -
// registry.Roster.EnumerationSourceScoped, the exact complement of
// EnumerationSource - is exactly the population
// internal/live/discovery/cloudcontrol_scoped.go's
// ParentScopedChildSpec/ListResourcesScoped mechanism was built for and
// which the ordinary -mode=cloudcontrol sweep structurally cannot reach: it
// only ever asks EnumerationSource, which by construction excludes every
// type on this leg.
//
// Against a real AWS account, a scoped listing genuinely needs a resolved
// parent value in hand before it can be probed. Against floci specifically,
// it does not: floci's ListResources implementation
// (CloudControlService.listResources, and CloudControlStoreLister beneath
// it) takes only a region and a type name - no ResourceModel parameter
// exists anywhere in its call chain, confirmed by reading
// CloudControlJsonHandler.listResources and CloudControlService.
// listResources directly - so the scope this leg's ResourceModel carries is
// never read on the way to an answer. internal/live/cloudcontrol.Client.
// ListResourcesScoped's own doc comment already states this ("floci's own
// ListResources implementation ignores ResourceModel entirely and returns
// every live resource of typeName unfiltered"); this file is what makes
// that fact load-bearing for a probe rather than only for discovery's own
// client-side identifierMatchesParent safety check.
//
// So the round trip is the unscoped leg's, unchanged: create one resource
// with an empty desired state, then call ListResourcesScoped - with a
// synthetic, placeholder value for every required scoping property, since
// floci never inspects it - and look for the identifier the create just
// named. What this DOES prove: whether the type has real
// Cloud-Control-reachable list support in floci at all, the same fact the
// unscoped leg establishes for its own population - no parent object has
// to be created first for that question to have an answer against this
// emulator. What this does NOT prove, and no row under
// mechanism="cloudcontrol-list-scoped" should be read as covering: whether
// floci's scoped listing actually filters by parent - it does not, for any
// type, so this leg cannot and does not exercise that half of the real
// discovery mechanism. That half is internal/live/discovery's own
// identifierMatchesParent safety net, proven (or not) by that package's own
// tests, never by this one.
func probeCloudControlScoped(ctx context.Context, root, endpoint, region string) (rows []typeRow, checked int, err error) {
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
		cfnType, requiredInput, ok := roster.EnumerationSourceScoped(tfType)
		if !ok {
			continue
		}
		checked++

		row, err := classifyListResourcesScoped(ctx, cc, seeder, tfType, cfnType, requiredInput)
		if err != nil {
			// Same restraint as probeCloudControl: a transport-level
			// failure says nothing about this type, skip rather than guess.
			continue
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Type < rows[j].Type })
	return rows, checked, nil
}

// listResourcesScopedSource is the source string every
// cloudcontrol-list-scoped row cites.
const listResourcesScopedSource = "live probe (tools/floci-capability-gen -mode=cloudcontrol-scoped, create/list round trip)"

// scopeProbeValue is the placeholder scoping value this leg sends for every
// required property. Any non-empty string round-trips identically against
// floci (see the package doc above) - a distinctive value rather than e.g.
// "x" only so a reader of a raw request log can tell which tool sent it.
const scopeProbeValue = "floci-capability-gen-probe"

// classifyListResourcesScoped is classifyListResources's counterpart for
// the scoped leg. Same five verdicts, same meaning, same restraint about
// what an error return means (transport-level only; every API-shaped
// outcome always produces a row) - see classifyListResources's own doc
// comment for the verdict definitions, which this mirrors exactly except
// for the call it makes and what the call's evidence text says it proved.
func classifyListResourcesScoped(ctx context.Context, cc *cloudcontrol.Client, seeder *ccSeeder, tfType, cfnType string, requiredInput []string) (typeRow, error) {
	row := func(status, evidence string) typeRow {
		return typeRow{
			Type:      tfType,
			Mechanism: "cloudcontrol-list-scoped",
			Status:    status,
			Evidence:  evidence,
			Source:    listResourcesScopedSource,
		}
	}

	scope := make(map[string]string, len(requiredInput))
	for _, p := range requiredInput {
		scope[p] = scopeProbeValue
	}

	before, callErr := cc.ListResourcesScoped(ctx, cfnType, scope)
	if callErr != nil {
		var apiErr *cloudcontrol.APIError
		if !errors.As(callErr, &apiErr) {
			return typeRow{}, callErr
		}
		switch {
		case apiErr.Code == cloudcontrol.CodeUnsupportedOperation || apiErr.Code == "UnknownOperationException":
			return row("unimplemented", fmt.Sprintf("ListResourcesScoped(%s, %v) returns %s: %s", cfnType, requiredInput, apiErr.Code, apiErr.Message)), nil
		case apiErr.Code == "":
			return row("broken", fmt.Sprintf("ListResourcesScoped(%s, %v) returned HTTP %d with no parseable AWS error body: %s", cfnType, requiredInput, apiErr.StatusCode, apiErr.Message)), nil
		default:
			return row("unverified", fmt.Sprintf("ListResourcesScoped(%s, %v) reached a handler that answered %s: %s, so it enumerated nothing; no round trip was attempted", cfnType, requiredInput, apiErr.Code, apiErr.Message)), nil
		}
	}

	seed, err := seeder.seed(ctx, cfnType)
	if err != nil {
		return typeRow{}, err
	}
	if !seed.ok() {
		return row("unverified", fmt.Sprintf(
			"ListResourcesScoped(%s, %v) returned %d resources without erroring, but nothing could be created to prove it answers: %s",
			cfnType, requiredInput, len(before), seed.describe(cfnType))), nil
	}

	after, callErr := cc.ListResourcesScoped(ctx, cfnType, scope)
	if callErr != nil {
		var apiErr *cloudcontrol.APIError
		if !errors.As(callErr, &apiErr) {
			return typeRow{}, callErr
		}
		return row("unverified", fmt.Sprintf(
			"CreateResource(%s, {}) made a resource, but the ListResourcesScoped that would have looked for it failed with %s: %s",
			cfnType, apiErr.Code, apiErr.Message)), nil
	}

	for _, desc := range after {
		if desc.Identifier != seed.identifier {
			continue
		}
		if len(desc.Properties) == 0 {
			return row("partial", fmt.Sprintf(
				"CreateResource(%s, {}) made a resource and the following ListResourcesScoped(%s, %v) enumerated it, but with an empty Properties model - a discovery leg gets the identifier and no attributes to match on",
				cfnType, cfnType, requiredInput)), nil
		}
		return row("implemented", fmt.Sprintf(
			"CreateResource(%s, {}) made a resource and the following ListResourcesScoped(%s, %v) enumerated it, carrying %d properties - the round trip closed. Scoped with a synthetic placeholder value because floci's ListResources ignores ResourceModel scoping entirely (internal/live/cloudcontrol.Client.ListResourcesScoped's own doc comment); this establishes the type has real Cloud-Control-reachable list support, not that floci's scoping filter itself works",
			cfnType, cfnType, requiredInput, len(desc.Properties))), nil
	}
	return row("unimplemented", fmt.Sprintf(
		"CreateResource(%s, {}) made a resource but the following ListResourcesScoped(%s, %v) returned %d resources, none of them the identifier the create had just named - the call succeeds without enumerating what exists",
		cfnType, cfnType, requiredInput, len(after))), nil
}
