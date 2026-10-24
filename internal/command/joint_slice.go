// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"regexp"
	"strings"
)

// jointSliceDoc cuts one unit out of the "show -json" document of the joint
// plan, so that "show -json" on that unit's plan file reads as it would had
// the unit been planned alone: the module's step is taken off every address,
// and the root outputs the joint root declares for the unit are the unit's
// outputs.
func jointSliceDoc(doc map[string]any, id string) map[string]any {
	out := map[string]any{}
	if doc == nil {
		return out
	}
	prefix := "module." + id
	strip := func(addr string) string {
		switch {
		case addr == prefix:
			return ""
		case strings.HasPrefix(addr, prefix+"."):
			return addr[len(prefix)+1:]
		}
		return addr
	}
	inUnit := func(addr string) bool { return addr == prefix || strings.HasPrefix(addr, prefix+".") }

	for _, k := range []string{"format_version", "terraform_version", "timestamp", "errored", "applyable", "complete", "variables"} {
		if v, ok := doc[k]; ok && k != "variables" {
			out[k] = v
		}
	}
	rewriteChanges := func(list any) []any {
		var res []any
		items, _ := list.([]any)
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			addr, _ := m["address"].(string)
			if !inUnit(addr) {
				continue
			}
			c := map[string]any{}
			for k, v := range m {
				c[k] = v
			}
			c["address"] = strip(addr)
			if ma, ok := m["module_address"].(string); ok {
				if s := strip(ma); s == "" {
					delete(c, "module_address")
				} else {
					c["module_address"] = s
				}
			}
			if pa, ok := m["previous_address"].(string); ok {
				c["previous_address"] = strip(pa)
			}
			res = append(res, c)
		}
		return res
	}
	out["resource_changes"] = rewriteChanges(doc["resource_changes"])
	if d := rewriteChanges(doc["resource_drift"]); len(d) > 0 {
		out["resource_drift"] = d
	}

	// output_changes: the root outputs "<id>__<name>" are the unit's.
	outs := map[string]any{}
	planned := map[string]any{}
	if oc, ok := doc["output_changes"].(map[string]any); ok {
		for k, v := range oc {
			name, found := strings.CutPrefix(k, id+"__")
			if !found {
				continue
			}
			outs[name] = v
			if m, ok := v.(map[string]any); ok {
				if after, known := m["after"]; known && !jointAnyTrue(m["after_unknown"]) {
					planned[name] = map[string]any{"sensitive": m["after_sensitive"] == true, "value": after}
				}
			}
		}
	}
	out["output_changes"] = outs

	var cut func(mod map[string]any) map[string]any
	cut = func(mod map[string]any) map[string]any {
		r := map[string]any{}
		for k, v := range mod {
			switch k {
			case "resources":
				var rs []any
				for _, it := range v.([]any) {
					m := it.(map[string]any)
					c := map[string]any{}
					for kk, vv := range m {
						c[kk] = vv
					}
					if a, ok := m["address"].(string); ok {
						c["address"] = strip(a)
					}
					rs = append(rs, c)
				}
				r[k] = rs
			case "child_modules":
				var cs []any
				for _, it := range v.([]any) {
					cs = append(cs, cut(it.(map[string]any)))
				}
				r[k] = cs
			case "address":
				if a, ok := v.(string); ok {
					if s := strip(a); s != "" {
						r[k] = s
					}
				}
			default:
				r[k] = v
			}
		}
		return r
	}
	find := func(root map[string]any) map[string]any {
		children, _ := root["child_modules"].([]any)
		for _, ch := range children {
			m, _ := ch.(map[string]any)
			if m != nil && m["address"] == prefix {
				return cut(m)
			}
		}
		return map[string]any{}
	}
	if pv, ok := doc["planned_values"].(map[string]any); ok {
		if rm, ok := pv["root_module"].(map[string]any); ok {
			out["planned_values"] = map[string]any{"outputs": planned, "root_module": find(rm)}
		}
	}
	if ps, ok := doc["prior_state"].(map[string]any); ok {
		vals, _ := ps["values"].(map[string]any)
		if rm, ok := vals["root_module"].(map[string]any); ok {
			out["prior_state"] = map[string]any{
				"format_version":    ps["format_version"],
				"terraform_version": ps["terraform_version"],
				"values":            map[string]any{"root_module": find(rm)},
			}
		}
	}
	if cfg, ok := doc["configuration"].(map[string]any); ok {
		c := map[string]any{}
		if rm, ok := cfg["root_module"].(map[string]any); ok {
			if calls, ok := rm["module_calls"].(map[string]any); ok {
				if call, ok := calls[id].(map[string]any); ok {
					if mod, ok := call["module"].(map[string]any); ok {
						c["root_module"] = mod
					}
				}
			}
		}
		if pc, ok := cfg["provider_config"].(map[string]any); ok {
			keep := map[string]any{}
			for k, v := range pc {
				if rest, found := strings.CutPrefix(k, prefix+":"); found {
					keep[rest] = v
				}
			}
			c["provider_config"] = keep
		}
		out["configuration"] = c
	}
	return out
}

// jointCounts totals the actions in a unit's slice.
func jointCounts(slice map[string]any) (add, change, destroy int) {
	rcs, _ := slice["resource_changes"].([]any)
	for _, rc := range rcs {
		m, _ := rc.(map[string]any)
		ch, _ := m["change"].(map[string]any)
		acts, _ := ch["actions"].([]any)
		var create, del, upd bool
		for _, a := range acts {
			switch a {
			case "create":
				create = true
			case "delete":
				del = true
			case "update":
				upd = true
			}
		}
		switch {
		case create && del:
			add++
			destroy++
		case create:
			add++
		case del:
			destroy++
		case upd:
			change++
		}
	}
	return
}

var jointAnsiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// jointUnitBlocks returns the parts of the joint plan's text that describe
// the unit's resources: each block that opens with "# module.<id>." up to
// the next resource block or the end of the resource list.
func jointUnitBlocks(text, id string) string {
	lines := strings.Split(text, "\n")
	open := "# module." + id + "."
	var out []string
	in := false
	for _, l := range lines {
		plain := strings.TrimSpace(jointAnsiRE.ReplaceAllString(l, ""))
		switch {
		case strings.HasPrefix(plain, "# module."):
			in = strings.HasPrefix(plain, open)
		case strings.HasPrefix(plain, "Plan:"), strings.HasPrefix(plain, "Changes to Outputs"), strings.HasPrefix(plain, "───"):
			in = false
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
