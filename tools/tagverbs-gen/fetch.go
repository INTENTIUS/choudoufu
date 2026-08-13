// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// botocoreOwner, botocoreRepo and botocoreTag pin the botocore release this
// tool reads service models from. Unlike the CloudFormation Registry zip
// tools/registry-gen fetches (a single "latest" artifact with no version in
// its URL, forcing a content-hash pin), botocore ships real git tags, so a
// tag is the pin - the same shape tools/importdocs-gen's providerVersion
// gives its provider release.
const (
	botocoreOwner = "boto"
	botocoreRepo  = "botocore"
	botocoreTag   = "1.43.70"
)

// treeAPIURLTemplate is GitHub's git-trees API, fetched once with
// recursive=1 to list every file in the pinned tag in a single call. This is
// deliberately the trees API rather than the contents API used per
// directory: the contents API is rate-limited to 60 unauthenticated
// requests/hour, and botocore's data/ directory alone carries ~460 service
// subdirectories - a per-directory listing call would exhaust that budget on
// one run. The trees API's single recursive call costs one request no matter
// how many services this tool ends up needing.
const treeAPIURLTemplate = "https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1"

// rawURLTemplate is where one resolved service's model is actually read
// from - raw.githubusercontent.com, which is not subject to the API's
// request-rate limit.
const rawURLTemplate = "https://raw.githubusercontent.com/%s/%s/%s/%s"

// defaultCacheDir is where fetched data is cached: the OS user cache
// directory, namespaced by the pinned botocore tag - the same reasoning
// tools/importdocs-gen's defaultCacheDir gives for namespacing by provider
// version, so a botocore bump does not silently serve a stale cache.
func defaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory: %w", err)
	}
	return filepath.Join(base, "choudoufu", "tagverbs-gen", botocoreTag), nil
}

// fetcher performs one HTTP GET, returning the status code and body - the
// same seam tools/importdocs-gen/fetch.go's fetcher gives its own acquire
// step, swapped for a fake in tests so no test ever reaches the network.
type fetcher func(ctx context.Context, url string) (status int, body []byte, err error)

// httpFetch is the production fetcher.
func httpFetch(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	// GitHub's API refuses requests with no User-Agent.
	req.Header.Set("User-Agent", "choudoufu-tagverbs-gen")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only response body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("reading the response body from %s: %w", url, err)
	}
	return resp.StatusCode, data, nil
}

// treeResponse is the slice of GitHub's git-trees API response this tool
// reads.
type treeResponse struct {
	Truncated bool `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"tree"`
}

// treeCacheFile is the cached, already-filtered path list's name under the
// cache directory.
const treeCacheFile = "tree.json"

// fetchTree resolves the pinned tag's full list of
// data/<service>/<version>/service-2.json paths: the on-disk cache when
// present, or one recursive git-trees API call otherwise, caching the
// filtered result so a rerun costs no further API request at all.
func fetchTree(ctx context.Context, cacheDir string, fetch fetcher) ([]string, error) {
	cachePath := filepath.Join(cacheDir, treeCacheFile)
	if data, err := os.ReadFile(cachePath); err == nil { //nolint:gosec // a fixed cache path this tool itself wrote
		var paths []string
		if err := json.Unmarshal(data, &paths); err != nil {
			return nil, fmt.Errorf("decoding the cached tree listing at %s: %w", cachePath, err)
		}
		return paths, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading the cached tree listing at %s: %w", cachePath, err)
	}

	url := fmt.Sprintf(treeAPIURLTemplate, botocoreOwner, botocoreRepo, botocoreTag)
	status, body, err := fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %d", url, status)
	}

	var tr treeResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("decoding the tree response: %w", err)
	}
	if tr.Truncated {
		return nil, fmt.Errorf("the tree response was truncated by GitHub's API; the recursive listing is incomplete and cannot be trusted")
	}

	var paths []string
	for _, entry := range tr.Tree {
		if entry.Type != "blob" {
			continue
		}
		if strings.HasPrefix(entry.Path, "botocore/data/") && strings.HasSuffix(entry.Path, "/service-2.json") {
			paths = append(paths, entry.Path)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("the tree response named no botocore/data/*/*/service-2.json paths at all")
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil { //nolint:gosec // a cache directory, not a secret
		return nil, fmt.Errorf("creating the cache directory %s: %w", cacheDir, err)
	}
	cacheData, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cachePath, cacheData, 0o644); err != nil { //nolint:gosec // a cached listing, not a secret
		return nil, fmt.Errorf("caching the tree listing at %s: %w", cachePath, err)
	}
	return paths, nil
}

// latestVersions groups service-2.json paths by their service directory and
// keeps the lexicographically greatest version per directory. Every version
// observed across the services this tool actually resolves is an ISO date
// (2016-11-15, 2010-08-01, ...), for which lexicographic and chronological
// order coincide; a service whose versioning does not follow that
// convention would still get a deterministic answer, just not necessarily
// the newest one - a case this tool has not seen in practice and does not
// specially guard against.
func latestVersions(paths []string) map[string]string {
	out := map[string]string{}
	for _, p := range paths {
		// botocore/data/<service>/<version>/service-2.json
		parts := strings.Split(p, "/")
		if len(parts) != 5 {
			continue
		}
		service, version := parts[2], parts[3]
		if cur, ok := out[service]; !ok || version > cur {
			out[service] = version
		}
	}
	return out
}

// fetchServiceModel resolves one service's model JSON at its resolved
// version: the on-disk cache, or a fetch from raw.githubusercontent.com
// otherwise, caching whichever it gets.
func fetchServiceModel(ctx context.Context, cacheDir, service, version string, fetch fetcher) ([]byte, error) {
	cachePath := filepath.Join(cacheDir, "services", service+"__"+version+".json")
	if data, err := os.ReadFile(cachePath); err == nil { //nolint:gosec // a fixed cache path this tool itself wrote
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading the cached model at %s: %w", cachePath, err)
	}

	path := fmt.Sprintf("botocore/data/%s/%s/service-2.json", service, version)
	url := fmt.Sprintf(rawURLTemplate, botocoreOwner, botocoreRepo, botocoreTag, path)
	status, body, err := fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %d", url, status)
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil { //nolint:gosec // a cache directory, not a secret
		return nil, fmt.Errorf("creating the model cache directory: %w", err)
	}
	if err := os.WriteFile(cachePath, body, 0o644); err != nil { //nolint:gosec // a cached download, not a secret
		return nil, fmt.Errorf("caching %s: %w", cachePath, err)
	}
	return body, nil
}
