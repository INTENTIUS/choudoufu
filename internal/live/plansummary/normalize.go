// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// Placeholders a normalized change carries where it stripped something.
const (
	estatePlaceholder  = "<estate>"
	addressPlaceholder = "<address>"
	keyPlaceholder     = "<key>"
	unknownValue       = "<known after apply>"
)

// Action names, as a normalized change and the summary spell them.
const (
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionDelete  = "delete"
	ActionReplace = "replace"
	ActionRead    = "read"
	ActionForget  = "forget"
	// ActionNoOp is only ever the action of an import that changes nothing.
	ActionNoOp = "no-op"
)

// hasValue reports whether a raw JSON field is present and not null.
func hasValue(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

// action folds a stock actions list into one word. "" means no-op: the
// change is not a change and is skipped.
func action(actions []string) string {
	switch {
	case len(actions) == 2 && contains(actions, "delete") && contains(actions, "create"):
		return ActionReplace
	case len(actions) == 1:
		switch actions[0] {
		case "no-op", "":
			return ""
		case "create":
			return ActionCreate
		case "update":
			return ActionUpdate
		case "delete":
			return ActionDelete
		case "read":
			return ActionRead
		case "forget":
			return ActionForget
		}
	}
	return strings.Join(actions, "+")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Destructive reports whether an action destroys an object.
func Destructive(action string) bool {
	return action == ActionDelete || action == ActionReplace
}

// identity is what one unit (a root, or one instance of an expansion) is
// allowed to have stripped from its change: the tokens that name it, and
// the estate its markers may carry.
type identity struct {
	// tokens are replaced wherever they stand as a whole word in a string
	// value or an address name segment. Longest first.
	tokens []token
	// estate is the value a tofu-estate marker is expected to hold. A
	// marker holding anything else is left visible.
	estate string
}

type token struct {
	text        string
	placeholder string
}

func newIdentity(estate string, keyTokens []string, nameTokens ...string) identity {
	id := identity{estate: estate}
	seen := map[string]bool{}
	add := func(s, placeholder string) {
		for _, v := range []string{s, strings.ReplaceAll(s, "-", "_"), strings.ReplaceAll(s, "_", "-")} {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			id.tokens = append(id.tokens, token{text: v, placeholder: placeholder})
		}
	}
	add(estate, estatePlaceholder)
	for _, n := range nameTokens {
		add(n, estatePlaceholder)
	}
	for _, k := range keyTokens {
		add(k, keyPlaceholder)
	}
	sort.SliceStable(id.tokens, func(i, j int) bool { return len(id.tokens[i].text) > len(id.tokens[j].text) })
	return id
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// replaceTokens replaces each token where it stands as a whole word: the
// bytes either side of it are not letters or digits. So the estate "acme"
// is stripped from "acme-web" and "team_acme" but not from "acmeweb", and
// the estate "estate-1" is not stripped from "estate-10".
//
// A token shorter than three characters (a count index, a one-letter
// for_each key) must also touch a '-' or '_', so the key "1" is stripped
// from "web-1" but never from "10.1.0.0/16", where a short token standing
// between dots is far more often part of a value than a name.
func replaceTokens(s string, id identity) string {
	if s == "" {
		return s
	}
	for _, t := range id.tokens {
		s = replaceToken(s, t)
	}
	return s
}

func replaceToken(s string, t token) string {
	if !strings.Contains(s, t.text) {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		j := strings.Index(s[i:], t.text)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		start, end := i+j, i+j+len(t.text)
		leftOK := start == 0 || !isAlnum(s[start-1])
		rightOK := end == len(s) || !isAlnum(s[end])
		if leftOK && rightOK && len(t.text) < 3 {
			joined := start > 0 && (s[start-1] == '-' || s[start-1] == '_') ||
				end < len(s) && (s[end] == '-' || s[end] == '_')
			leftOK = joined
		}
		if leftOK && rightOK {
			b.WriteString(s[i:start])
			b.WriteString(t.placeholder)
			i = end
			continue
		}
		b.WriteString(s[i : start+1])
		i = start + 1
	}
	return b.String()
}

// splitAddress removes every instance key from an address and returns the
// keys it removed, decoded: `module.a["x"].aws_instance.b[0]` is
// `module.a.aws_instance.b` with keys ["x", "0"].
func splitAddress(addr string) (string, []string) {
	var b strings.Builder
	var keys []string
	for i := 0; i < len(addr); {
		c := addr[i]
		if c != '[' {
			b.WriteByte(c)
			i++
			continue
		}
		j := i + 1
		inQuote := false
		for j < len(addr) {
			switch {
			case inQuote && addr[j] == '\\':
				j++
			case addr[j] == '"':
				inQuote = !inQuote
			case !inQuote && addr[j] == ']':
				goto done
			}
			j++
		}
	done:
		raw := addr[i+1 : min(j, len(addr))]
		if k, err := strconv.Unquote(raw); err == nil {
			keys = append(keys, k)
		} else {
			keys = append(keys, raw)
		}
		i = j + 1
	}
	return b.String(), keys
}

// normalizeAddress strips instance keys, then the unit's tokens from the
// NAME segments only: module call names and the resource name. The
// resource type is never touched, so an estate called "aws" cannot rewrite
// aws_instance.
func normalizeAddress(addr string, id identity) string {
	bare, _ := splitAddress(addr)
	parts := strings.Split(bare, ".")
	for i := 0; i < len(parts); i++ {
		switch {
		case parts[i] == "module" && i+1 < len(parts):
			parts[i+1] = replaceTokens(parts[i+1], id)
			i++
		case i == len(parts)-1:
			parts[i] = replaceTokens(parts[i], id)
		}
	}
	return strings.Join(parts, ".")
}

// configAddress is an address with every instance key removed and nothing
// else changed: the expansion an instance belongs to.
func configAddress(addr string) string {
	bare, _ := splitAddress(addr)
	return bare
}

func decode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil
	}
	return v
}

// normalizeValue rewrites a plan value for comparison. Strings lose the
// unit's tokens. Inside any map, the markers are handled by what they
// hold, never blanked: a tofu-estate equal to the unit's own estate
// becomes <estate>, and a tofu-address (with its continuations, or the
// Kubernetes address annotation) equal to the instance's own escaped
// address becomes <address>. A marker holding anything else is left as it
// is, so a resource whose marker names some other estate or address
// stays visibly different.
func normalizeValue(v any, id identity, ownAddress string) any {
	switch t := v.(type) {
	case string:
		return replaceTokens(t, id)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeValue(e, id, ownAddress)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		strs := map[string]string{}
		for k, e := range t {
			if s, ok := e.(string); ok {
				strs[k] = s
			}
		}
		addrOwn := false
		if gathered, corrupt := markers.GatherAddress(strs); !corrupt && gathered != "" && gathered == markers.EscapeAddress(ownAddress) {
			addrOwn = true
		}
		for k, e := range t {
			s, isStr := e.(string)
			switch {
			case isStr && k == markers.TagEstate && id.estate != "" && s == id.estate:
				out[k] = estatePlaceholder
			case isStr && addrOwn && isAddressKey(k):
				out[k] = addressPlaceholder
			case isStr && k == markers.AddressAnnotation && s == markers.EscapeAddress(ownAddress):
				out[k] = addressPlaceholder
			default:
				out[k] = normalizeValue(e, id, ownAddress)
			}
		}
		return out
	}
	return v
}

func isAddressKey(k string) bool {
	if k == markers.TagAddress {
		return true
	}
	rest, ok := strings.CutPrefix(k, markers.TagAddress+"-")
	if !ok {
		return false
	}
	_, err := strconv.Atoi(rest)
	return err == nil
}

// normalized is one resource change with everything unit-specific
// stripped. Key is its canonical form, which is what grouping compares;
// Line is what a reader sees.
type normalized struct {
	Key     string
	Line    string
	Action  string
	Address string
	// Attrs is each changed attribute's canonical JSON, for naming the
	// attributes two otherwise matching changes differ in.
	Attrs map[string]string
}

type attrChange struct {
	Before  any  `json:"before,omitempty"`
	After   any  `json:"after,omitempty"`
	Unknown bool `json:"unknown,omitempty"`
	// Sensitive marks an attribute the plan flags sensitive; it then carries
	// no values, so it compares by its path alone (chant's rule).
	Sensitive bool `json:"sensitive,omitempty"`
}

// normalizeChange is the rule for "identical": a change is its action, its
// normalized address, and for each top-level attribute it CHANGES, the
// normalized value on each side. An attribute equal on both sides is not
// part of the change, which is what drops server-assigned identifiers
// (id, arn) from an update: they differ between roots and say nothing
// about what is being done. A create carries every value it sets; a
// delete, a forget or a read carries only its address.
func normalizeChange(rc ResourceChange, id identity) (normalized, bool) {
	act := action(rc.Change.Actions)
	importing := hasValue(rc.Change.Importing)
	if act == "" {
		if !importing {
			return normalized{}, false
		}
		// An import that changes nothing is still an import: it is kept.
		act = ActionNoOp
	}
	addr := normalizeAddress(rc.Address, id)
	if rc.Deposed != "" {
		addr += " (deposed)"
	}

	attrs := map[string]attrChange{}
	// A read carries no attributes: a data source's result is what the
	// provider returned, not a change anyone wrote (chant's rule).
	if act != ActionDelete && act != ActionForget && act != ActionRead {
		before, _ := decode(rc.Change.Before).(map[string]any)
		after, _ := decode(rc.Change.After).(map[string]any)
		unknown, _ := decode(rc.Change.AfterUnknown).(map[string]any)
		beforeSensitive, _ := decode(rc.Change.BeforeSensitive).(map[string]any)
		afterSensitive, _ := decode(rc.Change.AfterSensitive).(map[string]any)
		names := map[string]bool{}
		for k := range after {
			names[k] = true
		}
		for k := range unknown {
			names[k] = true
		}
		if act != ActionCreate && act != ActionRead {
			for k := range before {
				names[k] = true
			}
		}
		for k := range names {
			unk := anyTrue(unknown[k])
			b, a := before[k], after[k]
			if act == ActionCreate || act == ActionRead {
				if a == nil && !unk {
					continue
				}
				b = nil
			} else if !unk && reflect.DeepEqual(b, a) {
				continue
			}
			if anyTrue(beforeSensitive[k]) || anyTrue(afterSensitive[k]) {
				attrs[k] = attrChange{Sensitive: true}
				continue
			}
			c := attrChange{
				Before:  normalizeValue(b, id, rc.Address),
				After:   normalizeValue(a, id, rc.Address),
				Unknown: unk,
			}
			if unk {
				c.After = unknownValue
			}
			attrs[k] = c
		}
	}

	var replacePaths any
	if act == ActionReplace {
		replacePaths = decode(rc.Change.ReplacePaths)
	}
	keyDoc := struct {
		Action       string                `json:"action"`
		Address      string                `json:"address"`
		Attrs        map[string]attrChange `json:"attrs,omitempty"`
		ReplacePaths any                   `json:"replace_paths,omitempty"`
		Importing    bool                  `json:"importing,omitempty"`
	}{act, addr, attrs, replacePaths, importing}
	key, _ := json.Marshal(keyDoc)

	names := make([]string, 0, len(attrs))
	for k := range attrs {
		names = append(names, k)
	}
	sort.Strings(names)
	line := symbol(act) + " " + addr
	if importing && act != ActionNoOp {
		line += " (import)"
	}
	if len(names) > 0 && act != ActionCreate && act != ActionRead {
		line += ": " + strings.Join(names, ", ")
	}
	canon := make(map[string]string, len(attrs))
	for k, c := range attrs {
		b, _ := json.Marshal(c)
		canon[k] = string(b)
	}
	return normalized{Key: string(key), Line: line, Action: act, Address: addr, Attrs: canon}, true
}

func anyTrue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case []any:
		for _, e := range t {
			if anyTrue(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if anyTrue(e) {
				return true
			}
		}
	}
	return false
}

func symbol(action string) string {
	switch action {
	case ActionCreate:
		return "+"
	case ActionUpdate:
		return "~"
	case ActionDelete:
		return "-"
	case ActionReplace:
		return "-/+"
	case ActionRead:
		return "<="
	case ActionForget:
		return "."
	case ActionNoOp:
		return "import"
	}
	return "?"
}
