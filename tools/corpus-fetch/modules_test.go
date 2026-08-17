// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	version "github.com/hashicorp/go-version"

	"github.com/intentius/choudoufu/internal/modsdir"
	"github.com/intentius/choudoufu/internal/registry/response"
)

func TestPackageKeyFromVersionsURL(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/v1/modules/terraform-aws-modules/vpc/aws/versions", "terraform-aws-modules/vpc/aws"},
		{"/v1/modules/terraform-aws-modules/vpc/aws/6.6.1/download", ""},
		{"/.well-known/terraform.json", ""},
		{"/registry/v1/modules/ns/name/sys/versions", "ns/name/sys"},
		{"/versions", ""},
	} {
		if got := packageKeyFromVersionsURL(tc.path); got != tc.want {
			t.Errorf("packageKeyFromVersionsURL(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// stubTransport stands in for the network and records whether it was
// reached at all, which is the property the lock exists to control.
type stubTransport struct {
	reached []string
	body    string
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.reached = append(s.reached, req.URL.Path)
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    req,
	}, nil
}

// TestPinnedVersionsServesTheLock is the test that has to be able to fail:
// it mutates the lock to a version the registry is not offering and asserts
// the installer's own view changes with it. A rule that only reproduced
// whatever the network said would pass no matter what the lock held.
func TestPinnedVersionsServesTheLock(t *testing.T) {
	live := `{"modules":[{"source":"terraform-aws-modules/vpc/aws","versions":[{"version":"9.9.9"}]}]}`
	base := &stubTransport{body: live}
	p := &pinnedVersions{
		base: base,
		pins: modulePins{Packages: map[string][]string{
			"terraform-aws-modules/vpc/aws": {"5.0.0", "6.1.0"},
		}},
		seen: map[string]bool{},
	}

	got := roundTripVersions(t, p, "https://registry.opentofu.org/v1/modules/terraform-aws-modules/vpc/aws/versions")
	if want := []string{"5.0.0", "6.1.0"}; !equalStrings(got, want) {
		t.Errorf("locked package served %v, want %v", got, want)
	}
	if len(base.reached) != 0 {
		t.Errorf("a locked package still reached the network: %v", base.reached)
	}
	if !p.seen["terraform-aws-modules/vpc/aws"] {
		t.Error("the queried package was not recorded as seen")
	}

	// An unlocked package must fall through, so a first-seen requirement can
	// still be acquired and then locked - the same first-run behaviour
	// live/corpus-provider-pins.json has.
	got = roundTripVersions(t, p, "https://registry.opentofu.org/v1/modules/other/thing/aws/versions")
	if want := []string{"9.9.9"}; !equalStrings(got, want) {
		t.Errorf("unlocked package served %v, want %v", got, want)
	}
	if len(base.reached) != 1 {
		t.Errorf("an unlocked package should have reached the network exactly once, got %v", base.reached)
	}

	// Anything that is not a version listing is none of this transport's
	// business, including the download endpoint the version above is about
	// to be fetched from.
	if _, err := p.RoundTrip(mustRequest(t, "https://registry.opentofu.org/v1/modules/terraform-aws-modules/vpc/aws/6.1.0/download")); err != nil {
		t.Fatal(err)
	}
	if len(base.reached) != 2 {
		t.Errorf("the download endpoint was intercepted; reached %v", base.reached)
	}
}

func roundTripVersions(t *testing.T, p *pinnedVersions, rawURL string) []string {
	t.Helper()
	resp, err := p.RoundTrip(mustRequest(t, rawURL))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded response.ModuleVersions
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Modules) != 1 {
		t.Fatalf("got %d module entries, want 1", len(decoded.Modules))
	}
	out := make([]string, 0, len(decoded.Modules[0].Versions))
	for _, v := range decoded.Modules[0].Versions {
		out = append(out, v.Version)
	}
	return out
}

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{Method: "GET", URL: u, Header: http.Header{}}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSourceKindUsesTheAddressParser(t *testing.T) {
	for source, want := range map[string]moduleSourceKind{
		"./modules/db": sourceLocal,
		"../..":        sourceLocal,
		"registry.opentofu.org/terraform-aws-modules/vpc/aws": sourceRegistry,
		"terraform-aws-modules/vpc/aws":                       sourceRegistry,
		"git::https://github.com/cisagov/x.git":               sourceGoGetter,
		"github.com/alphagov/terraform-govuk-tfe-workspacer":  sourceGoGetter,
	} {
		if got := sourceKind(source); got != want {
			t.Errorf("sourceKind(%q) = %v, want %v", source, got, want)
		}
	}
}

func TestPostProcessDropsGoGetterAndRelativizes(t *testing.T) {
	entryDir := filepath.Join(string(filepath.Separator), "corpus", "entry")
	v := version.Must(version.NewVersion("6.1.0"))
	installed := modsdir.Manifest{
		"": {Key: "", Dir: entryDir},
		"vpc": {
			Key:        "vpc",
			SourceAddr: "registry.opentofu.org/terraform-aws-modules/vpc/aws",
			Version:    v,
			Dir:        filepath.Join(entryDir, ".terraform", "modules", "vpc"),
		},
		"shared": {
			Key:        "shared",
			SourceAddr: "github.com/alphagov/terraform-govuk-tfe-workspacer",
			Dir:        filepath.Join(entryDir, ".terraform", "modules", "shared"),
		},
		// A registry module reached only THROUGH the go-getter one. It has
		// to go too: check.Load cannot see a child whose parent it never
		// resolved, so keeping the record would advertise a module nothing
		// can reach, and locking its version would put a package in the
		// lock that no measurement depends on.
		"shared.iam": {
			Key:        "shared.iam",
			SourceAddr: "registry.opentofu.org/terraform-aws-modules/iam/aws",
			Version:    version.Must(version.NewVersion("5.28.0")),
			Dir:        filepath.Join(entryDir, ".terraform", "modules", "shared.iam"),
		},
		"db": {
			Key:        "db",
			SourceAddr: "../..",
			Dir:        filepath.Join(string(filepath.Separator), "corpus", "root"),
		},
	}

	res := postProcess(installed, entryDir, nil, false)
	out, dropped, droppedSources, registryVersions := res.manifest, res.dropped, res.droppedSources, res.registryVersions

	if dropped != 2 {
		t.Errorf("dropped %d records, want 2 (the go-getter call and its descendant)", dropped)
	}
	if _, ok := out["shared"]; ok {
		t.Error("the go-getter record survived")
	}
	if _, ok := out["shared.iam"]; ok {
		t.Error("a module reached only through the dropped go-getter record survived")
	}
	if got := droppedSources["github.com/alphagov/terraform-govuk-tfe-workspacer"]; got != 1 {
		t.Errorf("dropped-source count for the go-getter source = %d, want 1", got)
	}
	if got := out["vpc"].Dir; got != ".terraform/modules/vpc" {
		t.Errorf("vpc Dir = %q, want a path relative to the entry", got)
	}
	if got := out["db"].Dir; got != "../root" {
		t.Errorf("local Dir outside the entry = %q, want %q", got, "../root")
	}
	if got := out[""].Dir; got != "." {
		t.Errorf("root Dir = %q, want %q", got, ".")
	}

	want := map[string]map[string]bool{
		"terraform-aws-modules/vpc/aws": {"6.1.0": true},
	}
	if len(registryVersions) != len(want) {
		t.Fatalf("collected %v, want %v", registryVersions, want)
	}
	for key, versions := range want {
		for v := range versions {
			if !registryVersions[key][v] {
				t.Errorf("missing %s %s from the collected versions: %v", key, v, registryVersions)
			}
		}
	}
}

func TestPostProcessKeepsGoGetterWhenAsked(t *testing.T) {
	entryDir := filepath.Join(string(filepath.Separator), "corpus", "entry")
	installed := modsdir.Manifest{
		"":       {Key: "", Dir: entryDir},
		"shared": {Key: "shared", SourceAddr: "github.com/alphagov/x", Dir: filepath.Join(entryDir, ".terraform", "modules", "shared")},
	}
	res := postProcess(installed, entryDir, nil, true)
	if res.dropped != 0 {
		t.Errorf("dropped %d with -remote-modules, want 0", res.dropped)
	}
	if _, ok := res.manifest["shared"]; !ok {
		t.Error("the go-getter record was dropped despite -remote-modules")
	}
	if len(res.unpinnedGit) != 1 || res.unpinnedGit[0] != "https://github.com/alphagov/x.git" {
		t.Errorf("unpinned go-getter repositories = %v, want the one kept record's clone URL", res.unpinnedGit)
	}
}

// TestPostProcessKeepsAPinnedGoGetterRecord is the half of the go-getter
// rule that the drop-everything default used to make unreachable: a
// repository live/corpus-manifest.json pins was installed from a local
// mirror at a frozen commit, so its record is as reproducible as a registry
// record and must survive with no flag.
func TestPostProcessKeepsAPinnedGoGetterRecord(t *testing.T) {
	entryDir := filepath.Join(string(filepath.Separator), "corpus", "entry")
	installed := modsdir.Manifest{
		"":       {Key: "", Dir: entryDir},
		"pinned": {Key: "pinned", SourceAddr: "github.com/alphagov/terraform-govuk-tfe-workspacer", Dir: filepath.Join(entryDir, ".terraform", "modules", "pinned")},
		"pinned.child": {
			Key:        "pinned.child",
			SourceAddr: "./child",
			Dir:        filepath.Join(entryDir, ".terraform", "modules", "pinned", "child"),
		},
		"floating": {Key: "floating", SourceAddr: "github.com/somebody/unpinned", Dir: filepath.Join(entryDir, ".terraform", "modules", "floating")},
	}
	mirrored := map[string]bool{"https://github.com/alphagov/terraform-govuk-tfe-workspacer.git": true}

	res := postProcess(installed, entryDir, mirrored, false)

	if _, ok := res.manifest["pinned"]; !ok {
		t.Error("a pinned go-getter record was dropped")
	}
	if _, ok := res.manifest["pinned.child"]; !ok {
		t.Error("a module reached through a pinned go-getter record was dropped")
	}
	if _, ok := res.manifest["floating"]; ok {
		t.Error("an unpinned go-getter record survived")
	}
	if res.dropped != 1 {
		t.Errorf("dropped %d records, want 1 (the unpinned one only)", res.dropped)
	}
	if len(res.unpinnedGit) != 1 || res.unpinnedGit[0] != "https://github.com/somebody/unpinned.git" {
		t.Errorf("unpinned = %v, want only the repository no module_sources entry names", res.unpinnedGit)
	}
}

func TestModulePinsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pins.json")

	empty, err := loadModulePins(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Packages) != 0 {
		t.Errorf("a missing lock read as %v, want empty", empty.Packages)
	}

	empty.Packages["b/x/aws"] = []string{"2.0.0", "1.0.0"}
	empty.Packages["a/x/aws"] = []string{"1.0.0"}
	if err := writeModulePins(path, empty); err != nil {
		t.Fatal(err)
	}
	back, err := loadModulePins(path)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(back.Packages["b/x/aws"], []string{"1.0.0", "2.0.0"}) {
		t.Errorf("versions were not sorted on write: %v", back.Packages["b/x/aws"])
	}
	if len(back.Comment) == 0 {
		t.Error("the written lock carries no comment explaining what it is")
	}
}
