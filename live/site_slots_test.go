// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The docs site's provider hubs carry the same six slots, in the same
// order, under the same names, on every platform (#1055): a reader who has
// learned one hub has learned them all, and a platform whose answer to a
// slot is honestly empty gets a page that says so rather than a missing
// page. site/data/providers.yaml is the roster; this test holds the content
// tree to it, in the shape TestLimitationsDocCoversDirs holds
// live/LIMITATIONS.md to its fixture directories.
//
// Proving it red: delete site/content/kubernetes/cost.md, or add a
// site/content/azure/ directory while providers.yaml still says azure is
// not planned; each fails a different check below.

const (
	siteProvidersYAML = "../site/data/providers.yaml"
	siteContentDir    = "../site/content"
)

// siteSlots are the six slot pages, in the order the hub shows them.
var siteSlots = []string{"adopt", "gate", "operate", "fit", "cost", "proof"}

type siteProvidersFile struct {
	Providers []struct {
		ID          string            `yaml:"id"`
		Name        string            `yaml:"name"`
		Status      string            `yaml:"status"`
		StatusLabel string            `yaml:"status_label"`
		Cells       map[string]string `yaml:"cells"`
	} `yaml:"providers"`
	Keys map[string]string `yaml:"keys"`
}

func TestSiteProviderHubsCarryTheSixSlots(t *testing.T) {
	raw, err := os.ReadFile(siteProvidersYAML)
	if err != nil {
		t.Fatalf("read %s: %v", siteProvidersYAML, err)
	}
	var f siteProvidersFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode %s: %v", siteProvidersYAML, err)
	}
	if len(f.Providers) == 0 {
		t.Fatalf("%s lists no providers; this test is checking nothing", siteProvidersYAML)
	}

	hubs := map[string]bool{}
	for _, p := range f.Providers {
		if p.Status == "" || p.StatusLabel == "" {
			t.Errorf("provider %q: status and status_label are both required", p.ID)
		}
		dir := filepath.Join(siteContentDir, p.ID)
		_, err := os.Stat(dir)
		if p.Status == "not-planned" {
			if err == nil {
				t.Errorf("provider %q is not-planned but %s exists; either plan it (status and cells) or remove the hub", p.ID, dir)
			}
			continue
		}
		hubs[p.ID] = true
		if err != nil {
			t.Errorf("provider %q (status %s) has no hub directory %s", p.ID, p.Status, dir)
			continue
		}
		index, err := os.ReadFile(filepath.Join(dir, "_index.md"))
		if err != nil {
			t.Errorf("provider %q: no %s/_index.md", p.ID, dir)
		} else if !strings.Contains(string(index), "\nlayout: hub\n") {
			t.Errorf("provider %q: %s/_index.md does not declare `layout: hub`", p.ID, dir)
		}
		for i, slot := range siteSlots {
			page := filepath.Join(dir, slot+".md")
			body, err := os.ReadFile(page)
			if err != nil {
				t.Errorf("provider %q: slot %q has no page at %s", p.ID, slot, page)
				continue
			}
			s := string(body)
			if !strings.Contains(s, "\nweight: "+strconv.Itoa(i+1)+"\n") {
				t.Errorf("%s: weight must be %d so the slots keep their order on every hub", page, i+1)
			}
			if !strings.Contains(s, "\ndescription: ") {
				t.Errorf("%s: no description; the hub card and the search index show it", page)
			}
			if !strings.Contains(s, "\ndeeper:\n") {
				t.Errorf("%s: no `deeper:` list; every slot page ends with where to dig", page)
			}
		}
		// Every extra page in a hub is a seventh slot nobody agreed on.
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if e.Name() == "_index.md" || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			known := false
			for _, slot := range siteSlots {
				if slot == name {
					known = true
				}
			}
			if !known {
				t.Errorf("%s/%s is not one of the six slots (%s); the hubs must stay identical in shape", dir, e.Name(), strings.Join(siteSlots, ", "))
			}
		}
		for key := range f.Keys {
			if _, ok := p.Cells[key]; !ok {
				t.Errorf("provider %q has no cell for key %q; the providers shortcode would drop its row silently", p.ID, key)
			}
		}
	}

	// A hub directory with no roster entry is a platform the site claims
	// and the data does not.
	entries, err := os.ReadDir(siteContentDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "docs" || e.Name() == "how-it-works" {
			continue
		}
		if !hubs[e.Name()] {
			t.Errorf("%s/%s is a top-level content directory with no provider entry in %s", siteContentDir, e.Name(), siteProvidersYAML)
		}
	}
}
