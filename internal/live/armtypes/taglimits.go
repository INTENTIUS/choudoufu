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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ARM's tag limits, as azurerm enforces them for every resource whose
// `tags` schema is commonschema.Tags() or TagsForceNew(): go-azure-helpers
// v0.82.0 (vendored by azurerm 5.10.0), resourcemanager/tags/validate.go,
// Validate:
//
//	if len(tagsMap) > 50 { "a maximum of 50 tags can be applied to each resource" }
//	if len(k) > 512      { "the maximum length for a tag key is 512 characters" }
//	if len(value) > 256  { "the maximum length for a tag value is 256 characters" }
//
// The same three figures are Azure's documented limits (Microsoft Learn,
// "Use tags to organize your Azure resources", Limitations), which also
// list the types that allow fewer; TagLimit carries the ones azapi's type
// data documents. A marker value longer than MaxValueLength has to be
// split, as the AWS continuation-tag scheme splits values over 256.
const (
	DefaultTagLimit = 50
	MaxKeyLength    = 512
	MaxValueLength  = 256
)

// ForbiddenKeyChars are the characters Azure documents that a tag name
// cannot contain (Microsoft Learn, "Use tags to organize your Azure
// resources", Limitations: "Tag names can't contain these characters: <,
// >, %, &, \, ?, /"). Neither azurerm 5.10.0 nor go-azure-helpers checks
// them: a key with one is refused by ARM at apply, not at plan. That is a
// docs-sourced fact, unlike the rest of this package, and the maintainer's
// floci-az measurement (ADDING-A-PLATFORM.md requirement 2) is what proves
// or moves it.
const ForbiddenKeyChars = `<>%&\?/`

// TagLimitsPath is the committed per-type limits, relative to this
// package's directory.
const TagLimitsPath = "taglimits_generated.txt"

//go:embed taglimits_generated.txt
var committedTagLimits []byte

// TypeTagLimit is one ARM type's documented tag limits. A zero field is
// undocumented for that type.
type TypeTagLimit struct {
	MaxTags     int
	MaxKeyLen   int
	MaxValueLen int
	// APIVersion is the version whose `tags` description gave MaxTags (the
	// first, in sorted order, giving the lowest).
	APIVersion string
	// Varies is true when the type's taggable versions do not all
	// document the same MaxTags: some say less, or nothing.
	Varies bool
}

// TagLimits is the per-type limits table.
type TagLimits struct {
	AzapiVersion string
	AzapiCommit  string
	rows         map[string]TypeTagLimit
}

// The `tags` descriptions in azapi's data spell a count limit three ways:
// "A maximum of 15 tags can be provided for a resource" (Azure's own
// resource providers), "Maximum number of tags per resource - 50" and
// "A maximum number of 50 tags can be specified" (the AWS connector
// types). Key and value lengths: "a key no greater than 128 characters
// and value no greater than 256 characters" with optional "with a length"
// / "in length" fillers. These read Azure's prose, not Go source.
var (
	reMaxTags = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bmaximum of (\d+) tags\b`),
		regexp.MustCompile(`(?i)\bmaximum number of (\d+) tags\b`),
		regexp.MustCompile(`(?i)\bmaximum number of tags per \w+\s*[-:]\s*(\d+)`),
	}
	reKeyLen   = regexp.MustCompile(`(?i)\bkey (?:with a length )?no greater (?:in length )?than (\d+) characters`)
	reValueLen = regexp.MustCompile(`(?i)\bvalue (?:with a length )?no greater (?:in length )?than (\d+) characters`)
)

func describedLimits(desc string) (maxTags, keyLen, valueLen int) {
	for _, re := range reMaxTags {
		if m := re.FindStringSubmatch(desc); m != nil {
			maxTags, _ = strconv.Atoi(m[1])
			break
		}
	}
	if m := reKeyLen.FindStringSubmatch(desc); m != nil {
		keyLen, _ = strconv.Atoi(m[1])
	}
	if m := reValueLen.FindStringSubmatch(desc); m != nil {
		valueLen, _ = strconv.Atoi(m[1])
	}
	return
}

// GenerateTagLimits reads every taggable (type, API version) body in
// azapi's data and keeps, per type, the tightest limits its `tags`
// description documents. Only types documenting something tighter than
// ARM's defaults get a row.
func GenerateTagLimits(repoDir, version, commit string) (*TagLimits, error) {
	type seen struct {
		lim       TypeTagLimit
		versions  int
		atMin     int
		anyRecord bool
	}
	byType := map[string]*seen{}
	err := walkBodies(repoDir, func(typ, ver string, _ bool, props map[string]bicepProp) error {
		p, ok := props["tags"]
		if !ok || p.Flags&flagReadOnly != 0 {
			return nil
		}
		lower := strings.ToLower(typ)
		s := byType[lower]
		if s == nil {
			s = &seen{}
			byType[lower] = s
		}
		s.versions++
		n, k, v := describedLimits(p.Description)
		if n > 0 && n < DefaultTagLimit {
			switch {
			case s.lim.MaxTags == 0 || n < s.lim.MaxTags:
				s.lim.MaxTags, s.lim.APIVersion, s.atMin = n, ver, 1
			case n == s.lim.MaxTags:
				s.atMin++
				if ver < s.lim.APIVersion {
					s.lim.APIVersion = ver
				}
			}
		}
		if k > 0 && k < MaxKeyLength && (s.lim.MaxKeyLen == 0 || k < s.lim.MaxKeyLen) {
			s.lim.MaxKeyLen = k
		}
		if v > 0 && v < MaxValueLength && (s.lim.MaxValueLen == 0 || v < s.lim.MaxValueLen) {
			s.lim.MaxValueLen = v
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	t := &TagLimits{AzapiVersion: version, AzapiCommit: commit, rows: map[string]TypeTagLimit{}}
	for typ, s := range byType {
		if s.lim.MaxTags == 0 && s.lim.MaxKeyLen == 0 && s.lim.MaxValueLen == 0 {
			continue
		}
		s.lim.Varies = s.lim.MaxTags > 0 && s.atMin != s.versions
		t.rows[typ] = s.lim
	}
	return t, nil
}

// Types returns the types with a row, lowercased and sorted.
func (t *TagLimits) Types() []string {
	out := make([]string, 0, len(t.rows))
	for k := range t.rows {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Limit returns one type's row; ok is false when the type documents
// nothing tighter than ARM's defaults (or is not in azapi's data).
func (t *TagLimits) Limit(armType string) (TypeTagLimit, bool) {
	r, ok := t.rows[strings.ToLower(armType)]
	return r, ok
}

// LimitCounts is the table's summary.
type LimitCounts struct {
	// Types is the number of rows; Fewer those with MaxTags below
	// DefaultTagLimit; Varies those whose count limit differs by version.
	Types, Fewer, Varies int
}

func (t *TagLimits) Counts() LimitCounts {
	var c LimitCounts
	for _, r := range t.rows {
		c.Types++
		if r.MaxTags > 0 {
			c.Fewer++
		}
		if r.Varies {
			c.Varies++
		}
	}
	return c
}

const (
	limitsGenerated = "# Code generated by tools/armtypes-gen; DO NOT EDIT."
	limitsCounts    = "# counts: "
)

// Render writes the table in its committed format: one line per type,
// "<type> max=<n> key=<n> value=<n> from=<api version>[ varies]", with 0
// for an undocumented field and from=- when MaxTags is 0.
func (t *TagLimits) Render() []byte {
	var b bytes.Buffer
	c := t.Counts()
	b.WriteString(limitsGenerated + "\n")
	fmt.Fprintf(&b, "%sazure/terraform-provider-azapi %s %s internal/azure/generated (tags property descriptions)\n", headerSource, t.AzapiVersion, t.AzapiCommit)
	fmt.Fprintf(&b, "%stypes=%d fewer-than-default=%d varies-by-version=%d\n", limitsCounts, c.Types, c.Fewer, c.Varies)
	fmt.Fprintf(&b, "# format: <arm type, lowercased> max=<tags> key=<key length> value=<value length> from=<api version giving max>[ varies]; 0 = undocumented, the ARM default (%d, %d, %d) applies\n",
		DefaultTagLimit, MaxKeyLength, MaxValueLength)
	for _, typ := range t.Types() {
		r := t.rows[typ]
		from := r.APIVersion
		if from == "" {
			from = "-"
		}
		fmt.Fprintf(&b, "%s max=%d key=%d value=%d from=%s", typ, r.MaxTags, r.MaxKeyLen, r.MaxValueLen, from)
		if r.Varies {
			b.WriteString(" varies")
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// ParseTagLimits reads the committed format and checks the counts header.
func ParseTagLimits(data []byte) (*TagLimits, error) {
	t := &TagLimits{rows: map[string]TypeTagLimit{}}
	var header string
	sc := bufio.NewScanner(bytes.NewReader(data))
	line := 0
	prev := ""
	for sc.Scan() {
		line++
		s := sc.Text()
		switch {
		case strings.HasPrefix(s, headerSource):
			f := strings.Fields(strings.TrimPrefix(s, headerSource))
			if len(f) < 3 {
				return nil, fmt.Errorf("line %d: malformed source header %q", line, s)
			}
			t.AzapiVersion, t.AzapiCommit = f[1], f[2]
			continue
		case strings.HasPrefix(s, limitsCounts):
			header = strings.TrimPrefix(s, limitsCounts)
			continue
		case strings.HasPrefix(s, "#") || s == "":
			continue
		}
		f := strings.Fields(s)
		if len(f) < 5 || len(f) > 6 || (len(f) == 6 && f[5] != "varies") {
			return nil, fmt.Errorf("line %d: malformed row %q", line, s)
		}
		typ := f[0]
		if typ != strings.ToLower(typ) || typ <= prev {
			return nil, fmt.Errorf("line %d: type %q is not lowercased, sorted and distinct", line, typ)
		}
		prev = typ
		var r TypeTagLimit
		for i, field := range []struct {
			name string
			dst  *int
		}{{"max", &r.MaxTags}, {"key", &r.MaxKeyLen}, {"value", &r.MaxValueLen}} {
			v, ok := strings.CutPrefix(f[1+i], field.name+"=")
			n, err := strconv.Atoi(v)
			if !ok || err != nil || n < 0 {
				return nil, fmt.Errorf("line %d: bad %s in %q", line, field.name, s)
			}
			*field.dst = n
		}
		from, ok := strings.CutPrefix(f[4], "from=")
		if !ok || (from == "-") != (r.MaxTags == 0) {
			return nil, fmt.Errorf("line %d: from=%q disagrees with max=%d", line, from, r.MaxTags)
		}
		if from != "-" {
			r.APIVersion = from
		}
		r.Varies = len(f) == 6
		if r.MaxTags >= DefaultTagLimit || r.MaxKeyLen >= MaxKeyLength || r.MaxValueLen >= MaxValueLength ||
			(r.MaxTags == 0 && r.MaxKeyLen == 0 && r.MaxValueLen == 0) {
			return nil, fmt.Errorf("line %d: %q is no tighter than ARM's defaults", line, s)
		}
		t.rows[typ] = r
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if t.AzapiVersion == "" || t.AzapiCommit == "" {
		return nil, fmt.Errorf("no source header naming the azapi version and commit")
	}
	c := t.Counts()
	want := fmt.Sprintf("types=%d fewer-than-default=%d varies-by-version=%d", c.Types, c.Fewer, c.Varies)
	if header != want {
		return nil, fmt.Errorf("counts header %q disagrees with the rows, which give %q", header, want)
	}
	return t, nil
}

var (
	limitsOnce    sync.Once
	limitsDefault *TagLimits
)

// DefaultTagLimits is the committed table; it panics if the embedded file
// does not parse, which TestCommittedTagLimitsParse catches first.
func DefaultTagLimits() *TagLimits {
	limitsOnce.Do(func() {
		t, err := ParseTagLimits(committedTagLimits)
		if err != nil {
			panic(fmt.Sprintf("armtypes: committed %s does not parse: %s", TagLimitsPath, err))
		}
		limitsDefault = t
	})
	return limitsDefault
}

// TagLimit returns how many tags an ARM type takes and where the figure
// comes from: the lowest count the type's `tags` description documents in
// azapi's data, at any API version, or ARM's default of 50. The type
// compares case-insensitively. azurerm's own validation, which is per
// Terraform type, is AzurermTagRules; a stamp checks both.
func TagLimit(armType string) (max int, source string) {
	t := DefaultTagLimits()
	if r, ok := t.Limit(armType); ok && r.MaxTags > 0 {
		return r.MaxTags, fmt.Sprintf("azapi %s: the tags description of %s@%s", t.AzapiVersion, armType, r.APIVersion)
	}
	return DefaultTagLimit, "ARM default (go-azure-helpers tags.Validate; Azure's documented limit)"
}

// CheckTag applies azurerm's validation of one tag to a Terraform type
// (AzurermTagRules, ARM's defaults when azurerm declares none or the type
// is unknown) and ARM's documented forbidden key characters. It returns
// every rule the pair breaks; a lint uses it to prove a stamped marker
// fits before plan does.
func CheckTag(tfType, key, value string) []error {
	rules, _ := AzurermTagRules(tfType)
	maxKey, maxValue := MaxKeyLength, MaxValueLength
	if rules.MaxKeyLen > 0 {
		maxKey = rules.MaxKeyLen
	}
	if rules.MaxValueLen > 0 {
		maxValue = rules.MaxValueLen
	}
	var errs []error
	if len(key) > maxKey {
		errs = append(errs, fmt.Errorf("tag key %q is %d characters; %s allows %d", key, len(key), tfType, maxKey))
	}
	if len(value) > maxValue {
		errs = append(errs, fmt.Errorf("the value of tag %q is %d characters; %s allows %d", key, len(value), tfType, maxValue))
	}
	if rules.LowercaseKeys && strings.ToLower(key) != key {
		errs = append(errs, fmt.Errorf("tag key %q is not lowercase; %s requires lowercase keys", key, tfType))
	}
	for _, k := range rules.ForbiddenKeys {
		if key == k {
			errs = append(errs, fmt.Errorf("tag key %q is refused by %s's validation", key, tfType))
		}
	}
	if i := strings.IndexAny(key, ForbiddenKeyChars); i >= 0 {
		errs = append(errs, fmt.Errorf("tag key %q contains %q, which ARM refuses in a tag name", key, key[i:i+1]))
	}
	return errs
}
