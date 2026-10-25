// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/intentius/choudoufu/internal/lang"
)

// Dependency wiring in a joint run (GitHub issue #2231, under #2213).
//
// A tool gives a unit the outputs of the units it depends on as ordinary
// inputs. Where it reads them from decides whether they are right for a
// joint plan:
//
//   - Terragrunt (v1.2.0 and later, by default) reads a `remote_state`
//     dependency's outputs straight from its state in S3, GCS or azurerm,
//     and Atmos's `!terraform.state` does the same. That state is the outputs
//     document choudoufu publishes after an apply (#2217), so it holds what
//     was last applied, not what the joint plan is about to apply.
//   - `output -json` (Terragrunt without a remote_state backend, Atmos's
//     `!terraform.output`) asks this binary, which answers from the joint
//     plan.
//
// So the joint plan does not take such an input's value from the tool. It
// reads the tool's own configuration (the unit's terragrunt.hcl and its
// includes; the Atmos stack as `atmos describe stacks` resolves it), finds
// the inputs that are reads of another unit's outputs, and gives the
// composed root those inputs as expressions over the upstream module's
// outputs: `dependency.network.outputs.subnet_b_id` becomes
// `module.network.subnet_b_id`. The value is then the planned one, of any
// type, and unknown where the upstream's is.
//
// A read it cannot trace to the input it feeds (a dependency output used in
// a local, a function the composed root does not have, an Atmos `!store`) is
// kept as an unsure read. The tool's value for it is right only while the
// upstream's outputs do not change, so after the joint plan every unsure
// read is checked against the plan, and a plan that changes what an unsure
// read reads fails, saying which read and why.

// jointWire is an input the composed root computes from other units'
// outputs. Expr is the input's expression with each read replaced by
// jointDepToken(i) for Deps[i].
type jointWire struct {
	Expr  string     `json:"expr"`
	Deps  []jointDep `json:"deps"`
	Where string     `json:"where,omitempty"`
}

// jointDep is one read of another unit's output: the unit's ref, and the
// output ("" for the whole outputs object).
type jointDep struct {
	Unit   string `json:"unit"`
	Output string `json:"output,omitempty"`
}

// jointUnsure is a read of another unit's outputs that is not traced to an
// input. Unit is "" when it is not known which unit is read; Outputs is
// empty when it is not known which outputs.
type jointUnsure struct {
	Unit    string   `json:"unit,omitempty"`
	Outputs []string `json:"outputs,omitempty"`
	Where   string   `json:"where"`
}

func jointDepToken(i int) string { return fmt.Sprintf("__choudoufu_dep_%d__", i) }

var jointDepTokenRE = regexp.MustCompile(`__choudoufu_dep_([0-9]+)__`)

// jointResolved is what the composed root makes of each unit's wiring: the
// expression per wired variable, and the unsure reads to check after the
// plan (a unit's own, and a wire whose upstream is partly outside the run).
type jointResolved struct {
	exprs  map[string]map[string]string
	unsure map[string][]jointUnsure
}

// jointResolve resolves every unit's wiring against the units of the run.
// A wire whose upstream units are none of them is left to the tool's value:
// those units are not in this run, so their outputs do not change in it. A
// wire that reads an output its upstream unit does not declare is an error.
func jointResolve(units []jointUnit) (jointResolved, error) {
	byRef := map[string]*jointUnit{}
	for i := range units {
		if units[i].Ref != "" {
			byRef[units[i].Ref] = &units[i]
		}
	}
	r := jointResolved{exprs: map[string]map[string]string{}, unsure: map[string][]jointUnsure{}}
	for _, u := range units {
		r.unsure[u.ID] = append(r.unsure[u.ID], u.Unsure...)
		names := make([]string, 0, len(u.Wired))
		for n := range u.Wired {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			w := u.Wired[n]
			in, out := 0, 0
			for _, d := range w.Deps {
				if byRef[d.Unit] != nil {
					in++
				} else {
					out++
				}
			}
			switch {
			case in == 0:
				continue
			case out > 0:
				// Some of what it reads is planned here and some is not: the
				// tool's value stands, and holds only if the planned part
				// does not change.
				for _, d := range w.Deps {
					if byRef[d.Unit] != nil {
						r.unsure[u.ID] = append(r.unsure[u.ID], jointUnsure{Unit: d.Unit, Outputs: jointNonEmpty(d.Output), Where: w.Where})
					}
				}
				continue
			}
			var err error
			expr := jointDepTokenRE.ReplaceAllStringFunc(w.Expr, func(tok string) string {
				i, _ := strconv.Atoi(jointDepTokenRE.FindStringSubmatch(tok)[1])
				if i >= len(w.Deps) {
					err = fmt.Errorf("unit %s, input %s: the wiring is malformed", u.ID, n)
					return tok
				}
				d := w.Deps[i]
				up := byRef[d.Unit]
				if d.Output == "" {
					return "module." + up.ID
				}
				declared := false
				for _, o := range up.Outputs {
					declared = declared || o.Name == d.Output
				}
				if !declared {
					err = fmt.Errorf("unit %s's input %s reads output %q of unit %s (%s), which declares no such output", u.ID, n, d.Output, up.ID, w.Where)
				}
				if hclsyntax.ValidIdentifier(d.Output) {
					return "module." + up.ID + "." + d.Output
				}
				return "module." + up.ID + "[" + jointQuote(d.Output) + "]"
			})
			if err != nil {
				return jointResolved{}, err
			}
			if r.exprs[u.ID] == nil {
				r.exprs[u.ID] = map[string]string{}
			}
			r.exprs[u.ID][n] = expr
		}
	}
	return r, nil
}

func jointNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// jointUnsureCheck reports, after a joint plan, every unsure read whose
// upstream outputs the plan changes or leaves unknown.
func jointUnsureCheck(units []jointUnit, unsure map[string][]jointUnsure, doc map[string]any) []string {
	byRef := map[string]*jointUnit{}
	for i := range units {
		if units[i].Ref != "" {
			byRef[units[i].Ref] = &units[i]
		}
	}
	changes, _ := doc["output_changes"].(map[string]any)
	var out []string
	for _, u := range units {
		for _, s := range unsure[u.ID] {
			var targets []*jointUnit
			if s.Unit == "" {
				for i := range units {
					if units[i].ID != u.ID {
						targets = append(targets, &units[i])
					}
				}
			} else if t := byRef[s.Unit]; t != nil && t.ID != u.ID {
				targets = append(targets, t)
			}
			for _, t := range targets {
				names := s.Outputs
				if len(names) == 0 {
					for _, o := range t.Outputs {
						names = append(names, o.Name)
					}
				}
				var moved []string
				for _, n := range names {
					ch, ok := changes[t.ID+"__"+n].(map[string]any)
					if !ok {
						continue
					}
					unknown := ch["after_unknown"]
					if b, isBool := unknown.(bool); jointActions(ch) != "no-op" || (isBool && b) || (!isBool && unknown != nil && jointAnyTrue(unknown)) {
						moved = append(moved, n)
					}
				}
				if len(moved) > 0 {
					sort.Strings(moved)
					out = append(out, fmt.Sprintf("unit %s reads unit %s's outputs through %s, which choudoufu cannot trace to the input it feeds, and this plan changes %s", u.ID, t.ID, s.Where, strings.Join(moved, ", ")))
				}
			}
		}
	}
	return out
}

// jointUnsureMessage is the error of a plan that failed jointUnsureCheck.
func jointUnsureMessage(kind string, lines []string) string {
	reads := "its dependency blocks' outputs (Terragrunt reads them from the state an apply published)"
	fix := "Use the output directly as an input (inputs = { x = dependency.<name>.outputs.<output> }, or an expression of such reads and built-in functions)"
	if kind == "atmos" {
		reads = "!terraform.state, !terraform.output or !store (Atmos reads them from what an apply published)"
		fix = "Give the output as a var of its own (x: !terraform.state <component> <output>, or a simple path such as .<output>.key)"
	}
	return fmt.Sprintf("joint run: the joint plan cannot stand behind these inputs:\n  %s\n\nThe tool gave each of those units the upstream's last-applied outputs, through %s. They are right only while the upstream's outputs do not change, and this plan changes them, so a plan made on them would not be the plan of this change, and the joint plan is refused.\n\n%s, or apply the upstream units first and plan again.\n", strings.Join(lines, "\n  "), reads, fix)
}

// --- Terragrunt -------------------------------------------------------------

// jointTerragruntDir is the Terragrunt unit directory of a working
// directory: the unit itself, or the unit whose .terragrunt-cache it is in.
func jointTerragruntDir(cwd string) string {
	d := filepath.ToSlash(cwd)
	if i := strings.Index(d, "/.terragrunt-cache/"); i >= 0 {
		return filepath.FromSlash(d[:i])
	}
	return cwd
}

// tgFile is a parsed Terragrunt configuration file.
type tgFile struct {
	path string
	src  []byte
	body *hclsyntax.Body
}

func tgParse(path string) (*tgFile, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	return &tgFile{path: path, src: src, body: f.Body.(*hclsyntax.Body)}, nil
}

// tgEvalPath evaluates a path attribute (an include's path, a dependency's
// config_path) with the few Terragrunt functions such paths are written with.
// A relative result is relative to the unit directory.
func tgEvalPath(expr hclsyntax.Expression, unitDir string) (string, error) {
	findUp := function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "args", Type: cty.String},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			name := "terragrunt.hcl"
			if len(args) > 0 {
				name = args[0].AsString()
			}
			for dir := filepath.Dir(unitDir); ; dir = filepath.Dir(dir) {
				p := filepath.Join(dir, name)
				if _, err := os.Stat(p); err == nil {
					return cty.StringVal(p), nil
				}
				if filepath.Dir(dir) == dir {
					if len(args) > 1 {
						return args[1], nil
					}
					return cty.NilVal, fmt.Errorf("no %s above %s", name, unitDir)
				}
			}
		},
	})
	here := function.New(&function.Spec{
		Type: function.StaticReturnType(cty.String),
		Impl: func([]cty.Value, cty.Type) (cty.Value, error) { return cty.StringVal(unitDir), nil },
	})
	ctx := &hcl.EvalContext{Functions: map[string]function.Function{
		"find_in_parent_folders":      findUp,
		"get_terragrunt_dir":          here,
		"get_original_terragrunt_dir": here,
	}}
	v, diags := expr.Value(ctx)
	if diags.HasErrors() {
		return "", diags
	}
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", fmt.Errorf("not a known string")
	}
	p := v.AsString()
	if !filepath.IsAbs(p) {
		p = filepath.Join(unitDir, p)
	}
	return filepath.Clean(p), nil
}

// tgCanonical is a directory as units are keyed: absolute, symlinks resolved.
func tgCanonical(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return dir
}

// jointTerragruntWiring reads the wiring of the Terragrunt unit in unitDir.
func jointTerragruntWiring(unitDir string) (wired map[string]jointWire, unsure []jointUnsure) {
	cfgName := "terragrunt.hcl"
	if v := os.Getenv("TG_CONFIG"); v != "" {
		cfgName = v
	}
	cfgPath := cfgName
	if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(unitDir, cfgName)
	}
	all := func(why string) (map[string]jointWire, []jointUnsure) {
		return nil, []jointUnsure{{Where: why}}
	}
	unit, err := tgParse(cfgPath)
	if err != nil {
		return all(fmt.Sprintf("%s (choudoufu could not read it: %s)", cfgPath, err))
	}
	files := []*tgFile{}
	for _, b := range unit.body.Blocks {
		if b.Type != "include" {
			continue
		}
		attr := b.Body.Attributes["path"]
		if attr == nil {
			return all(fmt.Sprintf("the include at %s", tgWhere(unit, b.Range())))
		}
		p, err := tgEvalPath(attr.Expr, unitDir)
		if err != nil {
			return all(fmt.Sprintf("the include at %s, whose path choudoufu cannot evaluate", tgWhere(unit, b.Range())))
		}
		if ms := b.Body.Attributes["merge_strategy"]; ms != nil {
			if v, d := ms.Expr.Value(nil); !d.HasErrors() && v.Type() == cty.String && v.AsString() == "no_merge" {
				continue
			}
		}
		inc, err := tgParse(p)
		if err != nil {
			return all(fmt.Sprintf("the included %s (choudoufu could not read it: %s)", p, err))
		}
		files = append(files, inc)
	}
	files = append(files, unit) // the unit's own file last: its inputs win

	// Dependency blocks, by name, to the upstream unit's directory.
	deps := map[string]string{}
	for _, f := range files {
		for _, b := range f.body.Blocks {
			if b.Type != "dependency" || len(b.Labels) != 1 {
				continue
			}
			deps[b.Labels[0]] = ""
			if attr := b.Body.Attributes["config_path"]; attr != nil {
				if p, err := tgEvalPath(attr.Expr, unitDir); err == nil {
					deps[b.Labels[0]] = tgCanonical(p)
				}
			}
		}
	}

	// The effective inputs: each key's expression from the last file that
	// sets it.
	type item struct {
		f    *tgFile
		expr hclsyntax.Expression
	}
	inputs := map[string]item{}
	var opaqueInputs []*tgFile
	for _, f := range files {
		attr := f.body.Attributes["inputs"]
		if attr == nil {
			continue
		}
		obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
		if !ok {
			opaqueInputs = append(opaqueInputs, f)
			continue
		}
		for _, it := range obj.Items {
			kv, d := it.KeyExpr.Value(nil)
			if d.HasErrors() || kv.IsNull() || !kv.IsKnown() || kv.Type() != cty.String {
				opaqueInputs = append(opaqueInputs, f)
				continue
			}
			inputs[kv.AsString()] = item{f: f, expr: it.ValueExpr}
		}
	}

	traced := map[string]bool{} // file:byte of each dependency traversal an input is wired from
	wired = map[string]jointWire{}
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		it := inputs[k]
		w, used, ok := tgWire(it.f, it.expr, deps)
		if !ok {
			continue
		}
		wired[k] = w
		for _, at := range used {
			traced[it.f.path+":"+strconv.Itoa(at)] = true
		}
	}

	// Every other read of a dependency is unsure.
	for _, f := range files {
		_ = hclsyntax.VisitAll(f.body, func(n hclsyntax.Node) hcl.Diagnostics {
			switch e := n.(type) {
			case *hclsyntax.FunctionCallExpr:
				if e.Name == "read_terragrunt_config" {
					unsure = append(unsure, jointUnsure{Where: fmt.Sprintf("read_terragrunt_config at %s", tgWhere(f, e.Range()))})
				}
			case *hclsyntax.ScopeTraversalExpr:
				t := e.Traversal
				name, out := tgDepRead(t)
				if name == "" || traced[f.path+":"+strconv.Itoa(t[0].SourceRange().Start.Byte)] {
					return nil
				}
				s := jointUnsure{Unit: deps[name], Outputs: jointNonEmpty(out), Where: fmt.Sprintf("dependency.%s at %s", name, tgWhere(f, e.Range()))}
				if s.Unit == "" {
					s.Where = fmt.Sprintf("dependency.%s at %s, whose config_path choudoufu cannot evaluate", name, tgWhere(f, e.Range()))
				}
				unsure = append(unsure, s)
			}
			return nil
		})
	}
	_ = opaqueInputs // their dependency reads are untraced, so unsure above
	return wired, unsure
}

// tgDepRead names the dependency a traversal reads, and the output when it
// is one: dependency.<name>.outputs.<output>, or the same through an
// exposed include (include.<x>.dependency.<name>...).
func tgDepRead(t hcl.Traversal) (name, output string) {
	steps := t
	if t.RootName() == "include" && len(t) >= 3 {
		if a, ok := t[2].(hcl.TraverseAttr); ok && a.Name == "dependency" {
			steps = append(hcl.Traversal{hcl.TraverseRoot{Name: "dependency"}}, t[3:]...)
		}
	}
	if steps.RootName() != "dependency" || len(steps) < 2 {
		return "", ""
	}
	a, ok := steps[1].(hcl.TraverseAttr)
	if !ok {
		return "", ""
	}
	if len(steps) >= 4 {
		if o, ok := steps[2].(hcl.TraverseAttr); ok && o.Name == "outputs" {
			output = tgStepName(steps[3])
		}
	}
	return a.Name, output
}

func tgStepName(s hcl.Traverser) string {
	switch s := s.(type) {
	case hcl.TraverseAttr:
		return s.Name
	case hcl.TraverseIndex:
		if s.Key.Type() == cty.String {
			return s.Key.AsString()
		}
	}
	return ""
}

func tgWhere(f *tgFile, r hcl.Range) string {
	return fmt.Sprintf("%s:%d", f.path, r.Start.Line)
}

var (
	jointFuncsOnce sync.Once
	jointFuncs     map[string]function.Function
)

// jointRootFunctions are the functions an input expression may call to be
// wired into the composed root: OpenTofu's own, less those that read files,
// the clock or randomness (which would mean something else in the root).
func jointRootFunctions() map[string]function.Function {
	jointFuncsOnce.Do(func() {
		jointFuncs = (&lang.Scope{}).Functions()
		for _, n := range []string{"abspath", "pathexpand", "file", "fileexists", "fileset", "filebase64", "filebase64sha256", "filebase64sha512", "filemd5", "filesha1", "filesha256", "filesha512", "templatefile", "timestamp", "plantimestamp", "uuid", "bcrypt"} {
			delete(jointFuncs, n)
		}
	})
	return jointFuncs
}

// tgWire makes an input expression a wire, when it reads at least one
// dependency output and nothing but dependency outputs, through functions
// the composed root has. It returns the byte offsets of the traversals it
// wired.
func tgWire(f *tgFile, expr hclsyntax.Expression, deps map[string]string) (jointWire, []int, bool) {
	funcs := jointRootFunctions()
	ok := true
	_ = hclsyntax.VisitAll(expr, func(n hclsyntax.Node) hcl.Diagnostics {
		if call, isCall := n.(*hclsyntax.FunctionCallExpr); isCall {
			if _, has := funcs[call.Name]; !has || strings.Contains(call.Name, "::") {
				ok = false
			}
		}
		return nil
	})
	if !ok {
		return jointWire{}, nil, false
	}
	type seg struct {
		start, end int
		dep        jointDep
	}
	var segs []seg
	for _, t := range expr.Variables() {
		if t.RootName() != "dependency" || len(t) < 3 {
			return jointWire{}, nil, false
		}
		a, isAttr := t[1].(hcl.TraverseAttr)
		o, isOut := t[2].(hcl.TraverseAttr)
		if !isAttr || !isOut || o.Name != "outputs" {
			return jointWire{}, nil, false
		}
		dir, known := deps[a.Name]
		if !known || dir == "" {
			return jointWire{}, nil, false
		}
		s := seg{start: t[0].SourceRange().Start.Byte, end: t[2].SourceRange().End.Byte, dep: jointDep{Unit: dir}}
		if len(t) >= 4 {
			if name := tgStepName(t[3]); name != "" {
				s.dep.Output = name
				s.end = t[3].SourceRange().End.Byte
			}
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 {
		return jointWire{}, nil, false
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].start < segs[j].start })
	r := expr.Range()
	var b strings.Builder
	at := r.Start.Byte
	w := jointWire{Where: tgWhere(f, r)}
	var used []int
	for i, s := range segs {
		if s.start < at || s.end > r.End.Byte {
			return jointWire{}, nil, false
		}
		b.Write(f.src[at:s.start])
		b.WriteString(jointDepToken(i))
		w.Deps = append(w.Deps, s.dep)
		used = append(used, s.start)
		at = s.end
	}
	b.Write(f.src[at:r.End.Byte])
	w.Expr = b.String()
	return w, used, true
}

// --- Atmos ------------------------------------------------------------------

// atmosRef is the ref of an Atmos component in a stack.
func atmosRef(stack, component string) string { return "atmos:" + stack + "\t" + component }

// jointAtmosStacks returns `atmos describe stacks` of the run, with YAML
// functions and templates left unprocessed, so each var shows how it is
// read. It is kept in the session: the stacks do not change within a run.
func (s *jointSession) atmosStacks(tool *jointTool) (map[string]any, error) {
	cache := s.path("atmos-stacks.json")
	if b, err := os.ReadFile(cache); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			return m, nil
		}
	}
	bin := ""
	if b, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", tool.PID)); err == nil {
		bin = b
	} else if len(tool.Args) > 0 {
		bin = tool.Args[0]
		if !filepath.IsAbs(bin) && !strings.Contains(bin, string(filepath.Separator)) {
			if p, err := exec.LookPath(bin); err == nil {
				bin = p
			}
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("the atmos binary of the run is not known")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "describe", "stacks", "--process-functions=false", "--process-templates=false", "--format", "json")
	// Atmos gives the component's process its configuration and base path.
	if p := os.Getenv("ATMOS_BASE_PATH"); p != "" {
		cmd.Dir = p
	}
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("atmos describe stacks: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf("atmos describe stacks: %w", err)
	}
	tmp := cache + ".tmp"
	if os.WriteFile(tmp, out, 0o600) == nil {
		_ = os.Rename(tmp, cache)
	}
	return m, nil
}

// jointAtmosComponent finds the stack and component Atmos is running from
// the var file it generated, <stack>-<component>.terraform.tfvars.json.
func jointAtmosComponent(stacks map[string]any, varFiles []string) (stack, component string, section map[string]any, ok bool) {
	want := map[string]bool{}
	for _, f := range varFiles {
		want[filepath.Base(f)] = true
	}
	found := 0
	for st, sv := range stacks {
		sm, _ := sv.(map[string]any)
		comps, _ := sm["components"].(map[string]any)
		tf, _ := comps["terraform"].(map[string]any)
		for name, cv := range tf {
			name2 := strings.ReplaceAll(name, "/", "-")
			if want[strings.ReplaceAll(st, "/", "-")+"-"+name2+".terraform.tfvars.json"] {
				section, _ = cv.(map[string]any)
				stack, component = st, name
				found++
			}
		}
	}
	return stack, component, section, found == 1
}

var (
	atmosPathRE      = regexp.MustCompile(`^\.?([A-Za-z_][A-Za-z0-9_-]*)((?:\.[A-Za-z_][A-Za-z0-9_-]*|\[[0-9]+\])*)$`)
	atmosComponentRE = regexp.MustCompile(`atmos\.Component\s+"([^"]+)"`)
)

// atmosRead parses a !terraform.state or !terraform.output call. It returns
// ok false for any other string.
func atmosRead(s, stack string) (dep jointDep, suffix string, parsed, ok bool) {
	var rest string
	switch {
	case strings.HasPrefix(s, "!terraform.state "):
		rest = strings.TrimPrefix(s, "!terraform.state ")
	case strings.HasPrefix(s, "!terraform.output "):
		rest = strings.TrimPrefix(s, "!terraform.output ")
	default:
		return jointDep{}, "", false, false
	}
	args := strings.Fields(rest)
	if strings.Contains(rest, "{{") || strings.ContainsAny(rest, `'"`) || len(args) < 2 || len(args) > 3 {
		if len(args) > 0 && !strings.Contains(args[0], "{{") {
			return jointDep{Unit: atmosRef(stack, args[0])}, "", false, true
		}
		return jointDep{}, "", false, true
	}
	if len(args) == 3 {
		stack = args[1]
	}
	m := atmosPathRE.FindStringSubmatch(args[len(args)-1])
	if m == nil {
		return jointDep{Unit: atmosRef(stack, args[0])}, "", false, true
	}
	return jointDep{Unit: atmosRef(stack, args[0]), Output: m[1]}, m[2], true, true
}

// jointAtmosWiring reads the wiring of an Atmos component from its section
// of `atmos describe stacks`.
func jointAtmosWiring(stack, component string, section map[string]any) (wired map[string]jointWire, unsure []jointUnsure) {
	wired = map[string]jointWire{}
	vars, _ := section["vars"].(map[string]any)
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	where := func(n string) string { return fmt.Sprintf("vars.%s of component %s in stack %s", n, component, stack) }
	for _, n := range names {
		if s, isStr := vars[n].(string); isStr {
			if dep, suffix, parsed, ok := atmosRead(s, stack); ok {
				if parsed {
					wired[n] = jointWire{Expr: jointDepToken(0) + suffix, Deps: []jointDep{dep}, Where: where(n)}
				} else {
					unsure = append(unsure, jointUnsure{Unit: dep.Unit, Where: where(n) + " (" + s + ")"})
				}
				continue
			}
		}
		unsure = append(unsure, atmosUnsureIn(vars[n], stack, where(n))...)
	}
	if env, ok := section["env"].(map[string]any); ok {
		for k, v := range env {
			unsure = append(unsure, atmosUnsureIn(v, stack, fmt.Sprintf("env.%s of component %s in stack %s", k, component, stack))...)
		}
	}
	return wired, unsure
}

// atmosUnsureIn finds reads of other components' outputs nested in a value.
func atmosUnsureIn(v any, stack, where string) []jointUnsure {
	var out []jointUnsure
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			if dep, _, _, ok := atmosRead(t, stack); ok {
				s := jointUnsure{Unit: dep.Unit, Outputs: jointNonEmpty(dep.Output), Where: where}
				out = append(out, s)
				return
			}
			if strings.HasPrefix(t, "!store") || strings.Contains(t, "atmos.Store") || strings.Contains(t, "!terraform.") {
				out = append(out, jointUnsure{Where: where})
				return
			}
			for _, m := range atmosComponentRE.FindAllStringSubmatch(t, -1) {
				out = append(out, jointUnsure{Unit: atmosRef(stack, m[1]), Where: where})
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

// jointWireUnit fills in a unit's ref and wiring from the tool's
// configuration. What it cannot read becomes an unsure read of any unit.
func jointWireUnit(sess *jointSession, tool *jointTool, u *jointUnit, varFiles []string) {
	switch tool.Kind {
	case "atmos":
		stacks, err := sess.atmosStacks(tool)
		if err != nil {
			u.Unsure = []jointUnsure{{Where: fmt.Sprintf("the stack configuration, which choudoufu could not read (%s)", err)}}
			return
		}
		stack, comp, section, ok := jointAtmosComponent(stacks, varFiles)
		if !ok {
			u.Unsure = []jointUnsure{{Where: fmt.Sprintf("the stack configuration of %s, which choudoufu could not find in atmos describe stacks", strings.Join(varFiles, ", "))}}
			return
		}
		u.Ref = atmosRef(stack, comp)
		u.Wired, u.Unsure = jointAtmosWiring(stack, comp, section)
	case "terragrunt":
		dir := tgCanonical(jointTerragruntDir(u.Dir))
		u.Ref = dir
		u.Wired, u.Unsure = jointTerragruntWiring(dir)
	}
	for n := range u.Wired {
		if _, given := u.Vars[n]; !given {
			delete(u.Wired, n)
		}
	}
	if len(u.Wired) == 0 {
		u.Wired = nil
	}
}

// jointKindOf names the tool from its units' refs, for messages.
func jointKindOf(units []jointUnit) string {
	for _, u := range units {
		if strings.HasPrefix(u.Ref, "atmos:") {
			return "atmos"
		}
	}
	return "terragrunt"
}
