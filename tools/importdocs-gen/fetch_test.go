// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestSlugForType(t *testing.T) {
	if got := slugForType("aws_iam_role_policy_attachment"); got != "iam_role_policy_attachment" {
		t.Errorf("slugForType = %q", got)
	}
}

// fakeFetcher counts calls per URL and serves canned responses, so tests
// can assert the cache - not the network - answers a repeat request. The
// same seam tools/registry-gen/fetch_test.go's fake downloader gives
// acquireZip (issue #42's "no network access in tests").
type fakeFetcher struct {
	calls   int
	status  int
	body    []byte
	fetchFn func(ctx context.Context, url string) (int, []byte, error)
}

func (f *fakeFetcher) fetch(ctx context.Context, url string) (int, []byte, error) {
	f.calls++
	if f.fetchFn != nil {
		return f.fetchFn(ctx, url)
	}
	return f.status, f.body, nil
}

func TestAcquireDoc_CachesAHit(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeFetcher{status: http.StatusOK, body: []byte("doc body")}

	data, found, err := acquireDoc(context.Background(), dir, "aws_kms_key", fake.fetch)
	if err != nil || !found || string(data) != "doc body" {
		t.Fatalf("first call: data=%q found=%v err=%v", data, found, err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 network call, got %d", fake.calls)
	}

	data, found, err = acquireDoc(context.Background(), dir, "aws_kms_key", fake.fetch)
	if err != nil || !found || string(data) != "doc body" {
		t.Fatalf("second call: data=%q found=%v err=%v", data, found, err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected the second call to be served from cache with no new network call, got %d total calls", fake.calls)
	}
}

func TestAcquireDoc_CachesA404(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeFetcher{status: http.StatusNotFound}

	_, found, err := acquireDoc(context.Background(), dir, "aws_alb", fake.fetch)
	if err != nil || found {
		t.Fatalf("first call: found=%v err=%v, want a cached miss", found, err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 network call, got %d", fake.calls)
	}

	_, found, err = acquireDoc(context.Background(), dir, "aws_alb", fake.fetch)
	if err != nil || found {
		t.Fatalf("second call: found=%v err=%v", found, err)
	}
	if fake.calls != 1 {
		t.Fatalf("expected the 404 to be served from the cached sentinel, got %d total calls", fake.calls)
	}
}

func TestAcquireDoc_UnexpectedStatusIsAnError(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeFetcher{status: http.StatusInternalServerError}

	_, _, err := acquireDoc(context.Background(), dir, "aws_kms_key", fake.fetch)
	if err == nil {
		t.Fatal("expected an error on an unexpected HTTP status")
	}
}

func TestSweep_SkipsMissingDocsAndDocsWithNoImportSection(t *testing.T) {
	dir := t.TempDir()
	roster := []string{"aws_has_doc", "aws_missing_doc", "aws_no_import_section"}

	fake := &fakeFetcher{fetchFn: func(_ context.Context, url string) (int, []byte, error) {
		switch {
		case containsStr(url, "has_doc"):
			return http.StatusOK, []byte("## Import\n\n```console\n% terraform import aws_has_doc.x abc\n```\n"), nil
		case containsStr(url, "missing_doc"):
			return http.StatusNotFound, nil, nil
		case containsStr(url, "no_import_section"):
			return http.StatusOK, []byte("# Resource: aws_no_import_section\n\nno import section here\n"), nil
		}
		return http.StatusNotFound, nil, nil
	}}

	rows, docsFound, docsMissing, aliasRows, err := sweep(context.Background(), dir, roster, fake.fetch)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if docsFound != 2 {
		t.Errorf("docsFound = %d, want 2", docsFound)
	}
	if docsMissing != 1 {
		t.Errorf("docsMissing = %d, want 1", docsMissing)
	}
	if len(rows) != 1 || rows[0].TFType != "aws_has_doc" {
		t.Errorf("rows = %v, want exactly one row for aws_has_doc", rows)
	}
	if aliasRows != 0 {
		t.Errorf("aliasRows = %d, want 0 (no doc in this fixture declares an alias note)", aliasRows)
	}
}

func containsStr(s, sub string) bool { return contains(s, sub) }

func TestDefaultCacheDir_NamespacedByProviderVersion(t *testing.T) {
	dir, err := defaultCacheDir()
	if err != nil {
		t.Fatalf("defaultCacheDir: %v", err)
	}
	if filepath.Base(dir) != providerVersion {
		t.Errorf("defaultCacheDir = %q, want it namespaced by providerVersion %q", dir, providerVersion)
	}
}
