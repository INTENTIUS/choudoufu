// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// RMARMTypesPath is the committed azurerm-to-ARM type join, relative to
// this package's directory.
const RMARMTypesPath = "azurerm_armtypes_generated.txt"

//go:embed azurerm_armtypes_generated.txt
var committedRMARMTypes []byte

// The ARM resource type of an azurerm resource type is what [TagLimit] is
// keyed by, and azurerm's schema does not state it. The id template
// tools/azurerm-id-gen reads off azurerm's id parsers does (live/azurerm-ids.json,
// epic #1120): ".../providers/Microsoft.DocumentDB/databaseAccounts/{name}"
// is a Microsoft.DocumentDB/databaseAccounts. This join is generated from
// that committed table, for the azurerm types [DefaultRMTags] holds (the
// ones with an ARM tags map), so a lint can ask the tag limit of an azurerm
// resource without the provider source or a hand-written type list.
//
// Each row also carries the API version of the go-azure-sdk package the
// type's id parser lives in
// (github.com/hashicorp/go-azure-sdk/resource-manager/<service>/<version>/<package>),
// read off the id table's source package (GitHub issue #2147). That is the
// version azurerm's client for the type calls: for the four azurerm types
// whose ARM type documents a tag count at some versions only
// (azurerm_api_management, azurerm_cognitive_account, azurerm_maps_account,
// azurerm_monitor_autoscale_setting), the service's client package at
// 5.10.0 is the same version. A type whose id parser is a go-azure-helpers
// commonids type or a hand parser names no version, and its row carries
// none.

// armTemplateType reads the ARM resource type off an id template: the
// namespace after the last "/providers/", then every type segment of the
// type/name pairs that follow it, joined with "/". A resource group's
// template, which has no "/providers/", is Microsoft.Resources/resourceGroups.
// ok is false when the template does not alternate fixed type segments with
// {name} segments (a DNS record set's type segment is itself a {placeholder})
// or is not an ARM path at all (a Key Vault data-plane URL).
func armTemplateType(template string) (string, bool) {
	if resourceGroupTemplate.MatchString(template) {
		return "Microsoft.Resources/resourceGroups", true
	}
	i := strings.LastIndex(template, "/providers/")
	if i < 0 || !strings.HasPrefix(template, "/") {
		return "", false
	}
	segs := strings.Split(template[i+len("/providers/"):], "/")
	if len(segs) < 3 || len(segs)%2 != 1 || isPlaceholder(segs[0]) {
		return "", false
	}
	parts := []string{segs[0]}
	for j := 1; j < len(segs); j += 2 {
		if isPlaceholder(segs[j]) || !isPlaceholder(segs[j+1]) {
			return "", false
		}
		parts = append(parts, segs[j])
	}
	return strings.Join(parts, "/"), true
}

var resourceGroupTemplate = regexp.MustCompile(`^/subscriptions/\{[^}/]+\}/resourceGroups/\{[^}/]+\}$`)

func isPlaceholder(seg string) bool {
	return strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}")
}

// RMARMTypes is the parsed or generated join.
type RMARMTypes struct {
	// Version and Commit are the azurerm pin live/azurerm-ids.json names.
	Version string
	Commit  string
	// Tagged is how many azurerm types with an ARM tags map the join was
	// asked about; the rows are the ones whose id template names an ARM type.
	Tagged int
	rows   map[string]string
	// versions is the API version per azurerm type, for the rows whose id
	// package names one.
	versions map[string]string
}

// sdkPackageVersion reads the API version out of a go-azure-sdk
// resource-manager package path; ok is false for any other package.
var sdkPackageVersion = regexp.MustCompile(`^github\.com/hashicorp/go-azure-sdk/resource-manager/[^/]+/(\d{4}-\d{2}-\d{2}(?:-[a-z]+)?)/[^/]+$`)

// apiVersionPattern is the shape an API version takes in the committed join.
var apiVersionPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(?:-[a-z]+)?$`)

// GenerateAzurermARMTypes builds the join from the bytes of
// live/azurerm-ids.json, for every type [DefaultRMTags] holds. A tagged type
// the id table has no template for, or whose template names no fixed ARM
// type, has no row, and [AzurermARMType] answers false for it.
func GenerateAzurermARMTypes(idsJSON []byte) (*RMARMTypes, error) {
	var doc struct {
		Pin struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		} `json:"pin"`
		Types []struct {
			Type     string `json:"type"`
			Template string `json:"template"`
			Source   struct {
				Kind    string `json:"kind"`
				Package string `json:"package"`
			} `json:"source"`
		} `json:"types"`
	}
	if err := json.Unmarshal(idsJSON, &doc); err != nil {
		return nil, err
	}
	facts := DefaultRMTags()
	if doc.Pin.Version != facts.Version || doc.Pin.Commit != facts.Commit {
		return nil, fmt.Errorf("the id table is azurerm %s (%s) and the tag facts are azurerm %s (%s); both must come from one pin", doc.Pin.Version, doc.Pin.Commit, facts.Version, facts.Commit)
	}
	t := &RMARMTypes{Version: doc.Pin.Version, Commit: doc.Pin.Commit, Tagged: len(facts.rows), rows: map[string]string{}, versions: map[string]string{}}
	for _, r := range doc.Types {
		if _, tagged := facts.rows[r.Type]; !tagged || r.Template == "" {
			continue
		}
		if arm, ok := armTemplateType(r.Template); ok {
			t.rows[r.Type] = arm
			if m := sdkPackageVersion.FindStringSubmatch(r.Source.Package); m != nil && r.Source.Kind == "resourceids" {
				t.versions[r.Type] = m[1]
			}
		}
	}
	return t, nil
}

const headerRMARMGenerated = "# Code generated by tools/azurerm-tags-gen -ids; DO NOT EDIT."

// Render writes the join in its committed format: a header naming the pin
// and counts, then "<azurerm type> <ARM type>[ <API version>]" per line,
// sorted.
func (t *RMARMTypes) Render() []byte {
	var b bytes.Buffer
	b.WriteString(headerRMARMGenerated + "\n")
	fmt.Fprintf(&b, "# source: live/azurerm-ids.json, hashicorp/terraform-provider-azurerm %s %s\n", t.Version, t.Commit)
	fmt.Fprintf(&b, "# counts: tagged=%d mapped=%d versioned=%d\n", t.Tagged, len(t.rows), len(t.versions))
	b.WriteString("# format: <azurerm type> <ARM type from its id template>[ <API version of its go-azure-sdk id package>]; a tagged type with no line has no template naming a fixed ARM type\n")
	names := make([]string, 0, len(t.rows))
	for k := range t.rows {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		if v, ok := t.versions[n]; ok {
			fmt.Fprintf(&b, "%s %s %s\n", n, t.rows[n], v)
			continue
		}
		fmt.Fprintf(&b, "%s %s\n", n, t.rows[n])
	}
	return b.Bytes()
}

// ParseRMARMTypes reads the committed format and checks the header's
// counts against the rows.
func ParseRMARMTypes(data []byte) (*RMARMTypes, error) {
	t := &RMARMTypes{rows: map[string]string{}, versions: map[string]string{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	mapped, versioned := -1, -1
	prev := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# source: "):
			f := strings.Fields(strings.TrimPrefix(line, "# source: "))
			if len(f) != 4 {
				return nil, fmt.Errorf("source line %q", line)
			}
			t.Version, t.Commit = f[2], f[3]
		case strings.HasPrefix(line, "# counts: "):
			var err error
			for _, kv := range strings.Fields(strings.TrimPrefix(line, "# counts: ")) {
				k, v, _ := strings.Cut(kv, "=")
				var n int
				if n, err = strconv.Atoi(v); err != nil {
					return nil, fmt.Errorf("counts line %q: %w", line, err)
				}
				switch k {
				case "tagged":
					t.Tagged = n
				case "mapped":
					mapped = n
				case "versioned":
					versioned = n
				}
			}
		case strings.HasPrefix(line, "#") || line == "":
		default:
			f := strings.Fields(line)
			if len(f) != 2 && len(f) != 3 {
				return nil, fmt.Errorf("row %q is not <azurerm type> <ARM type>[ <API version>]", line)
			}
			if len(f) == 3 {
				if !apiVersionPattern.MatchString(f[2]) {
					return nil, fmt.Errorf("row %q: %q is not an API version", line, f[2])
				}
				t.versions[f[0]] = f[2]
			}
			if f[0] <= prev {
				return nil, fmt.Errorf("rows are not sorted and distinct at %q", f[0])
			}
			prev = f[0]
			t.rows[f[0]] = f[1]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if t.Version == "" || len(t.Commit) != 40 {
		return nil, fmt.Errorf("no source line naming the azurerm version and commit")
	}
	if mapped != len(t.rows) {
		return nil, fmt.Errorf("counts line says mapped=%d and there are %d rows", mapped, len(t.rows))
	}
	if versioned != len(t.versions) {
		return nil, fmt.Errorf("counts line says versioned=%d and %d rows name an API version", versioned, len(t.versions))
	}
	return t, nil
}

var (
	rmARMOnce    sync.Once
	rmARMDefault *RMARMTypes
)

// DefaultRMARMTypes is the committed join; it panics if the embedded file
// does not parse, which TestCommittedRMARMTypesParse catches first.
func DefaultRMARMTypes() *RMARMTypes {
	rmARMOnce.Do(func() {
		t, err := ParseRMARMTypes(committedRMARMTypes)
		if err != nil {
			panic(fmt.Sprintf("armtypes: committed %s does not parse: %s", RMARMTypesPath, err))
		}
		rmARMDefault = t
	})
	return rmARMDefault
}

// AzurermARMType is the ARM resource type an azurerm resource type creates,
// read off its id template (live/azurerm-ids.json); ok is false for a type
// with no ARM tags map, or one whose template names no fixed ARM type.
func AzurermARMType(tfType string) (armType string, ok bool) {
	armType, ok = DefaultRMARMTypes().rows[tfType]
	return armType, ok
}

// AzurermAPIVersion is the API version azurerm sends for an azurerm
// resource type: the version of the go-azure-sdk package its id type lives
// in (live/azurerm-ids.json). ok is false for a type whose id parser is a
// commonids type or a hand parser, and for a type with no ARM type row.
func AzurermAPIVersion(tfType string) (apiVersion string, ok bool) {
	apiVersion, ok = DefaultRMARMTypes().versions[tfType]
	return apiVersion, ok
}

// Rows returns the join as azurerm type to ARM type.
func (t *RMARMTypes) Rows() map[string]string {
	out := make(map[string]string, len(t.rows))
	for k, v := range t.rows {
		out[k] = v
	}
	return out
}

// AzurermTypesForARMType is every azurerm resource type whose id template
// names armType, compared without regard to case (Resource Graph lowercases
// the types it lists), sorted, with the join's own spelling of armType.
// Several azurerm types can create one ARM type (the Linux and Windows
// virtual machines), so a caller naming an object by it says which it is
// only when exactly one does. GitHub issue #2138: the Resource Graph leg
// names a listed object by its own type this way.
func AzurermTypesForARMType(armType string) (tfTypes []string, spelled string) {
	for tf, arm := range DefaultRMARMTypes().rows {
		if strings.EqualFold(arm, armType) {
			tfTypes = append(tfTypes, tf)
			spelled = arm
		}
	}
	sort.Strings(tfTypes)
	return tfTypes, spelled
}
