// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeServicesPresentAndAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_localstack/health" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": map[string]string{
				"transfer": "running",
				"ecr":      "running",
				"s3":       "running",
			},
		})
	}))
	defer server.Close()

	watchlist := map[string]bool{"transfer": true, "networkmanager": true, "storagegateway": true}
	rows, err := probeServices(context.Background(), server.URL, watchlist)
	if err != nil {
		t.Fatalf("probeServices: %v", err)
	}
	if len(rows) != len(watchlist) {
		t.Fatalf("got %d rows, want %d (one per watched service, s3 excluded since unwatched)", len(rows), len(watchlist))
	}

	byService := map[string]serviceRow{}
	for _, row := range rows {
		byService[row.Service] = row
	}

	if got := byService["transfer"]; got.Status != "implemented" {
		t.Errorf("transfer status = %q, want implemented", got.Status)
	}
	if got := byService["networkmanager"]; got.Status != "unimplemented" {
		t.Errorf("networkmanager status = %q, want unimplemented", got.Status)
	}
	if got := byService["storagegateway"]; got.Status != "unimplemented" {
		t.Errorf("storagegateway status = %q, want unimplemented", got.Status)
	}
	if _, present := byService["s3"]; present {
		t.Error("s3 was not on the watchlist and should not have produced a row")
	}
}

func TestProbeServicesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if _, err := probeServices(context.Background(), server.URL, map[string]bool{"s3": true}); err == nil {
		t.Fatal("expected an error for a non-200 health response, got nil")
	}
}
