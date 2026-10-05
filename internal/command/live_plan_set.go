// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/setplan"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// LivePlanSetCommand plans a set of estate roots in one invocation (GitHub
// issue #1752, part of epic #1749). The orchestration, and why it runs each
// root's stages as child processes rather than in this one, is
// internal/live/setplan's package documentation.
//
// It is its own command rather than a flag on live-plan because the two
// answer different questions with different contracts: live-plan's option
// set is the plan command's, every one of which applies to one root, and
// its -json is GitHub issue #788's bound/omissions/unowned document for
// that root. A set plan has one output directory instead of -out, a
// document of per-root stock plans instead of #788's, and an exit code that
// can say "some roots failed" - three places a flag on live-plan would have
// had to refuse or reinterpret live-plan's own options.
type LivePlanSetCommand struct {
	Meta
}

// LivePlanSetCommander is live-plan-set's entry in the new CLI's command
// tree.
func LivePlanSetCommander() Command {
	cmd := Command{
		Name:  "live-plan-set",
		Short: (&LivePlanSetCommand{}).Synopsis(),
	}
	args := arguments.BindLivePlanSet(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LivePlanSetCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LivePlanSetCommand{Meta: meta}).Execute(args)
	}
	return cmd
}

func (c *LivePlanSetCommand) Run(rawArgs []string) int {
	return RunCommand(LivePlanSetCommander(), c.Meta, rawArgs)
}

// livePlanSetBin is the executable each root's stages run as. A variable
// so a test in this package can point it at a built binary; os.Executable
// is the real answer, because a set plan's children must be this exact
// build.
var livePlanSetBin = os.Executable

func (c *LivePlanSetCommand) Execute(args *arguments.LivePlanSet) (exitCode int) {
	// GitHub issue #1898: the command's span. Each root's span, each
	// stage's span and each stage's child process nest under it.
	ctx, span := tracing.Tracer().Start(c.CommandContext(), TraceNameLivePlanSet,
		tracing.SpanAttributes(traceattrs.Int64(traceattrs.AttrRoots, int64(len(args.Roots)))),
	)
	defer func() {
		span.SetAttributes(traceattrs.Int64(traceattrs.AttrExitCode, int64(exitCode)))
		if exitCode == setplan.ExitError || exitCode == setplan.ExitRootFailed {
			tracing.SetSpanError(span, fmt.Sprintf("live-plan-set exited %d", exitCode))
		}
		span.End()
	}()
	var diags tfdiags.Diagnostics
	c.Meta.input = false

	if dir := os.Getenv(setplan.EnvDataDir); dir != "" {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"TF_DATA_DIR cannot be set for live-plan-set",
			fmt.Sprintf("TF_DATA_DIR=%s names one data directory for a whole process, and every root in a set needs its own: the roots would install their modules and providers over each other. Unset it; each root's data directory is the .terraform inside it.", dir),
		))
		c.View.Diagnostics(diags)
		return setplan.ExitError
	}

	bin, err := livePlanSetBin()
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot find this executable", fmt.Sprintf("live-plan-set runs each root's stages as this same binary and could not find it: %s.", err)))
		c.View.Diagnostics(diags)
		return setplan.ExitError
	}

	base, err := filepath.Abs(".")
	if err != nil {
		diags = diags.Append(err)
		c.View.Diagnostics(diags)
		return setplan.ExitError
	}
	outDir := args.OutDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(base, outDir)
	}

	// The one provider cache every root shares. The CLI configuration's
	// plugin_cache_dir (or TF_PLUGIN_CACHE_DIR, which overrides it) wins,
	// so a CI runner's persistent cache keeps serving every root; without
	// one, the set gets its own beside its plan files, which still makes
	// the install once per invocation rather than once per root.
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
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot create the provider cache", fmt.Sprintf("live-plan-set shares one provider cache across every root and could not create %s: %s.", cacheDir, err)))
		c.View.Diagnostics(diags)
		return setplan.ExitError
	}

	runner := setplan.Exec{
		Bin:      bin,
		Env:      setplan.ChildEnv(os.Environ(), cacheDir),
		EstateOf: liveBlockEstate,
	}
	doc, err := setplan.Run(ctx, setplan.Options{
		Roots:          args.Roots,
		BaseDir:        base,
		OutDir:         outDir,
		Parallel:       args.ParallelEstates,
		PluginCacheDir: cacheDir,
		Runner:         runner,
	})
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot plan this set", err.Error()))
		c.View.Diagnostics(diags)
		return setplan.ExitError
	}
	views.NewLivePlanSet(args.View, c.View).Report(doc)
	return doc.ExitCode
}

// TraceNameLivePlanSet is the live-plan-set command's span name (#1898).
const TraceNameLivePlanSet = "live-plan-set"

// liveBlockEstate reads dir's live block, from its .tf files or its
// estate.chdf.hcl sidecar, the way liveSettings does for the working
// directory, and returns the estate it names.
func liveBlockEstate(dir string) (string, bool, error) {
	mod, diags := configs.NewParser(nil).LoadConfigDirSelective(dir, configs.SelectiveLoadBackend)
	if diags.HasErrors() {
		var msgs []string
		for _, err := range diags.Errs() {
			msgs = append(msgs, err.Error())
		}
		return "", false, fmt.Errorf("the configuration does not load: %s", strings.Join(msgs, "; "))
	}
	if mod == nil || mod.Live == nil {
		return "", false, nil
	}
	return mod.Live.Estate, true, nil
}

func (c *LivePlanSetCommand) Synopsis() string {
	return "Plan a set of estate roots in one invocation"
}

func (c *LivePlanSetCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-plan-set [options] ROOT...

  Plans every ROOT, a directory whose configuration carries a live block,
  and reports the whole set at once. Each root gets what "choudoufu init"
  then "choudoufu plan -out" would give it on its own, run as this same
  binary with -chdir=ROOT, at most -parallel-estates at a time. Every root
  shares one provider cache, so a provider release is installed once per
  invocation however many roots need it.

  A root that fails at any stage is reported with its error and never stops
  the others.

  Each root's plan file is OUT/ROOT.tfplan and its log OUT/ROOT.log, where
  ROOT is its path from the working directory: estates/e01 plans to
  choudoufu-plan-set/estates/e01.tfplan. Every ROOT must be inside the
  working directory, and none may be given twice. Apply one with
  "choudoufu -chdir=ROOT apply PLANFILE".

Options:

  -parallel-estates=n  How many roots run at once. Default 4.

  -out-dir=path        Where plan files and logs go. Default
                       choudoufu-plan-set. Unless the CLI configuration's
                       plugin_cache_dir or TF_PLUGIN_CACHE_DIR names one,
                       the shared provider cache is OUT/plugin-cache.

  -json                Print one JSON document instead of the summary:

                         {
                           "format_version": "1",
                           "roots": [
                             {
                               "root":        "estates/e01",
                               "estate":      "e01",
                               "status":      "planned" | "failed",
                               "error":       "",
                               "stage":       "estate" | "init" | "plan" | "show",
                               "changes":     true,
                               "plan_file":   "choudoufu-plan-set/estates/e01.tfplan",
                               "log_file":    "choudoufu-plan-set/estates/e01.log",
                               "duration_ms": 1234,
                               "plan":        { ...what "show -json PLANFILE" prints... },
                               "digest":      "sha256:..."
                             }
                           ],
                           "summary": {"roots": 1, "planned": 1, "changed": 1, "failed": 0},
                           "digest": "sha256:...",
                           "exit_code": 2,
                           "plugin_cache_dir": "/abs/path",
                           "parallel_estates": 4
                         }

                       "digest" is the set digest: SHA-256 over every
                       root's planned changes, the same whatever order the
                       roots were given in, and different when any one
                       root's changes differ. It is what an approval names
                       and what live-wave-apply checks; the summary prints
                       it too. Each root's "digest" is its share.

                       Roots keep the order they were given in. "error" is
                       set and "stage" names where when "status" is
                       "failed", and "plan" is null then. Fields are only
                       ever added; format_version moves if one is renamed
                       or dropped.

  -no-color            Disables color in this command's own diagnostics.
                       Every root's stages run without color regardless,
                       since their output goes to a log.

Exit status:

  0  every root planned and none has changes
  2  every root planned and at least one has changes (plan
     -detailed-exitcode's meaning, for the set)
  4  at least one root failed; the rest are still planned and reported
  1  the command could not run at all; no root was planned

  3 is left out on purpose: "choudoufu apply PLANFILE" exits 3 when the
  live system moved under an approved plan.

Environment:

  TF_DATA_DIR must not be set: it names one data directory for the whole
  process, and every root needs its own. The children are given
  TF_PLUGIN_CACHE_DIR and TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1,
  the second so that a root with no dependency lock file links the shared
  cache's copy instead of downloading another; such a root's new lock file
  records only this platform's checksum.
`)
}
