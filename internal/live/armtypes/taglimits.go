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
	// document the same MaxTags: some say more, or nothing.
	Varies bool
	// ByVersion is, for a type that varies, every taggable API version
	// whose figure is not MaxTags, keyed lowercased: the count it documents,
	// or 0 when its `tags` description gives none. A taggable version not
	// listed documents MaxTags (GitHub issue #2133, finding 8).
	ByVersion map[string]int
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
		lim TypeTagLimit
		// counts is every taggable version's documented count, 0 for
		// none.
		counts map[string]int
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
			s = &seen{counts: map[string]int{}}
			byType[lower] = s
		}
		n, k, v := describedLimits(p.Description)
		s.counts[strings.ToLower(ver)] = n
		if n > 0 && n < DefaultTagLimit {
			switch {
			case s.lim.MaxTags == 0 || n < s.lim.MaxTags:
				s.lim.MaxTags, s.lim.APIVersion = n, ver
			case n == s.lim.MaxTags:
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
		if s.lim.MaxTags > 0 {
			for ver, n := range s.counts {
				if n != s.lim.MaxTags {
					if s.lim.ByVersion == nil {
						s.lim.ByVersion = map[string]int{}
					}
					s.lim.ByVersion[ver] = n
				}
			}
		}
		s.lim.Varies = len(s.lim.ByVersion) > 0
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
// "<type> max=<n> key=<n> value=<n> from=<api version>[ varies=<api
// version>:<n>,...]", with 0 for an undocumented field and from=- when
// MaxTags is 0. varies lists ByVersion, sorted by version.
func (t *TagLimits) Render() []byte {
	var b bytes.Buffer
	c := t.Counts()
	b.WriteString(limitsGenerated + "\n")
	fmt.Fprintf(&b, "%sazure/terraform-provider-azapi %s %s internal/azure/generated (tags property descriptions)\n", headerSource, t.AzapiVersion, t.AzapiCommit)
	fmt.Fprintf(&b, "%stypes=%d fewer-than-default=%d varies-by-version=%d\n", limitsCounts, c.Types, c.Fewer, c.Varies)
	fmt.Fprintf(&b, "# format: <arm type, lowercased> max=<tags> key=<key length> value=<value length> from=<api version giving max>[ varies=<api version>:<tags>,... for each taggable version documenting other than max]; 0 = undocumented, the ARM default (%d, %d, %d) applies\n",
		DefaultTagLimit, MaxKeyLength, MaxValueLength)
	for _, typ := range t.Types() {
		r := t.rows[typ]
		from := r.APIVersion
		if from == "" {
			from = "-"
		}
		fmt.Fprintf(&b, "%s max=%d key=%d value=%d from=%s", typ, r.MaxTags, r.MaxKeyLen, r.MaxValueLen, from)
		if r.Varies {
			vers := make([]string, 0, len(r.ByVersion))
			for v := range r.ByVersion {
				vers = append(vers, v)
			}
			sort.Strings(vers)
			for i, v := range vers {
				sep := ","
				if i == 0 {
					sep = " varies="
				}
				fmt.Fprintf(&b, "%s%s:%d", sep, v, r.ByVersion[v])
			}
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
		if len(f) < 5 || len(f) > 6 || (len(f) == 6 && !strings.HasPrefix(f[5], "varies=")) {
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
		if len(f) == 6 {
			if r.MaxTags == 0 {
				return nil, fmt.Errorf("line %d: varies on a type documenting no count", line)
			}
			r.ByVersion = map[string]int{}
			prevVer := ""
			for _, entry := range strings.Split(strings.TrimPrefix(f[5], "varies="), ",") {
				ver, count, ok := strings.Cut(entry, ":")
				n, err := strconv.Atoi(count)
				// A listed version documents another figure than max:
				// none (0) or more, never less, since max is the lowest.
				if !ok || err != nil || ver == "" || ver != strings.ToLower(ver) || ver <= prevVer || (n != 0 && n <= r.MaxTags) {
					return nil, fmt.Errorf("line %d: bad varies entry %q", line, entry)
				}
				prevVer = ver
				r.ByVersion[ver] = n
			}
			r.Varies = true
		}
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

// TagLimit returns how many tags an ARM type takes at one API version and
// where the figure comes from: the count the type's `tags` description in
// azapi's data documents at that version, or ARM's default of 50 when the
// type documents none at any version. The type and version compare
// case-insensitively. azurerm's own validation, which is per Terraform
// type, is AzurermTagRules; a stamp checks both.
//
// Seven types document a count at some versions and none at others
// (GitHub issue #2133, finding 8; at azapi 2.13.0 no version of any type
// documents a second, different count). A version whose description gives
// no count gets ARM's default, as a type that never documents one does:
// API Management's 10 is in its 2016-07-07 description and in none of the
// 24 since. An empty apiVersion (an azurerm type whose id parser names no
// version, [AzurermAPIVersion]) and a version azapi's data does not know get
// the lowest count any version documents, and the source says so.
func TagLimit(armType, apiVersion string) (max int, source string) {
	t := DefaultTagLimits()
	r, ok := t.Limit(armType)
	if !ok || r.MaxTags == 0 {
		return DefaultTagLimit, "ARM default (go-azure-helpers tags.Validate; Azure's documented limit)"
	}
	lowest := fmt.Sprintf("azapi %s: the tags description of %s@%s", t.AzapiVersion, armType, r.APIVersion)
	if !r.Varies {
		return r.MaxTags, lowest
	}
	switch n, listed := r.ByVersion[strings.ToLower(apiVersion)]; {
	case apiVersion == "":
		return r.MaxTags, lowest + ", the lowest any API version documents; no API version was given"
	case listed && n > 0:
		return n, fmt.Sprintf("azapi %s: the tags description of %s@%s", t.AzapiVersion, armType, apiVersion)
	case listed:
		return DefaultTagLimit, fmt.Sprintf("ARM default: azapi %s's tags description of %s@%s documents no count (%s@%s documents %d)", t.AzapiVersion, armType, apiVersion, armType, r.APIVersion, r.MaxTags)
	}
	if _, known := Taggable(armType, apiVersion); !known {
		return r.MaxTags, fmt.Sprintf("%s, the lowest any API version documents; azapi %s's data does not know %s@%s", lowest, t.AzapiVersion, armType, apiVersion)
	}
	return r.MaxTags, fmt.Sprintf("azapi %s: the tags description of %s@%s", t.AzapiVersion, armType, apiVersion)
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
