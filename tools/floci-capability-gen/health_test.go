// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeRunner returns an awsRunner scripted by call sequence: results[i] is
// returned for the i-th invocation, wrapping around if there are more
// calls than scripted results is never expected to happen in a test.
func fakeRunner(t *testing.T, results ...struct {
	out string
	err error
}) awsRunner {
	t.Helper()
	i := 0
	return func(_ context.Context, args ...string) (string, error) {
		if i >= len(results) {
			t.Fatalf("fakeRunner called more times (%d) than scripted (%d); args=%v", i+1, len(results), args)
		}
		r := results[i]
		i++
		return r.out, r.err
	}
}

func awsErr(code, op, message string) error {
	return fmt.Errorf("aws %s: exit status 254: aws: [ERROR]: An error occurred (%s) when calling the %s operation: %s", op, code, op, message)
}

func TestProbeOneServiceImplementedOnFirstCandidateSuccess(t *testing.T) {
	probes := map[string]serviceProbe{
		"widgets": {cliCommand: "widgets", candidates: []string{"list-widgets", "describe-widgets"}},
	}
	run := fakeRunner(t, struct {
		out string
		err error
	}{out: `{"Widgets":[]}`, err: nil})

	row := probeOneService(context.Background(), run, "widgets", "running", probes)
	if row.Status != "implemented" {
		t.Fatalf("Status = %q, want implemented; evidence=%q", row.Status, row.Evidence)
	}
}

func TestProbeOneServiceFallsThroughToLaterCandidate(t *testing.T) {
	probes := map[string]serviceProbe{
		"widgets": {cliCommand: "widgets", candidates: []string{"list-widgets-in-recycle-bin", "describe-widgets"}},
	}
	run := fakeRunner(t,
		struct {
			out string
			err error
		}{err: awsErr("UnsupportedOperation", "ListWidgetsInRecycleBin", "not supported")},
		struct {
			out string
			err error
		}{out: `{"Widgets":[]}`},
	)

	row := probeOneService(context.Background(), run, "widgets", "running", probes)
	if row.Status != "implemented" {
		t.Fatalf("Status = %q, want implemented (second candidate should have been tried); evidence=%q", row.Status, row.Evidence)
	}
}

func TestProbeOneServiceImplementedOnNamedAWSError(t *testing.T) {
	// A real, service-specific error (not an operation-not-recognized
	// refusal) still means a real handler answered - the same rule
	// classifyListResources (sweep.go) applies at the type grain.
	probes := map[string]serviceProbe{
		"wafv2": {cliCommand: "wafv2", candidates: []string{"get-web-acl"}},
	}
	run := fakeRunner(t, struct {
		out string
		err error
	}{err: awsErr("WAFInvalidParameterException", "GetWebACL", "Name is required")})

	row := probeOneService(context.Background(), run, "wafv2", "running", probes)
	if row.Status != "implemented" {
		t.Fatalf("Status = %q, want implemented; evidence=%q", row.Status, row.Evidence)
	}
}

func TestProbeOneServiceUnimplementedWhenEveryCandidateRefused(t *testing.T) {
	probes := map[string]serviceProbe{
		"widgets": {cliCommand: "widgets", candidates: []string{"list-widgets", "describe-widgets"}},
	}
	run := fakeRunner(t,
		struct {
			out string
			err error
		}{err: awsErr("UnsupportedOperation", "ListWidgets", "nope")},
		struct {
			out string
			err error
		}{err: awsErr("UnknownOperationException", "DescribeWidgets", "nope")},
	)

	row := probeOneService(context.Background(), run, "widgets", "running", probes)
	if row.Status != "unimplemented" {
		t.Fatalf("Status = %q, want unimplemented; evidence=%q", row.Status, row.Evidence)
	}
}

func TestProbeOneServiceBrokenOnUnparseableErrorBody(t *testing.T) {
	probes := map[string]serviceProbe{
		"widgets": {cliCommand: "widgets", candidates: []string{"list-widgets"}},
	}
	run := fakeRunner(t, struct {
		out string
		err error
	}{err: awsErr("404", "ListWidgets", "<html><body>Resource not found</body></html>")})

	row := probeOneService(context.Background(), run, "widgets", "running", probes)
	if row.Status != "broken" {
		t.Fatalf("Status = %q, want broken; evidence=%q", row.Status, row.Evidence)
	}
}

func TestProbeOneServiceUnverifiedWhenNoTableEntry(t *testing.T) {
	row := probeOneService(context.Background(), nil, "some-brand-new-service", "running", map[string]serviceProbe{})
	if row.Status != "unverified" {
		t.Fatalf("Status = %q, want unverified", row.Status)
	}
}

func TestProbeOneServiceUnverifiedWhenNoCandidateEverReachesAWS(t *testing.T) {
	probes := map[string]serviceProbe{
		"widgets": {cliCommand: "wigdets-typo", candidates: []string{"list-widgets"}},
	}
	run := fakeRunner(t, struct {
		out string
		err error
	}{err: fmt.Errorf(`aws wigdets-typo list-widgets: exit status 252: usage: aws [options] <command> <subcommand> [parameters]
aws: error: argument command: Invalid choice, valid choices are: ...`)})

	row := probeOneService(context.Background(), run, "widgets", "running", probes)
	if row.Status != "unverified" {
		t.Fatalf("Status = %q, want unverified (never reached an AWS-shaped response); evidence=%q", row.Status, row.Evidence)
	}
}

// TestProbeServicesWatchlistIsSelfExpanding pins issue #276's actual fix: a
// service the health response names for the first time (never seen before,
// nothing pre-seeding a watchlist) still gets probed and recorded. The
// previous version seeded the watchlist from the manifest's own past
// output, which could never grow past whatever a prior run had already
// found.
func TestProbeServicesWatchlistIsSelfExpanding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_localstack/health" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": map[string]string{
				"brandnew": "running", // never recorded anywhere before this run
			},
		})
	}))
	defer server.Close()

	probesBackup := serviceProbes
	serviceProbes = map[string]serviceProbe{
		"brandnew": {cliCommand: "brandnew", candidates: []string{"list-things"}},
	}
	defer func() { serviceProbes = probesBackup }()

	run := fakeRunner(t, struct {
		out string
		err error
	}{out: "{}"})

	rows, err := probeServices(context.Background(), server.URL, run, nil)
	if err != nil {
		t.Fatalf("probeServices: %v", err)
	}
	if len(rows) != 1 || rows[0].Service != "brandnew" {
		t.Fatalf("rows = %+v, want exactly one row for the newly-named service", rows)
	}
	if rows[0].Status != "implemented" {
		t.Errorf("Status = %q, want implemented", rows[0].Status)
	}
}

// TestProbeServicesWatchRecordsAbsenceForAServiceHealthDoesNotName pins
// -watch's remaining purpose after #276: a service that is not in the
// health response at all still gets a row, recording its absence - the
// live round trip never runs for it, since there is nothing present to
// probe.
func TestProbeServicesWatchRecordsAbsenceForAServiceHealthDoesNotName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": map[string]string{"present": "running"},
		})
	}))
	defer server.Close()

	probesBackup := serviceProbes
	serviceProbes = map[string]serviceProbe{
		"present": {cliCommand: "present", candidates: []string{"list-things"}},
	}
	defer func() { serviceProbes = probesBackup }()

	run := fakeRunner(t, struct {
		out string
		err error
	}{out: "{}"})

	rows, err := probeServices(context.Background(), server.URL, run, map[string]bool{"absent-service": true})
	if err != nil {
		t.Fatalf("probeServices: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want 2 (present + watched-but-absent)", rows)
	}
	byName := map[string]serviceRow{}
	for _, r := range rows {
		byName[r.Service] = r
	}
	if got := byName["absent-service"]; got.Status != "unimplemented" {
		t.Errorf("absent-service status = %q, want unimplemented", got.Status)
	}
	if got := byName["present"]; got.Status != "implemented" {
		t.Errorf("present status = %q, want implemented", got.Status)
	}
}

func TestFetchHealthHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if _, err := fetchHealth(context.Background(), server.URL); err == nil {
		t.Fatal("expected an error for a non-200 health response, got nil")
	}
}
