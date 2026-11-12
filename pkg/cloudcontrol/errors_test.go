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
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsListRefusal drives each code through the real client, so the
// classifier is tested on the error ListResources actually returns (the
// "__type" prefix stripped, wrapped or not), not on a hand-built value.
// UnsupportedActionException's message is floci 920d475's own wording.
func TestIsListRefusal(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{CodeUnsupportedAction, true},
		{CodeUnsupportedOperation, true},
		{CodeUnknownOperation, true},
		{CodeAccessDenied, false},
		{CodeThrottlingError, false},
		{CodeValidationError, false},
		{CodeResourceNotFoundError, false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				body := map[string]string{"message": "ListResources is not supported for resource type AWS::EC2::RouteTable."}
				if tt.code != "" {
					body["__type"] = "com.amazonaws.cloudformation#" + tt.code
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()

			c := New(Config{Endpoint: server.URL, MaxAttempts: 1})
			_, err := c.ListResources(context.Background(), "AWS::EC2::RouteTable")
			if err == nil {
				t.Fatal("ListResources succeeded against a server that only errors")
			}
			if got := IsListRefusal(err); got != tt.want {
				t.Errorf("IsListRefusal(%v) = %v, want %v", err, got, tt.want)
			}
			if got := IsListRefusal(fmt.Errorf("wrapped: %w", err)); got != tt.want {
				t.Errorf("IsListRefusal on a wrapped error = %v, want %v", got, tt.want)
			}
		})
	}
	if IsListRefusal(errors.New("dial tcp: connection refused")) {
		t.Error("a transport error is not a refusal")
	}
	if IsListRefusal(nil) {
		t.Error("nil is not a refusal")
	}
}
