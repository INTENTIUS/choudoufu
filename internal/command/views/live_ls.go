// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
)

// LiveLsReport is what "choudoufu live-ls" prints, in a form this package
// can render without importing internal/live/cloudcontrol or
// internal/live/markers: the command package decides what is true (which
// resources carry the estate's marker, which ones a configuration declares
// but the listing cannot see, and why), and this package decides how it
// reads. Both LiveLsHuman and LiveLsJSON render the same report, which is
// what lets behold - the named consumer in GitHub issue #789 - trust that
// the JSON it parses and the text a human reads describe the same run.
type LiveLsReport struct {
	// Estate is the tofu-estate value that was listed.
	Estate string

	// Region is the region the Tagging API and IAM calls were sent to, or
	// empty when none was named and the AWS SDK's own default resolution
	// picked one this command never learns.
	Region string

	// Consistent is whether -consistent was passed, and Stabilized is
	// whether two consecutive reads agreed before Attempts ran out - see
	// [command.pollConsistent]. Stabilized is always true when Consistent is
	// false: a single read has nothing to disagree with itself about.
	Consistent bool
	Stabilized bool
	Attempts   int

	// ConfigDir is the configuration directory the listing was
	// cross-referenced against, or empty when none was given - see
	// LiveLs.ConfigDir's own doc comment in the arguments package.
	ConfigDir string

	// Items is every resource the listing found, in the order
	// [LiveLsReport] is built in - already sorted by type then address then
	// id.
	Items []LiveLsItem

	// Gaps are declared instances a configuration directory named that the
	// listing itself cannot see, each with the rung that explains why: the
	// tier definitions (#417)'s record-carried and declaration-carried
	// tiers name resources with no marker to find at all, ever, which this
	// listing's whole mechanism is reading markers. Empty when ConfigDir is
	// empty.
	Gaps []LiveLsGap
}

// LiveLsItem is one live resource carrying the listed estate's marker.
type LiveLsItem struct {
	// ID is the resource's ARN, or another stable identity when the
	// discovery path that found it (today, only the IAM native path) has no
	// ARN in hand at the point of listing - which does not happen for IAM,
	// since a role's ListRoles entry always carries its own Arn, but is
	// stated here because Source names a second path this field's contract
	// has to hold for.
	ID string

	// Type is the resource's type: the resource type name decoded from its
	// own tofu-address marker when that marker parses, or, when it does
	// not, a coarse ARN-derived label ("service:resource-type" or bare
	// "service") - the same approximation
	// examples/live-mv-workbench/tlmig/govern.py's read_inventory (the
	// prior art GitHub issue #789 names) uses for every item, unconditionally.
	Type string

	// Address is the unescaped configuration address decoded from
	// tofu-address and its continuation tags, per live/MARKERS.md - empty
	// when the resource carries no readable tofu-address marker at all
	// (malformed, or genuinely absent despite carrying tofu-estate, which
	// live/MARKERS.md calls out as a possible, if unusual, state).
	Address string

	// Slot is the tofu-slot marker value, empty when absent.
	Slot string

	// Declared is whether Address matches a declared instance in ConfigDir,
	// meaningful only when ConfigDir is non-empty (always false otherwise -
	// nothing to compare against).
	Declared bool

	// Source names which pass found this item: "tagging" for the Resource
	// Groups Tagging API's estate-wide GetResources call, or "iam" for the
	// second pass over iam:ListRoles/iam:ListRoleTags GitHub issue #789
	// asks for by name, because the tagging index does not serve IAM on a
	// real account. An item the tagging pass already found is never
	// reported a second time under "iam", even when the IAM pass would
	// have found it too - see the command package's own dedup-by-ARN.
	Source string

	// Tags are every marker tag this resource carries, unmodified - the raw
	// material GitHub issue #789 asks for alongside the decoded fields
	// above.
	Tags map[string]string
}

// LiveLsGap is one declared instance the listing itself cannot see, and why.
type LiveLsGap struct {
	// Address is the declared instance's address, exactly as
	// [addrs.AbsResourceInstance.String] renders it.
	Address string

	// Type is the resource type.
	Type string

	// Rung is "record" for an instance whose identity lives in the estate's
	// record store rather than on a cloud object's tags (GitHub issue
	// #417's record-carried tier, and #73's RECORD_ADMITTED / #270's
	// located-identity types both land here), or "declaration-carried" for
	// one whose type carries no settable tags argument at all, so no marker
	// was ever written for it to be found by (#417's declaration-carried
	// tier). Never anything else: an instance on the marker-carried tier
	// that this listing still could not find is a genuine absence, not a
	// gap, and is left out of this list entirely - see the command
	// package's own classification.
	Rung string

	// Detail is one sentence explaining Rung, aimed at a reader who has
	// never read live/MARKERS.md's tier definitions.
	Detail string
}

// LiveLs renders what "choudoufu live-ls" prints. Diagnostics do not come
// through here; they go to [View] like every other command's - see
// LiveCheck's own doc comment for the same convention.
type LiveLs interface {
	Report(rep LiveLsReport)
}

// NewLiveLs returns the LiveLs implementation for args.ViewType: the JSON
// implementation GitHub issue #789 asks for by name, or the ordinary human
// report every other live-* command already has one of.
func NewLiveLs(args arguments.ViewOptions, view *View) LiveLs {
	switch args.ViewType {
	case arguments.ViewJSON:
		return &LiveLsJSON{view: view}
	default:
		return &LiveLsHuman{view: view}
	}
}

// LiveLsJSON writes the report as one JSON object, so a scripted reader -
// GitHub issue #789 names behold - never parses prose.
type LiveLsJSON struct {
	view *View
}

var _ LiveLs = (*LiveLsJSON)(nil)

// liveLsJSONItem and liveLsJSONReport give the JSON output stable,
// lowercase field names independent of this package's own Go field
// names, the same discipline every other JSON-emitting view in this
// package already holds itself to.
type liveLsJSONItem struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Address  string            `json:"address,omitempty"`
	Slot     string            `json:"slot,omitempty"`
	Declared bool              `json:"declared"`
	Source   string            `json:"source"`
	Tags     map[string]string `json:"tags"`
}

type liveLsJSONGap struct {
	Address string `json:"address"`
	Type    string `json:"type"`
	Rung    string `json:"rung"`
	Detail  string `json:"detail"`
}

type liveLsJSONReport struct {
	Estate     string           `json:"estate"`
	Region     string           `json:"region,omitempty"`
	Consistent bool             `json:"consistent"`
	Stabilized bool             `json:"stabilized"`
	Attempts   int              `json:"attempts"`
	ConfigDir  string           `json:"config_dir,omitempty"`
	Items      []liveLsJSONItem `json:"items"`
	Gaps       []liveLsJSONGap  `json:"gaps,omitempty"`
}

func (v *LiveLsJSON) Report(rep LiveLsReport) {
	out := liveLsJSONReport{
		Estate:     rep.Estate,
		Region:     rep.Region,
		Consistent: rep.Consistent,
		Stabilized: rep.Stabilized,
		Attempts:   rep.Attempts,
		ConfigDir:  rep.ConfigDir,
		Items:      make([]liveLsJSONItem, 0, len(rep.Items)),
	}
	for _, item := range rep.Items {
		out.Items = append(out.Items, liveLsJSONItem{
			ID:       item.ID,
			Type:     item.Type,
			Address:  item.Address,
			Slot:     item.Slot,
			Declared: item.Declared,
			Source:   item.Source,
			Tags:     item.Tags,
		})
	}
	for _, gap := range rep.Gaps {
		out.Gaps = append(out.Gaps, liveLsJSONGap{
			Address: gap.Address,
			Type:    gap.Type,
			Rung:    gap.Rung,
			Detail:  gap.Detail,
		})
	}

	// MarshalIndent rather than Marshal: this is a report meant to be read
	// as well as parsed, the same choice providers_schema.go's raw-string
	// Output makes for a different reason (there, the JSON is opaque bytes
	// this package never decodes; here, this package built the value, so
	// indentation costs nothing extra to add).
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		// Every field above is a plain string, bool, int or map[string]string -
		// nothing this package puts in a LiveLsReport can fail to encode.
		panic(fmt.Sprintf("live-ls: encoding the report as JSON: %s", err))
	}
	v.view.streams.Println(string(encoded))
}

// LiveLsHuman writes the report as text: one line per item, then the gap
// section when a configuration directory was given.
type LiveLsHuman struct {
	view *View
}

var _ LiveLs = (*LiveLsHuman)(nil)

func (v *LiveLsHuman) Report(rep LiveLsReport) {
	var b strings.Builder

	fmt.Fprintf(&b, "\nEstate %q: %d resource(s) carry its marker", rep.Estate, len(rep.Items))
	if rep.Region != "" {
		fmt.Fprintf(&b, " in %s", rep.Region)
	}
	b.WriteString(".\n")
	if rep.Consistent {
		if rep.Stabilized {
			fmt.Fprintf(&b, "Consistent listing: two reads agreed after %d attempt(s).\n", rep.Attempts)
		} else {
			fmt.Fprintf(&b, "Consistent listing requested, but no two of %d attempt(s) agreed; showing the last one.\n", rep.Attempts)
		}
	}

	if len(rep.Items) == 0 {
		b.WriteString("Nothing found.\n")
	}
	for _, item := range rep.Items {
		fmt.Fprintf(&b, "\n%-28s %s\n", item.Type, item.ID)
		if item.Address != "" {
			declared := ""
			if rep.ConfigDir != "" {
				if item.Declared {
					declared = "  (declared)"
				} else {
					declared = "  (undeclared)"
				}
			}
			fmt.Fprintf(&b, "  address: %s%s\n", item.Address, declared)
		} else {
			b.WriteString("  address: (no readable tofu-address marker)\n")
		}
		if item.Slot != "" {
			fmt.Fprintf(&b, "  slot:    %s\n", item.Slot)
		}
		fmt.Fprintf(&b, "  found by: %s\n", item.Source)
		if len(item.Tags) > 0 {
			keys := make([]string, 0, len(item.Tags))
			for k := range item.Tags {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprintf(&b, "  tags:    %s\n", strings.Join(tagPairs(item.Tags, keys), ", "))
		}
	}

	if rep.ConfigDir != "" {
		fmt.Fprintf(&b, "\n%d declared instance(s) in %s the listing itself cannot see:\n", len(rep.Gaps), rep.ConfigDir)
		if len(rep.Gaps) == 0 {
			b.WriteString("None - every declared instance this configuration knows how to check for is either in the listing above or on a rung this run could not classify.\n")
		}
		for _, gap := range rep.Gaps {
			fmt.Fprintf(&b, "  %-40s %-20s rung=%s\n", gap.Address, gap.Type, gap.Rung)
			fmt.Fprintf(&b, "    %s\n", gap.Detail)
		}
	}

	v.view.streams.Print(b.String())
}

func tagPairs(tags map[string]string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%s=%s", k, tags[k]))
	}
	return out
}
