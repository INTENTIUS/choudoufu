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

	// Checked, Partial and Unchecked name the live-path stages behind this
	// verdict: the ones run in full, the ones run in part, and the ones
	// nobody looked at. All three are always printed: see
	// [LiveCheckHuman.Report].
	Checked   []string
	Partial   []string
	Unchecked []string

	// Schemas is whether provider schemas were available.
	Schemas bool

	// UnresolvedModules are module calls whose contents went unchecked.
	UnresolvedModules []LiveCheckModule

	// UnsetVariables are required input variables that had no value.
	UnsetVariables []string

	// VariableDependentFindings is how many refusals have at least one site
	// reading an unset variable, and FullyVariableDependent how many have
	// no other kind of site. The second is the number that would go away if
	// the variables were supplied; the first is the number that might
	// change. Issue #161.
	VariableDependentFindings int
	FullyVariableDependent    int

	// OnlyBackendRemains is true when the configuration is not blocked and
	// the only warning is the state-backend rule: the sole remaining
	// friction is a backend or cloud block, which choudoufu ignores rather
	// than reads. Deleting it is still the recommended edit - so the
	// configuration says what actually happens - though it is not required.
	//
	// Before #214 demoted state-backend from a fatal finding to a warning,
	// this named the "one edit from moving" case (#175): 11 of 145 real
	// published estates were blocked on exactly this rule and nothing else.
	// That case cannot occur anymore - state-backend never reaches
	// Findings, so it can no longer be what blocks a configuration - but the
	// clean verdict it now describes is still worth calling out by name
	// rather than folding into an unqualified "nothing is refused".
	OnlyBackendRemains bool
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

	// UnsetVarRefs are the valueless input variables this refusal's sites
	// read, and UnsetVarSites how many of its sites read one.
	UnsetVarRefs  []string
	UnsetVarSites int
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
// covers three of the six live-path stages and would otherwise read as a
// promise about the other three.
func (v *LiveCheckHuman) Report(rep LiveCheckReport) {
	var b strings.Builder

	if rep.Blocked {
		// The headline distinguishes refusals that survive missing
		// variable values from ones that may not (issue #161). Someone
		// assessing compatibility reads this line and stops, so a count
		// inflated by unsupplied variables is wrong in the direction that
		// makes this fork look worse than it is - and the audiences most
		// worth winning, whose variables come from a pipeline, are exactly
		// the ones who run with none of them set.
		settled := len(rep.Findings) - rep.FullyVariableDependent
		switch {
		case settled == 0 && rep.FullyVariableDependent > 0:
			fmt.Fprintf(&b, "\n%s is inconclusive: every refusal below depends on an input variable that\n", rep.Dir)
			fmt.Fprintf(&b, "had no value. Supply %s and run this again.\n", varPhrase(rep.UnsetVariables))
			fmt.Fprintf(&b, "%d refusal(s) across %d site(s); %d managed resource instance(s) resolved.\n",
				len(rep.Findings), rep.Sites, rep.Instances)
		case rep.FullyVariableDependent > 0:
			fmt.Fprintf(&b, "\n%s cannot move under live resource markers yet.\n", rep.Dir)
			fmt.Fprintf(&b, "%d refusal(s) across %d site(s); %d managed resource instance(s) resolved.\n",
				len(rep.Findings), rep.Sites, rep.Instances)
			fmt.Fprintf(&b, "%d of those refusal(s) depend entirely on an input variable that had no value and\n", rep.FullyVariableDependent)
			fmt.Fprintf(&b, "may not be real; supply %s to find out.\n", varPhrase(rep.UnsetVariables))
		default:
			fmt.Fprintf(&b, "\n%s cannot move under live resource markers yet.\n", rep.Dir)
			fmt.Fprintf(&b, "%d refusal(s) across %d site(s); %d managed resource instance(s) resolved.\n",
				len(rep.Findings), rep.Sites, rep.Instances)
		}
	} else if rep.OnlyBackendRemains {
		// #215: the estate is clean, but the only warning below is its
		// backend or cloud block. That block is inert under live resource
		// markers rather than merely superfluous, and the rule's own detail
		// text (internal/live/lint/lint.go, checkStateBackends) already
		// recommends deleting it; nothing else in this report surfaces that
		// recommendation, so it is worth this one dedicated sentence rather
		// than folding into the generic clean verdict below.
		//
		// #268: "not required" is only true of this check, which runs with
		// no live block by design. Plan and apply both need one, and a live
		// block beside this one refuses to load at all - so the wording
		// below says when deletion stops being optional, rather than
		// leaving a blanket "not required" that only holds here.
		fmt.Fprintf(&b, "\n%s already moves under live resource markers.\n", rep.Dir)
		fmt.Fprintf(&b, "The only warning below is its backend or cloud block: this check ran with no live\n")
		fmt.Fprintf(&b, "block, so the block is ignored. Deleting it is the recommended edit, and it is not\n")
		fmt.Fprintf(&b, "required for a check like this one - but it is required the moment a live block is\n")
		fmt.Fprintf(&b, "added, which plan and apply both need: a live block beside this one refuses to\n")
		fmt.Fprintf(&b, "load. Delete it now rather than at that point.\n")
		fmt.Fprintf(&b, "%d managed resource instance(s) resolved.\n", rep.Instances)
	} else {
		fmt.Fprintf(&b, "\nNothing in %s is refused by the two checks below.\n", rep.Dir)
		fmt.Fprintf(&b, "%d managed resource instance(s) resolved.\n", rep.Instances)
	}

	for _, finding := range rep.Findings {
		fmt.Fprintf(&b, "\n%s  (%d site(s), %s)\n", finding.Title, finding.SiteCount, finding.Layer)
		if finding.UnsetVarSites > 0 {
			if finding.UnsetVarSites == finding.SiteCount {
				fmt.Fprintf(&b, "  May not be real: every site reads %s, which had no value.\n", varPhrase(finding.UnsetVarRefs))
			} else {
				fmt.Fprintf(&b, "  %d of these site(s) read %s, which had no value, and may not be real.\n",
					finding.UnsetVarSites, varPhrase(finding.UnsetVarRefs))
			}
		}

		switch {
		case len(finding.Types) > 0:
			for _, tc := range finding.Types {
				fmt.Fprintf(&b, "    %-40s %d\n", tc.Label, tc.Count)
			}
		default:
			for _, site := range finding.Examples {
				switch {
				case site.Location == "" && site.Address == "":
					// A site with neither a position nor an address has
					// nothing to show, and printing it produced a line of
					// four spaces. internal/live/projection raises both of
					// the refusals internal/live/check computes offline with
					// tfdiags.Sourceless, so this is every site of every
					// projection finding today. The remedy and the docs
					// reference below carry what is known about it.
					continue
				case site.Address != "":
					fmt.Fprintf(&b, "    %s  %s\n", site.Location, site.Address)
				default:
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
	if len(rep.Partial) > 0 {
		fmt.Fprintf(&b, "Partly checked: %s.\n", strings.Join(rep.Partial, ", "))
	}
	// The caveat survives an empty Unchecked list; the sentence naming the
	// stages does not.
	//
	// "Not checked: ." is what the unconditional version printed once
	// Unchecked went empty, and issue #261 was closed partly on the argument
	// that emptying that list would make this report overstate the run.
	// Nothing asserted on the text, so the argument rested on a renderer
	// nobody had read. What is true either way is the last clause: no stage
	// here makes a cloud call, and the partly-checked stages still have
	// refusals that need one. That is what has to keep printing.
	if len(rep.Unchecked) > 0 {
		fmt.Fprintf(&b, "Not checked: %s. Each of those needs a cloud, and this command makes no cloud calls,\n", strings.Join(rep.Unchecked, ", "))
		b.WriteString("so a clean result above is not a promise that an apply succeeds.\n")
	} else {
		b.WriteString("No stage went entirely unchecked. This command still makes no cloud calls, so a clean\n")
		b.WriteString("result above is not a promise that an apply succeeds.\n")
	}

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
		fmt.Fprintf(&b, "\n%d required input variable(s) had no value: %s\n",
			len(rep.UnsetVariables), strings.Join(rep.UnsetVariables, ", "))
		b.WriteString("Expressions reading them are not statically evaluable. ")
		if rep.VariableDependentFindings > 0 {
			b.WriteString("The refusals this affects are\nmarked above.\n")
		} else {
			b.WriteString("No refusal above reads one, so none\nof them is an artifact of this.\n")
		}
	}

	if len(rep.Warnings) > 0 {
		b.WriteString("\nWarnings, which do not stop a run:\n")
		for _, warning := range rep.Warnings {
			fmt.Fprintf(&b, "    %-60s %d\n", warning.Label, warning.Count)
		}
	}

	v.view.streams.Print(b.String())
}

// varPhrase renders variable names as prose: "var.account_id", or
// "var.a and var.b", or "var.a, var.b and var.c".
func varPhrase(names []string) string {
	switch len(names) {
	case 0:
		return "an input variable"
	case 1:
		return "var." + names[0]
	}
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "var." + n
	}
	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}
