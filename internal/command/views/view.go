// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"maps"

	"github.com/hashicorp/hcl/v2"
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/format"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/mitchellh/colorstring"
)

// View is the base layer for command views, encapsulating a set of I/O
// streams, a colorize implementation, and implementing a human friendly view
// for diagnostics.
type View struct {
	streams  *terminal.Streams
	colorize *colorstring.Colorize

	compactWarnings     bool
	consolidateWarnings bool
	consolidateErrors   bool

	// When this is true it's a hint that OpenTofu is being run indirectly
	// via a wrapper script or other automation and so we may wish to replace
	// direct examples of commands to run with more conceptual directions.
	// However, we only do this on a best-effort basis, typically prioritizing
	// the messages that users are most likely to see.
	runningInAutomation bool

	// Concise is used to reduce the level of noise in the output and display
	// only the important details.
	concise bool

	// verbose is Concise's opposite: a command that summarizes something by
	// default may print the full detail instead when this is set. Unlike
	// Concise it is not parsed by [arguments.ParseView] - "-verbose" already
	// names an unrelated per-command flag on "choudoufu test" and "choudoufu
	// graph" (arguments/test.go, arguments/graph.go), each on its own flag
	// set, and ParseView's early pass runs ahead of every command's own flag
	// set and would swallow the flag before either one saw it. It is set via
	// [View.SetVerbose] instead, the same way [View.SetShowSensitive] is,
	// from -verbose on "choudoufu plan"'s and "choudoufu apply"'s own flag
	// sets (arguments.Plan.Verbose, arguments.Apply.Verbose) - which
	// "choudoufu live-plan" inherits by embedding Plan, and a plain
	// "choudoufu plan"/"apply" against a live block
	// (internal/command/live_mode.go's alias) inherits by being the same
	// command.
	verbose bool

	// ModuleDeprecationWarnLvl is used to filter out deprecation warnings for outputs and variables as requested by the user.
	ModuleDeprecationWarnLvl arguments.DeprecationWarningLevel

	// showSensitive is used to display the value of variables marked as sensitive.
	showSensitive bool

	// Because some commands used before the UI to print diagnostics, those were printed using an [*ln] function, so
	// we want to be able to configure this for some of the commands to be able to keep the behavior consistent.
	diagsPrinter func(severity tfdiags.Severity, msg string)

	// This unfortunate wart is required to enable rendering of diagnostics which
	// have associated source code in the configuration. This function pointer
	// will be dereferenced as late as possible when rendering diagnostics in
	// order to access the config loader cache.
	configSources func() map[string]*hcl.File

	// These other unfortunate warts are required to enable correct deduplication
	// and filtering of deprecation diagnostics
	isRemoteModuleSource func(addrs.Module) bool
	moduleSourceAddrs    func(addrs.Module) addrs.ModuleSource
}

// Initialize a View with the given streams, a disabled colorize object, and a
// no-op configSources callback.
func NewView(streams *terminal.Streams) *View {
	return &View{
		streams: streams,
		colorize: &colorstring.Colorize{
			Colors:  colorstring.DefaultColors,
			Disable: true,
			Reset:   true,
		},
		configSources:        func() map[string]*hcl.File { return nil },
		isRemoteModuleSource: func(addrs.Module) bool { return false },
		moduleSourceAddrs:    func(addrs.Module) addrs.ModuleSource { return nil },
		diagsPrinter: func(severity tfdiags.Severity, msg string) {
			if severity == tfdiags.Error {
				_, _ = streams.Eprint(msg)
			} else {
				_, _ = streams.Print(msg)
			}
		},
	}
}

// SetRunningInAutomation modifies the view's "running in automation" flag,
// which causes some slight adjustments to certain messages that would normally
// suggest specific OpenTofu commands to run, to make more conceptual gestures
// instead for situations where the user isn't running OpenTofu directly.
//
// For convenient use during initialization (in conjunction with NewView),
// SetRunningInAutomation returns the receiver after modifying it.
func (v *View) SetRunningInAutomation(new bool) *View {
	v.runningInAutomation = new
	return v
}

func (v *View) RunningInAutomation() bool {
	return v.runningInAutomation
}

// Configure applies the global view configuration flags.
func (v *View) Configure(view *arguments.View) {
	colors := maps.Clone(colorstring.DefaultColors)
	colors["purple"] = "38;5;57" // Add also purple to the colorise colors set

	v.colorize.Disable = view.NoColor
	v.colorize.Colors = colors
	v.compactWarnings = view.CompactWarnings
	v.consolidateWarnings = view.ConsolidateWarnings
	v.consolidateErrors = view.ConsolidateErrors
	v.concise = view.Concise
	v.ModuleDeprecationWarnLvl = view.ModuleDeprecationWarnLvl
}

func (v *View) DiagsWithNewline() {
	v.diagsPrinter = func(severity tfdiags.Severity, msg string) {
		if severity == tfdiags.Error {
			_, _ = v.streams.Eprintln(msg)
		} else {
			_, _ = v.streams.Println(msg)
		}
	}
}

// SetConfigSources overrides the default no-op callback with a new function
// pointer, and should be called when the config loader is initialized.
func (v *View) SetConfigSources(cb func() map[string]*hcl.File) {
	v.configSources = cb
}

func (v *View) SetIsRemoteModuleSource(cb func(addrs.Module) bool) {
	v.isRemoteModuleSource = cb
}

func (v *View) SetModuleSourceAddrs(cb func(addrs.Module) addrs.ModuleSource) {
	v.moduleSourceAddrs = cb
}

// Diagnostics renders a set of warnings and errors in human-readable form.
// Warnings are printed to stdout, and errors to stderr.
func (v *View) Diagnostics(diags tfdiags.Diagnostics) {
	v.diagnostics(diags, false)
}

// StdoutOnStderr returns a copy of this view whose Stdout stream IS its
// Stderr stream, so that everything built over the copy - a plan view's
// resource diff, the per-instance lines [UiHook] prints while the plan
// graph walks, warning diagnostics - lands on Stderr instead of
// interleaving with a machine-readable document another view is printing
// to the real Stdout.
//
// GitHub issue #894's second half. "choudoufu live-plan -json" prints one
// [LivePlanDocument] and nothing else, but the plan it runs still drives
// [UiHook], whose lines go through streams.Stdout unconditionally
// (hook_ui.go's println). A configuration with a data source therefore
// printed
//
//	data.aws_caller_identity.current: Reading...
//	data.aws_caller_identity.current: Read complete after 0s [id=...]
//
// ahead of the document, and a consumer piping stdout straight into a JSON
// parser got "jq: parse error: Invalid numeric literal at line 1, column
// 33" (reproduced against a live emulator, 2026-09-06). Redirecting the
// stream the hooks were built over fixes that at the one place both halves
// already agree on - the view - rather than teaching every hook a second
// stream it would have to be told about again next time one is added.
//
// The copy shares Stdin and every other setting; only the streams change.
// [View.DiagnosticsToStderr] is the narrower tool for the same problem
// where diagnostics alone are at stake.
func (v *View) StdoutOnStderr() *View {
	if v == nil || v.streams == nil {
		return v
	}
	copied := *v
	streams := &terminal.Streams{
		Stdout: v.streams.Stderr,
		Stderr: v.streams.Stderr,
		Stdin:  v.streams.Stdin,
	}
	copied.streams = streams

	// diagsPrinter closes over the streams it was built with (see
	// [NewView]), so carrying the original one across would send warnings
	// straight back to the real Stdout - the one thing this copy exists to
	// keep clear. Rebuilt over the redirected streams; the severity split
	// it encodes is moot once both halves are the same stream.
	if v.diagsPrinter != nil {
		copied.diagsPrinter = func(_ tfdiags.Severity, msg string) {
			_, _ = streams.Eprint(msg)
		}
	}

	// The three callbacks below are installed LAZILY on the original view -
	// Meta.configLoader sets all three the first time a command reads the
	// configuration, which is normally after this copy has been made - so
	// they are forwarded rather than snapshotted. Snapshotting them made
	// every source-ranged diagnostic rendered through the copy print
	// "(source code not available)" in place of its snippet, observed on
	// live-plan's own "Estate named by both the live block and -estate"
	// refusal.
	copied.configSources = func() map[string]*hcl.File {
		if v.configSources == nil {
			return nil
		}
		return v.configSources()
	}
	copied.isRemoteModuleSource = func(m addrs.Module) bool {
		if v.isRemoteModuleSource == nil {
			return false
		}
		return v.isRemoteModuleSource(m)
	}
	copied.moduleSourceAddrs = func(m addrs.Module) addrs.ModuleSource {
		if v.moduleSourceAddrs == nil {
			return nil
		}
		return v.moduleSourceAddrs(m)
	}
	return &copied
}

// DiagnosticsToStderr is [View.Diagnostics] with one difference: every
// diagnostic goes to Stderr, warnings included, rather than splitting
// warnings onto Stdout. It exists for a command whose successful Stdout is
// a single machine-parsed document - choudoufu live-mv -json (GitHub issue
// #791) is the first caller - where a warning landing on Stdout by
// Diagnostics' ordinary rule would interleave human prose into that
// document. Formatting is identical; only the stream changes, and unlike
// Diagnostics this ignores whatever [View.DiagsWithNewline] configured,
// because that configuration is itself a per-severity stream split of the
// same kind this method exists to turn off.
func (v *View) DiagnosticsToStderr(diags tfdiags.Diagnostics) {
	v.diagnostics(diags, true)
}

// diagnostics is [View.Diagnostics]' real body, shared with
// [View.DiagnosticsToStderr]: forceStderr false reproduces Diagnostics'
// long-standing behavior exactly (including compactWarnings' early return
// and diagsPrinter, both of which stay severity-split even under
// forceStderr's caller - a command asking for everything on Stderr has
// already committed to that split not mattering to it); forceStderr true
// sends every diagnostic to Stderr regardless of severity or diagsPrinter.
func (v *View) diagnostics(diags tfdiags.Diagnostics, forceStderr bool) {
	diags.Sort()

	if len(diags) == 0 {
		return
	}

	// Filter the deprecation warnings based on the cli arg.
	var newDiags tfdiags.Diagnostics
	seen := DeprecationDiagnosticAllowedSeen{}
	for _, diag := range diags {
		if !v.DeprecationDiagnosticAllowed(diag, seen) {
			continue
		}
		newDiags = append(newDiags, diag)
	}
	diags = newDiags

	if v.consolidateWarnings {
		diags = diags.Consolidate(1, tfdiags.Warning, func(diag tfdiags.Diagnostic) string {
			// Check to see if we have a DeprecationCause
			depExtra := v.DeprecationKeyExtra(diag)
			if depExtra != "" {
				return depExtra
			}
			return tfdiags.DefaultDiagnosticsConsolidation(diag)
		})
	}
	if v.consolidateErrors {
		diags = diags.Consolidate(1, tfdiags.Error, tfdiags.DefaultDiagnosticsConsolidation)
	}

	// Since warning messages are generally competing
	if v.compactWarnings && !forceStderr {
		// If the user selected compact warnings and all of the diagnostics are
		// warnings then we'll use a more compact representation of the warnings
		// that only includes their summaries.
		// We show full warnings if there are also errors, because a warning
		// can sometimes serve as good context for a subsequent error.
		useCompact := true
		for _, diag := range diags {
			if diag.Severity() != tfdiags.Warning {
				useCompact = false
				break
			}
		}
		if useCompact {
			msg := format.DiagnosticWarningsCompact(diags, v.colorize)
			msg = "\n" + msg + "\nTo see the full warning notes, run OpenTofu without -compact-warnings.\n"
			v.streams.Print(msg)
			return
		}
	}

	for _, diag := range diags {
		var msg string
		if v.colorize.Disable {
			msg = format.DiagnosticPlain(diag, v.configSources(), v.streams.Stderr.Columns())
		} else {
			msg = format.Diagnostic(diag, v.configSources(), v.colorize, v.streams.Stderr.Columns())
		}

		if forceStderr {
			v.streams.Eprint(msg)
			continue
		}

		// TODO meta-refactor: once we are done with migrating all the commands to views, we should get rid
		// of the check and just allow the diagsPrinter to be called directly.
		if v.diagsPrinter != nil {
			v.diagsPrinter(diag.Severity(), msg)
			continue
		}
		if diag.Severity() == tfdiags.Error {
			v.streams.Eprint(msg)
		} else {
			v.streams.Print(msg)
		}
	}
}

// HelpPrompt is intended to be called from commands which fail to parse all
// of their CLI arguments successfully. It refers users to the full help output
// rather than rendering it directly, which can be overwhelming and confusing.
func (v *View) HelpPrompt(command string) {
	v.streams.Eprintf(helpPrompt, command)
}

const helpPrompt = `
For more help on using this command, run:
  choudoufu %s -help
`

// outputColumns returns the number of text character cells any non-error
// output should be wrapped to.
//
// This is the number of columns to use if you are calling v.streams.Print or
// related functions.
func (v *View) outputColumns() int {
	return v.streams.Stdout.Columns()
}

// errorColumns returns the number of text character cells any error
// output should be wrapped to.
//
// This is the number of columns to use if you are calling v.streams.Eprint
// or related functions.
func (v *View) errorColumns() int {
	return v.streams.Stderr.Columns()
}

// outputHorizRule will call v.streams.Println with enough horizontal line
// characters to fill an entire row of output.
//
// If UI color is enabled, the rule will get a dark grey coloring to try to
// visually de-emphasize it.
func (v *View) outputHorizRule() {
	v.streams.Println(format.HorizontalRule(v.colorize, v.outputColumns()))
}

func (v *View) SetShowSensitive(showSensitive bool) {
	v.showSensitive = showSensitive
}

// SetVerbose sets the view's verbose flag. See the verbose field's own
// comment for why this is a setter called from a command's own -verbose
// flag rather than something [View.Configure] reads off [arguments.View].
func (v *View) SetVerbose(verbose bool) {
	v.verbose = verbose
}

// Colorize returns the [colorstring.Colorize] object within to be used in other places.
// TODO meta-refactor: this is a temporary solution. This should not be exposed. Whoever needs to use this
//
//	should do it through a View implementation instead.
func (v *View) Colorize() *colorstring.Colorize {
	return v.colorize
}

// StdinPiped returns true if the input is piped.
func (v *View) StdinPiped() bool {
	return !v.streams.Stdin.IsTerminal()
}
