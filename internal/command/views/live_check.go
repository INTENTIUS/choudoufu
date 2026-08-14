// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"
)

// LiveCheckReport is one configuration's verdict, in a form this package can
// render without importing internal/live/check.
//
// It follows [StatelessMvReport]'s convention for the same reason: the
// analysis package decides what is true, and this package decides how it
// reads. The fields are already-decided facts - the ranking, the site cap
// and the type summarization all happen before a report gets here.
type LiveCheckReport struct {
	// Dir is the directory that was checked, as the user named it.
	Dir string

	// Blocked is whether anything refused the configuration.
	Blocked bool

	// Instances is how many managed resource instances resolved, and Sites
	// how many refused positions were found.
	Instances int
	Sites     int

	// Findings are the refusals, already ranked.
	Findings []LiveCheckFinding

	// Warnings are non-fatal diagnostics, as a title and a count each.
	Warnings []LiveCheckCount

	// Checked and Unchecked name the live-path stages behind this verdict
	// and the ones nobody looked at. Both are always printed: see
	// [LiveCheckHuman.Report].
	Checked   []string
	Unchecked []string

	// Schemas is whether provider schemas were available.
	Schemas bool

	// UnresolvedModules are module calls whose contents went unchecked.
	UnresolvedModules []LiveCheckModule

	// UnsetVariables are required input variables that had no value.
	UnsetVariables []string
}

// LiveCheckFinding is one refusal and where it fired.
type LiveCheckFinding struct {
	// Title is the refusal's one-line summary, and Layer which pass
	// produced it.
	Title string
	Layer string

	// SiteCount is how many places it fired, which may exceed the number of
	// Examples.
	SiteCount int

	// Types is the per-resource-type breakdown for a type-shaped rule, and
	// is empty for every other rule. When it is set it replaces Examples in
	// the output: a rule that fires on four hundred resources of nine types
	// is read as nine lines, not four hundred.
	Types []LiveCheckCount

	// Examples are the first few positions, as "file:line" and an address.
	Examples []LiveCheckSite

	// MoreSites is how many positions were not shown.
	MoreSites int

	// Remedy is what to do about it, and DocsRef the shipped document that
	// explains it. An empty DocsRef is printed as the gap it is rather than
	// omitted.
	Remedy  string
	DocsRef string
}

// LiveCheckSite is one position.
type LiveCheckSite struct {
	Location string
	Address  string
}

// LiveCheckCount is a labelled count: a resource type's share of a
// type-shaped refusal, or a warning and how often it fired.
type LiveCheckCount struct {
	Label string
	Count int
}

// LiveCheckModule is one module call that could not be read.
type LiveCheckModule struct {
	Path   string
	Source string
}

// LiveCheck renders what "choudoufu live-check" prints. Diagnostics do not
// come through here; they go to [View] like every other command's.
type LiveCheck interface {
	Report(rep LiveCheckReport)
}

// NewLiveCheck returns the human-readable implementation. There is no JSON
// implementation: the other consumer of this analysis, tools/corpus-gen,
// calls internal/live/check directly rather than parsing command output.
func NewLiveCheck(view *View) LiveCheck {
	return &LiveCheckHuman{view: view}
}

// LiveCheckHuman writes the verdict to the view's output stream.
type LiveCheckHuman struct {
	view *View
}

var _ LiveCheck = (*LiveCheckHuman)(nil)

// Report prints the verdict, then what stops it, then what to do about each,
// then what was not checked.
//
// The last section is not optional and is not conditional on the verdict. It
// is the one a reader of a clean report most needs, because a clean report
// covers two of the five live-path stages and would otherwise read as a
// promise about the other three.
func (v *LiveCheckHuman) Report(rep LiveCheckReport) {
	var b strings.Builder

	if rep.Blocked {
		fmt.Fprintf(&b, "\n%s cannot move under live resource markers yet.\n", rep.Dir)
		fmt.Fprintf(&b, "%d refusal(s) across %d site(s); %d managed resource instance(s) resolved.\n",
			len(rep.Findings), rep.Sites, rep.Instances)
	} else {
		fmt.Fprintf(&b, "\nNothing in %s is refused by the two checks below.\n", rep.Dir)
		fmt.Fprintf(&b, "%d managed resource instance(s) resolved.\n", rep.Instances)
	}

	for _, finding := range rep.Findings {
		fmt.Fprintf(&b, "\n%s  (%d site(s), %s)\n", finding.Title, finding.SiteCount, finding.Layer)

		switch {
		case len(finding.Types) > 0:
			for _, tc := range finding.Types {
				fmt.Fprintf(&b, "    %-40s %d\n", tc.Label, tc.Count)
			}
		default:
			for _, site := range finding.Examples {
				if site.Address != "" {
					fmt.Fprintf(&b, "    %s  %s\n", site.Location, site.Address)
				} else {
					fmt.Fprintf(&b, "    %s\n", site.Location)
				}
			}
		}
		if finding.MoreSites > 0 {
			fmt.Fprintf(&b, "    ... and %d more\n", finding.MoreSites)
		}

		if finding.Remedy != "" {
			fmt.Fprintf(&b, "  %s\n", finding.Remedy)
		}
		if finding.DocsRef != "" {
			fmt.Fprintf(&b, "  See %s.\n", finding.DocsRef)
		} else {
			fmt.Fprintf(&b, "  No shipped document describes this refusal yet.\n")
		}
	}

	fmt.Fprintf(&b, "\nChecked: %s.\n", strings.Join(rep.Checked, ", "))
	fmt.Fprintf(&b, "Not checked: %s. Each of those needs a cloud, and this command makes no cloud calls,\n", strings.Join(rep.Unchecked, ", "))
	b.WriteString("so a clean result above is not a promise that an apply succeeds.\n")

	if !rep.Schemas {
		b.WriteString("\nNo provider schemas were available, so resource types were judged from the built-in\n")
		b.WriteString("admission table alone. Types the provider's own identity schema would have admitted read\n")
		b.WriteString("as refused above. Run \"choudoufu init\" here for the accurate answer.\n")
	}

	if len(rep.UnresolvedModules) > 0 {
		fmt.Fprintf(&b, "\n%d module call(s) could not be read without installing them; nothing inside them was\n", len(rep.UnresolvedModules))
		b.WriteString("checked. Run \"choudoufu init\" to fetch them:\n")
		for _, mod := range rep.UnresolvedModules {
			fmt.Fprintf(&b, "    %-24s %s\n", mod.Path, mod.Source)
		}
	}

	if len(rep.UnsetVariables) > 0 {
		fmt.Fprintf(&b, "\n%d required input variable(s) had no value, so expressions reading them are not\n", len(rep.UnsetVariables))
		b.WriteString("statically evaluable and some refusals above may be an artifact of that rather than of\n")
		fmt.Fprintf(&b, "the configuration: %s\n", strings.Join(rep.UnsetVariables, ", "))
	}

	if len(rep.Warnings) > 0 {
		b.WriteString("\nWarnings, which do not stop a run:\n")
		for _, warning := range rep.Warnings {
			fmt.Fprintf(&b, "    %-60s %d\n", warning.Label, warning.Count)
		}
	}

	v.view.streams.Print(b.String())
}
