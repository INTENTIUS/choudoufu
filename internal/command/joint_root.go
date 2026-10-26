// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// jointSensitiveVarsFile is the variables file of the composed root that
// holds its sensitive inputs (INTENTIUS/choudoufu#2270). It is not part of
// the root's configuration, so the plan's configuration snapshot never
// holds one of these values.
const jointSensitiveVarsFile = "joint-sensitive.auto.tfvars.json"

// jointSensitiveInput is one input a unit declares sensitive, passed to the
// composed root as a root variable of its own: Unit and Var name it in the
// unit, Value is its value.
type jointSensitiveInput struct {
	Unit  string
	Var   string
	Type  cty.Type
	Value json.RawMessage
}

// jointSensitiveVarName is the composed root's variable for input name of
// unit, beside the root's "<unit>__<output>" outputs.
func jointSensitiveVarName(unit, name string) string {
	return unit + "__" + name
}

// renderJointRoot writes the text of the composed root that joins units
// (GitHub issue #2213): one module call per unit, each carrying the inputs
// the tool gave that unit, and one root output per declared unit output so
// the plan reports what each unit will publish. An input holding the
// placeholder of another unit's output ("known after apply") becomes a
// reference to that module's output, which is what carries the unknown.
func renderJointRoot(rootDir string, units []jointUnit) (string, error) {
	text, _, err := renderJointRootInputs(rootDir, units)
	return text, err
}

// renderJointRootInputs is [renderJointRoot] with the sensitive inputs the
// root takes as variables, by root variable name.
func renderJointRootInputs(rootDir string, units []jointUnit) (string, map[string]jointSensitiveInput, error) {
	byID := map[string]*jointUnit{}
	for i := range units {
		byID[units[i].ID] = &units[i]
	}
	resolved, err := jointResolve(units)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	inputs := map[string]jointSensitiveInput{}
	for _, u := range units {
		src, err := jointRel(rootDir, u.Dir)
		if err != nil {
			return "", nil, err
		}
		fmt.Fprintf(&b, "module %q {\n  source = %q\n", u.ID, src)
		names := make([]string, 0, len(u.Vars))
		for n := range u.Vars {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			// An input read from another unit's outputs is that unit's
			// planned output, not the value the tool read (#2231).
			if expr, ok := resolved.exprs[u.ID][n]; ok {
				fmt.Fprintf(&b, "  %s = %s\n", n, expr)
				continue
			}
			v := u.Vars[n]
			ty, err := ctyjson.UnmarshalType(v.Type)
			if err != nil {
				return "", nil, fmt.Errorf("unit %s, variable %s: %w", u.ID, n, err)
			}
			val, err := ctyjson.Unmarshal(v.Value, ty)
			if err != nil {
				return "", nil, fmt.Errorf("unit %s, variable %s: %w", u.ID, n, err)
			}
			expr, err := jointHCL(val, byID)
			if err != nil {
				return "", nil, fmt.Errorf("unit %s, variable %s: %w", u.ID, n, err)
			}
			if v.Sensitive {
				// The unit declares it sensitive (#2219). A value that reads
				// no other unit's output is a sensitive root variable of its
				// own, set from jointSensitiveVarsFile, so neither the
				// configuration snapshot nor the configuration in show -json
				// holds it, and each unit's plan file can leave out the
				// others' values (#2270). A value that reads another unit's
				// output is an expression over that module and stays in the
				// configuration, wrapped in sensitive().
				if plain, perr := jointHCL(val, nil); perr == nil && plain == expr {
					rv := jointSensitiveVarName(u.ID, n)
					inputs[rv] = jointSensitiveInput{Unit: u.ID, Var: n, Type: ty, Value: v.Value}
					fmt.Fprintf(&b, "  %s = var.%s\n", n, rv)
					continue
				}
				expr = "sensitive(" + expr + ")"
			}
			fmt.Fprintf(&b, "  %s = %s\n", n, expr)
		}
		b.WriteString("}\n\n")
	}
	for _, u := range units {
		for _, o := range u.Outputs {
			fmt.Fprintf(&b, "output %q {\n  value = module.%s.%s\n", u.ID+"__"+o.Name, u.ID, o.Name)
			if o.Sensitive {
				b.WriteString("  sensitive = true\n")
			}
			b.WriteString("}\n\n")
		}
	}
	rvs := make([]string, 0, len(inputs))
	for rv := range inputs {
		rvs = append(rvs, rv)
	}
	sort.Strings(rvs)
	for _, rv := range rvs {
		fmt.Fprintf(&b, "variable %q {\n  type      = %s\n  sensitive = true\n}\n\n", rv, typeexpr.TypeString(inputs[rv].Type))
	}
	return b.String(), inputs, nil
}

// jointRel is the module source for a unit directory, relative to the root.
func jointRel(rootDir, unitDir string) (string, error) {
	rel, err := filepath.Rel(rootDir, unitDir)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel, nil
}

// jointHCL writes v as an HCL expression.
func jointHCL(v cty.Value, units map[string]*jointUnit) (string, error) {
	v, _ = v.UnmarkDeep()
	if !v.IsKnown() {
		return "", fmt.Errorf("an unknown value cannot be an input")
	}
	if v.IsNull() {
		return "null", nil
	}
	ty := v.Type()
	switch {
	case ty == cty.String:
		return jointStringHCL(v.AsString(), units)
	case ty == cty.Number:
		return v.AsBigFloat().Text('f', -1), nil
	case ty == cty.Bool:
		if v.True() {
			return "true", nil
		}
		return "false", nil
	case ty.IsListType() || ty.IsSetType() || ty.IsTupleType():
		var parts []string
		for it := v.ElementIterator(); it.Next(); {
			_, e := it.Element()
			s, err := jointHCL(e, units)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case ty.IsMapType() || ty.IsObjectType():
		var keys []string
		vals := v.AsValueMap()
		for k := range vals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			s, err := jointHCL(vals[k], units)
			if err != nil {
				return "", err
			}
			parts = append(parts, jointQuote(k)+" = "+s)
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	return "", fmt.Errorf("a value of type %s cannot be written as an input", ty.FriendlyName())
}

// jointStringHCL writes s as a string, a template when it holds the
// placeholder of another unit's output, and as the bare reference when the
// placeholder is the whole string.
func jointStringHCL(s string, units map[string]*jointUnit) (string, error) {
	locs := jointSentinelRE.FindAllStringSubmatchIndex(s, -1)
	if len(locs) == 0 {
		return jointQuote(s), nil
	}
	ref := func(m []int) (string, error) {
		unit, out := s[m[2]:m[3]], s[m[4]:m[5]]
		u := units[unit]
		if u == nil {
			return "", fmt.Errorf("the input reads output %q of unit %q, which is not part of this run's joint plan", out, unit)
		}
		for _, o := range u.Outputs {
			if o.Name == out {
				return "module." + unit + "." + out, nil
			}
		}
		return "", fmt.Errorf("the input reads output %q of unit %q, which declares no such output", out, unit)
	}
	if len(locs) == 1 && locs[0][0] == 0 && locs[0][1] == len(s) {
		return ref(locs[0])
	}
	var b strings.Builder
	b.WriteByte('"')
	last := 0
	for _, m := range locs {
		b.WriteString(jointEscape(s[last:m[0]]))
		r, err := ref(m)
		if err != nil {
			return "", err
		}
		b.WriteString("${" + r + "}")
		last = m[1]
	}
	b.WriteString(jointEscape(s[last:]))
	b.WriteByte('"')
	return b.String(), nil
}

func jointQuote(s string) string { return `"` + jointEscape(s) + `"` }

func jointEscape(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case (r == '$' || r == '%') && i+1 < len(rs) && rs[i+1] == '{':
			b.WriteRune(r)
			b.WriteRune(r)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeJointRoot lays out the root directory for units and reports whether
// the text of its configuration changed.
func writeJointRoot(rootDir string, units []jointUnit) (string, bool, error) {
	text, inputs, err := renderJointRootInputs(rootDir, units)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return "", false, err
	}
	main := filepath.Join(rootDir, "main.tf")
	old, _ := os.ReadFile(main)
	changed := string(old) != text
	if changed {
		if err := os.WriteFile(main, []byte(text), 0o600); err != nil {
			return "", false, err
		}
	}
	// The sensitive inputs' values (#2270). What is returned is hashed to
	// tell a new joint plan from the last one, so it covers them too.
	varsPath := filepath.Join(rootDir, jointSensitiveVarsFile)
	if len(inputs) == 0 {
		_ = os.Remove(varsPath)
	} else {
		vals := make(map[string]json.RawMessage, len(inputs))
		for rv, in := range inputs {
			vals[rv] = in.Value
		}
		body, err := json.Marshal(vals)
		if err != nil {
			return "", false, err
		}
		if err := os.WriteFile(varsPath, body, 0o600); err != nil {
			return "", false, err
		}
		text += "\n" + string(body)
	}
	if err := os.WriteFile(filepath.Join(rootDir, jointRootMarker), nil, 0o600); err != nil {
		return "", false, err
	}
	// Seed the lock file from a unit's, so the root installs the provider
	// versions the units were initialized with.
	lock := filepath.Join(rootDir, ".terraform.lock.hcl")
	if _, err := os.Stat(lock); err != nil {
		for _, u := range units {
			if b, err := os.ReadFile(filepath.Join(u.Dir, ".terraform.lock.hcl")); err == nil {
				_ = os.WriteFile(lock, b, 0o600)
				break
			}
		}
	}
	return text, changed, nil
}
