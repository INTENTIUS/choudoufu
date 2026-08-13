// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// registryZipURL is the CloudFormation Registry schema bundle: a single
// "latest" artifact with no version in the path, republished constantly.
const registryZipURL = "https://schema.cloudformation.us-east-1.amazonaws.com/CloudformationSchema.zip"

// cacheFileName is the cached copy's name under the cache directory.
const cacheFileName = "CloudformationSchema.zip"

// defaultCacheDir is where the downloaded bundle is cached: the OS user
// cache directory, namespaced the way a second tool's cache would need to
// avoid colliding with survey-gen's provider cache (which is
// TF_PLUGIN_CACHE_DIR / Terraform's own, not this tool's to manage).
func defaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory: %w", err)
	}
	return filepath.Join(base, "choudoufu", "registry-gen"), nil
}

// defaultZipPath is the cached bundle's path: defaultCacheDir plus
// cacheFileName. Both registry-gen itself and the gated test that asserts
// the artifact against a real bundle (registry_gen_test.go) resolve the
// cache through this one function, so seeding it once (via -zip) satisfies
// both.
func defaultZipPath() (string, error) {
	dir, err := defaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheFileName), nil
}

// acquireZip resolves the CloudFormation Registry schema zip: an explicit
// override (which also seeds/refreshes the cache from it, so a later run
// needs no flag), the cached copy at cachePath, or - only when neither
// exists - a fresh download.
// downloader fetches the bundle from a URL - downloadZip in production,
// swapped for a fake in tests so acquireZip's fall-through-to-download path
// is exercised without ever reaching the network (issue #42's "no network
// access in tests").
type downloader func(ctx context.Context, url string) ([]byte, error)

func acquireZip(ctx context.Context, cachePath, override string, log io.Writer) ([]byte, error) {
	return acquireZipWith(ctx, cachePath, override, log, downloadZip)
}

func acquireZipWith(ctx context.Context, cachePath, override string, log io.Writer, download downloader) ([]byte, error) {
	if override != "" {
		data, err := os.ReadFile(override) //nolint:gosec // an operator-supplied path, the same trust level as -init-bin
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", override, err)
		}
		if err := cacheZip(cachePath, data); err != nil {
			return nil, err
		}
		fmt.Fprintf(log, "registry-gen: seeded the cache at %s from %s\n", cachePath, override)
		return data, nil
	}

	data, err := os.ReadFile(cachePath) //nolint:gosec // a fixed cache path this tool itself wrote
	switch {
	case err == nil:
		fmt.Fprintf(log, "registry-gen: using the cached bundle at %s\n", cachePath)
		return data, nil
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("reading the cached bundle at %s: %w", cachePath, err)
	}

	fmt.Fprintf(log, "registry-gen: downloading %s\n", registryZipURL)
	data, err = download(ctx, registryZipURL)
	if err != nil {
		return nil, err
	}
	if err := cacheZip(cachePath, data); err != nil {
		return nil, err
	}
	return data, nil
}

// cacheZip writes data to path, creating its parent directory as needed.
func cacheZip(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a cache directory, not a secret
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a cached download, not a secret
		return fmt.Errorf("caching the bundle at %s: %w", path, err)
	}
	return nil
}

// downloadZip fetches the bundle over HTTP.
func downloadZip(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only response body
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the response body from %s: %w", url, err)
	}
	return data, nil
}

// extractSchemas reads the zip archive and returns the raw per-type JSON
// schema bytes keyed by typeName, mirroring chant's extractRawSchemas
// (lexicons/aws/src/spec/fetch.ts). An entry that isn't JSON, or parses but
// carries no typeName, is skipped rather than failing the whole extraction
// - the registry bundle is upstream's to shape, not ours to validate.
func extractSchemas(zipData []byte) (map[string][]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("opening the registry zip: %w", err)
	}

	schemas := make(map[string][]byte, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".json") {
			continue
		}
		data, err := readZipFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s in the registry zip: %w", f.Name, err)
		}
		var probe struct {
			TypeName string `json:"typeName"`
		}
		if err := json.Unmarshal(data, &probe); err != nil || probe.TypeName == "" {
			continue
		}
		schemas[probe.TypeName] = data
	}
	if len(schemas) == 0 {
		return nil, fmt.Errorf("the registry zip carried no schemas with a typeName")
	}
	return schemas, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close() //nolint:errcheck // a read-only zip entry
	return io.ReadAll(rc)
}
