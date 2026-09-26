// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"fmt"
	"strings"
)

// The report categories -filter selects among (GitHub issue #1197). The words
// are the ones the code and live-plan's -json document already use for the
// same three sets - LivePlanDocument's "unowned", "adoptable" and "foreign"
// fields - so a filter word and a document key never name different things.
//
// The 2026-09-26 ruling on #1197 fixed this vocabulary and deferred the other
// selectors the issue was filed with: "drifted" and "owned-by" are not
// computed as per-resource sets today, and "unclaimed" and "untagged" would be
// second names for foreign and unowned.
const (
	ReportUnowned   = "unowned"
	ReportAdoptable = "adoptable"
	ReportForeign   = "foreign"
)

// ReportFilterWords is every word -filter accepts, in the order the report
// prints the categories. The usage error for any other word lists exactly
// these.
var ReportFilterWords = []string{ReportUnowned, ReportAdoptable, ReportForeign}

// ReportFilter is the set of report categories a run asked to see. Empty
// means no filter: every category renders, as it always has.
//
// A filter narrows the REPORT, never the plan. The 2026-09-26 ruling on #1197
// is explicit that a filtered apply would be a partial apply and is not
// built: the planned changes, the resource diff and the exit code's meaning
// are the same with or without it. Only the three category sections named
// above, and the matching fields of the -json document, are narrowed.
//
// Repeated -filter flags union: "-filter unowned -filter foreign" shows both.
// The value is kept in [ReportFilterWords] order and without duplicates, so
// the document's "filter" field reads the same however the flags were
// ordered.
type ReportFilter []string

// Active reports whether any filter was given.
func (f ReportFilter) Active() bool { return len(f) > 0 }

// Shows reports whether category renders under this filter. Every category
// renders when no filter was given.
func (f ReportFilter) Shows(category string) bool {
	if len(f) == 0 {
		return true
	}
	for _, c := range f {
		if c == category {
			return true
		}
	}
	return false
}

// reportFilterFlag is the flag.Value behind -filter. Each Set adds one word
// to the set; an unknown word is a parse error naming the allowed words, so
// "-filter nonsense" can never be mistaken for a valid filter that matched
// nothing.
type reportFilterFlag struct {
	dst *ReportFilter
}

func (v reportFilterFlag) String() string {
	if v.dst == nil {
		return ""
	}
	return strings.Join(*v.dst, ",")
}

func (v reportFilterFlag) Set(word string) error {
	known := false
	for _, w := range ReportFilterWords {
		if w == word {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("-filter takes one of %s, not %q", strings.Join(ReportFilterWords, ", "), word)
	}
	have := map[string]bool{word: true}
	for _, c := range *v.dst {
		have[c] = true
	}
	next := make(ReportFilter, 0, len(have))
	for _, w := range ReportFilterWords {
		if have[w] {
			next = append(next, w)
		}
	}
	*v.dst = next
	return nil
}
