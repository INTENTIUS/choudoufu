// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// providerSource and providerVersion pin the provider release these docs
// are fetched at - the same release tools/survey-gen surveys
// (tools/survey-gen/main.go's own providerSource/providerVersion) and the
// estate fixture pins (live/e2e/estate/versions.tf). Mirrored rather than
// imported: each generator under tools/ is its own package main, so there
// is no shared constant to reach into without a larger refactor (issue #55
// names this the same tradeoff survey-gen's doc comment already accepts).
const (
	providerSource  = "hashicorp/aws"
	providerVersion = "6.58.0"

	providerOwner = "hashicorp"
	providerRepo  = "terraform-provider-aws"
)

// docURLTemplate is the provider's own Import documentation, fetched at
// the pinned release tag from GitHub raw content - the same per-file,
// on-disk-cached shape tools/registry-gen's zip cache uses, just one file
// per resource type instead of one zip for the whole registry. The
// extension is ".html.markdown", not ".markdown" - confirmed against the
// v6.58.0 tag's actual tree; earlier design notes assumed the shorter
// extension and were wrong.
const docURLTemplate = "https://raw.githubusercontent.com/%s/%s/v%s/website/docs/r/%s.html.markdown"

// slugForType derives a doc's filename slug from its TF type name:
// "aws_iam_role_policy_attachment" -> "iam_role_policy_attachment". Not
// every TF type has its own doc - aliases like aws_alb are documented
// once under their canonical name (aws_lb, "lb.html.markdown") - so a slug
// that 404s is an expected outcome, not an error.
func slugForType(tfType string) string {
	return strings.TrimPrefix(tfType, "aws_")
}

// defaultCacheDir is where fetched docs are cached: the OS user cache
// directory, namespaced per provider version so a provider bump does not
// silently serve stale docs out of an old cache - the same reasoning
// tools/registry-gen/fetch.go's defaultCacheDir gives for its own
// namespacing, one level deeper here because unlike the registry zip this
// source *does* have a version to key on.
func defaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory: %w", err)
	}
	return filepath.Join(base, "choudoufu", "importdocs-gen", providerVersion), nil
}

// notFoundSuffix marks a cached 404: caching the miss, not just the hit,
// is what makes a full ~1400-file sweep free on rerun (issue's own framing
// - "the tool fetches per-file lazily with an on-disk cache ... A full
// sweep is ~1400 files; cache makes reruns free").
const notFoundSuffix = ".notfound"

// fetcher performs one HTTP GET, returning the status code and body.
// Swapped for a fake in tests, the same seam
// tools/registry-gen/fetch.go's downloader type gives acquireZip.
type fetcher func(ctx context.Context, url string) (status int, body []byte, err error)

// httpFetch is the production fetcher.
func httpFetch(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
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

// acquireDoc resolves one type's Import doc: the on-disk cache (a hit or a
// cached 404), or - only on a cache miss - a fetch via fetch, caching
// whichever outcome it gets. found is false for a 404 (real or cached);
// err is non-nil only for a transport failure or an unexpected status.
func acquireDoc(ctx context.Context, cacheDir, tfType string, fetch fetcher) (data []byte, found bool, err error) {
	slug := slugForType(tfType)
	docPath := filepath.Join(cacheDir, slug+".html.markdown")
	notFoundPath := docPath + notFoundSuffix

	if data, err := os.ReadFile(docPath); err == nil { //nolint:gosec // a fixed cache path this tool itself wrote
		return data, true, nil
	} else if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("reading the cached doc at %s: %w", docPath, err)
	}
	if _, err := os.Stat(notFoundPath); err == nil {
		return nil, false, nil
	}

	url := fmt.Sprintf(docURLTemplate, providerOwner, providerRepo, providerVersion, slug)
	status, body, err := fetch(ctx, url)
	if err != nil {
		return nil, false, err
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil { //nolint:gosec // a cache directory, not a secret
		return nil, false, fmt.Errorf("creating the doc cache directory %s: %w", cacheDir, err)
	}

	switch status {
	case http.StatusOK:
		if err := os.WriteFile(docPath, body, 0o644); err != nil { //nolint:gosec // a cached download, not a secret
			return nil, false, fmt.Errorf("caching %s: %w", docPath, err)
		}
		return body, true, nil
	case http.StatusNotFound:
		if err := os.WriteFile(notFoundPath, nil, 0o644); err != nil { //nolint:gosec // a cache sentinel, not a secret
			return nil, false, fmt.Errorf("caching the not-found sentinel at %s: %w", notFoundPath, err)
		}
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("GET %s: unexpected status %d", url, status)
	}
}
