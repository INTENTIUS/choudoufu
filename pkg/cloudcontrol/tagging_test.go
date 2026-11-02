// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetResourcesHitsTaggingTarget(t *testing.T) {
	var gotTarget, gotContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("X-Amz-Target")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": []any{}})
	}))
	defer server.Close()

	c := NewTagging(Config{Endpoint: server.URL})
	if _, err := c.GetResources(context.Background(), nil, nil); err != nil {
		t.Fatalf("GetResources: %v", err)
	}
	if gotTarget != "ResourceGroupsTaggingAPI_20170126.GetResources" {
		t.Errorf("X-Amz-Target = %q, want ResourceGroupsTaggingAPI_20170126.GetResources", gotTarget)
	}
	// Pins a real bug caught probing floci directly (issue #51): the
	// Resource Groups Tagging API's real protocol version is JSON 1.1 (per
	// botocore's own resourcegroupstaggingapi service model), not Cloud
	// Control's 1.0, and floci refuses a GetResources call outright -
	// UnknownOperationException - when the header says 1.0 even though
	// X-Amz-Target already names the operation correctly.
	if gotContentType != "application/x-amz-json-1.1" {
		t.Errorf("Content-Type = %q, want application/x-amz-json-1.1 (the tagging API's real protocol version, not Cloud Control's 1.0)", gotContentType)
	}
}

// TestNewCloudControlContentTypeUnaffectedByTagging pins the other half:
// adding NewTagging's own protocol version must not change what [New]
// sends.
func TestNewCloudControlContentTypeUnaffectedByTagging(t *testing.T) {
	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	if _, err := c.ListResources(context.Background(), "AWS::EFS::FileSystem"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if gotContentType != "application/x-amz-json-1.0" {
		t.Errorf("Content-Type = %q, want application/x-amz-json-1.0", gotContentType)
	}
}

// TestTaggingBaseURLUsesTaggingSubdomain checks the real-AWS host template
// (no endpoint override) carries "tagging", not "cloudcontrolapi".
func TestTaggingBaseURLUsesTaggingSubdomain(t *testing.T) {
	c := NewTagging(Config{Region: "eu-west-1"})
	got := c.baseURL()
	want := "https://tagging.eu-west-1.amazonaws.com/"
	if got != want {
		t.Errorf("baseURL() = %q, want %q", got, want)
	}
}

func TestCloudControlBaseURLUnaffectedByTaggingAddition(t *testing.T) {
	c := New(Config{Region: "eu-west-1"})
	got := c.baseURL()
	want := "https://cloudcontrolapi.eu-west-1.amazonaws.com/"
	if got != want {
		t.Errorf("baseURL() = %q, want %q (the New() path must be unaffected by NewTagging)", got, want)
	}
}

func TestGetResourcesPaginates(t *testing.T) {
	pages := []struct {
		arns  []string
		token string
	}{
		{arns: []string{"arn:aws:efs:us-east-1:1234:file-system/fs-1"}, token: "next-1"},
		{arns: []string{"arn:aws:efs:us-east-1:1234:file-system/fs-2"}, token: ""},
	}
	call := 0
	var tokensSeen []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		tok, _ := body["PaginationToken"].(string)
		tokensSeen = append(tokensSeen, tok)

		page := pages[call]
		call++
		var mappings []map[string]any
		for _, arn := range page.arns {
			mappings = append(mappings, map[string]any{
				"ResourceARN": arn,
				"Tags": []map[string]string{
					{"Key": "tofu-estate", "Value": "demo"},
				},
			})
		}
		resp := map[string]any{"ResourceTagMappingList": mappings}
		if page.token != "" {
			resp["PaginationToken"] = page.token
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := NewTagging(Config{Endpoint: server.URL})
	got, err := c.GetResources(context.Background(), nil, []TagFilter{{Key: "tofu-estate", Values: []string{"demo"}}})
	if err != nil {
		t.Fatalf("GetResources: %v", err)
	}
	if call != 2 {
		t.Fatalf("expected 2 requests (one per page), got %d", call)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tagged resources across 2 pages, got %d: %+v", len(got), got)
	}
	if got[0].ResourceARN != "arn:aws:efs:us-east-1:1234:file-system/fs-1" {
		t.Errorf("got[0].ResourceARN = %q", got[0].ResourceARN)
	}
	if got[1].ResourceARN != "arn:aws:efs:us-east-1:1234:file-system/fs-2" {
		t.Errorf("got[1].ResourceARN = %q", got[1].ResourceARN)
	}
	for _, tr := range got {
		if tr.Tags["tofu-estate"] != "demo" {
			t.Errorf("Tags[tofu-estate] = %q, want demo", tr.Tags["tofu-estate"])
		}
	}
	if tokensSeen[0] != "" {
		t.Errorf("first request carried PaginationToken %q, want none", tokensSeen[0])
	}
	if tokensSeen[1] != "next-1" {
		t.Errorf("second request carried PaginationToken %q, want next-1", tokensSeen[1])
	}
}

func TestGetResourcesSendsFilters(t *testing.T) {
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": []any{}})
	}))
	defer server.Close()

	c := NewTagging(Config{Endpoint: server.URL})
	_, err := c.GetResources(context.Background(), []string{"AWS::EFS::FileSystem"}, []TagFilter{
		{Key: "tofu-estate", Values: []string{"demo"}},
	})
	if err != nil {
		t.Fatalf("GetResources: %v", err)
	}

	rtf, _ := gotBody["ResourceTypeFilters"].([]any)
	if len(rtf) != 1 || rtf[0] != "AWS::EFS::FileSystem" {
		t.Errorf("ResourceTypeFilters = %v, want [AWS::EFS::FileSystem]", rtf)
	}
	tf, _ := gotBody["TagFilters"].([]any)
	if len(tf) != 1 {
		t.Fatalf("TagFilters = %v, want one entry", tf)
	}
	entry, _ := tf[0].(map[string]any)
	if entry["Key"] != "tofu-estate" {
		t.Errorf("TagFilters[0].Key = %v, want tofu-estate", entry["Key"])
	}
}

// TestTagResourcesHitsTaggingTarget pins the wire shape GitHub issue
// #1084's post-create marker write sends: the TagResources operation on the
// tagging target, JSON 1.1, with the ARN list and the tag map exactly as the
// Resource Groups Tagging API names them.
func TestTagResourcesHitsTaggingTarget(t *testing.T) {
	var gotTarget, gotContentType string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("X-Amz-Target")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"FailedResourcesMap": map[string]any{}})
	}))
	defer server.Close()

	c := NewTagging(Config{Endpoint: server.URL})
	err := c.TagResources(context.Background(), []string{"arn:aws:route53:::hostedzone/Z1"}, map[string]string{"tofu-estate": "prod"})
	if err != nil {
		t.Fatalf("TagResources: %v", err)
	}
	if gotTarget != "ResourceGroupsTaggingAPI_20170126.TagResources" {
		t.Errorf("X-Amz-Target = %q, want ResourceGroupsTaggingAPI_20170126.TagResources", gotTarget)
	}
	if gotContentType != "application/x-amz-json-1.1" {
		t.Errorf("Content-Type = %q, want application/x-amz-json-1.1", gotContentType)
	}
	arns, _ := gotBody["ResourceARNList"].([]any)
	if len(arns) != 1 || arns[0] != "arn:aws:route53:::hostedzone/Z1" {
		t.Errorf("ResourceARNList = %v", gotBody["ResourceARNList"])
	}
	tags, _ := gotBody["Tags"].(map[string]any)
	if tags["tofu-estate"] != "prod" {
		t.Errorf("Tags = %v", gotBody["Tags"])
	}
}

// TestTagResourcesFailedResourcesMapIsAnError pins the API's own failure
// shape: HTTP 200 with a FailedResourcesMap naming the ARN that was not
// tagged is a failure, and the error names that ARN and the API's code.
func TestTagResourcesFailedResourcesMapIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"FailedResourcesMap": map[string]any{
			"arn:aws:route53:::hostedzone/Z1": map[string]any{
				"StatusCode": 400, "ErrorCode": "InvalidParameterException", "ErrorMessage": "no such zone",
			},
		}})
	}))
	defer server.Close()

	c := NewTagging(Config{Endpoint: server.URL})
	err := c.TagResources(context.Background(), []string{"arn:aws:route53:::hostedzone/Z1"}, map[string]string{"k": "v"})
	if err == nil {
		t.Fatalf("TagResources returned nil over a non-empty FailedResourcesMap")
	}
	if !HasCode(err, "InvalidParameterException") {
		t.Errorf("error code not carried: %v", err)
	}
	for _, want := range []string{"arn:aws:route53:::hostedzone/Z1", "no such zone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestTagResourcesHTTPErrorIsAnError pins the refusal shape the live pin
// (internal/live/lifecycle's TestTagOnCreateHostedZone) injects: a 403 with
// an AccessDeniedException envelope comes back as an *APIError carrying
// that code.
func TestTagResourcesHTTPErrorIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"__type":"AccessDeniedException","message":"refused"}`))
	}))
	defer server.Close()

	c := NewTagging(Config{Endpoint: server.URL})
	err := c.TagResources(context.Background(), []string{"arn:aws:route53:::hostedzone/Z1"}, map[string]string{"k": "v"})
	if !HasCode(err, "AccessDeniedException") {
		t.Fatalf("want an AccessDeniedException APIError, got %v", err)
	}
}
