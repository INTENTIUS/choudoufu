// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"regexp"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// This file is issue #161. A live-check run against a configuration whose
// required input variables have no value reports refusals that are artifacts
// of the missing values rather than facts about the configuration. Measured
// on an AFT account-customization fixture: 2 refusals across 4 sites with no
// tfvars, 1 across 2 with them.
//
// The report already said so, in a trailer after the verdict, the findings
// and two other paragraphs. That is honest and useless in the same breath -
// someone assessing compatibility reads the headline and stops, and the
// headline was wrong in the direction that makes this fork look worse than
// it is. It matters most for the audiences worth winning, because anyone
// whose variables come from a pipeline (AFT, Terragrunt, CI) runs with none
// of them set.
//
// Attribution is per site rather than per report, so the caveat can be
// attached to the refusals it actually explains instead of hanging over all
// of them. A configuration with one variable-dependent refusal and three
// real ones should say exactly that.

// AttributeUnsetVariables marks every site whose own source text references
// a required input variable that had no value, and returns how many it
// marked.
//
// It runs after Analyze rather than inside it, and must: the static
// evaluator is lazy, so most variables are first read during identity
// resolution and the unset set is not complete until the analysis has
// finished. See [LoadResult.UnsetVariables].
//
// What it reads is the refusal's own range - the offending construct as the
// author wrote it - and not the whole file. A refusal on line 40 is not
// excused by an unset variable used on line 12.
func (r *Report) AttributeUnsetVariables(unset []string, sources map[string]*hcl.File) int {
	if len(unset) == 0 || len(sources) == 0 {
		return 0
	}
	want := make(map[string]bool, len(unset))
	for _, name := range unset {
		want[name] = true
	}

	marked := 0
	for i := range r.Findings {
		f := &r.Findings[i]
		seen := map[string]bool{}
		for j := range f.Sites {
			site := &f.Sites[j]
			refs := unsetRefsAt(site, want, sources)
			if len(refs) == 0 {
				continue
			}
			site.UnsetVarRefs = refs
			marked++
			for _, name := range refs {
				seen[name] = true
			}
		}
		f.UnsetVarRefs = sortedKeys(seen)
		f.UnsetVarSites = countMarked(f.Sites)
	}
	return marked
}

// unsetRefsAt returns the unset variables the site's own source text
// references.
//
// Two readings, and the fallback is not decoration. Parsing the range as an
// expression is exact: it finds var.x inside a "${...}" interpolation, and
// it does not match the word "var" in a comment or a string. But a refusal's
// range is not always a standalone expression - lint rules point at block
// headers and whole resource blocks - and hclsyntax refuses those. Rather
// than attribute nothing there, the fallback scans the same bytes textually.
//
// Both are bounded by the same range, so the fallback's looseness costs at
// most a false positive inside that one construct, and a false positive here
// weakens a caveat rather than inventing a refusal.
func unsetRefsAt(site *Site, want map[string]bool, sources map[string]*hcl.File) []string {
	if site.File == "" || site.EndByte <= site.StartByte {
		return nil
	}
	file, ok := sources[site.File]
	if !ok || site.EndByte > len(file.Bytes) {
		return nil
	}
	src := file.Bytes[site.StartByte:site.EndByte]

	found := map[string]bool{}
	expr, diags := hclsyntax.ParseExpression(src, site.File, hcl.Pos{
		Line: site.Line, Column: site.Column, Byte: site.StartByte,
	})
	if !diags.HasErrors() && expr != nil {
		for _, traversal := range expr.Variables() {
			if len(traversal) < 2 || traversal.RootName() != "var" {
				continue
			}
			step, ok := traversal[1].(hcl.TraverseAttr)
			if !ok {
				continue
			}
			if want[step.Name] {
				found[step.Name] = true
			}
		}
	} else {
		for _, m := range varRefRe.FindAllSubmatch(src, -1) {
			if name := string(m[1]); want[name] {
				found[name] = true
			}
		}
	}
	return sortedKeys(found)
}

// varRefRe matches a root input variable reference in source text.
var varRefRe = regexp.MustCompile(`\bvar\.([A-Za-z0-9_-]+)`)

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func countMarked(sites []Site) int {
	n := 0
	for _, s := range sites {
		if len(s.UnsetVarRefs) > 0 {
			n++
		}
	}
	return n
}
