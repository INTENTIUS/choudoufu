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
	"regexp"
	"sort"
	"strings"
)

// healthResponse mirrors floci's (and upstream LocalStack's) own
// /_localstack/health shape closely enough for this tool's purpose: a flat
// service-name -> status-string map. live/e2e/estates/stragglers/README.md's
// "Floci coverage" section reads this exact endpoint by hand
// ("`/_localstack/health` lists which services it implements at all").
//
// That claim is weaker than it sounds (issue #276): floci's own
// HealthController just returns ServiceRegistry.getServices(), and that
// method's status string is descriptor.enabled() - a config-time flag set
// at startup, never anything dynamically probed. A service can be
// "running" here and still answer nothing for any operation floci hasn't
// actually implemented, and #279 already found the identical defect one
// layer down (Cloud Control's ListResources "the call returned" verdict).
// probeOneService below is the fix at this grain: a live, cheap,
// side-effect-free call per service, not a trust in what this struct says.
type healthResponse struct {
	Services map[string]string `json:"services"`
}

const servicesSource = "live probe (tools/floci-capability-gen -mode=services)"

// fetchHealth GETs endpoint's /_localstack/health and decodes it.
func fetchHealth(ctx context.Context, endpoint string) (healthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/_localstack/health", nil)
	if err != nil {
		return healthResponse{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return healthResponse{}, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return healthResponse{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var health healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return healthResponse{}, fmt.Errorf("decoding the health response: %w", err)
	}
	return health, nil
}

// probeServices GETs endpoint's /_localstack/health and returns one
// serviceRow per service in the watchlist, which is now self-expanding
// (issue #276): every name the health response itself lists, union
// extraWatch (the -watch flag). The previous version seeded the watchlist
// from the manifest's own past output (manifestArtifact.allKnownServices)
// instead - every service already carrying a row - so a service the
// response named for the first time was never recorded; the manifest was
// capped at whatever four services a past run had already found, forever.
// extraWatch stays useful for the opposite, smaller case: a service that
// is not in the health response at all, where the only thing to record is
// its absence.
//
// A service the response DOES name gets a live round trip
// (probeOneService), not a row for merely being present - see
// [healthResponse]'s doc comment for why presence alone is not evidence a
// service answers anything.
func probeServices(ctx context.Context, endpoint string, run awsRunner, extraWatch map[string]bool) ([]serviceRow, error) {
	health, err := fetchHealth(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	watchlist := map[string]bool{}
	for name := range health.Services {
		watchlist[name] = true
	}
	for name := range extraWatch {
		watchlist[name] = true
	}

	var names []string
	for name := range watchlist {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]serviceRow, 0, len(names))
	for _, name := range names {
		status, present := health.Services[name]
		if !present {
			rows = append(rows, serviceRow{
				Service:  name,
				Status:   "unimplemented",
				Evidence: "absent from /_localstack/health's service list entirely",
				Source:   servicesSource,
			})
			continue
		}
		rows = append(rows, probeOneService(ctx, run, name, status, serviceProbes))
	}
	return rows, nil
}

// awsErrorCodeRE extracts the AWS error code the aws CLI prints for an
// API-shaped failure: "An error occurred (CODE) when calling the X
// operation: message". A CLI-level failure (unknown top-level command,
// missing required argument) never reaches this shape at all - the CLI
// rejects the invocation itself before making any HTTP call - which is
// exactly the distinction probeOneService needs to tell "floci refused
// this operation" from "this candidate could not even be attempted".
var awsErrorCodeRE = regexp.MustCompile(`An error occurred \(([^)]+)\)`)

// numericCodeRE matches an HTTP status standing in for an AWS error code -
// the shape classifyListResources (sweep.go) calls "broken": a router that
// recognized the request enough to route it, then crashed instead of
// answering in floci's ordinary AWS-JSON error shape.
var numericCodeRE = regexp.MustCompile(`^[0-9]+$`)

// truncate bounds how much of a CLI's own output or error text lands in a
// manifest row's Evidence field - readable, but never the whole body.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// probeOneService is issue #276's actual fix: floci naming a service in
// /_localstack/health is a config-time claim, not a live one (see
// [healthResponse]'s doc comment), so this makes one real call instead of
// trusting it - the same "a call that returns is not a service that
// answers" standard #279 established for cloudcontrol-list, applied here
// because the health endpoint's own claim never made a live call at all.
//
// serviceProbes (servicecalls.go) names, for most services in this
// checkout's health response, a ranked list of candidate operations with
// no required input - every one a real, side-effect-free List/Describe/Get
// call. probeOneService tries them in order via the aws CLI against
// endpoint and stops at the first one that reaches a real handler,
// otherwise keeps going through every remaining candidate before settling
// on a verdict - a single crashing or refused operation does not mean the
// whole service is unreachable:
//
//   - a clean response, or any named AWS error code other than
//     UnsupportedOperation/UnknownOperationException (an
//     access-denied, validation, or not-found error still means the
//     request reached a real handler that answered in its own shape) ->
//     implemented immediately, the call and its outcome as evidence.
//   - the candidate list exhausted with at least one HTTP status standing
//     in for an AWS error code (no parseable AWS-JSON error body - a
//     router-recognized-but-crashing handler) among the attempts -> broken,
//     preferred over unimplemented when both were seen since it is the
//     more actionable finding.
//   - the candidate list exhausted with only
//     UnsupportedOperation/UnknownOperationException refusals ->
//     unimplemented, every refusal cited as evidence.
//   - every candidate failed before ever producing an AWS-shaped response
//     at all (a CLI-command-name problem, not a floci one) -> unverified,
//     rather than guessing which side is wrong.
//
// A service present in the health response with no entry in serviceProbes
// at all (five, as of the table's own derivation: appconfigdata,
// application-autoscaling, cognito-idp, rds-data, swf - none of their own
// APIs has a zero-required-param List/Describe/Get call to try) is
// unverified too: an honest "could not check" rather than trusting the
// health endpoint's bare claim, which is exactly the weak signal this
// function exists to stop trusting.
func probeOneService(ctx context.Context, run awsRunner, name, healthStatus string, probes map[string]serviceProbe) serviceRow {
	probe, ok := probes[name]
	if !ok {
		return serviceRow{
			Service:  name,
			Status:   "unverified",
			Evidence: fmt.Sprintf("present in /_localstack/health (status %q), but no side-effect-free operation with zero required input is known for this service's own API - see servicecalls.go", healthStatus),
			Source:   servicesSource,
		}
	}

	var refusals []string
	var brokenSightings []string
	var nonAWSFailures int
	for _, cand := range probe.candidates {
		out, err := run(ctx, probe.cliCommand, cand)
		if err == nil {
			return serviceRow{
				Service:  name,
				Status:   "implemented",
				Evidence: fmt.Sprintf("%s %s succeeded: %s", probe.cliCommand, cand, truncate(out, 200)),
				Source:   servicesSource,
			}
		}

		m := awsErrorCodeRE.FindStringSubmatch(err.Error())
		if m == nil {
			// Never reached floci as an AWS-shaped call at all (a CLI
			// argument-parsing failure, a transport error) - not evidence
			// about the service, so it does not count as a refusal.
			nonAWSFailures++
			continue
		}
		code := m[1]
		if code == "UnsupportedOperation" || code == "UnknownOperationException" {
			refusals = append(refusals, fmt.Sprintf("%s %s -> %s", probe.cliCommand, cand, code))
			continue
		}
		if numericCodeRE.MatchString(code) {
			// A router-recognized-but-crashing handler for this one
			// candidate does not mean the whole service is broken - keep
			// trying the rest before settling, the same way a refusal
			// does not stop the loop either.
			brokenSightings = append(brokenSightings, fmt.Sprintf("%s %s -> HTTP %s", probe.cliCommand, cand, code))
			continue
		}
		return serviceRow{
			Service:  name,
			Status:   "implemented",
			Evidence: fmt.Sprintf("%s %s reached a real handler, answering %s: %s", probe.cliCommand, cand, code, truncate(err.Error(), 200)),
			Source:   servicesSource,
		}
	}

	// Every candidate was tried and none reached a real answering handler.
	// Prefer reporting a genuine router bug (broken) over a bare refusal
	// (unimplemented) when both were seen, since "broken" is the more
	// actionable finding - and prefer either over unverified, which only
	// applies when nothing here ever produced an AWS-shaped response at
	// all.
	if len(brokenSightings) > 0 {
		return serviceRow{
			Service:  name,
			Status:   "broken",
			Evidence: fmt.Sprintf("every candidate operation either was refused or returned no parseable AWS error body: %s", strings.Join(append(append([]string{}, brokenSightings...), refusals...), "; ")),
			Source:   servicesSource,
		}
	}
	if len(refusals) == 0 {
		return serviceRow{
			Service:  name,
			Status:   "unverified",
			Evidence: fmt.Sprintf("none of %d candidate operation(s) ever reached floci as an AWS-shaped call (%d non-AWS failure(s)) - likely a CLI command-name mismatch in servicecalls.go, not a floci gap", len(probe.candidates), nonAWSFailures),
			Source:   servicesSource,
		}
	}
	return serviceRow{
		Service:  name,
		Status:   "unimplemented",
		Evidence: fmt.Sprintf("every candidate operation was refused: %s", strings.Join(refusals, "; ")),
		Source:   servicesSource,
	}
}
