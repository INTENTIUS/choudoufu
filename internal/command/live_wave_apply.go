// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/setplan"
	"github.com/intentius/choudoufu/internal/live/waves"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// LiveWaveApplyCommand applies one wave of an approved set (GitHub issue
// #1754, part of #1749). The approval itself is not stored here: the
// caller names the set plan document and the digest that was approved, and
// this command refuses with exit 3 unless every root of the wave still
// plans to exactly that. The rules are internal/live/waves' Apply.
type LiveWaveApplyCommand struct {
	Meta
}

// LiveWaveApplyCommander is live-wave-apply's entry in the new CLI's
// command tree. See [LiveCommanders].
func LiveWaveApplyCommander() Command {
	cmd := Command{
		Name:  "live-wave-apply",
		Short: (&LiveWaveApplyCommand{}).Synopsis(),
	}
	args := arguments.BindLiveWaveApply(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveWaveApplyCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveWaveApplyCommand{Meta: meta}).Execute(args)
	}
	return cmd
}

func (c *LiveWaveApplyCommand) Run(rawArgs []string) int {
	return RunCommand(LiveWaveApplyCommander(), c.Meta, rawArgs)
}

// liveWaveApplyRunner is the runner Execute uses; a test replaces it.
var liveWaveApplyRunner = func(c *LiveWaveApplyCommand, base, outDir string, parallel int) (waves.ApplyRunner, error) {
	bin, err := livePlanSetBin()
	if err != nil {
		return nil, fmt.Errorf("live-wave-apply runs each root as this same binary and could not find it: %s", err)
	}
	cacheDir := c.SystemCfg.PluginCacheDir
	if cacheDir == "" {
		cacheDir = os.Getenv(setplan.EnvPluginCacheDir)
	}
	if cacheDir == "" {
		cacheDir = filepath.Join(outDir, "plugin-cache")
	}
	if !filepath.IsAbs(cacheDir) {
		cacheDir = filepath.Join(base, cacheDir)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	return &execWaveRunner{
		exec:     setplan.Exec{Bin: bin, Env: setplan.ChildEnv(os.Environ(), cacheDir), EstateOf: liveBlockEstate},
		base:     base,
		outDir:   outDir,
		parallel: parallel,
		cacheDir: cacheDir,
	}, nil
}

// execWaveRunner is the real [waves.ApplyRunner]: a set plan of the roots
// left in the wave, then "apply PLANFILE" in each root, every stage a child
// process of this binary with -chdir=ROOT.
type execWaveRunner struct {
	exec     setplan.Exec
	base     string
	outDir   string
	parallel int
	cacheDir string
}

func (r *execWaveRunner) PlanRoots(ctx context.Context, roots []string) (map[string]waves.FreshPlan, error) {
	doc, err := setplan.Run(ctx, setplan.Options{
		Roots:          roots,
		BaseDir:        r.base,
		OutDir:         r.outDir,
		Parallel:       r.parallel,
		PluginCacheDir: r.cacheDir,
		Runner:         r.exec,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]waves.FreshPlan, len(doc.Roots))
	for _, root := range doc.Roots {
		planFile := root.PlanFile
		if planFile != "" && !filepath.IsAbs(planFile) {
			planFile = filepath.Join(r.base, filepath.FromSlash(planFile))
		}
		out[root.Root] = waves.FreshPlan{
			RootPlan: waves.RootPlan{Root: root.Root, Estate: root.Estate, Status: string(root.Status), Error: root.Error, Plan: root.Plan},
			PlanFile: planFile,
		}
	}
	return out, nil
}

func (r *execWaveRunner) Apply(ctx context.Context, root, planFile string) error {
	logPath := filepath.Join(r.outDir, filepath.FromSlash(setplan.PlanName(root))+".apply.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	dir := filepath.Join(r.base, filepath.FromSlash(root))
	if err := r.exec.Apply(ctx, dir, planFile, log); err != nil {
		rel, _ := filepath.Rel(r.base, logPath)
		if se, ok := err.(*setplan.StageError); ok && se.Code == ExitApprovalRefused {
			return fmt.Errorf("its plan moved between the wave's check and its apply (apply exited %d; log %s), as a producer landing earlier in this wave can make it: %s", se.Code, rel, firstLineOf(se.Stderr))
		}
		return fmt.Errorf("log %s: %s", rel, firstLineOf(err.Error()))
	}
	return nil
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	for _, line := range strings.Split(s, "\n") {
		// Skip the box-drawing lines a diagnostic is framed in.
		line = strings.TrimSpace(strings.TrimLeft(line, "╷│╵ "))
		if line != "" {
			return line
		}
	}
	return s
}

// TraceNameLiveWaveApply is the live-wave-apply command's span name (#1898).
const TraceNameLiveWaveApply = "live-wave-apply"

func (c *LiveWaveApplyCommand) Execute(args *arguments.LiveWaveApply) (exitCode int) {
	var diags tfdiags.Diagnostics
	fail := func(summary, detail string) int {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
		c.View.Diagnostics(diags)
		return waves.ExitError
	}
	if dir := os.Getenv(setplan.EnvDataDir); dir != "" {
		return fail("TF_DATA_DIR cannot be set for live-wave-apply", fmt.Sprintf("TF_DATA_DIR=%s names one data directory for a whole process, and every root of a wave needs its own.", dir))
	}
	ctx := c.CommandContext()
	if ctx == nil {
		ctx = context.Background()
	}
	// GitHub issue #1898: the command's span. The wave's gates (set digest,
	// resume check, fresh plan) and each root's apply are its children, so
	// a refusal shows at the gate that refused.
	ctx, span := tracing.Tracer().Start(ctx, TraceNameLiveWaveApply,
		tracing.SpanAttributes(traceattrs.Int64(traceattrs.AttrWave, int64(args.Wave))),
	)
	defer func() {
		span.SetAttributes(traceattrs.Int64(traceattrs.AttrExitCode, int64(exitCode)))
		if exitCode != waves.ExitApplied {
			tracing.SetSpanError(span, fmt.Sprintf("live-wave-apply exited %d", exitCode))
		}
		span.End()
	}()
	base, err := filepath.Abs(".")
	if err != nil {
		return fail("Cannot read the working directory", err.Error())
	}

	data, err := os.ReadFile(args.PlanSet)
	if err != nil {
		return fail("Cannot read the set plan document", err.Error())
	}
	set, err := waves.ParseSetDocument(data)
	if err != nil {
		return fail("Not a set plan document", fmt.Sprintf("%s: %s. -plan-set reads the document live-plan-set -json prints.", args.PlanSet, err))
	}
	doc, err := waves.Build(ctx, waves.Options{BaseDir: base, Canaries: args.Canaries, Set: set})
	if err != nil {
		return fail("Cannot split this set into waves", err.Error())
	}
	resume, err := waves.ReadResume(args.Resume)
	if err != nil {
		return fail("Cannot read the resume file", err.Error())
	}

	outDir := args.OutDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(base, outDir)
	}
	outDir = filepath.Join(outDir, fmt.Sprintf("wave-%d", args.Wave))
	runner, err := liveWaveApplyRunner(c, base, outDir, args.ParallelEstates)
	if err != nil {
		return fail("Cannot apply this wave", err.Error())
	}

	res, err := waves.Apply(ctx, waves.ApplyOptions{
		Set:      set,
		Approved: args.Digest,
		Waves:    doc,
		Wave:     args.Wave,
		Resume:   resume,
		Save:     func(r *waves.Resume) error { return waves.WriteResume(args.Resume, r) },
		Runner:   runner,
	})
	if err != nil {
		return fail("Cannot record this wave", err.Error())
	}
	out := res.Text()
	if args.JSON {
		if out, err = res.JSON(); err != nil {
			return fail("Cannot render the result", err.Error())
		}
	}
	views.NewLiveWaves(c.View).Output(out)
	if res.ExitCode == waves.ExitSetMoved || res.ExitCode == waves.ExitError {
		summary := "The approved set no longer matches"
		if res.ExitCode == waves.ExitError {
			summary = "Cannot apply this wave"
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, res.Error))
		c.View.Diagnostics(diags)
	}
	return res.ExitCode
}

func (c *LiveWaveApplyCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-wave-apply [options]

  Applies one wave of an approved set of estate roots. The set is the
  document "live-plan-set -json" printed, and the approval is its set
  digest: where an approval is kept is the caller's business, and this
  command keeps none.

  The waves are live-waves' over the document's roots, with the same
  -canary. For the wave named:

    - The document must hash to -digest, or exit 3.
    - Every root of the wave not already landed is planned again, and
      unless each fresh plan matches its approved plan exactly, nothing in
      the wave is applied and the command exits 3 naming the roots that
      moved. Plan the set again and approve the new digest.
    - Roots are applied with "apply PLANFILE" in an order that puts every
      producer first. A root that fails, or whose fresh plan fails, skips
      every root reading its estate, in this wave and later ones; roots
      that read nothing that failed still apply.
    - Every outcome (landed, failed, skipped, with the reason) is written
      to -resume after each root. A re-run with the same file plans and
      applies only what has not landed, and a root in a later wave whose
      producer has not landed is skipped.

Options:

  -plan-set=FILE        The approved set plan document.
  -digest=sha256:...    The approved set digest.
  -wave=n               The wave to apply, from 1.
  -resume=FILE          The resume file; created when absent. A file
                        written for another digest is refused.
  -canary=ROOT          As live-waves: the canaries, by directory or
                        estate, that form wave 1. Repeatable.
  -out-dir=path         Fresh plans and logs, under wave-n. Default
                        choudoufu-wave-apply.
  -parallel-estates=n   How many roots plan at once. Default 4. Roots
                        apply one at a time.
  -json                 Print the result as one JSON document.

Exit status:

  0  every root of the wave has landed
  3  the set moved: nothing in the wave was applied
  4  at least one root of the wave failed or was skipped; see -resume
  1  the command could not run
`)
}

func (c *LiveWaveApplyCommand) Synopsis() string {
	return "Apply one wave of an approved set of estate roots"
}
