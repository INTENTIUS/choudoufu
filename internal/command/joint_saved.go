// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/plans/planfile"
)

// Saved joint plans (GitHub issue #2226, under #2213: the reviewed plan is the applied
// plan).
//
// A pull request plans; the merge applies, in another job and often on
// another machine. Terragrunt keeps the plan files of "run --all --out-dir"
// and Atmos keeps them in its planfile store; nothing else from the plan's
// job survives, and the joint session lives in a temporary directory. So a
// unit's plan file carries the joint plan itself (a jointBundle), and
// applying it where its session is gone rebuilds the session from the file:
// the plan, the configuration snapshot inside the plan, the units and the
// root's lock file. The apply then plans the live system again and refuses,
// exit status 3, when the result is not the reviewed plan (#878's check).

// jointBundle is the joint plan a unit's plan file carries.
type jointBundle struct {
	Plan  []byte          `json:"plan"`
	Doc   json.RawMessage `json:"doc"`
	Units []jointUnit     `json:"units"`
	Lock  []byte          `json:"lock,omitempty"`
}

// writeStubs writes the plan file of every unit of the session that asked
// for one, each carrying the latest joint plan. A unit planned early would
// otherwise hold the plan as it was before later units joined it, and that
// is the file a tool applies first.
func (s *jointSession) writeStubs(tool *jointTool) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	st := s.loadState()
	if !st.OK {
		return fmt.Errorf("the joint plan of this run failed after this unit planned; see the failing unit")
	}
	plan, err := os.ReadFile(s.path("joint.plan"))
	if err != nil {
		return err
	}
	doc, err := os.ReadFile(s.path(fmt.Sprintf("doc-%d.json", st.Serial)))
	if err != nil {
		return err
	}
	lock, _ := os.ReadFile(filepath.Join(s.rootDir(st), ".terraform.lock.hcl"))
	units := s.units()
	b := &jointBundle{Plan: plan, Doc: doc, Units: units, Lock: lock}
	for _, u := range units {
		if u.Out == "" {
			continue
		}
		body, err := json.Marshal(jointStub{Magic: 1, Session: s.dir, Unit: u.ID, Serial: st.Serial, Units: st.Units, Run: filepath.Base(s.dir), Tool: tool.Kind, Bundle: b})
		if err != nil {
			return err
		}
		tmp := u.Out + ".choudoufu-tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, u.Out); err != nil {
			return err
		}
	}
	return nil
}

// rebuildSession makes a session for this apply run out of a plan file whose
// own session is not here. Every unit's apply in the run finds the same one.
func (c *jointCommand) rebuildSession(stub jointStub, tool *jointTool) (*jointSession, error) {
	sess, err := openJointSession(tool.ID + "-from-" + stub.Run)
	if err != nil {
		return nil, err
	}
	unlock, err := sess.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if st := sess.loadState(); st.OK {
		return sess, nil
	}
	b := stub.Bundle
	planPath := sess.path("joint.plan")
	if err := os.WriteFile(planPath, b.Plan, 0o600); err != nil {
		return nil, err
	}
	reader, err := planfile.Open(planPath, encryption.PlanEncryptionDisabled())
	if err != nil {
		return nil, fmt.Errorf("reading the joint plan the plan file carries: %w", err)
	}
	snap, err := reader.ReadConfigSnapshot()
	if err != nil {
		return nil, fmt.Errorf("reading the configuration snapshot of the joint plan: %w", err)
	}
	// The units sit beside the root, so their snapshot directories climb out
	// of it ("../../app/.terragrunt-cache/..."). The root is placed that many
	// levels below base, and every module lands inside base.
	depth := 0
	for key, m := range snap.Modules {
		d := filepath.Clean(filepath.FromSlash(m.Dir))
		if filepath.IsAbs(d) {
			return nil, fmt.Errorf("module %q of the snapshot has an absolute directory", key)
		}
		n := 0
		for _, part := range strings.Split(filepath.ToSlash(d), "/") {
			if part != ".." {
				break
			}
			n++
		}
		if n > depth {
			depth = n
		}
	}
	base := sess.path("config")
	rootDir := base
	for i := 0; i < depth; i++ {
		rootDir = filepath.Join(rootDir, "_joint")
	}
	rootDir = filepath.Join(rootDir, "root")
	for key, m := range snap.Modules {
		dir := filepath.Join(rootDir, filepath.FromSlash(m.Dir))
		if rel, err := filepath.Rel(base, dir); err != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("module %q of the snapshot lies outside the configuration", key)
		}
		for name, body := range m.Files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("file %q of module %q lies outside the module", name, key)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(p, body, 0o600); err != nil {
				return nil, err
			}
		}
	}
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(rootDir, jointRootMarker), nil, 0o600); err != nil {
		return nil, err
	}
	if len(b.Lock) > 0 {
		if err := os.WriteFile(filepath.Join(rootDir, ".terraform.lock.hcl"), b.Lock, 0o600); err != nil {
			return nil, err
		}
	}
	for _, u := range b.Units {
		body, _ := json.MarshalIndent(u, "", "  ")
		if err := os.WriteFile(sess.path("units", u.ID+".json"), body, 0o600); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(sess.path(fmt.Sprintf("doc-%d.json", stub.Serial)), b.Doc, 0o600); err != nil {
		return nil, err
	}

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	jointClearUnitEnv()
	if err := os.Chdir(rootDir); err != nil {
		return nil, err
	}
	m, read := c.capture(sess)
	if code := (&InitCommand{Meta: m}).Run([]string{"-input=false", "-no-color"}); code != 0 {
		out, errOut := read()
		return nil, fmt.Errorf("init of the joint root rebuilt from the plan file failed\n%s%s", out, errOut)
	}
	st := jointState{Serial: stub.Serial, OK: true, Units: stub.Units, Root: rootDir}
	if err := sess.saveState(st); err != nil {
		return nil, err
	}
	return sess, nil
}

// jointClearUnitEnv drops what the tool set for one unit, which is not the
// composed root's to read.
func jointClearUnitEnv() {
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(k, "TF_VAR_") || strings.HasPrefix(k, "TF_CLI_ARGS") || k == "TF_DATA_DIR" || k == "TF_WORKSPACE" {
			os.Unsetenv(k)
		}
	}
}

// jointRerunCommand is the tool's command that plans again: the command this
// apply runs under, with its apply or deploy step made a plan.
func jointRerunCommand(tool *jointTool) string {
	if tool == nil || len(tool.Args) == 0 {
		return ""
	}
	out := []string{filepath.Base(tool.Args[0])}
	swapped := false
	for _, a := range tool.Args[1:] {
		switch {
		case a == "-auto-approve" || a == "--auto-approve":
			continue
		case !swapped && (a == "apply" || a == "deploy"):
			a, swapped = "plan", true
		}
		out = append(out, a)
	}
	if !swapped {
		return ""
	}
	return strings.Join(out, " ")
}

// jointInputDiff names the inputs the tool gives a unit now with other values
// than the reviewed plan was made with. An input the tool does not give now is
// not compared, and neither is one that held another unit's placeholder: its
// value was unknown at review and is known now.
func jointInputDiff(reviewed, now jointUnit) []string {
	var out []string
	for name, rv := range reviewed.Vars {
		if bytes.Contains(rv.Value, []byte("(known after apply from ")) {
			continue
		}
		nv, ok := now.Vars[name]
		if !ok {
			continue
		}
		var a, b any
		if json.Unmarshal(rv.Value, &a) != nil || json.Unmarshal(nv.Value, &b) != nil || !reflect.DeepEqual(a, b) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// saveReviewed keeps a unit's slice of a reviewed plan for this run. Atmos's
// deploy shows the stored plan, makes a fresh one, and applies the fresh one;
// the slice kept here is what that apply is held to.
func (s *jointSession) saveReviewed(unit string, slice map[string]any) error {
	if err := os.MkdirAll(s.path("reviewed"), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(slice)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path("reviewed", jointSafe(unit)+".json"), b, 0o600)
}

func (s *jointSession) loadReviewed(unit string) (map[string]any, bool) {
	b, err := os.ReadFile(s.path("reviewed", jointSafe(unit)+".json"))
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	return m, true
}

// jointSliceDiff compares a unit's reviewed slice with the slice about to be
// applied: the same managed resources, the same actions, the same values
// before, and the same planned values, except where a value is unknown in
// either plan (known after apply). It returns one line per difference, with
// attribute names and never values.
func jointSliceDiff(reviewed, fresh map[string]any) []string {
	index := func(doc map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		list, _ := doc["resource_changes"].([]any)
		for _, it := range list {
			m, _ := it.(map[string]any)
			if m == nil || m["mode"] == "data" {
				continue
			}
			ch, _ := m["change"].(map[string]any)
			if ch == nil || jointActions(ch) == "no-op" {
				continue
			}
			addr, _ := m["address"].(string)
			out[addr] = ch
		}
		return out
	}
	r, f := index(reviewed), index(fresh)
	addrs := map[string]bool{}
	for a := range r {
		addrs[a] = true
	}
	for a := range f {
		addrs[a] = true
	}
	keys := make([]string, 0, len(addrs))
	for a := range addrs {
		keys = append(keys, a)
	}
	sort.Strings(keys)
	var out []string
	for _, a := range keys {
		rc, inR := r[a]
		fc, inF := f[a]
		switch {
		case !inF:
			out = append(out, fmt.Sprintf("%s  %s in the reviewed plan, not in this one", a, jointActions(rc)))
		case !inR:
			out = append(out, fmt.Sprintf("%s  %s in this plan, not in the reviewed one", a, jointActions(fc)))
		case jointActions(rc) != jointActions(fc):
			out = append(out, fmt.Sprintf("%s  %s in the reviewed plan, %s in this one", a, jointActions(rc), jointActions(fc)))
		default:
			var paths []string
			jointValueDiff("", rc["before"], fc["before"], nil, nil, &paths)
			jointValueDiff("", rc["after"], fc["after"], rc["after_unknown"], fc["after_unknown"], &paths)
			if len(paths) > 0 {
				sort.Strings(paths)
				out = append(out, fmt.Sprintf("%s  plans other values for %s", a, strings.Join(jointUniq(paths), ", ")))
			}
		}
	}
	return out
}

func jointActions(ch map[string]any) string {
	list, _ := ch["actions"].([]any)
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, fmt.Sprint(a))
	}
	return strings.Join(parts, ",")
}

func jointUniq(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// jointValueDiff walks two values with their unknown masks and records the
// paths at which they differ. A part unknown on either side is not compared.
func jointValueDiff(path string, a, b, ua, ub any, out *[]string) {
	if x, ok := ua.(bool); ok && x {
		return
	}
	if x, ok := ub.(bool); ok && x {
		return
	}
	at := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			if ua == nil && ub == nil || !reflect.DeepEqual(a, b) {
				*out = append(*out, jointTop(path))
			}
			return
		}
		mua, _ := ua.(map[string]any)
		mub, _ := ub.(map[string]any)
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		for k := range mua {
			keys[k] = true
		}
		for k := range mub {
			keys[k] = true
		}
		for k := range keys {
			jointValueDiff(at(k), av[k], bv[k], mua[k], mub[k], out)
		}
		return
	case []any:
		bv, ok := b.([]any)
		if ok && len(av) == len(bv) {
			lua, _ := ua.([]any)
			lub, _ := ub.([]any)
			for i := range av {
				var x, y any
				if i < len(lua) {
					x = lua[i]
				}
				if i < len(lub) {
					y = lub[i]
				}
				jointValueDiff(path, av[i], bv[i], x, y, out)
			}
			return
		}
	}
	if !reflect.DeepEqual(a, b) {
		*out = append(*out, jointTop(path))
	}
}

// jointTop is the attribute a path is under, the granularity a refusal names.
func jointTop(path string) string {
	if path == "" {
		return "the object"
	}
	top, _, _ := strings.Cut(path, ".")
	return top
}
