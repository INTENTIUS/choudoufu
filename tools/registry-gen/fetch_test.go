// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// buildTestZip packs the given typeName -> schema bytes into an in-memory
// zip, the same shape extractSchemas reads from the real bundle. No network,
// no committed binary fixture: the archive container is a few lines of
// archive/zip either way, so this builds it from the testdata JSON files
// that already do the interesting work of representing real schema shapes.
func buildTestZip(t *testing.T, schemas map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	i := 0
	for typeName, data := range schemas {
		i++
		f, err := w.Create(filepath.Join("schemas", typeName+".json"))
		if err != nil {
			t.Fatalf("adding %s to the test zip: %v", typeName, err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatalf("writing %s to the test zip: %v", typeName, err)
		}
	}
	// A non-JSON entry and a JSON entry with no typeName, both of which
	// extractSchemas must skip rather than fail on.
	if f, err := w.Create("README.txt"); err != nil {
		t.Fatal(err)
	} else if _, err := f.Write([]byte("not a schema")); err != nil {
		t.Fatal(err)
	}
	if f, err := w.Create("schemas/provider.definition.schema.v1.json"); err != nil {
		t.Fatal(err)
	} else if _, err := f.Write([]byte(`{"description": "no typeName here"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the test zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractSchemas(t *testing.T) {
	want := loadTestdataSchemas(t)
	zipData := buildTestZip(t, want)

	got, err := extractSchemas(zipData)
	if err != nil {
		t.Fatalf("extractSchemas: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %d schemas, want %d", len(got), len(want))
	}
	for typeName, wantBytes := range want {
		gotBytes, ok := got[typeName]
		if !ok {
			t.Errorf("%s: not extracted", typeName)
			continue
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Errorf("%s: extracted bytes do not match the source schema", typeName)
		}
	}
}

func TestExtractSchemas_EmptyArchiveRefused(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if f, err := w.Create("README.txt"); err != nil {
		t.Fatal(err)
	} else if _, err := f.Write([]byte("nothing here")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := extractSchemas(buf.Bytes()); err == nil {
		t.Fatal("extractSchemas on an archive with no schemas: want an error, got nil")
	}
}

// TestAcquireZip_CacheRoundTrip checks all three acquireZip paths with no
// network access at all (issue #42): an override seeds the cache and is
// returned as-is; a pre-populated cache is read back without the override
// and without calling the downloader; neither present falls through to the
// downloader, which is a fake here rather than the real HTTP client - the
// only thing standing between this test and the actual registry endpoint.
func TestAcquireZip_CacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache", cacheFileName)
	overridePath := filepath.Join(dir, "override.zip")

	payload := []byte("pretend zip bytes")
	if err := os.WriteFile(overridePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	failDownload := func(context.Context, string) ([]byte, error) {
		return nil, fmt.Errorf("the downloader must not be called: a cache or override was available")
	}

	// The override seeds the cache, without calling the downloader.
	got, err := acquireZipWith(context.Background(), cachePath, overridePath, testDiscard{}, failDownload)
	if err != nil {
		t.Fatalf("acquireZip with override: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("acquireZip with override returned %q, want %q", got, payload)
	}
	cached, err := os.ReadFile(cachePath) //nolint:gosec // a fixed test path
	if err != nil {
		t.Fatalf("reading the seeded cache: %v", err)
	}
	if !bytes.Equal(cached, payload) {
		t.Errorf("seeded cache holds %q, want %q", cached, payload)
	}

	// With no override, the cache is read back as-is, again without calling
	// the downloader.
	got, err = acquireZipWith(context.Background(), cachePath, "", testDiscard{}, failDownload)
	if err != nil {
		t.Fatalf("acquireZip from cache: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("acquireZip from cache returned %q, want %q", got, payload)
	}

	// Neither override nor cache: acquireZip falls through to the
	// downloader, and only the downloader - a fake here, so this never
	// reaches the network - and caches what it returns.
	downloadPayload := []byte("downloaded zip bytes")
	fakeDownload := func(ctx context.Context, url string) ([]byte, error) {
		if url != registryZipURL {
			t.Errorf("downloader called with %q, want %q", url, registryZipURL)
		}
		return downloadPayload, nil
	}
	emptyCachePath := filepath.Join(dir, "empty", cacheFileName)
	got, err = acquireZipWith(context.Background(), emptyCachePath, "", testDiscard{}, fakeDownload)
	if err != nil {
		t.Fatalf("acquireZip with neither override nor cache: %v", err)
	}
	if !bytes.Equal(got, downloadPayload) {
		t.Errorf("acquireZip via download returned %q, want %q", got, downloadPayload)
	}
	cached, err = os.ReadFile(emptyCachePath) //nolint:gosec // a fixed test path
	if err != nil {
		t.Fatalf("reading the cache the download should have populated: %v", err)
	}
	if !bytes.Equal(cached, downloadPayload) {
		t.Errorf("cache after download holds %q, want %q", cached, downloadPayload)
	}
}

// testDiscard is an io.Writer that discards everything, standing in for
// os.Stderr in tests that don't want acquireZip's progress lines in `go
// test -v` output.
type testDiscard struct{}

func (testDiscard) Write(p []byte) (int, error) { return len(p), nil }
