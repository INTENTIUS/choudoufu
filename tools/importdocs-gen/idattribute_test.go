// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestStatedIDSeparatorReadings covers each reading with a description
// taken verbatim from the provider's own docs at 6.59.0, plus the shapes
// that must NOT fire.
//
// The last three cases are the ones worth keeping: they are the readings a
// plausible implementation gets wrong, and two of them were wrong here
// before this test existed.
func TestStatedIDSeparatorReadings(t *testing.T) {
	for _, tc := range []struct {
		name        string
		desc        string
		wantSep     string
		wantReading string
	}{
		{
			name:        "separator quoted after separated-by",
			desc:        "AppConfig application ID, configuration profile ID, and version number separated by a slash (`/`).",
			wantSep:     "/",
			wantReading: idAttrReadingSeparatedByBacktick,
		},
		{
			name:        "separator spelled out with no quoting",
			desc:        "Provisioning artifact identifier and product identifier separated by a colon.",
			wantSep:     ":",
			wantReading: idAttrReadingSeparatedByWord,
		},
		{
			name:        "adjectival delimited",
			desc:        "A pipe-delimited string combining `configuration_set_name` and `event_destination_name`.",
			wantSep:     "|",
			wantReading: idAttrReadingDelimitedWord,
		},
		{
			name:        "adjectival separated",
			desc:        "A comma-separated string made up of `ip` and `destination_pool_name`.",
			wantSep:     ",",
			wantReading: idAttrReadingDelimitedWord,
		},
		{
			name:        "format token",
			desc:        "API Key ID (Formatted as ApiId:Key)",
			wantSep:     ":",
			wantReading: idAttrReadingFormatToken,
		},
		{
			name:        "a quoted token spelling the whole composite",
			desc:        "The combination of `service_name` and `user_name` as such: `service_name:user_name:service_specific_credential_id`.",
			wantSep:     ":",
			wantReading: idAttrReadingCompositeToken,
		},

		// A description naming ONE value states no composite, however
		// composite the type's documented import is. This is the group
		// #337's roster reports as unproven, and reading it as a leaf
		// verdict would be inventing a fact.
		{name: "single value", desc: "The EMR Instance ID"},
		{name: "single value with an article", desc: "ID of the traffic policy."},

		// THE UNDERSCORE TRAP. Every argument name in these docs is
		// snake_case, so a reading that finds its separator by scanning a
		// quoted token for a candidate character calls "`disk_name`" an
		// underscore-joined composite. Measured before splitSegments took
		// over the split: seven pages fired with "_" against an Import
		// section that had independently scraped "," or ":".
		{
			name: "composition word beside snake_case argument names only",
			desc: "Combination of attributes to create a unique id: `disk_name`,`instance_name`.",
		},

		// The composition-word gate. Without it, any quoted value
		// containing a separator states a grammar the page never claimed.
		{
			name: "a quoted composite-looking token with no composition claim",
			desc: "The endpoint, in the form `example.com:443`.",
		},

		// The alphabet is candidateSeparators and nothing else, so a
		// hyphen-joined format token yields nothing rather than a
		// separator the rest of the tool refuses to emit.
		{
			name: "hyphen is outside the alphabet",
			desc: "API Function ID (Formatted as ApiId-FunctionId)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sep, reading, ok := statedIDSeparator(tc.desc)
			if tc.wantSep == "" {
				if ok {
					t.Fatalf("statedIDSeparator(%q) = %q via %s, want no reading at all.\n"+
						"A description that does not state a composite form must yield nothing: the roster this feeds "+
						"(live/composite-import-roster.json) turns a stated separator into a verdict that the exported "+
						"id round-trips through import, so a separator invented from prose becomes a wrong identity recorded silently.",
						tc.desc, sep, reading)
				}
				return
			}
			if !ok {
				t.Fatalf("statedIDSeparator(%q) found nothing, want %q via %s", tc.desc, tc.wantSep, tc.wantReading)
			}
			if sep != tc.wantSep || reading != tc.wantReading {
				t.Errorf("statedIDSeparator(%q) = %q via %s, want %q via %s", tc.desc, sep, reading, tc.wantSep, tc.wantReading)
			}
		})
	}
}

// TestIDAttributeSeparatorNeverContradictsTheImportSection is the reading's
// external check, and the only reason to believe it at all.
//
// The Attribute Reference and the Import section are different sections of
// the page, written for different purposes, and scraped here by different
// readers that share no code. Where both name a join character for the same
// type they must name the SAME one. This asserts that over every row in the
// committed artifact - 1699 types, not the 27 that motivated the field -
// which is what distinguishes a faithful reading from one tuned until the
// population it was built for agreed with it.
//
// A row where the Import section scraped no separator is not a
// contradiction; it is one source describing something the other did not.
func TestIDAttributeSeparatorNeverContradictsTheImportSection(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "live", "import-grammar.json"))
	if err != nil {
		t.Fatal(err)
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatal(err)
	}

	var stated, corroborated, uncheckable int
	for _, r := range art.Rows {
		if r.IDAttribute == nil || r.IDAttribute.StatedSeparator == "" {
			continue
		}
		stated++
		if r.Separator == nil {
			uncheckable++
			continue
		}
		if *r.Separator != r.IDAttribute.StatedSeparator {
			t.Errorf("%s: the Attribute Reference states the exported id is joined by %q, the Import section scraped %q.\n"+
				"  attribute reference: %s\n"+
				"Two sections of one page describing the same string differently means at least one reading is wrong, "+
				"and the roster in live/composite-import-roster.json treats their agreement as proof that recording the id round-trips.",
				r.TFType, r.IDAttribute.StatedSeparator, *r.Separator, r.IDAttribute.Description)
			continue
		}
		corroborated++
	}
	if stated == 0 {
		t.Fatal("no row states a composite id at all, so this test asserts nothing; regenerate live/import-grammar.json")
	}
	t.Logf("%d rows state a composite id: %d corroborated by the Import section's own separator, %d with no Import separator to check against, %d contradicting it",
		stated, corroborated, uncheckable, stated-corroborated-uncheckable)
}
