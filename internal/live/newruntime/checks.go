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
	"github.com/intentius/choudoufu/internal/engine/forkport"
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
			o := occurrence{what: fmt.Sprintf("the configuration declares data source %s", r.Addr()), rng: r.DeclRange}
			// choudoufu fork port (#2370): remove when upstream #4058
			// lands. Gap "in-run-estates".
			if inrunestates.Covers(r) {
				o.port = inrunestates.Name
			}
			out = append(out, o)
		}
		return out
	},
	"removed": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, r := range m.Removed {
			out = append(out, occurrence{what: "the configuration has a removed block", rng: r.DeclRange})
		}
		return out
	},
	"moved": func(c *configs.Config) (out []occurrence) {
		for _, r := range c.Module.Moved {
			out = append(out, occurrence{what: "the configuration has a moved block", rng: r.DeclRange})
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
				out = append(out, occurrence{what: "the configuration has a moved block an ownership marker cannot follow: " + reason, rng: stmt.DeclRange})
			}
		}
		// A block whose endpoints did not decode or unify has no Statement,
		// so discovery never aliases it. It is refused rather than skipped.
		for _, r := range c.Module.Moved {
			if !seen[r.DeclRange] {
				out = append(out, occurrence{what: "the configuration has a moved block whose endpoints could not be resolved to addresses", rng: r.DeclRange})
			}
		}
		return out
	},
	"lifecycle_destroy": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, r := range m.ManagedResources {
			if r.Managed != nil && r.Managed.Destroy != nil {
				out = append(out, occurrence{what: fmt.Sprintf("%s sets lifecycle destroy", r.Addr()), rng: r.Managed.Destroy.Range()})
			}
		}
		return out
	},
	"checks_conditions": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, c := range m.Checks {
			out = append(out, occurrence{what: fmt.Sprintf("the configuration has check block %q", c.Name), rng: c.DeclRange})
		}
		for _, set := range []map[string]*configs.Resource{m.ManagedResources, m.DataResources, m.EphemeralResources} {
			for _, r := range set {
				for _, cr := range append(append([]*configs.CheckRule(nil), r.Preconditions...), r.Postconditions...) {
					out = append(out, occurrence{what: fmt.Sprintf("%s has a precondition or postcondition", r.Addr()), rng: cr.DeclRange})
				}
			}
		}
		for _, o := range m.Outputs {
			for _, cr := range o.Preconditions {
				out = append(out, occurrence{what: fmt.Sprintf("output %q has a precondition", o.Name), rng: cr.DeclRange})
			}
		}
		return out
	},
	"variable_validation": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, v := range m.Variables {
			for _, cr := range v.Validations {
				out = append(out, occurrence{what: fmt.Sprintf("variable %q has a validation block", v.Name), rng: cr.DeclRange})
			}
		}
		return out
	},
	"sensitive": func(c *configs.Config) (out []occurrence) {
		m := c.Module
		for _, v := range m.Variables {
			if v.Sensitive {
				out = append(out, occurrence{what: fmt.Sprintf("variable %q is sensitive", v.Name), rng: v.DeclRange})
			}
		}
		for _, o := range m.Outputs {
			if o.Sensitive {
				out = append(out, occurrence{what: fmt.Sprintf("output %q is sensitive", o.Name), rng: o.DeclRange})
			}
		}
		return append(out, sensitiveCalls(m.SourceDir)...)
	},
}

type occurrence struct {
	what string
	rng  hcl.Range
	// port, when set, names the registered fork port (internal/engine/forkport)
	// that handles this occurrence on the new runtime. Preflight admits it
	// while forkport.Enabled(port), and refuses it under the detector's row
	// when the port is switched off. A port only has to tag what it covers.
	port string
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
				out = append(out, occurrence{what: "the configuration calls sensitive()", rng: call.NameRange})
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
				if o.port != "" && forkport.Enabled(o.port) {
					continue
				}
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
		switch r.Construct {
		case "replace":
		case "targeted_destroy":
			if r.InScope(live) {
				for _, what := range overDestroys(plan, changes) {
					diags = diags.Append(refusal(r, what, nil))
				}
			}
		default:
			panic(fmt.Sprintf("gap %q: no post-plan detector for construct %q", r.ID, r.Construct))
		}
	}
	return diags
}

// overDestroys is the destroy-target row's detector (#2387). On the new
// runtime, destroy -target X also deletes what X depends on. The default
// runtime deletes the targets and, transitively, whatever the prior state
// records as depending on them. Every delete of a managed object outside
// that set is reported. Dependents are read from the state's recorded
// dependencies, so an object whose dependencies were not recorded is not
// counted as a dependent and its delete is refused rather than admitted.
func overDestroys(plan *plans.Plan, changes []*plans.ResourceInstanceChangeSrc) []string {
	if plan.UIMode != plans.DestroyMode || len(plan.TargetAddrs) == 0 {
		return nil
	}
	targeted := func(addr addrs.AbsResourceInstance) bool {
		for _, t := range plan.TargetAddrs {
			if t.TargetContains(addr) {
				return true
			}
		}
		return false
	}
	// wanted holds the config resources of everything the destroy may
	// delete, grown to a fixpoint over the state's recorded dependencies.
	wanted := map[string]bool{}
	var objs []*states.ResourceInstance
	var addrsOf []addrs.AbsResourceInstance
	// Both states are read: the refreshed prior state may hold objects
	// without the dependencies the previous run recorded.
	for _, prior := range []*states.State{plan.PrevRunState, plan.PriorState} {
		if prior == nil {
			continue
		}
		for _, ms := range prior.Modules {
			for _, rs := range ms.Resources {
				for key, is := range rs.Instances {
					addr := rs.Addr.Instance(key)
					if targeted(addr) {
						wanted[addr.ConfigResource().String()] = true
					}
					objs = append(objs, is)
					addrsOf = append(addrsOf, addr)
				}
			}
		}
	}
	allowed := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for i, is := range objs {
			key := addrsOf[i].String()
			if allowed[key] {
				continue
			}
			if !targeted(addrsOf[i]) && !dependsOnAny(is, wanted) {
				continue
			}
			allowed[key] = true
			wanted[addrsOf[i].ConfigResource().String()] = true
			changed = true
		}
	}
	var out []string
	for _, ch := range changes {
		if ch.Action != plans.Delete || ch.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if allowed[ch.Addr.String()] || targeted(ch.Addr) {
			continue
		}
		out = append(out, fmt.Sprintf("the destroy plan deletes %s, which is neither a -target nor something that depends on one", ch.Addr))
	}
	return out
}

func dependsOnAny(is *states.ResourceInstance, wanted map[string]bool) bool {
	objs := []*states.ResourceInstanceObjectSrc{is.Current}
	for _, d := range is.Deposed {
		objs = append(objs, d)
	}
	for _, o := range objs {
		if o == nil {
			continue
		}
		for _, dep := range o.Dependencies {
			if wanted[dep.String()] {
				return true
			}
		}
	}
	return false
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
