// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// TestFetchTree_CacheRoundTrip: the same no-network discipline
// tools/registry-gen/fetch_test.go's TestAcquireZip_CacheRoundTrip and
// tools/importdocs-gen/fetch.go's acquireDoc hold themselves to (issue #42)
// - a warm cache never calls the fetcher, and the fetcher's own result is
// cached for the next call.
func TestFetchTree_CacheRoundTrip(t *testing.T) {
	dir := t.TempDir()

	treeJSON := `{
		"truncated": false,
		"tree": [
			{"path": "botocore/data/ec2/2016-11-15/service-2.json", "type": "blob"},
			{"path": "botocore/data/ec2/2014-09-01/service-2.json", "type": "blob"},
			{"path": "botocore/data/kms/2014-11-01/service-2.json", "type": "blob"},
			{"path": "botocore/data/ec2/2016-11-15/paginators-1.json", "type": "blob"},
			{"path": "botocore/data/ec2", "type": "tree"}
		]
	}`
	calls := 0
	fake := func(ctx context.Context, url string) (int, []byte, error) {
		calls++
		return http.StatusOK, []byte(treeJSON), nil
	}

	paths, err := fetchTree(context.Background(), dir, fake)
	if err != nil {
		t.Fatalf("fetchTree: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetcher called %d times, want 1", calls)
	}
	want := []string{
		"botocore/data/ec2/2016-11-15/service-2.json",
		"botocore/data/ec2/2014-09-01/service-2.json",
		"botocore/data/kms/2014-11-01/service-2.json",
	}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v (paginators-1.json and the bare tree entry must be filtered out)", paths, want)
	}

	failFetch := func(context.Context, string) (int, []byte, error) {
		t.Fatal("the fetcher must not be called once the tree is cached")
		return 0, nil, nil
	}
	cachedPaths, err := fetchTree(context.Background(), dir, failFetch)
	if err != nil {
		t.Fatalf("fetchTree from cache: %v", err)
	}
	if len(cachedPaths) != len(paths) {
		t.Errorf("cached fetchTree returned %d paths, want %d", len(cachedPaths), len(paths))
	}
}

func TestFetchTree_TruncatedRefused(t *testing.T) {
	dir := t.TempDir()
	fake := func(ctx context.Context, url string) (int, []byte, error) {
		return http.StatusOK, []byte(`{"truncated": true, "tree": []}`), nil
	}
	if _, err := fetchTree(context.Background(), dir, fake); err == nil {
		t.Fatal("fetchTree accepted a truncated response")
	}
}

func TestLatestVersions(t *testing.T) {
	paths := []string{
		"botocore/data/ec2/2014-09-01/service-2.json",
		"botocore/data/ec2/2016-11-15/service-2.json",
		"botocore/data/ec2/2015-03-01/service-2.json",
		"botocore/data/kms/2014-11-01/service-2.json",
	}
	got := latestVersions(paths)
	if got["ec2"] != "2016-11-15" {
		t.Errorf("ec2 latest = %q, want 2016-11-15", got["ec2"])
	}
	if got["kms"] != "2014-11-01" {
		t.Errorf("kms latest = %q, want 2014-11-01", got["kms"])
	}
}

func TestFetchServiceModel_CacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	payload := []byte(`{"operations": {}, "shapes": {}}`)

	calls := 0
	fake := func(ctx context.Context, url string) (int, []byte, error) {
		calls++
		want := fmt.Sprintf(rawURLTemplate, botocoreOwner, botocoreRepo, botocoreTag, "botocore/data/kms/2014-11-01/service-2.json")
		if url != want {
			t.Errorf("fetched %q, want %q", url, want)
		}
		return http.StatusOK, payload, nil
	}

	got, err := fetchServiceModel(context.Background(), dir, "kms", "2014-11-01", fake)
	if err != nil {
		t.Fatalf("fetchServiceModel: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
	if calls != 1 {
		t.Fatalf("fetcher called %d times, want 1", calls)
	}

	failFetch := func(context.Context, string) (int, []byte, error) {
		t.Fatal("the fetcher must not be called once the model is cached")
		return 0, nil, nil
	}
	cached, err := fetchServiceModel(context.Background(), dir, "kms", "2014-11-01", failFetch)
	if err != nil {
		t.Fatalf("fetchServiceModel from cache: %v", err)
	}
	if string(cached) != string(payload) {
		t.Errorf("cached model = %q, want %q", cached, payload)
	}
}

func TestFetchServiceModel_NotFoundRefused(t *testing.T) {
	dir := t.TempDir()
	fake := func(context.Context, string) (int, []byte, error) {
		return http.StatusNotFound, nil, nil
	}
	if _, err := fetchServiceModel(context.Background(), dir, "nope", "2020-01-01", fake); err == nil {
		t.Fatal("fetchServiceModel accepted a 404")
	}
}

// TestDefaultCacheDir_Namespaced pins that the cache path is namespaced by
// the pinned botocore tag, the same reasoning
// tools/importdocs-gen/fetch.go's defaultCacheDir doc comment gives its own
// provider-version namespacing.
func TestDefaultCacheDir_Namespaced(t *testing.T) {
	dir, err := defaultCacheDir()
	if err != nil {
		t.Fatalf("defaultCacheDir: %v", err)
	}
	if filepath.Base(dir) != botocoreTag {
		t.Errorf("cache dir %q is not namespaced by the pinned tag %q", dir, botocoreTag)
	}
}
