// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// GitHub issue #587's documentation pin.
//
// site/content/docs/use/reference.md's "-adoption-only" section names three
// resource types as the ones a generated terralith carries that need no
// ownership marker. That is a claim about the provider's schema, not about
// choudoufu, so a provider bump can falsify it - and a page that tells an
// operator "these carry no marker and never will" about a type that has
// since grown a tags argument is worse than one that says nothing.
//
// The instance counts on that page (41 of 79 at scale 1) come from
// tools/terralith-gen's own output and are not checked here: nothing in the
// tree holds a generated terralith to check them against, and inventing one
// would pin the generator rather than the claim. What is checked is the part
// live/readiness.json can settle, which is also the part a provider bump
// moves.
//
// Proving it red: flip any of the three types' facts.taggable in
// live/readiness.json, or name a taggable type in that bullet, and this
// fails naming the type.

// adoptionDocPath is the page whose claim this test holds to the artifact.
const adoptionDocPath = "../site/content/docs/use/reference.md"

// adoptionUntaggableClaim captures the backticked type names in the
// "Identity by declaration" bullet's closing sentence. Anchored on the
// sentence rather than on the whole file so that a type named anywhere else
// on the page is not swept in.
var adoptionUntaggableClaim = regexp.MustCompile(`(?s)it is 41 of 79 instances at scale 1, all of them\s+(.*?)\.\n`)

var adoptionTypeName = regexp.MustCompile("`(aws_[a-z0-9_]+)`")

func TestAdoptionDocNamesOnlyUntaggableTypes(t *testing.T) {
	raw, err := os.ReadFile(adoptionDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", adoptionDocPath, err)
	}

	m := adoptionUntaggableClaim.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s no longer carries the -adoption-only section's untaggable-types sentence; "+
			"if the wording changed, update adoptionUntaggableClaim here rather than deleting the check", adoptionDocPath)
	}
	names := adoptionTypeName.FindAllSubmatch(m[1], -1)
	if len(names) == 0 {
		t.Fatalf("the -adoption-only section names no resource types; the sentence matched but the claim is empty:\n%s", m[1])
	}

	artifact, err := os.ReadFile("readiness.json")
	if err != nil {
		t.Fatalf("read live/readiness.json: %v", err)
	}
	var art readinessCounts
	if err := json.Unmarshal(artifact, &art); err != nil {
		t.Fatalf("parse live/readiness.json: %v", err)
	}
	taggable := make(map[string]bool, len(art.Types))
	known := make(map[string]bool, len(art.Types))
	for _, row := range art.Types {
		known[row.Type] = true
		taggable[row.Type] = row.Facts.Taggable
	}

	for _, name := range names {
		typeName := string(name[1])
		if !known[typeName] {
			t.Errorf("%s names %s, which live/readiness.json does not classify at all", adoptionDocPath, typeName)
			continue
		}
		if taggable[typeName] {
			t.Errorf("%s says %s carries no ownership marker and never will, but live/readiness.json "+
				"reports facts.taggable=true for it at provider %s. The provider grew a tags argument; "+
				"the page's claim is now wrong and an operator reading it would leave a real marker unwritten.",
				adoptionDocPath, typeName, providerVersionOf(artifact))
		}
	}
}

// providerVersionOf reads the artifact's own provider_version, so the
// failure message names the version that moved rather than making the reader
// go and look.
func providerVersionOf(artifact []byte) string {
	var head struct {
		ProviderVersion string `json:"provider_version"`
	}
	if err := json.Unmarshal(artifact, &head); err != nil {
		return "(unknown)"
	}
	return head.ProviderVersion
}
