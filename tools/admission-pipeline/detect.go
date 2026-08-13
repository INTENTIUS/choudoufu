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
	"sort"
	"strconv"
	"strings"
)

// providerVersionsURL is the registry.terraform.io versions endpoint for
// the pinned provider (tools/survey-gen/main.go's providerSource).
const providerVersionsURL = "https://registry.terraform.io/v1/providers/hashicorp/aws/versions"

// registryZipURL mirrors tools/registry-gen/fetch.go's own registryZipURL:
// the CloudFormation Registry schema bundle DETECT re-fetches to compute a
// fresh content digest without writing live/registry.json. registry-gen
// itself has no dry-run mode and is package main (not importable), so this
// file duplicates its small acquisition/digest routines in registry_pin.go
// rather than shelling out to a tool that would write the artifact as a
// side effect of merely checking it.
const registryZipURL = "https://schema.cloudformation.us-east-1.amazonaws.com/CloudformationSchema.zip"

// PinState is one pin's before/after: what's committed, what the source
// currently serves, and whether that's a difference DETECT should act on.
type PinState struct {
	Name    string
	Pinned  string
	Current string
	Drifted bool
	// Detail is extra human-readable context - e.g. whether a registry
	// digest move was a resource-set change or byte-only drift. Empty when
	// there's nothing more to say than Pinned/Current.
	Detail string
}

func (p PinState) line() string {
	status := "OK"
	if p.Drifted {
		status = "DRIFT"
	}
	s := fmt.Sprintf("[%s] %s: pinned %s, current %s", status, p.Name, p.Pinned, p.Current)
	if p.Detail != "" {
		s += " (" + p.Detail + ")"
	}
	return s
}

// DriftReport is DETECT's output: one PinState per source.
type DriftReport struct {
	Provider PinState
	Registry PinState
}

// Any reports whether either pin drifted.
func (r DriftReport) Any() bool {
	return r.Provider.Drifted || r.Registry.Drifted
}

// String renders the human-readable drift report DETECT prints before
// deciding whether to proceed.
func (r DriftReport) String() string {
	var b strings.Builder
	fmt.Fprintln(&b, "admission-pipeline: DETECT")
	fmt.Fprintln(&b, "  "+r.Provider.line())
	fmt.Fprintln(&b, "  "+r.Registry.line())
	if !r.Any() {
		fmt.Fprintln(&b, "  no drift")
	}
	return b.String()
}

// httpGetter fetches a URL's body - http.DefaultClient in production,
// swapped for a fixture server (httptest) in tests so Detect's parsing and
// comparison logic runs with no network.
type httpGetter func(ctx context.Context, url string) ([]byte, error)

func httpGet(ctx context.Context, url string) ([]byte, error) {
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

// Detect runs both DETECT checks (provider release, registry digest)
// against the real network and returns the combined report.
func Detect(ctx context.Context, root string) (DriftReport, error) {
	provider, err := detectProvider(ctx, root, httpGet)
	if err != nil {
		return DriftReport{}, fmt.Errorf("provider pin: %w", err)
	}
	registry, err := detectRegistry(ctx, root, httpGet)
	if err != nil {
		return DriftReport{}, fmt.Errorf("registry pin: %w", err)
	}
	return DriftReport{Provider: provider, Registry: registry}, nil
}

// pinnedProviderVersion reads the provider version committed in
// live/survey.json's header - the value survey-gen's own unexported
// providerVersion constant stamped onto the artifact it last wrote, so
// DETECT reads committed data rather than parsing Go source.
func pinnedProviderVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "live", "survey.json"))
	if err != nil {
		return "", fmt.Errorf("reading live/survey.json: %w", err)
	}
	var hdr struct {
		ProviderVersion string `json:"provider_version"`
	}
	if err := json.Unmarshal(data, &hdr); err != nil {
		return "", fmt.Errorf("parsing live/survey.json: %w", err)
	}
	if hdr.ProviderVersion == "" {
		return "", fmt.Errorf("live/survey.json carries no provider_version")
	}
	return hdr.ProviderVersion, nil
}

// providerVersionsResponse is the shape of registry.terraform.io's
// /v1/providers/<ns>/<type>/versions response - only the fields DETECT
// reads.
type providerVersionsResponse struct {
	Versions []struct {
		Version string `json:"version"`
	} `json:"versions"`
}

// latestStableVersion picks the highest non-prerelease x.y.z version out of
// a registry.terraform.io versions response body. Prereleases (versions
// carrying a "-" suffix, e.g. "6.0.0-beta1") are excluded - DETECT compares
// against what a plain provider pin bump would take, not a beta.
func latestStableVersion(body []byte) (string, error) {
	var resp providerVersionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing the versions response: %w", err)
	}

	var best string
	var bestKey [3]int
	found := false
	for _, v := range resp.Versions {
		if strings.Contains(v.Version, "-") {
			continue
		}
		key, ok := parseSemverCore(v.Version)
		if !ok {
			continue
		}
		if !found || semverLess(bestKey, key) {
			best, bestKey, found = v.Version, key, true
		}
	}
	if !found {
		return "", fmt.Errorf("no stable x.y.z version found in the versions response")
	}
	return best, nil
}

// parseSemverCore parses "x.y.z" into three ints; anything else (a
// prerelease suffix should already be stripped by the caller, build
// metadata or a malformed version) reports ok=false.
func parseSemverCore(v string) ([3]int, bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func semverLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func detectProvider(ctx context.Context, root string, fetch httpGetter) (PinState, error) {
	pinned, err := pinnedProviderVersion(root)
	if err != nil {
		return PinState{}, err
	}

	body, err := fetch(ctx, providerVersionsURL)
	if err != nil {
		return PinState{}, fmt.Errorf("fetching %s: %w", providerVersionsURL, err)
	}

	latest, err := latestStableVersion(body)
	if err != nil {
		return PinState{}, fmt.Errorf("parsing %s: %w", providerVersionsURL, err)
	}

	return PinState{
		Name:    "provider hashicorp/aws",
		Pinned:  pinned,
		Current: latest,
		Drifted: latest != pinned,
	}, nil
}

// committedRegistryPin reads live/registry.json's pin header - the same
// digest/resources tools/registry-gen/pinned_spec.go's PinnedSpec carries,
// reflected in the committed artifact for the same reason
// pinnedProviderVersion reads survey.json instead of parsing Go source.
func committedRegistryPin(root string) (digest string, resources int, err error) {
	data, err := os.ReadFile(filepath.Join(root, "live", "registry.json"))
	if err != nil {
		return "", 0, fmt.Errorf("reading live/registry.json: %w", err)
	}
	var hdr struct {
		Pin struct {
			Digest    string `json:"digest"`
			Resources int    `json:"resources"`
		} `json:"pin"`
	}
	if err := json.Unmarshal(data, &hdr); err != nil {
		return "", 0, fmt.Errorf("parsing live/registry.json: %w", err)
	}
	if hdr.Pin.Digest == "" {
		return "", 0, fmt.Errorf("live/registry.json carries no pin.digest")
	}
	return hdr.Pin.Digest, hdr.Pin.Resources, nil
}

// pinnedRegistryTypeNames reads tools/registry-gen/pinned-types.json - a
// plain committed data file, not Go source, so DETECT reads it directly
// instead of importing registry-gen (which is package main).
func pinnedRegistryTypeNames(root string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "tools", "registry-gen", "pinned-types.json"))
	if err != nil {
		return nil, fmt.Errorf("reading tools/registry-gen/pinned-types.json: %w", err)
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("parsing tools/registry-gen/pinned-types.json: %w", err)
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out, nil
}

func detectRegistry(ctx context.Context, root string, fetch httpGetter) (PinState, error) {
	digest, resources, err := committedRegistryPin(root)
	if err != nil {
		return PinState{}, err
	}
	pinnedNames, err := pinnedRegistryTypeNames(root)
	if err != nil {
		return PinState{}, err
	}

	zipData, err := fetch(ctx, registryZipURL)
	if err != nil {
		return PinState{}, fmt.Errorf("fetching %s: %w", registryZipURL, err)
	}
	schemas, err := extractRegistrySchemas(zipData)
	if err != nil {
		return PinState{}, err
	}
	current := registryContentDigest(schemas)

	state := PinState{
		Name:    "CFN registry schema (#42)",
		Pinned:  fmt.Sprintf("%s (%d types)", digest, resources),
		Current: fmt.Sprintf("%s (%d types)", current, len(schemas)),
		Drifted: current != digest,
	}
	if state.Drifted {
		added, removed := diffTypeSets(schemas, pinnedNames)
		if len(added) > 0 || len(removed) > 0 {
			state.Detail = fmt.Sprintf("resource set moved: +%d/-%d types", len(added), len(removed))
		} else {
			state.Detail = "byte-only drift: schema content edited, type set unchanged"
		}
	}
	return state, nil
}

// sortedKeys is a small shared helper (report.go's diffCountsLine uses it
// too) - sorted map[string]any keys, for deterministic output.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
