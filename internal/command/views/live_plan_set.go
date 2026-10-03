// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/setplan"
)

// LivePlanSet renders live-plan-set's result (GitHub issue #1752).
type LivePlanSet interface {
	Report(doc *setplan.Document)
}

// NewLivePlanSet returns the renderer -json asked for.
func NewLivePlanSet(args *arguments.View, view *View) LivePlanSet {
	if args.ViewType == arguments.ViewJSON {
		return &LivePlanSetJSON{view: view}
	}
	return &LivePlanSetHuman{view: view}
}

// LivePlanSetJSON prints the document, once, as the whole of stdout.
type LivePlanSetJSON struct {
	view *View
}

func (v *LivePlanSetJSON) Report(doc *setplan.Document) {
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		// Every field is a string, bool, int or json.RawMessage that
		// setplan validated with json.Valid before keeping it.
		panic(fmt.Sprintf("live-plan-set: encoding the document as JSON: %s", err))
	}
	v.view.streams.Println(string(encoded))
}

// LivePlanSetHuman prints one line per root and a count.
type LivePlanSetHuman struct {
	view *View
}

func (v *LivePlanSetHuman) Report(doc *setplan.Document) {
	var b strings.Builder
	fmt.Fprintf(&b, "\nPlanned %d root(s), %d at a time, sharing the provider cache %s.\n\n", doc.Summary.Roots, doc.ParallelEstates, doc.PluginCacheDir)
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, r := range doc.Roots {
		estate := r.Estate
		if estate == "" {
			estate = "-"
		}
		switch {
		case r.Status != setplan.StatusPlanned:
			first, _, _ := strings.Cut(strings.TrimSpace(r.Error), "\n")
			fmt.Fprintf(tw, "  %s\t%s\tFAILED at %s\t%s (log %s)\n", r.Root, estate, r.Stage, first, r.LogFile)
		case r.Changes:
			fmt.Fprintf(tw, "  %s\t%s\tchanges\t%s\n", r.Root, estate, r.PlanFile)
		default:
			fmt.Fprintf(tw, "  %s\t%s\tno changes\t%s\n", r.Root, estate, r.PlanFile)
		}
	}
	_ = tw.Flush()
	fmt.Fprintf(&b, "\n%d root(s): %d with changes, %d without, %d failed.\n",
		doc.Summary.Roots, doc.Summary.Changed, doc.Summary.Planned-doc.Summary.Changed, doc.Summary.Failed)
	if doc.Digest != "" {
		fmt.Fprintf(&b, "Set digest: %s\n", doc.Digest)
	}
	v.view.streams.Print(b.String())
}
