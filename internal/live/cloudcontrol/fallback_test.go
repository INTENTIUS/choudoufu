// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetResourceByIdentityUsesGetResourceWhenItWorks(t *testing.T) {
	var sawOperations []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawOperations = append(sawOperations, r.Header.Get("X-Amz-Target"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ResourceDescription": map[string]string{
				"Identifier": "fs-0123",
				"Properties": `{"FileSystemId":"fs-0123"}`,
			},
		})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	desc, err := GetResourceByIdentity(context.Background(), c, "AWS::EFS::FileSystem", "fs-0123")
	if err != nil {
		t.Fatalf("GetResourceByIdentity: %v", err)
	}
	if desc == nil || desc.Identifier != "fs-0123" {
		t.Fatalf("desc = %+v, want Identifier fs-0123", desc)
	}
	if len(sawOperations) != 1 || sawOperations[0] != "CloudApiService.GetResource" {
		t.Errorf("operations called = %v, want exactly one GetResource (no fallback needed)", sawOperations)
	}
}

// TestGetResourceByIdentityFallsBackOnUnsupportedOperation is floci's exact
// shape: GetResource refuses the type outright while ListResources on it
// works, and the fallback finds the identifier in the listed population.
func TestGetResourceByIdentityFallsBackOnUnsupportedOperation(t *testing.T) {
	var sawOperations []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		sawOperations = append(sawOperations, target)
		switch target {
		case "CloudApiService.GetResource":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"__type":  "com.amazonaws.cloudformation#UnsupportedOperation",
				"message": "GetResource is not supported for AWS::EFS::FileSystem",
			})
		case "CloudApiService.ListResources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ResourceDescriptions": []map[string]string{
					{"Identifier": "fs-other", "Properties": `{"FileSystemId":"fs-other"}`},
					{"Identifier": "fs-0123", "Properties": `{"FileSystemId":"fs-0123","Tags":[{"Key":"tofu-estate","Value":"demo"}]}`},
				},
			})
		default:
			t.Fatalf("unexpected operation %q", target)
		}
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	desc, err := GetResourceByIdentity(context.Background(), c, "AWS::EFS::FileSystem", "fs-0123")
	if err != nil {
		t.Fatalf("GetResourceByIdentity: %v", err)
	}
	if desc == nil {
		t.Fatal("desc = nil, want a match from the list fallback")
	}
	if desc.Identifier != "fs-0123" {
		t.Errorf("Identifier = %q, want fs-0123", desc.Identifier)
	}
	if desc.Properties["Tags"] == nil {
		t.Errorf("Properties = %#v, want the Tags carried by the listed entry", desc.Properties)
	}
	if len(sawOperations) != 2 || sawOperations[0] != "CloudApiService.GetResource" || sawOperations[1] != "CloudApiService.ListResources" {
		t.Errorf("operations called = %v, want GetResource then ListResources", sawOperations)
	}
}

func TestGetResourceByIdentityFallbackMiss(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Amz-Target") {
		case "CloudApiService.GetResource":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"__type": "com.amazonaws.cloudformation#UnsupportedOperation"})
		case "CloudApiService.ListResources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ResourceDescriptions": []map[string]string{
					{"Identifier": "fs-other", "Properties": `{}`},
				},
			})
		}
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	desc, err := GetResourceByIdentity(context.Background(), c, "AWS::EFS::FileSystem", "fs-missing")
	if err != nil {
		t.Fatalf("GetResourceByIdentity: %v, want a confirmed-absent nil,nil rather than an error", err)
	}
	if desc != nil {
		t.Errorf("desc = %+v, want nil (the identifier is not in the listed population)", desc)
	}
}

func TestGetResourceByIdentityPropagatesOtherRefusals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"__type":  "com.amazonaws.cloudformation#ResourceNotFoundException",
			"message": "not found",
		})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	desc, err := GetResourceByIdentity(context.Background(), c, "AWS::EFS::FileSystem", "fs-gone")
	if err == nil {
		t.Fatal("expected ResourceNotFoundException to propagate as an error, got nil")
	}
	if desc != nil {
		t.Errorf("desc = %+v, want nil alongside the propagated error", desc)
	}
	if !HasCode(err, CodeResourceNotFoundError) {
		t.Errorf("err = %v, want it to carry CodeResourceNotFoundError", err)
	}
}

func TestGetResourceByIdentityPropagatesListFailureDuringFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Amz-Target") {
		case "CloudApiService.GetResource":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"__type": "com.amazonaws.cloudformation#UnsupportedOperation"})
		case "CloudApiService.ListResources":
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"__type": "com.amazonaws.cloudformation#ThrottlingException"})
		}
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	_, err := GetResourceByIdentity(context.Background(), c, "AWS::EFS::FileSystem", "fs-0123")
	if err == nil {
		t.Fatal("expected the ListResources failure during fallback to propagate")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != CodeThrottlingError {
		t.Errorf("err = %v, want a ThrottlingException APIError", err)
	}
}
