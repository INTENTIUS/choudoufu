// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/format"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #619's guard.
//
// The fork used to answer "which stock options can a live-markers run not
// honor" out of TWO functions - statelessRejections for plan and apply,
// livePlanRejectUnsupported for live-plan - and they had already drifted:
// #320 (ruled in #425) lifted the -destroy refusal from the first and not the
// second, and live-plan's help text described neither. That is the shape
// markers.Taggable's three copies had. There is now one function, and this
// file is what keeps it one.
//
// The claim these tests make is not "the two surfaces are identical". It is
// stronger and more useful: the surfaces differ in exactly the three places
// this file names, over the WHOLE input space of the function, so a fourth
// divergence cannot be added without a failure here.

// renderRejections renders one surface's refusals the way the CLI prints them
// with -no-color, keyed by summary. Rendered text rather than a count or a
// HasErrors boolean, because the thing that drifted last time was the wording
// as much as the verdict: live-plan's -out diagnostic asserted "this
// configuration has no live block" over configurations that had one.
func renderRejections(surface statelessSurface, op *arguments.Operation, state *arguments.State, viewOpts arguments.ViewOptions, planOut, generateConfigOut, planFile string) map[string]string {
	diags := statelessRejections(surface, op, state, viewOpts, planOut, generateConfigOut, planFile)
	out := make(map[string]string, len(diags))
	for _, d := range diags {
		out[d.Description().Summary] = format.DiagnosticPlain(d, nil, 78)
	}
	return out
}

// unwrapped collapses the hard line breaks format.DiagnosticPlain inserts at
// its width, so that a content assertion below is about the sentence rather
// than about where the wrapper happened to fold it.
func unwrapped(rendered string) string {
	return strings.Join(strings.Fields(rendered), " ")
}

func sortedRejectionSummaries(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The three summaries this file expects to see behave differently. Anything
// else diverging is the failure these tests exist to report.
const (
	destroyOnlyOnEstateFlag = "Only the normal planning mode is available under live resource markers yet"
	savedPlanFileSummary    = "Saved plan files are not available under live resource markers"
	jsonOutputSummary       = "Machine-readable output is not available under live resource markers yet"
)

// TestStatelessRejections_surfacesAgree walks the function's entire input
// space - every planning mode including no operation at all, every view
// selection, both settings of each of the three path arguments, and each of
// the three state paths - and asserts for all 384 combinations that the two
// surfaces produce the same refusals with the same wording, except for the
// three documented divergences.
//
// The cross product is exhaustive rather than a hand-picked table on purpose.
// A hand-picked table only catches a new surface-conditional clause if
// somebody thought to add the case that fires it; this catches one whatever
// input it keys on, because the function takes nothing else.
func TestStatelessRejections_surfacesAgree(t *testing.T) {
	modes := []struct {
		name string
		op   *arguments.Operation
	}{
		{"no-operation", nil},
		{"normal", &arguments.Operation{PlanMode: plans.NormalMode}},
		{"destroy", &arguments.Operation{PlanMode: plans.DestroyMode}},
		{"refresh-only", &arguments.Operation{PlanMode: plans.RefreshOnlyMode}},
	}
	views := []struct {
		name string
		opts arguments.ViewOptions
	}{
		{"human", arguments.ViewOptions{ViewType: arguments.ViewHuman}},
		{"json", arguments.ViewOptions{ViewType: arguments.ViewJSON}},
		// JSONInto is only ever compared against nil, so any open file
		// serves; nothing here writes to it.
		{"json-into", arguments.ViewOptions{ViewType: arguments.ViewHuman, JSONInto: os.Stdout}},
	}
	states := []struct {
		name  string
		state *arguments.State
	}{
		{"no-state-args", nil},
		{"state", &arguments.State{StatePath: "other.tfstate"}},
		{"state-out", &arguments.State{StateOutPath: "other.tfstate"}},
		{"backup", &arguments.State{BackupPath: "other.tfstate.bak"}},
	}
	paths := []string{"", "set"}

	cases := 0
	for _, mode := range modes {
		for _, view := range views {
			for _, st := range states {
				for _, planOut := range paths {
					for _, genConfig := range paths {
						for _, planFile := range paths {
							cases++
							name := fmt.Sprintf("%s/%s/%s/out=%q/genconfig=%q/planfile=%q",
								mode.name, view.name, st.name, planOut, genConfig, planFile)

							block := renderRejections(surfaceLiveBlock, mode.op, st.state, view.opts, planOut, genConfig, planFile)
							estate := renderRejections(surfaceEstateFlag, mode.op, st.state, view.opts, planOut, genConfig, planFile)

							// Divergence 1: the estate-flag surface has one
							// extra refusal, and only for -destroy.
							wantExtra := mode.op != nil && mode.op.PlanMode == plans.DestroyMode
							_, blockHasDestroy := block[destroyOnlyOnEstateFlag]
							_, estateHasDestroy := estate[destroyOnlyOnEstateFlag]
							if blockHasDestroy {
								t.Errorf("%s: the live-block surface refused the planning mode as %q, which is #320's lifted refusal coming back:\n%s",
									name, destroyOnlyOnEstateFlag, block[destroyOnlyOnEstateFlag])
							}
							if estateHasDestroy != wantExtra {
								t.Errorf("%s: live-plan's -estate surface refused the planning mode = %v, want %v", name, estateHasDestroy, wantExtra)
							}

							// Every other summary must appear on both
							// surfaces. Compared as sorted key lists so the
							// failure names the offending refusal.
							blockKeys := sortedRejectionSummaries(block)
							estateKeys := sortedRejectionSummaries(estate)
							estateKeys = withoutKey(estateKeys, destroyOnlyOnEstateFlag)

							// Divergence 3 (GitHub issue #788): live-plan's
							// -estate form accepts -json (ViewType: ViewJSON,
							// no -json-into) and prints its own document
							// instead of refusing; the live-block surface is
							// unchanged and keeps refusing it. Opposite shape
							// from divergence 1 above - here it is blockKeys,
							// not estateKeys, that carries the extra entry,
							// because #788 widened what the ESTATE surface
							// accepts rather than adding a refusal the
							// live-block surface alone raises.
							wantJSONExemptOnEstate := view.opts.ViewType == arguments.ViewJSON && view.opts.JSONInto == nil
							_, blockHasJSON := block[jsonOutputSummary]
							_, estateHasJSON := estate[jsonOutputSummary]
							switch {
							case wantJSONExemptOnEstate && estateHasJSON:
								t.Errorf("%s: live-plan's -estate surface still refused -json, which GitHub issue #788 exempts it from", name)
							case wantJSONExemptOnEstate && !blockHasJSON:
								t.Errorf("%s: the live-block surface stopped refusing -json - GitHub issue #788 does not widen that surface", name)
							case !wantJSONExemptOnEstate && blockHasJSON != estateHasJSON:
								t.Errorf("%s: -json's refusal is not symmetric outside the #788 exemption case (live block=%v -estate=%v)", name, blockHasJSON, estateHasJSON)
							}
							if wantJSONExemptOnEstate {
								blockKeys = withoutKey(blockKeys, jsonOutputSummary)
							}

							if strings.Join(blockKeys, "|") != strings.Join(estateKeys, "|") {
								t.Errorf("%s: the two surfaces refuse different things.\nlive block: %v\n-estate:    %v",
									name, blockKeys, estateKeys)
							}

							// Divergence 2: -out is worded differently, and
							// only -out. Every shared refusal must render
							// byte-identically on both surfaces.
							for _, summary := range blockKeys {
								same := block[summary] == estate[summary]
								wantSame := !(summary == savedPlanFileSummary && planOut != "")
								if same == wantSame {
									continue
								}
								if wantSame {
									t.Errorf("%s: %q is worded differently on the two surfaces.\nlive block:\n%s\n-estate:\n%s",
										name, summary, block[summary], estate[summary])
								} else {
									t.Errorf("%s: %q lost its surface-specific guidance - both surfaces now render:\n%s",
										name, summary, block[summary])
								}
							}
						}
					}
				}
			}
		}
	}
	if cases != 384 {
		t.Errorf("the cross product covered %d cases, want 384 - an input dimension was added or dropped without updating this count", cases)
	}
}

func withoutKey(keys []string, drop string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k != drop {
			out = append(out, k)
		}
	}
	return out
}

// TestStatelessRejections_divergencesSayWhy pins the CONTENT of the two
// divergences above, so that "the surfaces differ here" cannot decay into
// differing for a reason nobody can read. The test above proves there are
// exactly two; this one proves each still explains itself.
func TestStatelessRejections_divergencesSayWhy(t *testing.T) {
	human := arguments.ViewOptions{ViewType: arguments.ViewHuman}

	t.Run("destroy names the mechanical reason, not a principle", func(t *testing.T) {
		got := unwrapped(renderRejections(surfaceEstateFlag, &arguments.Operation{PlanMode: plans.DestroyMode}, nil, human, "", "", "")[destroyOnlyOnEstateFlag])
		// The refusal survives only while live-plan's own call site
		// hardcodes the mode. It must say that, and it must not repeat the
		// old claim that a live-markers destroy is unverified - #320 lifted
		// that and TestStatelessMode_destroyAlias exercises it.
		for _, want := range []string{
			"calling the planner directly in the normal planning mode",
			"run this same pipeline in destroy mode",
			"Rerun without -destroy",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("the -destroy refusal no longer says %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "not verified against a live-markers apply") {
			t.Errorf("the -destroy refusal still claims a live-markers destroy is unverified, which #320 lifted:\n%s", got)
		}
	})

	t.Run("-out guidance is on the surface it is true of", func(t *testing.T) {
		const guidance = "this configuration has no live block"
		estate := unwrapped(renderRejections(surfaceEstateFlag, nil, nil, human, "tfplan", "", "")[savedPlanFileSummary])
		block := unwrapped(renderRejections(surfaceLiveBlock, nil, nil, human, "tfplan", "", "")[savedPlanFileSummary])
		if !strings.Contains(estate, guidance) {
			t.Errorf("live-plan's -out refusal lost the warning that plain plan and apply here are state-backed:\n%s", estate)
		}
		if strings.Contains(block, guidance) {
			t.Errorf("the live-block surface's -out refusal asserts %q over a configuration that has one:\n%s", guidance, block)
		}
	})

	t.Run("-json's live-block refusal names its own -estate exception", func(t *testing.T) {
		jsonView := arguments.ViewOptions{ViewType: arguments.ViewJSON}
		block := unwrapped(renderRejections(surfaceLiveBlock, nil, nil, jsonView, "", "", "")[jsonOutputSummary])
		if block == "" {
			t.Fatalf("the live-block surface accepted -json - GitHub issue #788 only widens the -estate surface")
		}
		if _, estateRefused := renderRejections(surfaceEstateFlag, nil, nil, jsonView, "", "", "")[jsonOutputSummary]; estateRefused {
			t.Fatalf("live-plan's -estate surface still refused -json under GitHub issue #788's own exemption")
		}
		for _, want := range []string{
			`live-plan's own "-estate" form is the one exception`,
			"GitHub issue #788",
		} {
			if !strings.Contains(block, want) {
				t.Errorf("the live-block surface's -json refusal does not name its own exception:\n%s\nwant substring %q", block, want)
			}
		}
	})
}

// TestStatelessMode_aliasUsesTheLiveBlockSurface is the end-to-end half of
// GitHub issue #619, and the defect it pins is the one the divergence caused
// rather than the divergence itself.
//
// livePlanRejectUnsupported ran BEFORE LivePlanCommand.Run's alias check, so
// a configuration carrying a live block - where live-plan IS "choudoufu
// plan", down to the flag set - got live-plan's refusal list anyway. Two
// consequences, both user-visible, both checked here: "live-plan -destroy"
// was refused in a directory where "plan -destroy" ran, and "live-plan -out"
// answered with a diagnostic asserting the configuration has no live block
// over one that does.
func TestStatelessMode_aliasUsesTheLiveBlockSurface(t *testing.T) {
	t.Run("-destroy delegates and plans the same destroy", func(t *testing.T) {
		viaBlock := func() string {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			c := &PlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
			code := c.Run([]string{"-no-color", "-destroy"})
			out := done(t)
			if code != 0 {
				t.Fatalf("plan -destroy exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
			}
			assertNoStateArtifacts(t, td)
			return out.Stdout()
		}()

		viaCommand := func() string {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
			code := c.Run([]string{"-no-color", "-destroy"})
			out := done(t)
			if code != 0 {
				t.Fatalf("live-plan -destroy exit code %d, want 0 - the alias refused a flag the command it delegates to accepts\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
			}
			assertNoStateArtifacts(t, td)
			return out.Stdout()
		}()

		// The estate is two owned resources, so a destroy plan proposes
		// exactly two destroys. Asserted on the rendered summary rather than
		// inferred from the exit code, which is 0 either way.
		if !strings.Contains(viaBlock, "2 to destroy") {
			t.Fatalf("the fixture did not produce a destroy plan under plain plan, so this test proves nothing:\n%s", viaBlock)
		}
		if got, want := statelessPlanBody(viaCommand), statelessPlanBody(viaBlock); got != want {
			t.Errorf("live-plan -destroy and plan -destroy disagree over the same live block.\n--- plan -destroy ---\n%s\n--- live-plan -destroy ---\n%s", want, got)
		}
	})

	t.Run("-out does not claim the configuration has no live block", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		view, done := testView(t)
		c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
		code := c.Run([]string{"-no-color", "-out=tfplan"})
		out := done(t)
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
		}
		combined := unwrapped(out.Stderr() + "\n" + out.Stdout())
		if !strings.Contains(combined, savedPlanFileSummary) {
			t.Fatalf("-out was not refused at all:\n%s", combined)
		}
		if strings.Contains(combined, "this configuration has no live block") {
			t.Errorf("the refusal asserts the configuration has no live block, over one that does:\n%s", combined)
		}
	})
}

// TestStatelessRejections_oneList is the structural half: the refusal
// vocabulary lives in one function, and there is no second copy of it to
// drift from. It reads the source rather than the behavior, because the
// failure it guards against is a NEW function being introduced beside this
// one - which no behavioral test over the existing entry points can see.
func TestStatelessRejections_oneList(t *testing.T) {
	// Every diagnostic summary the single list can produce. If a second
	// implementation appears, it will almost certainly reuse this wording,
	// so this is the string to hunt for.
	summaries := []string{
		"Machine-readable output is not available under live resource markers yet",
		savedPlanFileSummary,
		"Config generation is not available under live resource markers yet",
		"Only the normal planning mode is available under live resource markers",
		"State file options are not available under live resource markers",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %s", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %s", name, err)
		}
		body := string(src)
		for _, summary := range summaries {
			if !strings.Contains(body, summary) {
				continue
			}
			if name != "live_mode.go" {
				t.Errorf("%s carries the refusal summary %q, which belongs to statelessRejections in live_mode.go alone. "+
					"GitHub issue #619: a second copy of this list is how the -destroy refusal came to be lifted from one surface and not the other.",
					name, summary)
			}
		}
	}
}

// Compile-time reminder that these tests are about a real signature: if
// statelessRejections stops taking a surface, this stops building and
// whoever removed it has to decide deliberately what happens to the two
// divergences above.
var _ = func(surface statelessSurface) tfdiags.Diagnostics {
	return statelessRejections(surface, nil, nil, arguments.ViewOptions{ViewType: arguments.ViewHuman}, "", "", "")
}
