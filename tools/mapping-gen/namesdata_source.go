// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Content pin and acquisition for names_data.hcl (issue #52), following the
// #42 pattern: {digest, accepted, roster-ish summary}. Unlike the
// CloudFormation Registry zip (a "latest" artifact with nothing to version),
// names_data.hcl is fetched at a pinned provider release tag - a real
// version exists, so the pin's Digest is a belt-and-suspenders check that
// the tag's content has not moved underneath the tag (rare, but not
// impossible for a force-pushed tag) rather than the only way to detect
// drift the CFN zip pin exists for. Any digest mismatch refuses outright;
// there is no soft-warn tier the way the CFN zip pin has, because there is
// no legitimate "upstream repackaged the same content" case for a fetch
// pinned to an immutable tag.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// namesDataTag is the pinned hashicorp/terraform-provider-aws release tag
// names_data.hcl is fetched from.
const namesDataTag = "v6.58.0"

// namesDataURL is names_data.hcl's raw content at the pinned tag.
const namesDataURL = "https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/" + namesDataTag + "/names/data/names_data.hcl"

// namesDataAcceptEnv accepts whatever the pinned tag currently serves when
// the cached/downloaded content no longer matches the committed digest:
// generation proceeds and warns instead of refusing, printing the pin block
// to paste into namesDataPin below.
const namesDataAcceptEnv = "CHOUDOUFU_ACCEPT_NAMES_DATA"

// namesDataCachePath resolves the on-disk cache location for the downloaded
// file, namespaced under this tool the same way registry-gen's
// defaultCacheDir is.
func namesDataCachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving the user cache directory: %w", err)
	}
	return filepath.Join(base, "choudoufu", "mapping-gen", "names_data-"+namesDataTag+".hcl"), nil
}

// nsDownloader fetches names_data.hcl's raw bytes - downloadNamesData in
// production, swapped for a fake in tests so acquireNamesData's
// fall-through-to-download path never reaches the network.
type nsDownloader func(ctx context.Context, url string) ([]byte, error)

// acquireNamesData resolves names_data.hcl's raw bytes: an explicit
// override (which also seeds/refreshes the cache from it), the cached copy,
// or - only when neither exists - a fresh download. Mirrors
// tools/registry-gen/fetch.go's acquireZip exactly, one file instead of a
// zip archive.
func acquireNamesData(ctx context.Context, cachePath, override string, log io.Writer) ([]byte, error) {
	return acquireNamesDataWith(ctx, cachePath, override, log, downloadNamesData)
}

func acquireNamesDataWith(ctx context.Context, cachePath, override string, log io.Writer, download nsDownloader) ([]byte, error) {
	if override != "" {
		data, err := os.ReadFile(override) //nolint:gosec // an operator-supplied path
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", override, err)
		}
		if err := cacheNamesData(cachePath, data); err != nil {
			return nil, err
		}
		fmt.Fprintf(log, "mapping-gen: seeded the names_data cache at %s from %s\n", cachePath, override)
		return data, nil
	}

	data, err := os.ReadFile(cachePath) //nolint:gosec // a fixed cache path this tool itself wrote
	switch {
	case err == nil:
		fmt.Fprintf(log, "mapping-gen: using the cached names_data.hcl at %s\n", cachePath)
		return data, nil
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("reading the cached names_data.hcl at %s: %w", cachePath, err)
	}

	fmt.Fprintf(log, "mapping-gen: downloading %s\n", namesDataURL)
	data, err = download(ctx, namesDataURL)
	if err != nil {
		return nil, err
	}
	if err := cacheNamesData(cachePath, data); err != nil {
		return nil, err
	}
	return data, nil
}

func cacheNamesData(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a cache directory
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a cached download
		return fmt.Errorf("caching names_data.hcl at %s: %w", path, err)
	}
	return nil
}

func downloadNamesData(ctx context.Context, url string) ([]byte, error) {
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

// namesDataDigest hashes the raw file content: "sha256:<hex>".
func namesDataDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NamesDataPin is the committed pin over names_data.hcl's raw content at
// namesDataTag.
type NamesDataPin struct {
	Digest     string `json:"digest"`
	Tag        string `json:"tag"`
	Accepted   string `json:"accepted"`
	Families   int    `json:"families"`
	Prefixes   int    `json:"prefixes"`
	Mismatches int    `json:"mismatches"`
}

// NamesDataArtifact is the committed derived artifact
// (tools/mapping-gen/namesdata-generated.json): the pin, the generated
// service_aliases-shaped table, and the full mismatch roster (issue #52's
// "count them and report" requirement - the roster, not just the count, so
// a reviewer can see exactly which AWS ids failed to join).
type NamesDataArtifact struct {
	Pin        NamesDataPin        `json:"pin"`
	Aliases    map[string][]string `json:"aliases"`
	Mismatches []NamesDataMismatch `json:"mismatches"`
}

// buildNamesDataArtifact parses raw names_data.hcl content and derives the
// full committed artifact, digest included.
func buildNamesDataArtifact(raw []byte, cfnTypes []string, accepted string) (NamesDataArtifact, error) {
	services, err := parseNamesData(raw, "names_data.hcl")
	if err != nil {
		return NamesDataArtifact{}, err
	}
	aliases, mismatches := deriveServiceAliases(services, cfnTypes)
	sort.Slice(mismatches, func(i, j int) bool {
		if mismatches[i].Prefix != mismatches[j].Prefix {
			return mismatches[i].Prefix < mismatches[j].Prefix
		}
		return mismatches[i].AWSID < mismatches[j].AWSID
	})
	return NamesDataArtifact{
		Pin: NamesDataPin{
			Digest:     namesDataDigest(raw),
			Tag:        namesDataTag,
			Accepted:   accepted,
			Families:   len(services),
			Prefixes:   len(aliases),
			Mismatches: len(mismatches),
		},
		Aliases:    aliases,
		Mismatches: mismatches,
	}, nil
}

// marshal renders the artifact deterministically, the same shape
// live/mapping.json's own Mapping.marshal uses: sorted map keys (Go's
// encoding/json already sorts map[string]... keys), two-space indent,
// trailing newline, no HTML escaping.
func (a NamesDataArtifact) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// AssertNamesDataPinned enforces the pin: nil when raw's digest matches
// pin.Digest, or when accept is true (proceeds anyway, warning); an error
// naming the drift otherwise.
func AssertNamesDataPinned(raw []byte, pin NamesDataPin, accept bool, warn func(string)) error {
	digest := namesDataDigest(raw)
	if digest == pin.Digest {
		return nil
	}
	msg := fmt.Sprintf(
		"names_data.hcl at %s has moved since the pinned digest.\n\n  pinned    %s (accepted %s)\n  fetched   %s\n\nEither the tag was force-moved (rare) or the local cache is stale. To refresh the pin, rerun with -refresh-names-data and %s=1, review the regenerated tools/mapping-gen/namesdata-generated.json diff, and commit the new digest.",
		namesDataTag, pin.Digest, pin.Accepted, digest, namesDataAcceptEnv,
	)
	if accept {
		warn(msg)
		return nil
	}
	return fmt.Errorf("%s", msg)
}

//go:embed namesdata-generated.json
var namesDataGeneratedJSON []byte

// PinnedNamesData is the committed derived artifact: the default,
// no-network source buildMapping reads (mirrors live/registry.json being
// read directly rather than mapping-gen re-parsing the CFN zip itself).
var PinnedNamesData = mustLoadNamesDataArtifact()

func mustLoadNamesDataArtifact() NamesDataArtifact {
	var art NamesDataArtifact
	if err := json.Unmarshal(namesDataGeneratedJSON, &art); err != nil {
		panic(fmt.Sprintf("mapping-gen: namesdata-generated.json does not parse: %v", err))
	}
	return art
}
