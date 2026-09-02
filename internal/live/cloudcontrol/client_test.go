// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeRequest reads a request body's TypeName/NextToken/Identifier
// fields, which is all these tests' fake servers need to branch on.
func decodeRequest(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decoding request body: %v", err)
	}
	return body
}

func TestListResourcesPaginatesAcrossPages(t *testing.T) {
	var targets []string
	var tokensSeen []string

	pages := []struct {
		ids  []string
		next string
	}{
		{ids: []string{"a1", "a2"}, next: "token-2"},
		{ids: []string{"b1", "b2"}, next: "token-3"},
		{ids: []string{"c1"}, next: ""},
	}
	call := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets = append(targets, r.Header.Get("X-Amz-Target"))
		body := decodeRequest(t, r)
		tokensSeen = append(tokensSeen, body["NextToken"])

		page := pages[call]
		call++

		descs := make([]map[string]string, 0, len(page.ids))
		for _, id := range page.ids {
			descs = append(descs, map[string]string{
				"Identifier": id,
				"Properties": fmt.Sprintf(`{"id":%q}`, id),
			})
		}
		resp := map[string]any{"ResourceDescriptions": descs}
		if page.next != "" {
			resp["NextToken"] = page.next
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	got, err := c.ListResources(context.Background(), "AWS::EC2::Instance")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	if call != 3 {
		t.Fatalf("expected 3 requests (one per page), got %d", call)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 resources across 3 pages, got %d: %+v", len(got), got)
	}
	wantIDs := []string{"a1", "a2", "b1", "b2", "c1"}
	for i, id := range wantIDs {
		if got[i].Identifier != id {
			t.Errorf("result %d: identifier = %q, want %q", i, got[i].Identifier, id)
		}
		if got[i].Properties["id"] != id {
			t.Errorf("result %d: Properties[id] = %v, want %q", i, got[i].Properties["id"], id)
		}
	}

	for _, target := range targets {
		if target != "CloudApiService.ListResources" {
			t.Errorf("X-Amz-Target = %q, want CloudApiService.ListResources", target)
		}
	}
	if tokensSeen[0] != "" {
		t.Errorf("first request carried NextToken %q, want none", tokensSeen[0])
	}
	if tokensSeen[1] != "token-2" || tokensSeen[2] != "token-3" {
		t.Errorf("pagination tokens seen = %v, want [\"\" token-2 token-3]", tokensSeen)
	}
}

// TestListResourcesScopedSendsResourceModel confirms the wire shape this
// package's own doc comment claims: ResourceModel travels as a JSON string
// (not a nested object - AWS's Properties shape, the same encoding
// [ResourceDescription.Properties] already uses on the read side), and it
// rides along on every paginated page, not just the first.
func TestListResourcesScopedSendsResourceModel(t *testing.T) {
	var modelsSeen []string
	pages := [][]string{{"r1"}, {"r2"}}
	call := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeRequest(t, r)
		modelsSeen = append(modelsSeen, body["ResourceModel"])

		ids := pages[call]
		call++
		descs := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			descs = append(descs, map[string]string{"Identifier": id})
		}
		resp := map[string]any{"ResourceDescriptions": descs}
		if call < len(pages) {
			resp["NextToken"] = "next"
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	got, err := c.ListResourcesScoped(context.Background(), "AWS::ApiGateway::Resource", map[string]string{"RestApiId": "abc123"})
	if err != nil {
		t.Fatalf("ListResourcesScoped: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results across 2 pages, got %d", len(got))
	}
	if call != 2 {
		t.Fatalf("expected 2 requests, got %d", call)
	}
	for i, raw := range modelsSeen {
		var decoded map[string]string
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("page %d: ResourceModel %q did not decode as JSON: %v", i, raw, err)
		}
		if decoded["RestApiId"] != "abc123" {
			t.Errorf("page %d: ResourceModel = %v, want RestApiId=abc123", i, decoded)
		}
	}
}

// TestListResourcesScopedRefusesEmptyModel confirms the guard that keeps a
// caller from ever sending a scoped call with nothing to scope by - a bug in
// the caller, not a request this client should let through, because an
// empty ResourceModel is indistinguishable on the wire from "{}", which is
// not the same as sending no ResourceModel at all and might not mean
// "unscoped" to every backend.
func TestListResourcesScopedRefusesEmptyModel(t *testing.T) {
	c := New(Config{Endpoint: "http://unused.invalid"})
	_, err := c.ListResourcesScoped(context.Background(), "AWS::ApiGateway::Resource", nil)
	if err == nil {
		t.Fatal("ListResourcesScoped with an empty resourceModel: got nil error, want a refusal")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("error = %q, want it to explain the empty-model refusal", err.Error())
	}
}

// TestListResourcesUnscopedSendsNoResourceModel confirms the plain
// [Client.ListResources] path never sends a ResourceModel field at all -
// omitempty means Cloud Control sees no key, not an empty string - so a
// type that needs no scoping keeps behaving exactly as it did before this
// method existed.
func TestListResourcesUnscopedSendsNoResourceModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading request body: %v", err)
		}
		if strings.Contains(string(raw), "ResourceModel") {
			t.Errorf("unscoped ListResources request body carried a ResourceModel field: %s", raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []map[string]string{}})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	if _, err := c.ListResources(context.Background(), "AWS::EC2::Instance"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}
}

func TestErrorCodeMapping(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wireType   string
		message    string
		wantCode   string
	}{
		{
			name:       "UnsupportedOperation",
			statusCode: http.StatusBadRequest,
			wireType:   "com.amazonaws.cloudformation#UnsupportedOperation",
			message:    "GetResource is not supported for AWS::EC2::Instance",
			wantCode:   CodeUnsupportedOperation,
		},
		{
			name:       "ResourceNotFoundException",
			statusCode: http.StatusNotFound,
			wireType:   "com.amazonaws.cloudformation#ResourceNotFoundException",
			message:    "resource not found",
			wantCode:   CodeResourceNotFoundError,
		},
		{
			name:       "ValidationException",
			statusCode: http.StatusBadRequest,
			wireType:   "com.amazonaws.cloudformation#ValidationException",
			message:    "1 validation error detected",
			wantCode:   CodeValidationError,
		},
		{
			name:       "ThrottlingException",
			statusCode: http.StatusTooManyRequests,
			wireType:   "com.amazonaws.cloudformation#ThrottlingException",
			message:    "Rate exceeded",
			wantCode:   CodeThrottlingError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"__type":  tt.wireType,
					"message": tt.message,
				})
			}))
			defer server.Close()

			// MaxAttempts: 1 - this test is about error-code classification,
			// not the retry policy (that is retry_test.go's job), so the
			// ThrottlingException case must not sit through real backoff
			// sleeps here.
			c := New(Config{Endpoint: server.URL, MaxAttempts: 1})
			_, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error is not an *APIError: %v (%T)", err, err)
			}
			if apiErr.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tt.wantCode)
			}
			if apiErr.StatusCode != tt.statusCode {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.statusCode)
			}
			if apiErr.Message != tt.message {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.message)
			}
			if !HasCode(err, tt.wantCode) {
				t.Errorf("HasCode(err, %q) = false, want true", tt.wantCode)
			}
		})
	}
}

func TestErrorWithoutType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	_, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %v (%T)", err, err)
	}
	if apiErr.Code != "" {
		t.Errorf("Code = %q, want empty", apiErr.Code)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "HTTP 500") {
		t.Errorf("Message = %q, want it to mention HTTP 500", apiErr.Message)
	}
}

func TestGetResourcePropertiesParsing(t *testing.T) {
	tests := []struct {
		name           string
		propertiesJSON string // "" omits the field entirely
		wantProps      map[string]any
	}{
		{
			name:           "ordinary object",
			propertiesJSON: `{"InstanceId":"i-0123","Tags":[{"Key":"Name","Value":"web"}]}`,
			wantProps: map[string]any{
				"InstanceId": "i-0123",
				"Tags":       []any{map[string]any{"Key": "Name", "Value": "web"}},
			},
		},
		{
			name:           "absent properties",
			propertiesJSON: "",
			wantProps:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				desc := map[string]string{"Identifier": "i-0123"}
				if tt.propertiesJSON != "" {
					desc["Properties"] = tt.propertiesJSON
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescription": desc})
			}))
			defer server.Close()

			c := New(Config{Endpoint: server.URL})
			got, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
			if err != nil {
				t.Fatalf("GetResource: %v", err)
			}
			if got.Identifier != "i-0123" {
				t.Errorf("Identifier = %q, want i-0123", got.Identifier)
			}
			if len(got.Properties) != len(tt.wantProps) {
				t.Errorf("Properties = %#v, want %#v", got.Properties, tt.wantProps)
			}
			for k, want := range tt.wantProps {
				if fmt.Sprint(got.Properties[k]) != fmt.Sprint(want) {
					t.Errorf("Properties[%q] = %#v, want %#v", k, got.Properties[k], want)
				}
			}
		})
	}
}

func TestGetResourceUnparseablePropertiesIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ResourceDescription": map[string]string{
				"Identifier": "i-0123",
				"Properties": "not json",
			},
		})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	_, err := c.GetResource(context.Background(), "AWS::EC2::Instance", "i-0123")
	if err == nil {
		t.Fatal("expected an error for unparseable Properties, got nil")
	}
}

func TestJoinIdentifier(t *testing.T) {
	got := JoinIdentifier("rtb-0123", "subnet-0456")
	if got != "rtb-0123|subnet-0456" {
		t.Errorf("JoinIdentifier = %q, want rtb-0123|subnet-0456", got)
	}
}
