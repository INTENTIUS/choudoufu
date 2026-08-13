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
	"testing"
)

func TestGetResourcesHitsTaggingTarget(t *testing.T) {
	var gotTarget string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("X-Amz-Target")
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
