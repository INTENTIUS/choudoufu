// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/registry/response"
)

// modulePins is the checked-in lock that keeps registry module installation
// reproducible, the same job live/corpus-provider-pins.json does for
// providers (#222) and the pinned commits in live/corpus-manifest.json do
// for the corpus sources themselves.
//
// # Why a version LIST rather than a resolved version
//
// A module block names a source and, usually, a version CONSTRAINT: 178 of
// the 212 versioned module blocks in this corpus use "~>" or ">=" rather
// than an exact version. The installer resolves each constraint to the
// latest matching release the registry currently offers, so an unpinned
// install makes live/corpus-refusals.json move whenever a module author
// publishes - the drift the source pins exist to prevent, reintroduced one
// layer down.
//
// Pinning the RESOLUTION would not work, because two corpus entries can
// impose different constraints on the same package and must still resolve
// differently. What is pinned instead is the registry's answer to "which
// versions of this package exist": [pinnedVersions] replaces the version
// listing the registry returns with the frozen list recorded here, and the
// installer's own constraint matching then runs against that.
//
// That reproduces the original resolution exactly, for every constraint
// this corpus contains, and the argument is short: the installer picks the
// greatest listed version satisfying the constraint. Restricting the
// listing to a subset that still CONTAINS that version cannot produce a
// greater match, and the original match is still available, so the pick is
// unchanged. The frozen list is built as exactly the union of the versions
// this corpus's own constraints resolved to, so it contains every one of
// them by construction.
//
// The consequence to know about: a constraint the lock has never seen -
// which means a corpus source pin was bumped and the configuration changed
// - resolves against the frozen listing rather than against the registry,
// and may pick an older release than a real user would. Bumping a corpus
// source pin therefore means deleting the affected package entries here and
// re-locking in the same commit, the same deliberateness a provider pin bump
// already requires.
//
// # What is NOT pinned
//
// Nothing here pins a go-getter source ("github.com/org/repo"), because
// there is no version listing to freeze: go-getter clones a branch head. In
// this corpus 133 of the 134 such calls carry no "?ref=", so installing
// them would make the corpus float on six third-party repositories' default
// branches. They are therefore left uninstalled by default; see
// [installOptions.RemoteModules].
type modulePins struct {
	Comment []string `json:"_comment,omitempty"`

	// Packages maps a registry package - "<namespace>/<name>/<target
	// system>", the three path components the modules.v1 protocol's version
	// listing endpoint is addressed by - to the frozen version list.
	//
	// The registry hostname is deliberately NOT part of the key. Both sides
	// of this lock have to agree on it and they see different things: the
	// interceptor has only a discovered service URL, whose host can differ
	// from the source address's host, while the collector has only the
	// source address. Two hosts publishing the same namespace/name/system
	// triple would collide; this corpus has 21 distinct packages and 3
	// calls to a non-default host, so the collision is documented rather
	// than defended against.
	Packages map[string][]string `json:"packages"`
}

// packageKey builds the [modulePins.Packages] key from a package's three
// addressing components.
func packageKey(namespace, name, targetSystem string) string {
	return namespace + "/" + name + "/" + targetSystem
}

// packageKeyFromVersionsURL recovers the key from a modules.v1 version
// listing request path, which ends "<namespace>/<name>/<target
// system>/versions" (see registry.modulePackageEndpointURL). It returns ""
// for any other request, which the interceptor passes straight through.
func packageKeyFromVersionsURL(urlPath string) string {
	parts := strings.Split(strings.Trim(path.Clean(urlPath), "/"), "/")
	if len(parts) < 4 || parts[len(parts)-1] != "versions" {
		return ""
	}
	parts = parts[len(parts)-4 : len(parts)-1]
	return packageKey(parts[0], parts[1], parts[2])
}

func loadModulePins(path string) (modulePins, error) {
	var pins modulePins
	data, err := os.ReadFile(path) //nolint:gosec // fixed path under live/, passed by flag
	if errors.Is(err, os.ErrNotExist) {
		pins.Packages = map[string][]string{}
		return pins, nil
	}
	if err != nil {
		return pins, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &pins); err != nil {
		return pins, fmt.Errorf("decoding %s: %w", path, err)
	}
	if pins.Packages == nil {
		pins.Packages = map[string][]string{}
	}
	return pins, nil
}

func writeModulePins(path string, pins modulePins) error {
	pins.Comment = modulePinsComment
	for key, versions := range pins.Packages {
		sort.Strings(versions)
		pins.Packages[key] = versions
	}
	encoded, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o600) //nolint:gosec // generated artifact, not a secret
}

var modulePinsComment = []string{
	"Generated by tools/corpus-fetch -modules. Do not hand-edit except to",
	"DELETE an entry, which is how a package is deliberately re-locked to",
	"whatever the registry offers today.",
	"",
	"Each value is the frozen version listing corpus-fetch serves to the",
	"module installer in place of the registry's own, so that a ranged",
	"version constraint in a corpus configuration resolves to the same",
	"release on every run. See the doc comment on modulePins in",
	"tools/corpus-fetch/modulepins.go for why a listing is pinned rather",
	"than a resolution, and for what this does not cover.",
}

// pinnedVersions is an http.RoundTripper that answers the module registry's
// version listing endpoint from [modulePins] instead of from the network,
// for every package the lock already knows. Everything else - service
// discovery, the per-version download endpoint, the package tarball itself -
// goes to the wrapped transport untouched.
//
// This sits at the HTTP layer rather than inside the installer because the
// installer's version selection is not parameterizable: internal/initwd
// resolves a constraint against whatever registry.Client.ModulePackageVersions
// returns, with no seam for a caller to narrow it. Freezing the registry's
// answer is the same effect with none of the fork's own install path
// changed, which matters because this corpus is meant to measure the
// product's real behaviour rather than a corpus-only variant of it.
type pinnedVersions struct {
	base http.RoundTripper
	pins modulePins

	// seen records every package key a version listing was requested for,
	// whether or not the lock covered it, so a run can report which
	// packages were floating.
	seen map[string]bool
}

func (p *pinnedVersions) RoundTrip(req *http.Request) (*http.Response, error) {
	key := packageKeyFromVersionsURL(req.URL.Path)
	if key == "" {
		return p.base.RoundTrip(req)
	}
	if p.seen != nil {
		p.seen[key] = true
	}
	versions, locked := p.pins.Packages[key]
	if !locked || len(versions) == 0 {
		return p.base.RoundTrip(req)
	}

	listing := response.ModuleVersions{
		Modules: []*response.ModuleProviderVersions{{
			Source:   key,
			Versions: make([]*response.ModuleVersion, 0, len(versions)),
		}},
	}
	for _, v := range versions {
		listing.Modules[0].Versions = append(listing.Modules[0].Versions, &response.ModuleVersion{Version: v})
	}
	body, err := json.Marshal(listing)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(string(body))),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}
