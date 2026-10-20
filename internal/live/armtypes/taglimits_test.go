// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bytes"
	"strings"
	"testing"
)

// The committed tag limits' counts at the azapi pin. TO CHANGE A NUMBER
// HERE, regenerate with tools/armtypes-gen and say in the commit message
// what moved.
var pinnedLimitCounts = LimitCounts{Types: 38, Fewer: 38, Varies: 7}

// TestCommittedTagLimitsParse pins the committed limits' source and
// counts, and that they survive a parse and render unchanged.
func TestCommittedTagLimitsParse(t *testing.T) {
	lims, err := ParseTagLimits(committedTagLimits)
	if err != nil {
		t.Fatal(err)
	}
	if lims.AzapiVersion != pinnedAzapiVersion || lims.AzapiCommit != pinnedAzapiCommit {
		t.Errorf("limits are from azapi %s (%s), pinned %s (%s)", lims.AzapiVersion, lims.AzapiCommit, pinnedAzapiVersion, pinnedAzapiCommit)
	}
	if got := lims.Counts(); got != pinnedLimitCounts {
		t.Errorf("counts %+v, pinned %+v", got, pinnedLimitCounts)
	}
	if !bytes.Equal(lims.Render(), committedTagLimits) {
		t.Errorf("%s does not round-trip through ParseTagLimits and Render; it was edited by hand", TagLimitsPath)
	}
}

// TestTagLimitKnownRows pins types whose `tags` description in azapi's
// data documents a count below ARM's 50, and the default.
func TestTagLimitKnownRows(t *testing.T) {
	cases := []struct {
		armType, apiVersion string
		max                 int
		from                string // a substring of the source
	}{
		// Cosmos DB: "A maximum of 15 tags can be provided for a resource".
		{"Microsoft.DocumentDB/databaseAccounts", "", 15, "Microsoft.DocumentDB/databaseAccounts@2015-04-01"},
		{"microsoft.documentdb/DATABASEACCOUNTS/sqlDatabases/containers", "2024-05-15", 15, "@2019-08-01"},
		// API Management: "A maximum of 10 tags can be provided" at
		// 2016-07-07 and in no description since (#2133, finding 8).
		{"Microsoft.ApiManagement/service", "2016-07-07", 10, "Microsoft.ApiManagement/service@2016-07-07"},
		{"Microsoft.ApiManagement/service", "2024-05-01", DefaultTagLimit, "ARM default: azapi v2.13.0's tags description of Microsoft.ApiManagement/service@2024-05-01 documents no count"},
		{"Microsoft.ApiManagement/service", "", 10, "the lowest any API version documents; no API version was given"},
		{"Microsoft.ApiManagement/service", "2099-01-01", 10, "does not know Microsoft.ApiManagement/service@2099-01-01"},
		// Storage documents 15 at every version but its first, which says
		// nothing; azurerm's own validation allows 50 (AzurermTagRules).
		{"Microsoft.Storage/storageAccounts", "2023-05-01", 15, "Microsoft.Storage/storageAccounts@2023-05-01"},
		{"Microsoft.Storage/storageAccounts", "2015-05-01-PREVIEW", DefaultTagLimit, "documents no count (Microsoft.Storage/storageAccounts@2015-06-15 documents 15)"},
		{"Microsoft.Storage/storageAccounts", "", 15, "@2015-06-15, the lowest"},
		{"Microsoft.Network/virtualNetworks", "2024-05-01", DefaultTagLimit, "ARM default"},
		{"Microsoft.Nope/things", "", DefaultTagLimit, "ARM default"},
	}
	for _, c := range cases {
		max, src := TagLimit(c.armType, c.apiVersion)
		if max != c.max || !strings.Contains(src, c.from) {
			t.Errorf("TagLimit(%q, %q) = (%d, %q), want (%d, ...%s...)", c.armType, c.apiVersion, max, src, c.max, c.from)
		}
	}
	r, ok := DefaultTagLimits().Limit("Microsoft.Storage/storageAccounts")
	if !ok || !r.Varies || r.MaxKeyLen != 128 || r.MaxValueLen != 0 || len(r.ByVersion) != 1 || r.ByVersion["2015-05-01-preview"] != 0 {
		t.Errorf("storageAccounts row = %+v (%t), want varies at 2015-05-01-preview alone, with 128-character keys and no value figure", r, ok)
	}
	if r, ok := DefaultTagLimits().Limit("Microsoft.DocumentDB/databaseAccounts"); !ok || r.Varies {
		t.Errorf("databaseAccounts row = %+v (%t), want 15 at every taggable version", r, ok)
	}
}

// TestDescribedLimits pins the three phrasings the descriptions use, and
// that a description about something else reads as nothing.
func TestDescribedLimits(t *testing.T) {
	cases := []struct {
		desc          string
		max, key, val int
	}{
		{"Tags are a list of key-value pairs that describe the resource. These tags can be used in viewing and grouping this resource (across resource groups). A maximum of 15 tags can be provided for a resource. Each tag must have a key no greater than 128 characters and value no greater than 256 characters.", 15, 128, 256},
		{"Gets or sets a list of key value pairs that describe the resource. A maximum of 15 tags can be provided for a resource. Each tag must have a key with a length no greater than 128 characters and a value with a length no greater than 256 characters.", 15, 128, 256},
		{"API Management service tags. A maximum of 10 tags can be provided for a resource, and each tag must have a key no greater than 128 characters (and a value no greater than 256 characters).", 10, 128, 256},
		{"The following basic restrictions apply to tags:  +  Maximum number of tags per resource - 50  +  For each resource", 50, 0, 0},
		{"A maximum number of 50 tags can be specified.", 50, 0, 0},
		{"Resource tags", 0, 0, 0},
		{"There can be a maximum of 1000 blob containers with hasLegalHold=true for a given account.", 0, 0, 0},
	}
	for _, c := range cases {
		max, key, val := describedLimits(c.desc)
		if max != c.max || key != c.key || val != c.val {
			t.Errorf("describedLimits(%.60q...) = (%d, %d, %d), want (%d, %d, %d)", c.desc, max, key, val, c.max, c.key, c.val)
		}
	}
}

// TestParseTagLimitsRefusesDamage proves ParseTagLimits's own checks can fail.
func TestParseTagLimitsRefusesDamage(t *testing.T) {
	good := "# source: azure/terraform-provider-azapi v0 abc internal/azure/generated\n" +
		"# counts: types=2 fewer-than-default=1 varies-by-version=1\n" +
		"a/b max=15 key=128 value=0 from=2020-01-01 varies=2019-01-01:0,2021-01-01:20\n" +
		"c/d max=0 key=128 value=0 from=-\n"
	if _, err := ParseTagLimits([]byte(good)); err != nil {
		t.Fatalf("baseline does not parse: %s", err)
	}
	for name, bad := range map[string]string{
		"counts disagree":  strings.Replace(good, "fewer-than-default=1", "fewer-than-default=2", 1),
		"no source":        strings.Replace(good, "# source:", "# sauce:", 1),
		"unsorted":         strings.Replace(good, "a/b", "e/f", 1),
		"uppercase type":   strings.Replace(good, "a/b", "A/b", 1),
		"no from":          strings.Replace(good, "from=2020-01-01", "from=-", 1),
		"default limit":    strings.Replace(good, "max=15", "max=50", 1),
		"bad flag":         strings.Replace(good, " varies=", " vary=", 1),
		"bare varies":      strings.Replace(good, "varies=2019-01-01:0,2021-01-01:20", "varies", 1),
		"varies below max": strings.Replace(good, "2021-01-01:20", "2021-01-01:10", 1),
		"varies at max":    strings.Replace(good, "2021-01-01:20", "2021-01-01:15", 1),
		"varies unsorted":  strings.Replace(good, "2019-01-01:0,2021-01-01:20", "2021-01-01:20,2019-01-01:0", 1),
		"varies no count":  strings.Replace(good, "2019-01-01:0", "2019-01-01", 1),
		"varies, no max":   strings.Replace(good, "c/d max=0 key=128 value=0 from=-", "c/d max=0 key=128 value=0 from=- varies=2020-01-01:0", 1),
	} {
		if bad == good {
			t.Fatalf("%s: the damage did not apply", name)
		}
		if _, err := ParseTagLimits([]byte(bad)); err == nil {
			t.Errorf("%s: ParseTagLimits accepted it", name)
		}
	}
}
