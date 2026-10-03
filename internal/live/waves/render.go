// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatVersion is the -json document's format version. It moves when a
// field is renamed or dropped, never when one is added.
const FormatVersion = "1"

// Document is what live-waves -json prints.
type Document struct {
	FormatVersion string `json:"format_version"`
	// Roots are the roots read, in directory order, with what each reads.
	Roots []Root `json:"roots"`
	Waves []Wave `json:"waves"`
	// Edges are every ordering constraint between two roots of the set.
	Edges []Edge `json:"edges"`
	// ExternalReads are reads of estates no root of the set owns.
	ExternalReads []External `json:"external_reads"`
	// Digest is the set digest over every root, present when plans were
	// given.
	Digest string `json:"digest,omitempty"`
	// RootDigests are each root's digest, present when plans were given.
	RootDigests map[string]string `json:"root_digests,omitempty"`
}

// JSON renders d.
func (d *Document) JSON() (string, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Text renders d for a reader.
func (d *Document) Text() string {
	var b strings.Builder
	estateOf := make(map[string]string, len(d.Roots))
	for _, r := range d.Roots {
		estateOf[r.Root] = r.Estate
	}
	readsOf := make(map[string][]Edge)
	for _, e := range d.Edges {
		readsOf[e.Reader] = append(readsOf[e.Reader], e)
	}

	fmt.Fprintf(&b, "%d %s over %d %s.", len(d.Waves), plural(len(d.Waves), "wave", "waves"), len(d.Roots), plural(len(d.Roots), "root", "roots"))
	if d.Digest != "" {
		fmt.Fprintf(&b, " Set digest %s.", d.Digest)
	}
	b.WriteString("\n")
	for _, w := range d.Waves {
		b.WriteString("\n")
		kind := ""
		if w.Canary {
			kind = " (canaries)"
		}
		fmt.Fprintf(&b, "Wave %d%s: %d %s", w.Number, kind, len(w.Roots), plural(len(w.Roots), "root", "roots"))
		if w.Digest != "" {
			fmt.Fprintf(&b, ", digest %s", w.Digest)
		}
		b.WriteString("\n")
		for _, r := range w.Roots {
			fmt.Fprintf(&b, "  %s (estate %s)", r, estateOf[r])
			var after []string
			for _, e := range readsOf[r] {
				after = append(after, fmt.Sprintf("%s via %s", e.Producer, e.From))
			}
			if len(after) > 0 {
				fmt.Fprintf(&b, ", after %s", strings.Join(after, ", "))
			}
			b.WriteString("\n")
		}
	}
	if len(d.ExternalReads) > 0 {
		b.WriteString("\nReads of estates outside the set, which order nothing:\n")
		for _, e := range d.ExternalReads {
			fmt.Fprintf(&b, "  %s reads %s via %s\n", e.Root, e.Estate, e.From)
		}
	}
	return b.String()
}

// WaveLines is one wave's roots, one per line: the form another tool
// reads to open one change per wave.
func WaveLines(w Wave) string {
	if len(w.Roots) == 0 {
		return ""
	}
	return strings.Join(w.Roots, "\n") + "\n"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Text renders a wave apply's result for a reader.
func (r *ApplyResult) Text() string {
	var b strings.Builder
	switch r.ExitCode {
	case ExitSetMoved:
		fmt.Fprintf(&b, "Wave %d: refused, nothing applied.\n%s\n", r.Wave, r.Error)
		for _, m := range r.Moved {
			fmt.Fprintf(&b, "  %s moved: approved %s, now %s\n", m.Root, m.Approved, m.Fresh)
		}
		return b.String()
	case ExitError:
		fmt.Fprintf(&b, "Wave %d: %s\n", r.Wave, r.Error)
		return b.String()
	}
	counts := map[string]int{}
	for _, e := range r.Outcomes {
		counts[e.Outcome]++
	}
	fmt.Fprintf(&b, "Wave %d: %d landed, %d failed, %d skipped; %d applied by this run.\n",
		r.Wave, counts[OutcomeLanded], counts[OutcomeFailed], counts[OutcomeSkipped], len(r.Applied))
	for _, e := range r.Outcomes {
		fmt.Fprintf(&b, "  %s %s", e.Root, e.Outcome)
		if e.Reason != "" {
			fmt.Fprintf(&b, ": %s", e.Reason)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// JSON renders a wave apply's result.
func (r *ApplyResult) JSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	return string(b), err
}
