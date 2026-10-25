// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitchellh/cli"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/terminal"
)

// Joint runs (GitHub issue #2213).
//
// Terragrunt (run --all) and Atmos (--all, --affected) run a unit by invoking
// the binary in that unit's directory: init, then plan or apply, with the
// unit's inputs in TF_VAR_ variables or a generated var file, and
// "output -json" in a dependency's directory to read what it publishes.
// Several such invocations of one run are made into one choudoufu run:
//
//   - Each unit that has a live block registers itself in a session shared
//     by the invocations of the same tool process (the parent pid): its
//     directory, the variable values it was given, the outputs it declares.
//   - A composed root (live_compose.go) is written over the units, one module
//     call each carrying that unit's inputs, and planned. It is re-planned as
//     units register, so the plan after the last unit is the plan across all
//     of them. Units are not planned in the order they arrive but together.
//   - "output -json" on a unit during the plan answers from that joint plan.
//     An output not known until apply is returned as a placeholder string;
//     the dependent unit's input then holds the placeholder, and the root
//     reads it as a reference to the upstream module's output, so the value
//     is carried as "known after apply" and resolved by the one apply.
//   - "plan -out" leaves a small stub in place of a plan file. "show" of the
//     stub is the unit's slice of the joint plan; "apply" of it applies the
//     joint plan once, and the other units' applies find it done.
//
// A run of one unit, a tool this does not recognise, or any flag the joint
// plan cannot honour (-target, -destroy, -refresh-only, -json, ...) takes the
// ordinary path unchanged.

// jointPublishWorkspaces is, for the joint apply in progress, the workspace
// of each unit (by module call) whose tool selected a non-default one.
var jointPublishWorkspaces map[string]string

type jointCommand struct {
	cli.Command
	name string
	meta Meta
}

// JointWrap wraps the factory of plan, apply, show or output.
func JointWrap(name string, meta Meta, f func() (cli.Command, error)) func() (cli.Command, error) {
	return func() (cli.Command, error) {
		inner, err := f()
		if err != nil {
			return nil, err
		}
		return &jointCommand{Command: inner, name: name, meta: meta}, nil
	}
}

// JointIntercept offers an invocation to the joint run before the ordinary
// command runs it. It reports false, having done nothing, for every
// invocation that is not part of a multi-unit Terragrunt or Atmos run.
func JointIntercept(name string, meta Meta, args []string) (int, bool) {
	return (&jointCommand{name: name, meta: meta}).try(args)
}

func (c *jointCommand) Run(args []string) int {
	if code, ok := c.try(args); ok {
		return code
	}
	return c.Command.Run(args)
}

func (c *jointCommand) try(args []string) (int, bool) {
	var code int
	var ok bool
	switch c.name {
	case "plan":
		code, ok = c.jointPlan(args)
	case "apply":
		code, ok = c.jointApply(args)
	case "show":
		code, ok = c.jointShow(args)
	case "output":
		code, ok = c.jointOutput(args)
	}
	return code, ok
}

func (c *jointCommand) stderr(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "choudoufu: "+format+"\n", a...)
}

// jointPlanFlags are the flags passed on to the plan of the root.
func jointPlanFlags(p *arguments.Plan) []string {
	flags := []string{"-input=false"}
	if p.View.NoColor {
		flags = append(flags, "-no-color")
	}
	if p.View.CompactWarnings {
		flags = append(flags, "-compact-warnings")
	}
	if p.View.Concise {
		flags = append(flags, "-concise")
	}
	if !p.Operation.Refresh {
		flags = append(flags, "-refresh=false")
	}
	return flags
}

func (c *jointCommand) jointPlan(args []string) (int, bool) {
	p, closer, diags := arguments.ParsePlan(args)
	if closer != nil {
		defer closer()
	}
	if diags.HasErrors() || p.Operation.PlanMode != plans.NormalMode ||
		len(p.Operation.Targets) > 0 || len(p.Operation.Excludes) > 0 || len(p.Operation.ForceReplace) > 0 ||
		p.GenerateConfigPath != "" || p.View.ViewType != arguments.ViewHuman ||
		p.AdoptionOnly || p.Cone != "" || len(p.EstateOutputsFrom) > 0 {
		jointDebugf("plan flags are not joinable (parse errors: %v)", diags.HasErrors())
		return 0, false
	}
	tool := detectJointTool()
	if tool == nil {
		jointDebugf("plan: not run by a multi-unit Terragrunt or Atmos")
		return 0, false
	}
	orig, _ := os.Getwd()
	outPath := p.OutPath
	if outPath != "" && !filepath.IsAbs(outPath) {
		outPath = filepath.Join(orig, outPath)
	}
	sess, err := openJointSession(tool.ID)
	if err != nil {
		c.stderr("joint run unavailable (%s); planning this unit alone", err)
		return 0, false
	}
	unit, ok := c.registerUnit(sess, tool, p.Vars.All(), outPath)
	if !ok {
		return 0, false
	}
	st, err := c.ensurePlan(sess, jointPlanFlags(p))
	_ = os.Chdir(orig)
	if err != nil {
		c.stderr("%s", err)
		return 1, true
	}
	if !st.OK {
		fmt.Fprint(os.Stderr, st.Err)
		return 1, true
	}
	if outPath != "" {
		// Every unit's plan file is written again, so each carries the
		// joint plan as it stands after this unit joined it.
		if err := sess.writeStubs(tool); err != nil {
			c.stderr("writing %s: %s", outPath, err)
			return 1, true
		}
	}
	doc := sess.loadDoc(st.Serial)
	slice := jointSliceDoc(doc, unit.ID)
	add, chg, del := jointCounts(slice)
	fmt.Printf("Unit %s, planned jointly with the %d units of this %s run that have started so far (%s).\n", unit.ID, len(st.Units), tool.Kind, strings.Join(st.Units, ", "))
	fmt.Printf("A unit that starts later joins this plan and plans it again. The whole plan is in %s; a value another unit has not produced yet is shown as (known after apply).\n\n", sess.path("plan.txt"))
	if b, err := os.ReadFile(sess.path("plan.txt")); err == nil {
		fmt.Println(jointUnitBlocks(string(b), unit.ID))
	}
	fmt.Printf("\nUnit %s: Plan: %d to add, %d to change, %d to destroy.\n", unit.ID, add, chg, del)
	if p.DetailedExitCode && add+chg+del > 0 {
		return 2, true
	}
	return 0, true
}

// registerUnit records this unit in the session, from its own directory,
// environment and flags. It reports false when the unit is not an estate (no
// live block) or its configuration will not load, so the ordinary path
// reports that in its own voice. out is the plan file the unit asked for, or
// "".
func (c *jointCommand) registerUnit(sess *jointSession, tool *jointTool, vars arguments.Vars, out string) (jointUnit, bool) {
	u, varFiles, ok := c.unitFromCwd(tool, vars)
	if !ok {
		return jointUnit{}, false
	}
	u.Out = out
	jointWireUnit(sess, tool, &u, varFiles)
	unlock, err := sess.lock()
	if err != nil {
		c.stderr("%s", err)
		return jointUnit{}, false
	}
	defer unlock()
	return sess.register(u, jointUnitName(u.Dir, varFiles)), true
}

// unitFromCwd reads the unit in the working directory: the variable values
// the tool gave it and the outputs it declares. It also returns the var files
// named on the command line, which name an Atmos component.
func (c *jointCommand) unitFromCwd(tool *jointTool, vars arguments.Vars) (jointUnit, []string, bool) {
	m := c.meta
	m.variableArgs = vars.All()
	ctx := m.CommandContext()
	mod, diags := m.loadSingleModule(ctx, ".", configs.SelectiveLoadAll)
	if diags.HasErrors() || mod == nil || mod.Live == nil || mod.Live.IsComposed() {
		jointDebugf("not joining: no live block or configuration did not load")
		return jointUnit{}, nil, false
	}
	vals, vdiags := m.collectVariableValues()
	if vdiags.HasErrors() {
		return jointUnit{}, nil, false
	}
	u := jointUnit{Vars: map[string]jointVar{}}
	for name, v := range mod.Variables {
		raw, ok := vals[name]
		if !ok {
			continue
		}
		iv, d := raw.ParseVariableValue(v.ParsingMode)
		if d.HasErrors() || iv == nil || !iv.Value.IsKnown() {
			return jointUnit{}, nil, false
		}
		val, err := convert.Convert(iv.Value, v.ConstraintType)
		if err != nil {
			return jointUnit{}, nil, false
		}
		ty, _ := val.Type().MarshalJSON()
		vb, err := ctyjson.Marshal(val, val.Type())
		if err != nil {
			return jointUnit{}, nil, false
		}
		u.Vars[name] = jointVar{Type: ty, Value: vb}
	}
	outNames := make([]string, 0, len(mod.Outputs))
	for n, o := range mod.Outputs {
		if !o.Ephemeral {
			outNames = append(outNames, n)
		}
	}
	sort.Strings(outNames)
	for _, n := range outNames {
		u.Outputs = append(u.Outputs, jointOutputDecl{Name: n, Sensitive: mod.Outputs[n].Sensitive})
	}
	cwd, err := os.Getwd()
	if err != nil {
		return jointUnit{}, nil, false
	}
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}
	u.Dir = cwd
	u.Key = cwd
	if ws := currentWorkspaceName(); ws != backend.DefaultStateName {
		u.Workspace = ws
	}
	var varFiles []string
	for _, f := range vars.All() {
		if f.Name == "-var-file" {
			varFiles = append(varFiles, f.Value)
		}
	}
	if tool.Kind == "atmos" {
		for _, f := range varFiles {
			u.Key += "|" + filepath.Base(f)
		}
	}
	return u, varFiles, true
}

// ensurePlan plans the root over the units registered so far, unless the
// root is what the last plan was made of. The caller is in a unit's
// directory; this leaves the process in the root's.
func (c *jointCommand) ensurePlan(sess *jointSession, flags []string) (jointState, error) {
	unlock, err := sess.lock()
	if err != nil {
		return jointState{}, err
	}
	defer unlock()
	units := sess.units()
	rootDir := sess.path("root")
	text, changed, err := writeJointRoot(rootDir, units)
	if err != nil {
		return jointState{}, fmt.Errorf("joint run: %w", err)
	}
	h := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(h[:])
	st := sess.loadState()
	if st.Hash == hash && !st.Applied {
		return st, nil
	}
	ids := make([]string, len(units))
	for i, u := range units {
		ids[i] = u.ID
	}
	next := jointState{Serial: st.Serial + 1, Hash: hash, Units: ids}

	jointClearUnitEnv()
	if err := os.Chdir(rootDir); err != nil {
		return jointState{}, err
	}
	m, read := c.capture(sess)
	if _, err := os.Stat(filepath.Join(rootDir, ".terraform", "modules", "modules.json")); err != nil || changed {
		code := (&InitCommand{Meta: m}).Run(append([]string{}, flags...))
		out, errOut := read()
		if code != 0 {
			next.Err = "joint run: init of the composed root failed\n" + out + errOut
			_ = sess.saveState(next)
			return next, nil
		}
	}
	planPath := sess.path("joint.plan")
	code := (&PlanCommand{Meta: m}).Run(append(append([]string{}, flags...), "-out="+planPath))
	out, errOut := read()
	if code != 0 {
		next.Err = out + errOut
		_ = sess.saveState(next)
		return next, nil
	}
	_ = os.WriteFile(sess.path("plan.txt"), []byte(out), 0o600)
	if errOut != "" {
		_ = os.WriteFile(sess.path("plan.err"), []byte(errOut), 0o600)
	}
	showCode := (&ShowCommand{Meta: m}).Run([]string{"-json", "-no-color", planPath})
	doc, showErr := read()
	if showCode != 0 {
		next.Err = "joint run: reading the joint plan failed\n" + doc + showErr
		_ = sess.saveState(next)
		return next, nil
	}
	_ = os.WriteFile(sess.path(fmt.Sprintf("doc-%d.json", next.Serial)), []byte(doc), 0o600)
	// An input the tool read from an upstream's last-applied outputs, in a
	// way not traced to the input, is right only if this plan leaves those
	// outputs as they are (#2231).
	if resolved, err := jointResolve(units); err == nil {
		var parsed map[string]any
		_ = json.Unmarshal([]byte(doc), &parsed)
		if lines := jointUnsureCheck(units, resolved.unsure, parsed); len(lines) > 0 {
			next.Err = jointUnsureMessage(jointKindOf(units), lines)
			_ = sess.saveState(next)
			return next, nil
		}
	}
	next.OK = true
	_ = sess.saveState(next)
	return next, nil
}

// capture returns a Meta for the root whose output is collected, and a
// function that returns (and clears) what was written to stdout and stderr.
func (c *jointCommand) capture(sess *jointSession) (Meta, func() (string, string)) {
	outF, _ := os.OpenFile(sess.path("cap-out"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	errF, _ := os.OpenFile(sess.path("cap-err"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	streams := &terminal.Streams{
		Stdout: &terminal.OutputStream{File: outF},
		Stderr: &terminal.OutputStream{File: errF},
		Stdin:  &terminal.InputStream{File: os.Stdin},
	}
	m := c.meta
	m.WorkingDir = workdir.NewDir(".")
	m.inputVariableCache = nil
	m.variableArgs = nil
	m.View = views.NewView(streams).SetRunningInAutomation(c.meta.View.RunningInAutomation())
	read := func() (string, string) {
		rd := func(f *os.File) string {
			_, _ = f.Seek(0, 0)
			b, _ := os.ReadFile(f.Name())
			_ = f.Truncate(0)
			_, _ = f.Seek(0, 0)
			return string(b)
		}
		return rd(outF), rd(errF)
	}
	return m, read
}

func (s *jointSession) loadDoc(serial int) map[string]any {
	var doc map[string]any
	if b, err := os.ReadFile(s.path(fmt.Sprintf("doc-%d.json", serial))); err == nil {
		_ = json.Unmarshal(b, &doc)
	}
	return doc
}

// readJointStub returns the stub a plan file holds, if it is one.
func readJointStub(path string) (jointStub, bool) {
	f, err := os.Open(path)
	if err != nil {
		return jointStub{}, false
	}
	defer f.Close()
	head := make([]byte, len(jointStubMagic)+8)
	n, _ := io.ReadFull(f, head)
	if !strings.HasPrefix(string(head[:n]), `{"`+jointStubMagic+`":1,`) {
		return jointStub{}, false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return jointStub{}, false
	}
	var st jointStub
	if json.Unmarshal(body, &st) != nil {
		return jointStub{}, false
	}
	return st, true
}

// firstPositional is the first argument that is not a flag.
func firstPositional(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func (c *jointCommand) jointShow(args []string) (int, bool) {
	path := firstPositional(args)
	if path == "" {
		return 0, false
	}
	stub, ok := readJointStub(path)
	if !ok {
		return 0, false
	}
	// The plan the file carries is the one shown. A file from before plan
	// files carried their plan is read from its session.
	var doc map[string]any
	units := stub.Units
	if stub.Bundle != nil && len(stub.Bundle.Doc) > 0 {
		if err := json.Unmarshal(stub.Bundle.Doc, &doc); err != nil {
			c.stderr("the joint plan in %s cannot be read: %s", path, err)
			return 1, true
		}
	} else {
		sess := &jointSession{dir: stub.Session}
		st := sess.loadState()
		if !st.OK {
			c.stderr("the joint plan this file points to is gone or failed; plan again")
			return 1, true
		}
		doc, units = sess.loadDoc(st.Serial), st.Units
	}
	slice := jointSliceDoc(doc, stub.Unit)
	// A reviewed plan shown inside another run (Atmos's deploy shows the
	// stored plan before it plans afresh) is what that run's apply of the
	// unit is held to.
	if tool := detectJointTool(); tool != nil && stub.Bundle != nil && stub.Run != "" && stub.Run != tool.ID {
		if sess, err := openJointSession(tool.ID); err == nil {
			if err := sess.saveReviewed(stub.Unit, slice); err != nil {
				c.stderr("keeping the reviewed plan of unit %s: %s", stub.Unit, err)
				return 1, true
			}
		}
	}
	for _, a := range args {
		if a == "-json" {
			b, _ := json.MarshalIndent(slice, "", "  ")
			fmt.Println(string(b))
			return 0, true
		}
	}
	add, chg, del := jointCounts(slice)
	fmt.Printf("Unit %s of the joint plan across %s\n\n", stub.Unit, strings.Join(units, ", "))
	if rcs, ok := slice["resource_changes"].([]any); ok {
		for _, rc := range rcs {
			m, _ := rc.(map[string]any)
			ch, _ := m["change"].(map[string]any)
			fmt.Printf("  # %v: %v\n", m["address"], ch["actions"])
		}
	}
	fmt.Printf("\nPlan: %d to add, %d to change, %d to destroy.\n", add, chg, del)
	return 0, true
}

func (c *jointCommand) jointApply(args []string) (int, bool) {
	a, closer, diags := arguments.ParseApply(args)
	if closer != nil {
		defer closer()
	}
	if diags.HasErrors() || a.PlanPath == "" {
		return 0, false
	}
	stub, ok := readJointStub(a.PlanPath)
	if !ok {
		return 0, false
	}
	tool := detectJointTool()
	if tool == nil {
		c.stderr("%s is the plan file of a joint run across %s. It cannot be applied on its own: apply it from the same Terragrunt or Atmos run (run --all, --all, --affected), or set %s.", a.PlanPath, strings.Join(stub.Units, ", "), jointEnv)
		return 1, true
	}
	stubPath := a.PlanPath
	if abs, err := filepath.Abs(stubPath); err == nil {
		stubPath = abs
	}
	rerun := jointRerunCommand(tool)
	planAgain := "plan again"
	if rerun != "" {
		planAgain = "plan again with " + rerun
	}

	// A plan made by this same run (Atmos's deploy plans and applies each
	// component) is applied from its session. Any other is applied from the
	// joint plan the file carries, in a session rebuilt for this run.
	var sess *jointSession
	rebuilt := false
	if stub.Run == "" || stub.Run == tool.ID {
		sess = &jointSession{dir: stub.Session}
		if _, err := os.Stat(sess.path("joint.plan")); err != nil {
			c.stderr("the joint plan %s points to is gone; %s", a.PlanPath, planAgain)
			return 1, true
		}
	} else {
		if stub.Bundle == nil || len(stub.Bundle.Plan) == 0 {
			c.stderr("%s does not carry its joint plan (it was written by an older choudoufu); %s", a.PlanPath, planAgain)
			return 1, true
		}
		var err error
		if sess, err = c.rebuildSession(stub, tool); err != nil {
			c.stderr("%s", err)
			return 1, true
		}
		rebuilt = true
	}
	unlock, err := sess.lock()
	if err != nil {
		c.stderr("%s", err)
		return 1, true
	}
	defer unlock()
	st := sess.loadState()
	if st.Applied {
		if !st.ApplyOK {
			c.stderr("the joint apply started by unit %s failed:\n%s", st.ApplyBy, st.ApplyErr)
			return 1, true
		}
		fmt.Printf("Unit %s was applied by the joint apply that unit %s started, across %s.\n", stub.Unit, st.ApplyBy, strings.Join(st.Units, ", "))
		return 0, true
	}
	if !st.OK {
		c.stderr("the joint plan failed; %s", planAgain)
		return 1, true
	}
	refuse := func(detail string) (int, bool) {
		c.stderr("%s\n\nNothing was applied. Exit status %d says: send it back to review. %s.", detail, ExitApprovalRefused, strings.ToUpper(planAgain[:1])+planAgain[1:])
		st.Applied, st.ApplyOK, st.ApplyBy = true, false, stub.Unit
		st.ApplyErr = "refused: the reviewed plan does not cover this apply (the reason is above, in the run of unit " + stub.Unit + ")"
		_ = sess.saveState(st)
		return ExitApprovalRefused, true
	}

	if rebuilt {
		// The inputs the tool gives this unit now are read from what its
		// upstream units have published since the plan. If they moved, the
		// reviewed plan was made on other inputs. (Terragrunt gives none to
		// an apply of a plan file; the live plan below still covers it.)
		if now, _, ok := c.unitFromCwd(tool, a.Vars.All()); ok {
			for _, reviewed := range stub.Bundle.Units {
				if reviewed.ID != stub.Unit {
					continue
				}
				if diff := jointInputDiff(reviewed, now); len(diff) > 0 {
					return refuse(fmt.Sprintf("Unit %s is given other values now than the reviewed plan was made with, for %s. An upstream unit has published other outputs since the plan, or the inputs changed.", stub.Unit, strings.Join(diff, ", ")))
				}
			}
		}
	} else if reviewed, ok := sess.loadReviewed(stub.Unit); ok {
		if diff := jointSliceDiff(reviewed, jointSliceDoc(sess.loadDoc(st.Serial), stub.Unit)); len(diff) > 0 {
			return refuse(fmt.Sprintf("The plan this run made for unit %s is not the reviewed plan of the unit:\n  %s\nValues that were known after apply in the reviewed plan are not compared.", stub.Unit, strings.Join(diff, "\n  ")))
		}
	}

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	if rebuilt {
		jointClearUnitEnv()
	}
	if err := os.Chdir(sess.rootDir(st)); err != nil {
		c.stderr("%s", err)
		return 1, true
	}
	m := c.meta
	m.WorkingDir = workdir.NewDir(".")
	m.inputVariableCache = nil
	m.variableArgs = nil
	m.jointPlanFile = stubPath
	m.jointRerun = rerun
	applyArgs := []string{"-input=false"}
	if a.View.NoColor {
		applyArgs = append(applyArgs, "-no-color")
	}
	jointPublishWorkspaces = map[string]string{}
	for _, u := range sess.units() {
		if u.Workspace != "" {
			jointPublishWorkspaces[u.ID] = u.Workspace
		}
	}
	fmt.Printf("Applying the joint plan across %d units: %s\n\n", len(st.Units), strings.Join(st.Units, ", "))
	code := (&ApplyCommand{Meta: m}).Run(append(applyArgs, sess.path("joint.plan")))
	st.Applied, st.ApplyOK, st.ApplyBy = true, code == 0, stub.Unit
	if code != 0 {
		st.ApplyErr = fmt.Sprintf("exit status %d (the output is above, in the run of unit %s)", code, stub.Unit)
	}
	_ = sess.saveState(st)
	return code, true
}

func (c *jointCommand) jointOutput(args []string) (int, bool) {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "-json":
			jsonOut = true
		case "-no-color":
		default:
			return 0, false
		}
	}
	if !jsonOut {
		return 0, false
	}
	tool := detectJointTool()
	if tool == nil || tool.Phase != "plan" {
		return 0, false
	}
	sess, err := openJointSession(tool.ID)
	if err != nil {
		return 0, false
	}
	orig, _ := os.Getwd()
	var unit jointUnit
	if tool.Kind == "atmos" {
		// Atmos gives "output" no inputs, so a component is only in the
		// joint plan once Atmos has planned it.
		cwd := orig
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = r
		}
		var found []jointUnit
		for _, u := range sess.units() {
			if u.Dir == cwd {
				found = append(found, u)
			}
		}
		if len(found) != 1 {
			return 0, false
		}
		unit = found[0]
	} else {
		var ok bool
		unit, ok = c.registerUnit(sess, tool, nil, "")
		if !ok {
			return 0, false
		}
	}
	st, err := c.ensurePlan(sess, []string{"-input=false", "-no-color"})
	_ = os.Chdir(orig)
	if err != nil {
		c.stderr("%s", err)
		return 1, true
	}
	if !st.OK {
		fmt.Fprint(os.Stderr, st.Err)
		return 1, true
	}
	doc := sess.loadDoc(st.Serial)
	changes, _ := doc["output_changes"].(map[string]any)
	out := map[string]any{}
	for _, o := range unit.Outputs {
		ch, ok := changes[unit.ID+"__"+o.Name].(map[string]any)
		if !ok {
			continue
		}
		var value any = ch["after"]
		unknown := ch["after_unknown"]
		if b, isBool := unknown.(bool); (isBool && b) || (!isBool && unknown != nil && jointAnyTrue(unknown)) {
			value = jointSentinel(unit.ID, o.Name)
		}
		sensitive, _ := ch["after_sensitive"].(bool)
		out[o.Name] = map[string]any{"sensitive": sensitive, "type": jointTypeOf(value), "value": value}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return 0, true
}

// jointAnyTrue reports whether an after_unknown value marks anything unknown.
func jointAnyTrue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case map[string]any:
		for _, e := range t {
			if jointAnyTrue(e) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if jointAnyTrue(e) {
				return true
			}
		}
	}
	return false
}

func jointTypeOf(v any) any {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case []any:
		return []any{"tuple", []any{}}
	case map[string]any:
		return []any{"object", map[string]any{}}
	}
	return "string"
}
