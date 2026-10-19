// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The docs site's provider hubs carry the same five slots, in the same
// order, under the same names, on every platform (#1055): a reader who has
// learned one hub has learned them all, and a platform whose answer to a
// slot is honestly empty gets a page that says so rather than a missing
// page.
//
// Since the Astro cutover (#2092) the roster is the sidebar in
// site/astro.config.mjs: each hub is a sidebar group whose pages are the
// hub's index and then the five slots, in slot order. The Hugo site held the
// order in each page's `weight:` and the roster in site/data/providers.yaml;
// both went with it. This test holds the content tree and the sidebar to
// each other, in the shape TestLimitationsDocCoversDirs holds
// live/LIMITATIONS.md to its fixture directories.
//
// Proving it red: delete site/src/content/docs/kubernetes/compatibility.mdx,
// swap two slots in the sidebar, or add a site/src/content/docs/azure/
// directory with no sidebar group; each fails a different check below.

const (
	siteDir          = "../site"
	siteContentDir   = "../site/src/content/docs"
	siteAstroConfig  = "../site/astro.config.mjs"
	siteRoomsTS      = "../site/src/data/rooms.ts"
	siteRedirectJSON = "../site/src/data/redirects.json"
)

// siteSlots are the five slot pages, in the order the hub shows them.
var siteSlots = []string{"adopt", "gate", "operate", "compatibility", "proof"}

// siteNonHubDirs are the top-level content directories that are not
// provider hubs.
var siteNonHubDirs = map[string]bool{"docs": true, "how-it-works": true}

// sitePage returns the path of the page at rel (no extension) under
// siteContentDir, as .md or .mdx, or "" when neither exists.
func sitePage(rel string) string {
	for _, ext := range []string{".md", ".mdx"} {
		p := filepath.Join(siteContentDir, filepath.FromSlash(rel)+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

var sidebarSlug = regexp.MustCompile(`slug:\s*'([^']+)'`)

func TestSiteProviderHubsCarryTheFiveSlots(t *testing.T) {
	cfg, err := os.ReadFile(siteAstroConfig)
	if err != nil {
		t.Fatalf("read %s: %v", siteAstroConfig, err)
	}
	var slugs []string
	for _, m := range sidebarSlug.FindAllStringSubmatch(string(cfg), -1) {
		slugs = append(slugs, m[1])
	}

	entries, err := os.ReadDir(siteContentDir)
	if err != nil {
		t.Fatal(err)
	}
	hubs := 0
	for _, e := range entries {
		if !e.IsDir() || siteNonHubDirs[e.Name()] {
			continue
		}
		hub := e.Name()
		hubs++
		dir := filepath.Join(siteContentDir, hub)

		index := sitePage(hub + "/index")
		if index == "" {
			t.Errorf("hub %q: no %s/index.md or index.mdx", hub, dir)
		} else if body, _ := os.ReadFile(index); !strings.Contains(string(body), "\ndescription: ") {
			t.Errorf("%s: no description; the hub card and the search index show it", index)
		}
		for _, slot := range siteSlots {
			page := sitePage(hub + "/" + slot)
			if page == "" {
				t.Errorf("hub %q: slot %q has no page at %s/%s.md or .mdx", hub, slot, dir, slot)
				continue
			}
			body, err := os.ReadFile(page)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "\ndescription: ") {
				t.Errorf("%s: no description; the hub card and the search index show it", page)
			}
		}

		// Every extra page in a hub is a sixth slot nobody agreed on.
		pages, _ := os.ReadDir(dir)
		for _, p := range pages {
			name := strings.TrimSuffix(strings.TrimSuffix(p.Name(), ".mdx"), ".md")
			if name == "index" || name == p.Name() {
				continue
			}
			known := false
			for _, slot := range siteSlots {
				if slot == name {
					known = true
				}
			}
			if !known {
				t.Errorf("%s/%s is not one of the five slots (%s); the hubs must stay identical in shape", dir, p.Name(), strings.Join(siteSlots, ", "))
			}
		}

		// The sidebar is the order: the hub's index, then the five slots.
		want := []string{hub}
		for _, slot := range siteSlots {
			want = append(want, hub+"/"+slot)
		}
		var got []string
		for _, s := range slugs {
			if s == hub || strings.HasPrefix(s, hub+"/") {
				got = append(got, s)
			}
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s lists hub %q as %v; it must list %v, in that order, so every hub shows its slots the same way", siteAstroConfig, hub, got, want)
		}
	}
	if hubs == 0 {
		t.Fatalf("found no hub directories under %s; this test is checking nothing", siteContentDir)
	}
}
