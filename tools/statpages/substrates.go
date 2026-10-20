// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"fmt"
	"strings"
)

// substratesData is live/substrates.json, as far as the page reads it.
type substratesData struct {
	Rows []struct {
		Family   string `json:"family"`
		Identity struct {
			Description string `json:"description"`
		} `json:"identity"`
		Surfaces []struct {
			Surface       string `json:"surface"`
			MarkerNoun    string `json:"marker_noun"`
			CarrierPhrase string `json:"carrier_phrase"`
			Writes        struct {
				Create     string `json:"create"`
				Adopt      string `json:"adopt"`
				PostCreate string `json:"post_create"`
			} `json:"writes"`
		} `json:"surfaces"`
		Sweep struct {
			Kind        string `json:"kind"`
			Description string `json:"description"`
		} `json:"sweep"`
		ControllerHeld struct {
			Recognized bool   `json:"recognized"`
			Mechanism  string `json:"mechanism"`
		} `json:"controller_held"`
		Untaggable struct {
			Count      int            `json:"count"`
			ByTier     map[string]int `json:"by_tier"`
			NotCounted bool           `json:"not_counted"`
		} `json:"untaggable"`
		Fence   string `json:"fence"`
		Harness struct {
			Name    string `json:"name"`
			PinFile string `json:"pin_file"`
			Pin     string `json:"pin"`
			Notes   string `json:"notes"`
		} `json:"harness"`
	} `json:"rows"`
}

// substratesSpans is the substrates-table shortcode turned on its side: one
// column per provider family and one row per property, which reads on a
// phone where nine columns do not.
func substratesSpans(root string) (map[string]string, error) {
	var d substratesData
	if err := readJSON(root, SubstratesRel, &d); err != nil {
		return nil, err
	}
	var s strings.Builder
	s.WriteString("| |")
	for _, r := range d.Rows {
		fmt.Fprintf(&s, " %s |", strings.ToUpper(r.Family))
	}
	s.WriteString("\n|---|")
	for range d.Rows {
		s.WriteString("---|")
	}
	s.WriteString("\n")
	row := func(label string, f func(i int) string) {
		fmt.Fprintf(&s, "| %s |", label)
		for i := range d.Rows {
			fmt.Fprintf(&s, " %s |", cell(f(i)))
		}
		s.WriteString("\n")
	}
	row("Identity", func(i int) string { return d.Rows[i].Identity.Description })
	row("Marker surfaces", func(i int) string {
		var parts []string
		for _, x := range d.Rows[i].Surfaces {
			parts = append(parts, fmt.Sprintf("`%s`: %s (%s)", x.Surface, x.CarrierPhrase, x.MarkerNoun))
		}
		return strings.Join(parts, "<br>")
	})
	row("Writes (create / adopt / post-create)", func(i int) string {
		var parts []string
		for _, x := range d.Rows[i].Surfaces {
			parts = append(parts, fmt.Sprintf("`%s`: %s / %s / %s", x.Surface, x.Writes.Create, x.Writes.Adopt, x.Writes.PostCreate))
		}
		return strings.Join(parts, "<br>")
	})
	row("Sweep", func(i int) string { return d.Rows[i].Sweep.Kind + ": " + d.Rows[i].Sweep.Description })
	row("Controller-held", func(i int) string {
		if !d.Rows[i].ControllerHeld.Recognized {
			return "not recognized"
		}
		return d.Rows[i].ControllerHeld.Mechanism
	})
	row("Untaggable types", func(i int) string {
		u := d.Rows[i].Untaggable
		if u.NotCounted {
			return "not yet counted"
		}
		out := fmt.Sprint(u.Count)
		var parts []string
		for _, k := range sortedKeys(u.ByTier) {
			parts = append(parts, fmt.Sprintf("%s: %d", k, u.ByTier[k]))
		}
		if len(parts) > 0 {
			out += " (" + strings.Join(parts, "; ") + ")"
		}
		return out
	})
	row("Fence", func(i int) string { return d.Rows[i].Fence })
	row("Harness", func(i int) string {
		h := d.Rows[i].Harness
		if h.PinFile == "" {
			return fmt.Sprintf("%s, not yet pinned. %s", h.Name, h.Notes)
		}
		return fmt.Sprintf("%s, pinned in `%s` to `%s`. %s", h.Name, h.PinFile, h.Pin, h.Notes)
	})
	return map[string]string{"substrates": s.String()}, nil
}
