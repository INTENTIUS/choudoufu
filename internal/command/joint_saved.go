// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plans/planfile"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
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

	// Scrubbed says Plan and Doc hold no sensitive planned values (#2277):
	// each is null, and Sensitive keeps a keyed hash of each one that was
	// known. An apply plans again, which recomputes them, and refuses when
	// one does not match its hash.
	Scrubbed  bool                 `json:"scrubbed,omitempty"`
	Sensitive []jointSensitiveHash `json:"sensitive,omitempty"`
}

// jointSensitiveHash is one sensitive planned value a joint plan file left
// out (#2277), and the HMAC of it under the planning session's key
// ([jointSession.sensitiveKey]).
type jointSensitiveHash struct {
	// Unit is the unit the value belongs to.
	Unit string `json:"unit"`
	// Address is the resource instance change ([planfile.ChangeAddress]) or
	// the output of the composed root.
	Address string `json:"address"`
	Output  bool   `json:"output,omitempty"`
	// Side is "before" or "after", and Path where the value is in it (none:
	// the whole side).
	Side string          `json:"side"`
	Path []jointPathStep `json:"path,omitempty"`
	// Raw says the plan carried no schema for the change, so the whole side
	// was dropped and its encoding is what HMAC covers.
	Raw bool `json:"raw,omitempty"`
	// HMAC is empty for a value not wholly known at plan time, which an
	// apply does not compare (the reviewed plan said "known after apply").
	HMAC string `json:"hmac,omitempty"`
}

// jointPathStep is one step of a cty.Path: an attribute name, or an index
// key in cty's JSON form with its type.
type jointPathStep struct {
	Attr string          `json:"attr,omitempty"`
	Key  json.RawMessage `json:"key,omitempty"`
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
	_, inputs, err := renderJointRootInputs(s.rootDir(st), units)
	if err != nil {
		return err
	}
	// The sensitive planned values are dropped from what every plan file
	// carries, each kept as an HMAC under the session's key (#2277).
	key, err := s.sensitiveKey()
	if err != nil {
		return fmt.Errorf("making the key of this session's sensitive value hashes: %w", err)
	}
	plan, doc, hashes, err := jointDropSensitive(plan, doc, key)
	if err != nil {
		return fmt.Errorf("leaving sensitive planned values out of the plan files: %w", err)
	}
	for _, u := range units {
		if u.Out == "" {
			continue
		}
		b, err := jointBundleFor(u.ID, plan, doc, units, lock, inputs)
		if err != nil {
			return err
		}
		b.Scrubbed, b.Sensitive = true, hashes
		body, err := json.Marshal(jointStub{Magic: 1, Session: s.dir, Unit: u.ID, Serial: st.Serial, Units: st.Units, Run: filepath.Base(s.dir), Tool: tool.Kind, Bundle: b})
		if err != nil {
			return err
		}
		// 0600: the file carries the joint plan. Its sensitive planned
		// values are left out (#2277), but its other planned values, and
		// this unit's own sensitive inputs (#2270), are in it.
		tmp := u.Out + ".choudoufu-tmp"
		if err := os.WriteFile(tmp, body, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, u.Out); err != nil {
			return err
		}
	}
	return nil
}

// jointBundleFor is the bundle unit's plan file carries: the joint plan
// with every other unit's sensitive inputs left out (#2270). inputs are the
// composed root's sensitive input variables ([renderJointRootInputs]). The
// plan's values for those variables are dropped, show -json's variables
// lose them, and the units' Vars hold null for them, marked Withheld. The
// unit's own sensitive inputs stay, so the file of each unit is where its
// values are found again at apply ([jointRestoreWithheld]).
func jointBundleFor(unit string, plan, doc []byte, units []jointUnit, lock []byte, inputs map[string]jointSensitiveInput) (*jointBundle, error) {
	withheld := map[string]bool{}
	for rv, in := range inputs {
		if in.Unit != unit {
			withheld[rv] = true
		}
	}
	if len(withheld) == 0 {
		return &jointBundle{Plan: plan, Doc: doc, Units: units, Lock: lock}, nil
	}
	stripped, err := planfile.RewriteVariableValues(plan, func(vv map[string]plans.DynamicValue) {
		for rv := range withheld {
			delete(vv, rv)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("leaving other units' sensitive inputs out of the plan file of unit %s: %w", unit, err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(doc, &parsed); err == nil {
		var vars map[string]json.RawMessage
		if err := json.Unmarshal(parsed["variables"], &vars); err == nil {
			for rv := range withheld {
				delete(vars, rv)
			}
			if vb, err := json.Marshal(vars); err == nil {
				parsed["variables"] = vb
				if db, err := json.Marshal(parsed); err == nil {
					doc = db
				}
			}
		}
	}
	out := make([]jointUnit, len(units))
	for i, u := range units {
		out[i] = u
		if u.ID == unit {
			continue
		}
		vars := make(map[string]jointVar, len(u.Vars))
		for n, v := range u.Vars {
			if withheld[jointSensitiveVarName(u.ID, n)] {
				v.Value, v.Withheld = json.RawMessage("null"), true
			}
			vars[n] = v
		}
		out[i].Vars = vars
	}
	return &jointBundle{Plan: stripped, Doc: doc, Units: out, Lock: lock}, nil
}

// jointRestoreWithheld puts back into stub's bundle the sensitive inputs of
// the other units, which their own plan files carry (#2270). Each unit's
// file is looked for where it lies relative to this one as at plan time
// (Terragrunt's --out-dir keeps that layout), then at the path it was
// planned to. A file found must be of the same tool run and joint plan.
func jointRestoreWithheld(stub jointStub, stubPath string) error {
	b := stub.Bundle
	var self jointUnit
	for _, u := range b.Units {
		if u.ID == stub.Unit {
			self = u
		}
	}
	values := map[string]plans.DynamicValue{}
	for i, u := range b.Units {
		var names []string
		for n, v := range u.Vars {
			if v.Withheld {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		other, where, err := jointFindUnitFile(stub, stubPath, self, u)
		if err != nil {
			return fmt.Errorf("unit %s's sensitive inputs (%s) are kept only in its own plan file, and %s. Apply with every unit's plan file where it was planned (for Terragrunt, the same --out-dir layout)", u.ID, strings.Join(names, ", "), err)
		}
		vv, err := planfile.ReadVariableValues(other.Bundle.Plan)
		if err != nil {
			return fmt.Errorf("reading the plan file of unit %s at %s: %w", u.ID, where, err)
		}
		var theirs jointUnit
		for _, ou := range other.Bundle.Units {
			if ou.ID == u.ID {
				theirs = ou
			}
		}
		vars := make(map[string]jointVar, len(u.Vars))
		for n, v := range u.Vars {
			vars[n] = v
		}
		for _, n := range names {
			rv := jointSensitiveVarName(u.ID, n)
			dv, ok := vv[rv]
			tv, tok := theirs.Vars[n]
			if !ok || !tok || tv.Withheld {
				return fmt.Errorf("the plan file of unit %s at %s does not carry its sensitive input %s", u.ID, where, n)
			}
			values[rv] = dv
			vars[n] = tv
		}
		b.Units[i].Vars = vars
	}
	if len(values) == 0 {
		return nil
	}
	plan, err := planfile.RewriteVariableValues(b.Plan, func(vv map[string]plans.DynamicValue) {
		for rv, dv := range values {
			vv[rv] = dv
		}
	})
	if err != nil {
		return err
	}
	b.Plan = plan
	return nil
}

// jointFindUnitFile finds the plan file of unit u for an apply of the file
// at stubPath, which is self's.
func jointFindUnitFile(stub jointStub, stubPath string, self, u jointUnit) (jointStub, string, error) {
	var candidates []string
	if self.Out != "" && u.Out != "" {
		if rel, err := filepath.Rel(filepath.Dir(self.Out), u.Out); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(stubPath), rel))
		}
	}
	if u.Out != "" {
		candidates = append(candidates, u.Out)
	}
	if len(candidates) == 0 {
		return jointStub{}, "", fmt.Errorf("the unit asked for no plan file")
	}
	for _, c := range candidates {
		other, ok := readJointStub(c)
		if !ok || other.Bundle == nil || other.Unit != u.ID {
			continue
		}
		if other.Run != stub.Run || other.Serial != stub.Serial {
			continue
		}
		return other, c, nil
	}
	return jointStub{}, "", fmt.Errorf("no plan file of this joint plan for it was found at %s", strings.Join(candidates, " or "))
}

// jointDropSensitive returns the joint plan file plan and its show -json
// doc with every sensitive planned value set to null, and a keyed hash of
// each (#2277). The plan file's part is [planfile.DropSensitiveValues]; the
// doc's values are set to null wherever its *_sensitive masks say so.
//
// The hash is an HMAC-SHA256 under key, the planning session's own random
// key, which stays in the session's 0700 directory and is never written to
// a plan file. It covers the value's address, side and path with the value,
// so a hash cannot be moved to another value, and without the key a
// low-entropy secret cannot be brute-forced from the file.
func jointDropSensitive(plan, doc, key []byte) ([]byte, []byte, []jointSensitiveHash, error) {
	var hashes []jointSensitiveHash
	var keepErr error
	out, err := planfile.DropSensitiveValues(plan, func(d planfile.DroppedValue) {
		h := jointSensitiveHash{Unit: d.Module, Address: d.Address, Output: d.Output, Side: d.Side}
		if d.Output && h.Unit == "" {
			// A root output of the composed root is "<unit>__<output>".
			name := strings.TrimPrefix(d.Address, "output.")
			h.Unit, _, _ = strings.Cut(name, "__")
		}
		path, err := jointEncodePath(d.Path)
		if err != nil {
			keepErr = err
			return
		}
		h.Path = path
		switch {
		case d.Raw != nil:
			h.Raw = true
			h.HMAC = jointSensitiveMAC(key, h, d.Raw)
		case d.Value.IsWhollyKnown():
			payload, err := ctyjson.Marshal(d.Value, cty.DynamicPseudoType)
			if err != nil {
				keepErr = err
				return
			}
			h.HMAC = jointSensitiveMAC(key, h, payload)
		}
		hashes = append(hashes, h)
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if keepErr != nil {
		return nil, nil, nil, keepErr
	}
	scrubbed, err := jointScrubDoc(doc)
	if err != nil {
		return nil, nil, nil, err
	}
	return out, scrubbed, hashes, nil
}

// jointSensitiveMAC is the HMAC of payload, the value's JSON with its type
// (or its encoding, for a Raw record), bound to where the value is.
func jointSensitiveMAC(key []byte, h jointSensitiveHash, payload []byte) string {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "choudoufu-joint-sensitive-v1\x00%s\x00%t\x00%s\x00%t\x00", h.Address, h.Output, h.Side, h.Raw)
	for _, st := range h.Path {
		fmt.Fprintf(m, "%s\x00%s\x00", st.Attr, st.Key)
	}
	m.Write([]byte{0})
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

func jointEncodePath(p cty.Path) ([]jointPathStep, error) {
	var out []jointPathStep
	for _, step := range p {
		switch st := step.(type) {
		case cty.GetAttrStep:
			out = append(out, jointPathStep{Attr: st.Name})
		case cty.IndexStep:
			b, err := ctyjson.Marshal(st.Key, cty.DynamicPseudoType)
			if err != nil {
				return nil, err
			}
			out = append(out, jointPathStep{Key: b})
		default:
			return nil, fmt.Errorf("unsupported path step %T", step)
		}
	}
	return out, nil
}

func jointDecodePath(steps []jointPathStep) (cty.Path, error) {
	var p cty.Path
	for _, st := range steps {
		if len(st.Key) == 0 {
			p = p.GetAttr(st.Attr)
			continue
		}
		k, err := ctyjson.Unmarshal(st.Key, cty.DynamicPseudoType)
		if err != nil {
			return nil, err
		}
		p = p.Index(k)
	}
	return p, nil
}

// where names the value for a refusal: the unit, the address, the side and
// path. Never the value.
func (h jointSensitiveHash) where() string {
	p, _ := jointDecodePath(h.Path)
	at := h.Side
	if len(p) > 0 {
		at += " " + strings.TrimPrefix(tfdiags.FormatCtyPath(p), ".")
	}
	return fmt.Sprintf("unit %s: %s (sensitive %s)", h.Unit, h.Address, at)
}

// jointScrubDoc sets to null each value of a show -json document that its
// mask marks sensitive: resource changes and drift (before/after against
// before_sensitive/after_sensitive), output changes, and the values in
// planned_values and prior_state (against sensitive_values, and an
// output's "sensitive").
func jointScrubDoc(doc []byte) ([]byte, error) {
	if len(doc) == 0 {
		return doc, nil
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var parsed map[string]any
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("reading the joint plan's show -json: %w", err)
	}
	change := func(ch any) {
		m, _ := ch.(map[string]any)
		if m == nil {
			return
		}
		for _, side := range []string{"before", "after"} {
			if v, ok := m[side]; ok {
				m[side] = jointScrubMasked(v, m[side+"_sensitive"])
			}
		}
	}
	for _, key := range []string{"resource_changes", "resource_drift"} {
		list, _ := parsed[key].([]any)
		for _, rc := range list {
			if m, _ := rc.(map[string]any); m != nil {
				change(m["change"])
			}
		}
	}
	if outs, _ := parsed["output_changes"].(map[string]any); outs != nil {
		for _, ch := range outs {
			change(ch)
		}
	}
	values := func(v any) {
		vals, _ := v.(map[string]any)
		if vals == nil {
			return
		}
		if outs, _ := vals["outputs"].(map[string]any); outs != nil {
			for _, o := range outs {
				if m, _ := o.(map[string]any); m != nil && m["sensitive"] == true {
					m["value"] = nil
				}
			}
		}
		var module func(any)
		module = func(mv any) {
			mod, _ := mv.(map[string]any)
			if mod == nil {
				return
			}
			rs, _ := mod["resources"].([]any)
			for _, r := range rs {
				if m, _ := r.(map[string]any); m != nil {
					if v, ok := m["values"]; ok {
						m["values"] = jointScrubMasked(v, m["sensitive_values"])
					}
				}
			}
			cms, _ := mod["child_modules"].([]any)
			for _, cm := range cms {
				module(cm)
			}
		}
		module(vals["root_module"])
	}
	values(parsed["planned_values"])
	if ps, _ := parsed["prior_state"].(map[string]any); ps != nil {
		values(ps["values"])
	}
	return json.Marshal(parsed)
}

// jointScrubMasked is v with null wherever mask (show -json's form: true, or
// an object or array of masks) marks it sensitive.
func jointScrubMasked(v, mask any) any {
	switch m := mask.(type) {
	case bool:
		if m {
			return nil
		}
	case map[string]any:
		if obj, ok := v.(map[string]any); ok {
			for k, sub := range m {
				if child, ok := obj[k]; ok {
					obj[k] = jointScrubMasked(child, sub)
				}
			}
		}
	case []any:
		if list, ok := v.([]any); ok {
			for i := range list {
				if i < len(m) {
					list[i] = jointScrubMasked(list[i], m[i])
				}
			}
		}
	}
	return v
}

// jointSchemaFor resolves the schema of a resource type, or nil.
type jointSchemaFor func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema

// jointCheckSensitive checks the values of fresh, a plan made again at
// apply, against the hashes a joint plan file kept of the sensitive values
// it left out (#2277), and returns one line per value that does not match,
// naming the unit and the address. A change fresh does not make is not
// checked here: the approval comparison refuses it as missing. When
// approved, the plan of the file, is not nil, each value that matches is
// put back into it from fresh, so the approval comparison that follows
// compares the two plans whole.
func jointCheckSensitive(hashes []jointSensitiveHash, key []byte, fresh, approved *plans.Plan, schemaFor jointSchemaFor) []string {
	if fresh == nil || fresh.Changes == nil {
		return nil
	}
	freshRes := map[string]*plans.ResourceInstanceChangeSrc{}
	for _, rc := range fresh.Changes.Resources {
		freshRes[planfile.ChangeAddress(rc)] = rc
	}
	freshOut := map[string]*plans.OutputChangeSrc{}
	for _, oc := range fresh.Changes.Outputs {
		freshOut[oc.Addr.String()] = oc
	}
	approvedRes := map[string]*plans.ResourceInstanceChangeSrc{}
	approvedOut := map[string]*plans.OutputChangeSrc{}
	if approved != nil && approved.Changes != nil {
		for _, rc := range approved.Changes.Resources {
			approvedRes[planfile.ChangeAddress(rc)] = rc
		}
		for _, oc := range approved.Changes.Outputs {
			approvedOut[oc.Addr.String()] = oc
		}
	}
	side := func(cs *plans.ChangeSrc, name string) *plans.DynamicValue {
		if name == "before" {
			return &cs.Before
		}
		return &cs.After
	}
	var out []string
	for _, h := range hashes {
		if h.HMAC != "" && len(key) == 0 {
			out = append(out, h.where()+": the key of the plan's hashes is not here")
			continue
		}
		var freshCS, approvedCS *plans.ChangeSrc
		var ty cty.Type
		if h.Output {
			oc := freshOut[h.Address]
			if oc == nil {
				continue
			}
			freshCS, ty = &oc.ChangeSrc, cty.DynamicPseudoType
			if aoc := approvedOut[h.Address]; aoc != nil {
				approvedCS = &aoc.ChangeSrc
			}
		} else {
			rc := freshRes[h.Address]
			if rc == nil {
				continue
			}
			freshCS = &rc.ChangeSrc
			if arc := approvedRes[h.Address]; arc != nil {
				approvedCS = &arc.ChangeSrc
			}
			if !h.Raw {
				var schema *providers.Schema
				if schemaFor != nil {
					schema = schemaFor(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
				}
				if schema == nil || schema.Block == nil {
					out = append(out, h.where()+": this run has no schema for "+rc.Addr.Resource.Resource.Type+" to check it with")
					continue
				}
				ty = schema.Block.ImpliedType()
			}
		}
		fv := side(freshCS, h.Side)
		if h.Raw {
			if jointSensitiveMAC(key, h, *fv) != h.HMAC {
				out = append(out, h.where()+": this apply plans another value than the reviewed plan")
				continue
			}
			if approvedCS != nil {
				*side(approvedCS, h.Side) = fv.Copy()
			}
			continue
		}
		path, err := jointDecodePath(h.Path)
		if err != nil {
			out = append(out, h.where()+": "+err.Error())
			continue
		}
		whole, err := fv.Decode(ty)
		if err != nil {
			out = append(out, h.where()+": this apply's value cannot be read: "+err.Error())
			continue
		}
		if whole == cty.NilVal {
			whole = cty.NullVal(ty)
		}
		at, err := path.Apply(whole)
		if err != nil {
			out = append(out, h.where()+": this apply plans no value there")
			continue
		}
		if h.HMAC != "" && at.IsWhollyKnown() {
			payload, err := ctyjson.Marshal(at, cty.DynamicPseudoType)
			if err != nil || jointSensitiveMAC(key, h, payload) != h.HMAC {
				out = append(out, h.where()+": this apply plans another value than the reviewed plan")
				continue
			}
		}
		if approvedCS == nil {
			continue
		}
		av := side(approvedCS, h.Side)
		aval := cty.NullVal(ty)
		if len(*av) > 0 {
			if aval, err = av.Decode(ty); err != nil {
				out = append(out, h.where()+": the reviewed plan's value cannot be read: "+err.Error())
				continue
			}
		}
		if aval, err = planfile.SetPath(aval, path, at); err == nil {
			var enc plans.DynamicValue
			if enc, err = plans.NewDynamicValue(aval, ty); err == nil {
				*av = enc
			}
		}
		if err != nil {
			out = append(out, h.where()+": putting the value back into the reviewed plan failed: "+err.Error())
		}
	}
	sort.Strings(out)
	return out
}

// jointSensitiveCheck is what an apply of a scrubbed joint plan file checks
// its fresh plan against (#2277). It is set on the apply's Meta.
type jointSensitiveCheck struct {
	Hashes []jointSensitiveHash
	Key    []byte
}

const summaryJointSensitive = "The joint plan's sensitive values do not match this apply"

// guard runs before inner, the approval guard: it checks the fresh plan's
// sensitive values against the hashes, refusing on any that differs, and
// puts the matched values back into the approved plan for inner to compare.
func (c *jointSensitiveCheck) guard(approved *approvedPlan, inner func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics, refused *bool) func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics {
	return func(plan *plans.Plan, schemas *tofu.Schemas) tfdiags.Diagnostics {
		var diags tfdiags.Diagnostics
		schemaFor := func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema {
			if schemas == nil {
				return nil
			}
			s, _ := schemas.ResourceTypeConfig(provider, mode, typeName)
			return s
		}
		var ap *plans.Plan
		path, rerun := "", ""
		if approved != nil {
			ap, path, rerun = approved.Plan, approved.Path, approved.Rerun
		}
		if lines := jointCheckSensitive(c.Hashes, c.Key, plan, ap, schemaFor); len(lines) > 0 {
			*refused = true
			return diags.Append(tfdiags.Sourceless(tfdiags.Error, summaryJointSensitive, jointSensitiveDetail(path, rerun, lines)))
		}
		if inner != nil {
			return inner(plan, schemas)
		}
		return diags
	}
}

// jointSensitiveDetail is the refusal's paragraph. It names values by unit,
// address and attribute, never by value.
func jointSensitiveDetail(path, rerun string, lines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The plan file %s leaves out the sensitive values it planned and keeps a keyed hash of each. This apply planned again, and these values do not match the reviewed plan's, or cannot be checked:\n", path)
	b.WriteString(jointCappedLines(lines))
	b.WriteString("\n")
	if rerun != "" {
		fmt.Fprintf(&b, "Plan again with the command that made the reviewed plan, review that plan, and apply it:\n  %s\n\n", rerun)
	}
	fmt.Fprintf(&b, "Exit status %d says: send it back to review. Nothing was applied.", ExitApprovalRefused)
	return b.String()
}

func jointCappedLines(lines []string) string {
	const limit = 10
	var b strings.Builder
	for i, l := range lines {
		if i == limit {
			fmt.Fprintf(&b, "  ... and %d more\n", len(lines)-limit)
			break
		}
		fmt.Fprintf(&b, "  %s\n", l)
	}
	return b.String()
}

// jointMissingKeyDetail is the refusal when the key of a plan file's hashes
// is not on this machine: the session that planned is gone, or was on
// another machine.
func jointMissingKeyDetail(planPath, session string, hashes []jointSensitiveHash, err error) string {
	var lines []string
	for _, h := range hashes {
		if h.HMAC != "" {
			lines = append(lines, h.where())
		}
	}
	sort.Strings(lines)
	return fmt.Sprintf("%s leaves out the sensitive values it planned and keeps an HMAC of each, under a key that only the session that planned holds (%s). That key is not here (%s), so these values cannot be checked against what this apply plans:\n%s\nThe key never goes into a plan file, so that a short secret cannot be guessed from the file. Plan and apply where the planning session is still on the machine, or plan again in this job.", planPath, session, err, jointCappedLines(lines))
}

// jointHasHashes reports whether any hash needs the key.
func jointHasHashes(hashes []jointSensitiveHash) bool {
	for _, h := range hashes {
		if h.HMAC != "" {
			return true
		}
	}
	return false
}

// jointReadPlanWithSchemas reads the plan file at path, with a schema
// resolver over the schemas it embeds.
func jointReadPlanWithSchemas(path string) (*plans.Plan, jointSchemaFor, error) {
	reader, err := planfile.Open(path, encryption.PlanEncryptionDisabled())
	if err != nil {
		return nil, nil, err
	}
	plan, err := reader.ReadPlan()
	if err != nil {
		return nil, nil, err
	}
	schemas, err := reader.ReadSchemas()
	if err != nil && !errors.Is(err, planfile.ErrNoSchemas) {
		return nil, nil, err
	}
	return plan, func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema {
		ps, ok := schemas[provider]
		if !ok {
			return nil
		}
		s, _ := ps.SchemaForResourceType(mode, typeName)
		return s
	}, nil
}

// rebuildSession makes a session for this apply run out of a plan file whose
// own session is not here. Every unit's apply in the run finds the same one.
func (c *jointCommand) rebuildSession(stub jointStub, stubPath string, tool *jointTool) (*jointSession, error) {
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
	if err := jointRestoreWithheld(stub, stubPath); err != nil {
		return nil, err
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
		// A wired input was planned from its upstream's planned output; the
		// tool's value now is that output applied.
		if _, wired := reviewed.Wired[name]; wired {
			continue
		}
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

// jointReviewedSensitive is kept beside a reviewed slice that came from a
// scrubbed plan file (#2277): the unit's hashes, and the session that holds
// their key.
type jointReviewedSensitive struct {
	Session string               `json:"session"`
	Hashes  []jointSensitiveHash `json:"hashes,omitempty"`
}

func (s *jointSession) saveReviewedSensitive(unit string, rs jointReviewedSensitive) error {
	if err := os.MkdirAll(s.path("reviewed"), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path("reviewed", jointSafe(unit)+".sensitive.json"), b, 0o600)
}

func (s *jointSession) loadReviewedSensitive(unit string) (jointReviewedSensitive, bool) {
	var rs jointReviewedSensitive
	b, err := os.ReadFile(s.path("reviewed", jointSafe(unit)+".sensitive.json"))
	if err != nil || json.Unmarshal(b, &rs) != nil {
		return jointReviewedSensitive{}, false
	}
	return rs, true
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
	return jointSliceDiffSensitive(reviewed, fresh, false)
}

// jointSliceDiffSensitive is [jointSliceDiff], and with skipSensitive it
// also does not compare a value the reviewed slice marks sensitive: a
// scrubbed plan file holds null there (#2277), and its hashes are checked
// against the fresh plan instead.
func jointSliceDiffSensitive(reviewed, fresh map[string]any, skipSensitive bool) []string {
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
			var skipBefore, skipAfter any = nil, rc["after_unknown"]
			if skipSensitive {
				skipBefore = rc["before_sensitive"]
				skipAfter = jointMaskOr(skipAfter, rc["after_sensitive"])
			}
			jointValueDiff("", rc["before"], fc["before"], skipBefore, nil, &paths)
			jointValueDiff("", rc["after"], fc["after"], skipAfter, fc["after_unknown"], &paths)
			if len(paths) > 0 {
				sort.Strings(paths)
				out = append(out, fmt.Sprintf("%s  plans other values for %s", a, strings.Join(jointUniq(paths), ", ")))
			}
		}
	}
	return out
}

// jointMaskOr is the union of two show -json masks (true, or objects and
// arrays of masks).
func jointMaskOr(a, b any) any {
	if x, ok := a.(bool); ok && x {
		return true
	}
	if x, ok := b.(bool); ok && x {
		return true
	}
	switch am := a.(type) {
	case map[string]any:
		bm, ok := b.(map[string]any)
		if !ok {
			return a
		}
		out := map[string]any{}
		for k, v := range am {
			out[k] = v
		}
		for k, v := range bm {
			out[k] = jointMaskOr(out[k], v)
		}
		return out
	case []any:
		bl, ok := b.([]any)
		if !ok {
			return a
		}
		n := len(am)
		if len(bl) > n {
			n = len(bl)
		}
		out := make([]any, n)
		for i := range out {
			var x, y any
			if i < len(am) {
				x = am[i]
			}
			if i < len(bl) {
				y = bl[i]
			}
			out[i] = jointMaskOr(x, y)
		}
		return out
	}
	return b
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
