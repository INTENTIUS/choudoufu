// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// TablePath is the committed table, relative to this package's directory.
const TablePath = "table_generated.txt"

//go:embed table_generated.txt
var committedTable []byte

// Caps is what an ARM type's request body lets a caller write at one API
// version. Each bit is set when the body declares the property without the
// ReadOnly flag (see the package doc for the rule and its source).
type Caps uint8

const (
	CapTags     Caps = 1 << 0
	CapName     Caps = 1 << 1
	CapLocation Caps = 1 << 2
)

// Has reports whether every bit of want is set.
func (c Caps) Has(want Caps) bool { return c&want == want }

// typeRow is one ARM type's versions, sorted, and the caps at each.
type typeRow struct {
	versions []string
	caps     []Caps
}

func (r *typeRow) at(apiVersion string) (Caps, bool) {
	i := sort.SearchStrings(r.versions, apiVersion)
	if i < len(r.versions) && r.versions[i] == apiVersion {
		return r.caps[i], true
	}
	return 0, false
}

// latest is the lexically greatest version, which is the order azapi's own
// GetApiVersions returns (sort.Strings), preview versions included.
func (r *typeRow) latest() (string, Caps) {
	n := len(r.versions) - 1
	return r.versions[n], r.caps[n]
}

func (r *typeRow) tagsVary() bool {
	for _, c := range r.caps[1:] {
		if c.Has(CapTags) != r.caps[0].Has(CapTags) {
			return true
		}
	}
	return false
}

// Table is the parsed or generated type-capability table.
type Table struct {
	// AzapiVersion and AzapiCommit are the azure/terraform-provider-azapi
	// tag and commit the data came from.
	AzapiVersion string
	AzapiCommit  string
	// DiscriminatedTags counts the type-versions whose `tags` is writable
	// only through a DiscriminatedObjectType's baseProperties - the ones
	// azapi's canResourceHaveProperty would answer false for.
	DiscriminatedTags int

	rows map[string]*typeRow
}

// Counts is the table's summary, the numbers the docs and the readiness
// generator quote.
type Counts struct {
	// Types is the number of distinct ARM resource types (lowercased).
	Types int
	// TypeVersions is the number of (type, API version) pairs.
	TypeVersions int
	// TaggableAtLatest counts types taggable at their latest API version.
	TaggableAtLatest int
	// VersionDependent counts types whose taggability differs between
	// two of their API versions.
	VersionDependent int
}

// Counts recomputes the summary from the rows.
func (t *Table) Counts() Counts {
	var c Counts
	c.Types = len(t.rows)
	for _, r := range t.rows {
		c.TypeVersions += len(r.versions)
		if _, caps := r.latest(); caps.Has(CapTags) {
			c.TaggableAtLatest++
		}
		if r.tagsVary() {
			c.VersionDependent++
		}
	}
	return c
}

// Capabilities answers for one ARM type and API version. known is false
// when azapi's data has no such pair; the type compares case-insensitively
// and the version exactly, as azapi's GetResourceDefinition does.
func (t *Table) Capabilities(armType, apiVersion string) (caps Caps, known bool) {
	r := t.rows[strings.ToLower(armType)]
	if r == nil {
		return 0, false
	}
	return r.at(apiVersion)
}

// Types returns every ARM type in the table, lowercased and sorted.
func (t *Table) Types() []string {
	out := make([]string, 0, len(t.rows))
	for k := range t.rows {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Versions returns armType's API versions, sorted; nil when unknown.
func (t *Table) Versions(armType string) []string {
	r := t.rows[strings.ToLower(armType)]
	if r == nil {
		return nil
	}
	return append([]string(nil), r.versions...)
}

// Latest returns armType's lexically greatest API version and its caps.
func (t *Table) Latest(armType string) (apiVersion string, caps Caps, known bool) {
	r := t.rows[strings.ToLower(armType)]
	if r == nil {
		return "", 0, false
	}
	v, c := r.latest()
	return v, c, true
}

// VersionDependent reports whether armType's taggability differs between
// two of its API versions.
func (t *Table) VersionDependent(armType string) bool {
	r := t.rows[strings.ToLower(armType)]
	return r != nil && r.tagsVary()
}

var (
	defaultOnce  sync.Once
	defaultTable *Table
)

// Default is the committed table. It panics if the embedded file does not
// parse, which TestCommittedTableParses turns into a test failure first.
func Default() *Table {
	defaultOnce.Do(func() {
		t, err := Parse(committedTable)
		if err != nil {
			panic(fmt.Sprintf("armtypes: committed %s does not parse: %s", TablePath, err))
		}
		defaultTable = t
	})
	return defaultTable
}

// Taggable reports whether azapi's type data gives armType a writable
// `tags` at apiVersion. known is false when azapi has no schema for that
// pair, and a caller must then not stamp: a guess from another version
// is exactly what this table exists to avoid.
func Taggable(armType, apiVersion string) (taggable, known bool) {
	caps, known := Default().Capabilities(armType, apiVersion)
	return caps.Has(CapTags), known
}

// Types returns every ARM type azapi's data knows, lowercased and sorted.
func Types() []string { return Default().Types() }

// Summary returns the committed table's counts.
func Summary() Counts { return Default().Counts() }

// Source returns the azapi tag and commit the committed table came from.
func Source() (azapiVersion, commit string) {
	return Default().AzapiVersion, Default().AzapiCommit
}

// ParseAzapiType splits azapi's `type` argument, "Microsoft.X/y@2024-01-01",
// the way azapi does (utils.GetAzureResourceTypeApiVersion: strings.Split on
// "@", exactly two parts). It is stricter in one place: an empty type or
// version is not ok, since azapi has no schema for either. armType keeps
// the configuration's spelling; the table lowercases on lookup.
func ParseAzapiType(s string) (armType, apiVersion string, ok bool) {
	parts := strings.Split(s, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// The table's text format. Header lines start with "#"; the ones that
// carry data are "# source:", "# counts:" and "# discriminated-tags:".
// Every other line is one ARM type, lowercased, then its API versions in
// sorted order separated by single spaces. A version written "v=D" sets
// the caps (a decimal Caps) for itself and every following version up to
// the next "=D"; the first version always carries one. Since caps change
// rarely between versions, most versions are written bare.
const (
	headerGenerated = "# Code generated by tools/armtypes-gen; DO NOT EDIT."
	headerSource    = "# source: "
	headerCounts    = "# counts: "
	headerDiscrim   = "# discriminated-tags: "
)

// Render writes the table in its committed format.
func (t *Table) Render() []byte {
	var b bytes.Buffer
	c := t.Counts()
	b.WriteString(headerGenerated + "\n")
	fmt.Fprintf(&b, "%sazure/terraform-provider-azapi %s %s internal/azure/generated\n", headerSource, t.AzapiVersion, t.AzapiCommit)
	fmt.Fprintf(&b, "%stypes=%d type-versions=%d taggable-at-latest=%d version-dependent=%d\n",
		headerCounts, c.Types, c.TypeVersions, c.TaggableAtLatest, c.VersionDependent)
	fmt.Fprintf(&b, "%s%d\n", headerDiscrim, t.DiscriminatedTags)
	b.WriteString("# format: <arm type, lowercased> <api version>[=<caps>] ...; caps carries forward until the next =; bits: 1 tags, 2 name, 4 location\n")
	for _, typ := range t.Types() {
		r := t.rows[typ]
		b.WriteString(typ)
		for i, v := range r.versions {
			b.WriteByte(' ')
			b.WriteString(v)
			if i == 0 || r.caps[i] != r.caps[i-1] {
				b.WriteByte('=')
				b.WriteString(strconv.Itoa(int(r.caps[i])))
			}
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// Parse reads the committed format, and checks the "# counts:" header
// against the rows so that a hand edit to either is caught.
func Parse(data []byte) (*Table, error) {
	t := &Table{rows: map[string]*typeRow{}}
	var header string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		s := sc.Text()
		switch {
		case strings.HasPrefix(s, headerSource):
			f := strings.Fields(strings.TrimPrefix(s, headerSource))
			if len(f) != 4 {
				return nil, fmt.Errorf("line %d: malformed source header %q", line, s)
			}
			t.AzapiVersion, t.AzapiCommit = f[1], f[2]
			continue
		case strings.HasPrefix(s, headerCounts):
			header = strings.TrimPrefix(s, headerCounts)
			continue
		case strings.HasPrefix(s, headerDiscrim):
			n, err := strconv.Atoi(strings.TrimPrefix(s, headerDiscrim))
			if err != nil {
				return nil, fmt.Errorf("line %d: %s", line, err)
			}
			t.DiscriminatedTags = n
			continue
		case strings.HasPrefix(s, "#") || s == "":
			continue
		}
		f := strings.Split(s, " ")
		if len(f) < 2 {
			return nil, fmt.Errorf("line %d: type %q has no versions", line, f[0])
		}
		typ := f[0]
		if typ != strings.ToLower(typ) {
			return nil, fmt.Errorf("line %d: type %q is not lowercased", line, typ)
		}
		if t.rows[typ] != nil {
			return nil, fmt.Errorf("line %d: type %q appears twice", line, typ)
		}
		r := &typeRow{}
		var cur Caps
		for i, tok := range f[1:] {
			v, capsStr, hasCaps := strings.Cut(tok, "=")
			if hasCaps {
				n, err := strconv.Atoi(capsStr)
				if err != nil || n < 0 || n > int(CapTags|CapName|CapLocation) {
					return nil, fmt.Errorf("line %d: bad caps in %q", line, tok)
				}
				cur = Caps(n)
			} else if i == 0 {
				return nil, fmt.Errorf("line %d: first version %q carries no caps", line, tok)
			}
			if v == "" || (i > 0 && v <= r.versions[i-1]) {
				return nil, fmt.Errorf("line %d: versions of %q are not sorted and distinct at %q", line, typ, tok)
			}
			r.versions = append(r.versions, v)
			r.caps = append(r.caps, cur)
		}
		t.rows[typ] = r
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if t.AzapiVersion == "" || t.AzapiCommit == "" {
		return nil, fmt.Errorf("no %q header naming the azapi version and commit", strings.TrimSpace(headerSource))
	}
	c := t.Counts()
	want := fmt.Sprintf("types=%d type-versions=%d taggable-at-latest=%d version-dependent=%d",
		c.Types, c.TypeVersions, c.TaggableAtLatest, c.VersionDependent)
	if header != want {
		return nil, fmt.Errorf("counts header %q disagrees with the rows, which give %q", header, want)
	}
	return t, nil
}
