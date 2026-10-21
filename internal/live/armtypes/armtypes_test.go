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

// The committed table's pin. TO CHANGE A NUMBER HERE, regenerate with
// tools/armtypes-gen against the new azapi tag and say in the commit
// message what moved: these are the figures #1122's ruling quotes, and
// live/ADDING-A-PLATFORM.md quotes them again.
const (
	pinnedAzapiVersion = "v2.13.0"
	pinnedAzapiCommit  = "cf3ecee858fa48c0aca95d0b995a8bfd4950c8ec"
)

var pinnedCounts = Counts{
	Types:            3397,
	TypeVersions:     33979,
	TaggableAtLatest: 1031,
	VersionDependent: 84,
}

const pinnedDiscriminatedTags = 13

// TestCommittedTableParses pins the committed table's source and counts,
// and that it survives a parse and render unchanged, so a hand edit to a
// row (which Parse's counts check may not see, if it keeps the counts)
// still shows up as a byte difference.
func TestCommittedTableParses(t *testing.T) {
	tbl, err := Parse(committedTable)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.AzapiVersion != pinnedAzapiVersion || tbl.AzapiCommit != pinnedAzapiCommit {
		t.Errorf("table is from azapi %s (%s), pinned %s (%s)", tbl.AzapiVersion, tbl.AzapiCommit, pinnedAzapiVersion, pinnedAzapiCommit)
	}
	if got := tbl.Counts(); got != pinnedCounts {
		t.Errorf("counts %+v, pinned %+v", got, pinnedCounts)
	}
	if tbl.DiscriminatedTags != pinnedDiscriminatedTags {
		t.Errorf("%d type-versions take tags only via a discriminated body, pinned %d", tbl.DiscriminatedTags, pinnedDiscriminatedTags)
	}
	if !bytes.Equal(tbl.Render(), committedTable) {
		t.Errorf("%s does not round-trip through Parse and Render; it was edited by hand", TablePath)
	}
	if v, c := Source(); v != pinnedAzapiVersion || c != pinnedAzapiCommit {
		t.Errorf("Source() = %s %s", v, c)
	}
	if got := Summary(); got != pinnedCounts {
		t.Errorf("Summary() = %+v", got)
	}
	if n := len(Types()); n != pinnedCounts.Types {
		t.Errorf("Types() has %d entries", n)
	}
}

// TestKnownRows pins rows whose answer is known from Azure's own resource
// model, so a generator change that inverts the rule cannot pass on
// counts that happen to balance.
func TestKnownRows(t *testing.T) {
	cases := []struct {
		armType, version string
		taggable, known  bool
	}{
		// A tracked resource: tags, name and location.
		{"Microsoft.Network/virtualNetworks", "2024-05-01", true, true},
		{"microsoft.network/virtualnetworks", "2024-05-01", true, true},
		{"MICROSOFT.NETWORK/VIRTUALNETWORKS", "2015-05-01-preview", true, true},
		{"Microsoft.Resources/resourceGroups", "2025-04-01", true, true},
		// Proxy resources: no tags at any version.
		{"Microsoft.Network/virtualNetworks/subnets", "2024-05-01", false, true},
		{"Microsoft.Authorization/roleAssignments", "2022-04-01", false, true},
		{"Microsoft.Storage/storageAccounts/blobServices/containers", "2018-02-01", false, true},
		// Version-dependent: the one preview version took tags, every GA
		// version after it does not.
		{"Microsoft.Kusto/clusters/databases", "2018-09-07-preview", true, true},
		{"Microsoft.Kusto/clusters/databases", "2019-01-21", false, true},
		// Unknown: a version azapi has no schema for, and a made-up type.
		{"Microsoft.Network/virtualNetworks", "2024-05-02", false, false},
		{"Microsoft.Network/virtualNetworks", "2024-05-01 ", false, false},
		{"Microsoft.Nope/things", "2024-05-01", false, false},
	}
	for _, c := range cases {
		taggable, known := Taggable(c.armType, c.version)
		if taggable != c.taggable || known != c.known {
			t.Errorf("Taggable(%q, %q) = (%t, %t), want (%t, %t)", c.armType, c.version, taggable, known, c.taggable, c.known)
		}
	}

	if !Default().VersionDependent("Microsoft.Kusto/clusters/databases") {
		t.Error("Microsoft.Kusto/clusters/databases is not version-dependent")
	}
	if Default().VersionDependent("Microsoft.Network/virtualNetworks") {
		t.Error("Microsoft.Network/virtualNetworks is version-dependent")
	}
	caps, known := Default().Capabilities("Microsoft.Network/virtualNetworks/subnets", "2024-05-01")
	if !known || caps != CapName {
		t.Errorf("subnets caps = %d (known %t), want name only", caps, known)
	}
	if v, caps, ok := Default().Latest("microsoft.resources/resourcegroups"); !ok || v != "2025-04-01" || caps != CapTags|CapName|CapLocation {
		t.Errorf("resourceGroups latest = %s %d %t", v, caps, ok)
	}
}

func TestParseAzapiType(t *testing.T) {
	cases := []struct {
		in, typ, ver string
		ok           bool
	}{
		{"Microsoft.Network/virtualNetworks@2024-05-01", "Microsoft.Network/virtualNetworks", "2024-05-01", true},
		{"Microsoft.Kusto/clusters/databases@2018-09-07-preview", "Microsoft.Kusto/clusters/databases", "2018-09-07-preview", true},
		{"Microsoft.Network/virtualNetworks", "", "", false},
		{"Microsoft.Network/virtualNetworks@", "", "", false},
		{"@2024-05-01", "", "", false},
		{"a@b@c", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		typ, ver, ok := ParseAzapiType(c.in)
		if typ != c.typ || ver != c.ver || ok != c.ok {
			t.Errorf("ParseAzapiType(%q) = (%q, %q, %t), want (%q, %q, %t)", c.in, typ, ver, ok, c.typ, c.ver, c.ok)
		}
	}
}

// TestParseRefusesDamage proves Parse's own checks can fail.
func TestParseRefusesDamage(t *testing.T) {
	good := "# source: azure/terraform-provider-azapi v0 abc internal/azure/generated\n" +
		"# counts: types=1 type-versions=2 taggable-at-latest=0 version-dependent=1\n" +
		"x/y 2020-01-01=7 2021-01-01=6\n"
	if _, err := Parse([]byte(good)); err != nil {
		t.Fatalf("baseline does not parse: %s", err)
	}
	for name, bad := range map[string]string{
		"counts disagree": strings.Replace(good, "taggable-at-latest=0", "taggable-at-latest=1", 1),
		"no source":       strings.Replace(good, "# source:", "# sauce:", 1),
		"unsorted":        strings.Replace(good, "2020-01-01=7 2021-01-01=6", "2021-01-01=7 2020-01-01=6", 1),
		"no first caps":   strings.Replace(good, "2020-01-01=7", "2020-01-01", 1),
		"uppercase type":  strings.Replace(good, "x/y", "X/y", 1),
		"bad caps":        strings.Replace(good, "=6", "=16", 1),
		// GitHub issue #2147: the via-base bit only qualifies tags, and
		// the discriminated-tags header counts the rows carrying it.
		"via base without tags":        strings.Replace(good, "=6", "=10", 1),
		"discriminated header missing": strings.Replace(good, "=7", "=15", 1),
		"discriminated header too big": strings.Replace(good, "# counts:", "# discriminated-tags: 1\n# counts:", 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: Parse accepted it", name)
		}
	}
}

// TestEveryTypeLookupIgnoresCase pins that every lookup by ARM type name
// folds case, not only Taggable. chant shipped a taggable-set lookup that
// was exact-case beside an API-version lookup that was not, so one type
// read as both known and untaggable. The API version stays exact, as
// azapi's GetResourceDefinition compares it with ==.
func TestEveryTypeLookupIgnoresCase(t *testing.T) {
	const mixed = "microsoft.network/VIRTUALNETWORKS"
	tbl := Default()

	if taggable, known := Taggable(mixed, "2024-05-01"); !taggable || !known {
		t.Errorf("Taggable(%q) = (%t, %t)", mixed, taggable, known)
	}
	if caps, known := tbl.Capabilities(mixed, "2024-05-01"); !known || caps != CapTags|CapName|CapLocation {
		t.Errorf("Capabilities(%q) = (%d, %t)", mixed, caps, known)
	}
	want := tbl.Versions("microsoft.network/virtualnetworks")
	if got := tbl.Versions(mixed); len(want) == 0 || len(got) != len(want) {
		t.Errorf("Versions(%q) has %d versions, the lowercase spelling %d", mixed, len(got), len(want))
	}
	if v, _, ok := tbl.Latest(mixed); !ok || v != want[len(want)-1] {
		t.Errorf("Latest(%q) = (%q, %t)", mixed, v, ok)
	}
	if !tbl.VersionDependent("Microsoft.KUSTO/Clusters/Databases") {
		t.Error("VersionDependent does not fold case")
	}

	// The version is exact: azapi has no schema for a re-cased one.
	if _, known := Taggable(mixed, "2015-05-01-PREVIEW"); known {
		t.Error("an upper-cased API version matched; azapi compares versions exactly")
	}
}

// TestDefaultTagsApplyIsCanResourceHaveProperty pins [DefaultTagsApply] to
// azapi's own gate for default_tags (GitHub issue #2147): a writable tags
// on an ObjectType body. A discriminated body that takes tags only through
// its baseProperties is taggable but gets no default tags.
func TestDefaultTagsApplyIsCanResourceHaveProperty(t *testing.T) {
	for _, c := range []struct {
		typ, ver          string
		taggable, applies bool
	}{
		{"Microsoft.Network/virtualNetworks", "2023-09-01", true, true},
		{"Microsoft.Network/virtualNetworks/subnets", "2024-05-01", false, false},
		{"Microsoft.Resources/deploymentScripts", "2020-10-01", true, false},
		{"Microsoft.TimeSeriesInsights/environments", "2017-11-15", true, true},
		{"Microsoft.TimeSeriesInsights/environments", "2020-05-15", true, false},
	} {
		taggable, known := Taggable(c.typ, c.ver)
		applies, known2 := DefaultTagsApply(c.typ, c.ver)
		if !known || !known2 || taggable != c.taggable || applies != c.applies {
			t.Errorf("%s@%s: Taggable %t, DefaultTagsApply %t (known %t %t); want %t, %t", c.typ, c.ver, taggable, applies, known, known2, c.taggable, c.applies)
		}
	}
	viaBase := 0
	tbl := Default()
	for _, typ := range tbl.Types() {
		for _, v := range tbl.Versions(typ) {
			if taggable, _ := Taggable(typ, v); !taggable {
				continue
			}
			if applies, _ := DefaultTagsApply(typ, v); !applies {
				viaBase++
			}
		}
	}
	if viaBase != pinnedDiscriminatedTags {
		t.Errorf("%d taggable type-versions get no default tags, want the %d discriminated ones", viaBase, pinnedDiscriminatedTags)
	}
}
