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

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// TestClassifyListResourcesSuccess covers a clean ListResources call - even
// an empty list, since [http.HandlerFunc] below returns no
// ResourceDescriptions at all - classifying as implemented.
func TestClassifyListResourcesSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL})
	row, err := classifyListResources(context.Background(), cc, "aws_vpc", "AWS::EC2::VPC")
	if err != nil {
		t.Fatalf("classifyListResources: %v", err)
	}
	if row.Status != "implemented" {
		t.Errorf("Status = %q, want implemented", row.Status)
	}
	if row.Mechanism != "cloudcontrol-list" {
		t.Errorf("Mechanism = %q, want cloudcontrol-list", row.Mechanism)
	}
}

// TestClassifyListResourcesUnsupportedOperation covers floci's router
// refusing a type outright - unimplemented, the wire shape
// internal/live/cloudcontrol/client_test.go's own TestErrorCodeMapping
// exercises at the client layer.
func TestClassifyListResourcesUnsupportedOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"__type":  "com.amazonaws.cloudformation#UnsupportedOperation",
			"message": "ListResources is not supported for AWS::NetworkManager::CoreNetwork",
		})
	}))
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	row, err := classifyListResources(context.Background(), cc, "aws_networkmanager_core_network", "AWS::NetworkManager::CoreNetwork")
	if err != nil {
		t.Fatalf("classifyListResources: %v", err)
	}
	if row.Status != "unimplemented" {
		t.Errorf("Status = %q, want unimplemented", row.Status)
	}
}

// TestClassifyListResourcesBrokenHandler covers the HTML-error-page shape
// the databases and stragglers cohort READMEs both document: HTTP round
// trips, but the body carries no __type at all, so the client's own
// APIError.Code comes back empty - a router-recognized but broken handler,
// not an absent one.
func TestClassifyListResourcesBrokenHandler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	row, err := classifyListResources(context.Background(), cc, "aws_docdbelastic_cluster", "AWS::DocDBElastic::Cluster")
	if err != nil {
		t.Fatalf("classifyListResources: %v", err)
	}
	if row.Status != "broken" {
		t.Errorf("Status = %q, want broken", row.Status)
	}
}

// TestClassifyListResourcesOrdinaryValidationError covers a handler that is
// real but complains about this call's own missing input the way a bare
// ListResources sometimes does - still a real handler, so implemented.
func TestClassifyListResourcesOrdinaryValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"__type":  "com.amazonaws.cloudformation#ValidationException",
			"message": "1 validation error detected",
		})
	}))
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	row, err := classifyListResources(context.Background(), cc, "aws_some_type", "AWS::Some::Type")
	if err != nil {
		t.Fatalf("classifyListResources: %v", err)
	}
	if row.Status != "implemented" {
		t.Errorf("Status = %q, want implemented (reached a real handler)", row.Status)
	}
}
