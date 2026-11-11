// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport/inrunestates"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// RefusalSummary is the summary of every refusal this package raises.
// Tests match on it.
const RefusalSummary = "Not supported on the new runtime yet"

func refusal(r Row, what string, subject *hcl.Range) *hcl.Diagnostic {
	return &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  RefusalSummary,
		Detail: fmt.Sprintf("%s is set, and %s. The new runtime cannot handle %s yet, so it is refused rather than planned wrongly (gap %q in internal/live/newruntime/gaps.json). It waits for %s. Unset %s to use the default runtime.",
			Variable, what, r.Gap, r.ID, r.WaitsFor(), Variable),
		Subject: subject,
	}
}

// preflightDetectors maps a preflight row's construct to what finds it in
// one module of the configuration tree. Each returns a description and range
// for every occurrence. The node is the whole configs.Config for the module,
// because the live moved detector needs the module's children too; the rest
// read only c.Module.
var preflightDetectors = map[string]func(c *configs.Config) []occurrence{
	"data": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, r := range m.DataResources {
			// choudoufu fork port (#2370): remove when upstream #4058
			// lands. Gap "in-run-estates": what the port covers passes
			// while it is on, and is refused here when it is switched off.
			if inrunestates.Covers(r) {
				continue
			}
			out = append(out, occurrence{fmt.Sprintf("the configuration declares data source %s", r.Addr()), r.DeclRange})
		}
		return out
	},
	"removed": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, r := range m.Removed {
			out = append(out, occurrence{"the configuration has a removed block", r.DeclRange})
		}
		return out
	},
	"moved": func(c *configs.Config) (out []occurrence) {
		for _, r := range c.Module.Moved {
			out = append(out, occurrence{"the configuration has a moved block", r.DeclRange})
		}
		return out
	},
	// #2256 U10 (#2374): in live mode a moved block is resolved by marker
	// before the engine runs. internal/live/discovery binds the object that
	// still carries the old tofu-address to the new address, the projected
	// prior state holds it there, and the move finishes as the ordinary
	// in-place tag update; the engine never executes a move. That holds only
	// for the shapes moved.Honourable admits, which are the ones
	// internal/live/lint admits and discovery aliases. Every other block is
	// refused here, so live mode on the new runtime admits nothing more
	// than live mode on the default runtime does.
	"moved_unhonourable": func(c *configs.Config) (out []occurrence) {
		seen := map[hcl.Range]bool{}
		for _, stmt := range moved.StatementsIn(c.Module, c.Path) {
			seen[stmt.DeclRange] = true
			if reason, ok := moved.Honourable(c, stmt); !ok {
				out = append(out, occurrence{"the configuration has a moved block an ownership marker cannot follow: " + reason, stmt.DeclRange})
			}
		}
		// A block whose endpoints did not decode or unify has no Statement,
		// so discovery never aliases it. It is refused rather than skipped.
		for _, r := range c.Module.Moved {
			if !seen[r.DeclRange] {
				out = append(out, occurrence{"the configuration has a moved block whose endpoints could not be resolved to addresses", r.DeclRange})
			}
		}
		return out
	},
	"lifecycle_destroy": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, r := range m.ManagedResources {
			if r.Managed != nil && r.Managed.Destroy != nil {
				out = append(out, occurrence{fmt.Sprintf("%s sets lifecycle destroy", r.Addr()), r.Managed.Destroy.Range()})
			}
		}
		return out
	},
	"checks_conditions": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, c := range m.Checks {
			out = append(out, occurrence{fmt.Sprintf("the configuration has check block %q", c.Name), c.DeclRange})
		}
		for _, set := range []map[string]*configs.Resource{m.ManagedResources, m.DataResources, m.EphemeralResources} {
			for _, r := range set {
				for _, cr := range append(append([]*configs.CheckRule(nil), r.Preconditions...), r.Postconditions...) {
					out = append(out, occurrence{fmt.Sprintf("%s has a precondition or postcondition", r.Addr()), cr.DeclRange})
				}
			}
		}
		for _, o := range m.Outputs {
			for _, cr := range o.Preconditions {
				out = append(out, occurrence{fmt.Sprintf("output %q has a precondition", o.Name), cr.DeclRange})
			}
		}
		return out
	},
	"variable_validation": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, v := range m.Variables {
			for _, cr := range v.Validations {
				out = append(out, occurrence{fmt.Sprintf("variable %q has a validation block", v.Name), cr.DeclRange})
			}
		}
		return out
	},
	"sensitive": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, v := range m.Variables {
			if v.Sensitive {
				out = append(out, occurrence{fmt.Sprintf("variable %q is sensitive", v.Name), v.DeclRange})
			}
		}
		for _, o := range m.Outputs {
			if o.Sensitive {
				out = append(out, occurrence{fmt.Sprintf("output %q is sensitive", o.Name), o.DeclRange})
			}
		}
		return append(out, sensitiveCalls(m.SourceDir)...)
	},
}

type occurrence struct {
	what string
	rng  hcl.Range
}

// sensitiveCalls finds calls to the sensitive() function in a module's
// native-syntax files. The loaded configuration keeps expressions but not
// a list of them, so the files are read again; a module with no source
// directory (built in memory) has none to read.
func sensitiveCalls(dir string) []occurrence {
	if dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	more, _ := filepath.Glob(filepath.Join(dir, "*.tofu"))
	files = append(files, more...)
	sort.Strings(files)
	var out []occurrence
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		_ = hclsyntax.VisitAll(body, func(n hclsyntax.Node) hcl.Diagnostics {
			if call, ok := n.(*hclsyntax.FunctionCallExpr); ok && (call.Name == "sensitive" || strings.HasSuffix(call.Name, "::sensitive")) {
				out = append(out, occurrence{"the configuration calls sensitive()", call.NameRange})
			}
			return nil
		})
	}
	return out
}

// stateDetectors are preflight constructs found in the prior state. A
// construct may have both a state and a configuration detector; both run.
var stateDetectors = map[string]func(s *states.State) []string{
	// lifecycle { destroy = false } is also recorded on the object in state,
	// where it outlives the resource block: an orphaned object carrying it
	// must be forgotten, not deleted. The configuration detector cannot see
	// an orphan, so the state is read too.
	"lifecycle_destroy": func(s *states.State) (out []string) {
		if s == nil {
			return nil
		}
		for _, ms := range s.Modules {
			for _, rs := range ms.Resources {
				for key, is := range rs.Instances {
					if is.Current != nil && is.Current.SkipDestroy {
						out = append(out, fmt.Sprintf("the state records lifecycle destroy = false on %s", rs.Addr.Instance(key)))
					}
					for dk, obj := range is.Deposed {
						if obj != nil && obj.SkipDestroy {
							out = append(out, fmt.Sprintf("the state records lifecycle destroy = false on deposed object %s of %s", dk, rs.Addr.Instance(key)))
						}
					}
				}
			}
		}
		sort.Strings(out)
		return out
	},
	"deposed_state": func(s *states.State) (out []string) {
		if s == nil {
			return nil
		}
		for _, ms := range s.Modules {
			for _, rs := range ms.Resources {
				for key, is := range rs.Instances {
					for dk := range is.Deposed {
						out = append(out, fmt.Sprintf("the state holds deposed object %s of %s", dk, rs.Addr.Instance(key)))
					}
				}
			}
		}
		sort.Strings(out)
		return out
	},
}

// commandConstructs are preflight constructs that are a command rather
// than something in the configuration.
var commandConstructs = map[string]bool{"test_command": true}

// Preflight refuses, before planning, every construct the table's preflight
// rows name that the configuration or the prior state holds. It is run on
// plan with the toggle on, and on apply of a saved plan with the plan's
// configuration snapshot and prior state. live says whether the run is in
// live mode, which decides the scope of a row, as it does for PostPlan.
func Preflight(cfg *configs.Config, prior *states.State, live bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, r := range RowsFor(CheckPreflight) {
		if commandConstructs[r.Construct] {
			continue
		}
		if !r.InScope(live) {
			continue
		}
		stateDetect, hasState := stateDetectors[r.Construct]
		if hasState {
			for _, what := range stateDetect(prior) {
				diags = diags.Append(refusal(r, what, nil))
			}
		}
		detect, ok := preflightDetectors[r.Construct]
		if !ok {
			if hasState {
				continue
			}
			panic(fmt.Sprintf("gap %q: no preflight detector for construct %q", r.ID, r.Construct))
		}
		if cfg == nil {
			continue
		}
		cfg.DeepEach(func(c *configs.Config) {
			if c.Module == nil {
				return
			}
			occ := detect(c)
			sort.Slice(occ, func(i, j int) bool { return occ[i].rng.String() < occ[j].rng.String() })
			for _, o := range occ {
				rng := o.rng
				diags = diags.Append(refusal(r, o.what, &rng))
			}
		})
	}
	return diags
}

// RefuseCommand refuses a command one of the table's preflight rows names,
// such as "test". The test harness still evaluates on the default runtime.
func RefuseCommand(name string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	construct := name + "_command"
	for _, r := range RowsFor(CheckPreflight) {
		if r.Construct == construct {
			diags = diags.Append(refusal(r, fmt.Sprintf("the %q command was run", name), nil))
		}
	}
	return diags
}

// PostPlan refuses, before a plan is shown or saved, every change the
// table's post-plan rows name. live says whether the run is in live mode,
// which decides the scope of a row.
func PostPlan(plan *plans.Plan, live bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if plan == nil || plan.Changes == nil {
		return diags
	}
	changes := append([]*plans.ResourceInstanceChangeSrc(nil), plan.Changes.Resources...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Addr.String() < changes[j].Addr.String() })

	for _, construct := range []string{"replace"} {
		var applicable []Row
		for _, r := range RowsFor(CheckPostPlan) {
			if r.Construct != construct {
				continue
			}
			if !r.InScope(live) {
				continue
			}
			applicable = append(applicable, r)
		}
		if len(applicable) == 0 {
			continue
		}
		// The first applicable row in table order names the refusal.
		r := applicable[0]
		for _, ch := range changes {
			if ch.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode || ch.DeposedKey != states.NotDeposed {
				continue
			}
			switch construct {
			case "replace":
				if ch.Action.IsReplace() {
					diags = diags.Append(refusal(r, fmt.Sprintf("the plan replaces %s", ch.Addr), nil))
				}
			default:
				panic(fmt.Sprintf("gap %q: no post-plan detector for construct %q", r.ID, construct))
			}
		}
	}
	for _, r := range RowsFor(CheckPostPlan) {
		if r.Construct != "replace" {
			panic(fmt.Sprintf("gap %q: no post-plan detector for construct %q", r.ID, r.Construct))
		}
	}
	return diags
}

// DeferralRefusal is the engine row's diagnostic: the new runtime deferred
// an instance for a reason other than -target or -exclude. Plans have no
// record of deferral (a deferred instance simply has no planned change), so
// this is raised inside planning rather than by a post-plan check.
func DeferralRefusal(addr addrs.AbsResourceInstance) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, r := range RowsFor(CheckEngine) {
		if r.Construct == "deferral" {
			diags = diags.Append(refusal(r, fmt.Sprintf("planning deferred %s for a reason other than -target or -exclude", addr), nil))
		}
	}
	return diags
}
