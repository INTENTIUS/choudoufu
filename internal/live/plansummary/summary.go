// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plansummary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Summary is a grouped plan summary, and the -json document.
type Summary struct {
	// Kind is "set" for a set document, whose units are estate roots, and
	// "plan" for one plan, whose units are resource instances.
	Kind string `json:"kind"`
	// Units counts the units summarized: every root of a set document,
	// failed ones included, or every changing instance of a plan.
	Units int `json:"units"`
	// Failed are the roots that did not plan, each with its reason. A
	// failed root is never a member of a group.
	Failed []Failure `json:"failed"`
	// Groups are the units with identical normalized change sets, largest
	// first.
	Groups []Group `json:"groups"`
	// Destructive counts every destroy and replace across all groups. Each
	// one is listed by address in its group's Destroys.
	Destructive int `json:"destroys_and_replaces"`
}

// Failure is a root that did not plan.
type Failure struct {
	Root   string `json:"root"`
	Estate string `json:"estate,omitempty"`
	Reason string `json:"reason"`
}

// Group is a set of units whose changes are identical after normalization.
type Group struct {
	ID int `json:"id"`
	// Resource is, for a plan's groups, the expansion the instances belong
	// to (the address with every instance key removed). Empty for a set.
	Resource string `json:"resource,omitempty"`
	// Members are the units in the group: root directories, or instance
	// addresses.
	Members []string `json:"members"`
	// Outlier is a group of one where its peers grouped differently.
	Outlier bool `json:"outlier"`
	// NoChanges is a group of roots whose plans change nothing.
	NoChanges bool `json:"no_changes,omitempty"`
	// Hash is the digest of the normalized change set, for matching a group
	// across two summaries.
	Hash string `json:"hash"`
	// Changes is the normalized change set: one line per distinct change,
	// with how many times one unit makes it.
	Changes []ChangeLine `json:"changes"`
	// Extends is the ID of the group whose whole change set this group's
	// contains, when it is that change plus more; Plus is the more.
	Extends int          `json:"extends,omitempty"`
	Plus    []ChangeLine `json:"plus,omitempty"`
	// Destroys lists every destroy and replace any member makes, by its
	// real (unnormalized) address. Never folded.
	Destroys []Destroy `json:"destroys_and_replaces"`
	// TriggeredActions lists every triggered action (action_invocations)
	// any member runs. A triggered action is part of its unit's change: a
	// unit that runs one never groups with the same change without it.
	TriggeredActions []string `json:"triggered_actions,omitempty"`
}

// ChangeLine is one distinct normalized change.
type ChangeLine struct {
	Line   string `json:"line"`
	Action string `json:"action"`
	Count  int    `json:"count"`
	// DiffersFrom and DiffersIn say, for a change whose address and action
	// match one in the baseline group but whose values do not, which group
	// that is and which attributes differ. Values are never printed.
	DiffersFrom int      `json:"differs_from,omitempty"`
	DiffersIn   []string `json:"differs_in,omitempty"`
	key         string
	norm        normalized
}

// Destroy is one destroy or replace, by address.
type Destroy struct {
	// Unit is the root the address is in, for a set; empty for a plan.
	Unit    string `json:"root,omitempty"`
	Address string `json:"address"`
	Action  string `json:"action"`
}

type unit struct {
	name     string
	family   string
	changes  []normalized
	destroys []Destroy
	// effects are the unit's normalized triggered actions, compared when
	// grouping; effectLines are the same, as a reader sees them.
	effects     []string
	effectLines []string
}

// effectOf normalizes one triggered action for a unit. The trigger address
// loses the unit's instance keys and tokens like any other address.
func effectOf(ai ActionInvocation, id identity) (key, line string) {
	trigger, event := "", ""
	if ai.Trigger != nil {
		trigger, event = normalizeAddress(ai.Trigger.Resource, id), ai.Trigger.Event
	}
	doc, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Trigger string `json:"trigger"`
		Event   string `json:"event"`
	}{ai.Type, trigger, event})
	line = ai.Address
	if trigger != "" {
		line += ", triggered by " + trigger
		if event != "" {
			line += " " + event
		}
	}
	return string(doc), line
}

// Summarize groups an input's units by their normalized change sets.
func Summarize(in Input) Summary {
	if in.Set != nil {
		return summarizeSet(in.Set)
	}
	if in.Plan != nil {
		return summarizePlan(in.Plan)
	}
	return Summary{}
}

func summarizeSet(doc *SetDocument) Summary {
	s := Summary{Kind: "set", Units: len(doc.Roots), Failed: []Failure{}}
	var units []unit
	for _, r := range doc.Roots {
		name := r.Root
		if name == "" {
			name = r.Estate
		}
		if reason := failureReason(r); reason != "" {
			s.Failed = append(s.Failed, Failure{Root: name, Estate: r.Estate, Reason: reason})
			continue
		}
		var names []string
		if base := path.Base(path.Clean(strings.ReplaceAll(r.Root, `\`, "/"))); base != "." && base != "/" && base != r.Estate {
			names = append(names, base)
		}
		id := newIdentity(r.Estate, nil, names...)
		u := unit{name: name}
		for _, rc := range r.Plan.ResourceChanges {
			n, ok := normalizeChange(rc, id)
			if !ok {
				continue
			}
			u.changes = append(u.changes, n)
			if Destructive(n.Action) {
				u.destroys = append(u.destroys, Destroy{Unit: name, Address: realAddress(rc), Action: n.Action})
			}
		}
		for _, ai := range r.Plan.ActionInvocations {
			key, line := effectOf(ai, id)
			u.effects = append(u.effects, key)
			u.effectLines = append(u.effectLines, name+": "+line)
		}
		units = append(units, u)
	}
	s.Groups = group(units, false)
	s.Destructive = countDestroys(s.Groups)
	return s
}

func failureReason(r SetRoot) string {
	switch {
	case r.Status != "planned":
		reason := r.Error
		if reason == "" {
			reason = fmt.Sprintf("status %q, with no error given", r.Status)
		}
		return reason
	case r.Plan == nil:
		return "status planned, but the document holds no plan for it"
	case r.Plan.Errored:
		if r.Error != "" {
			return "the plan errored: " + r.Error
		}
		return "the plan errored"
	}
	return ""
}

func summarizePlan(p *Plan) Summary {
	s := Summary{Kind: "plan", Failed: []Failure{}}
	if p.Errored {
		s.Failed = append(s.Failed, Failure{Root: ".", Reason: "the plan errored"})
	}
	var units []unit
	for _, rc := range p.ResourceChanges {
		_, keys := splitAddress(rc.Address)
		id := newIdentity("", keys)
		n, ok := normalizeChange(rc, id)
		if !ok {
			continue
		}
		u := unit{name: realAddress(rc), family: configAddress(rc.Address), changes: []normalized{n}}
		for _, ai := range p.ActionInvocations {
			if ai.Trigger == nil || ai.Trigger.Resource != rc.Address {
				continue
			}
			key, line := effectOf(ai, id)
			u.effects = append(u.effects, key)
			u.effectLines = append(u.effectLines, u.name+": "+line)
		}
		if Destructive(n.Action) {
			u.destroys = []Destroy{{Address: realAddress(rc), Action: n.Action}}
		}
		units = append(units, u)
	}
	s.Units = len(units)
	s.Groups = group(units, true)
	s.Destructive = countDestroys(s.Groups)
	return s
}

func realAddress(rc ResourceChange) string {
	if rc.Deposed != "" {
		return rc.Address + " (deposed " + rc.Deposed + ")"
	}
	return rc.Address
}

func countDestroys(gs []Group) int {
	n := 0
	for _, g := range gs {
		n += len(g.Destroys)
	}
	return n
}

// group puts units with the same family and the same multiset of
// normalized changes into one group.
func group(units []unit, perFamily bool) []Group {
	type acc struct {
		g     *Group
		lines []ChangeLine
	}
	byHash := map[string]*acc{}
	var order []string
	for _, u := range units {
		keys := make([]string, len(u.changes))
		for i, c := range u.changes {
			keys[i] = c.Key
		}
		sort.Strings(keys)
		effects := append([]string(nil), u.effects...)
		sort.Strings(effects)
		in := u.family + "\x00" + strings.Join(keys, "\n")
		if len(effects) > 0 {
			in += "\x00" + strings.Join(effects, "\n")
		}
		sum := sha256.Sum256([]byte(in))
		h := hex.EncodeToString(sum[:])[:12]
		a, ok := byHash[h]
		if !ok {
			a = &acc{g: &Group{Resource: u.family, Hash: h, NoChanges: len(u.changes) == 0 && len(u.effects) == 0, Destroys: []Destroy{}}, lines: changeLines(u.changes)}
			byHash[h] = a
			order = append(order, h)
		}
		a.g.Members = append(a.g.Members, u.name)
		a.g.Destroys = append(a.g.Destroys, u.destroys...)
		a.g.TriggeredActions = append(a.g.TriggeredActions, u.effectLines...)
	}

	groups := make([]Group, 0, len(order))
	lines := map[string][]ChangeLine{}
	for _, h := range order {
		a := byHash[h]
		a.g.Changes = a.lines
		groups = append(groups, *a.g)
		lines[h] = a.lines
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if perFamily && groups[i].Resource != groups[j].Resource {
			return groups[i].Resource < groups[j].Resource
		}
		if len(groups[i].Members) != len(groups[j].Members) {
			return len(groups[i].Members) > len(groups[j].Members)
		}
		return groups[i].Members[0] < groups[j].Members[0]
	})
	for i := range groups {
		groups[i].ID = i + 1
	}

	// The baseline of a family is its largest group that changes anything;
	// every other group is described against it.
	baseline := map[string]int{}
	familySize := map[string]int{}
	for i, g := range groups {
		familySize[g.Resource]++
		if _, ok := baseline[g.Resource]; !ok && !g.NoChanges {
			baseline[g.Resource] = i
		}
	}
	for i := range groups {
		g := &groups[i]
		g.Outlier = len(g.Members) == 1 && familySize[g.Resource] > 1
		b, ok := baseline[g.Resource]
		if !ok || b == i || g.NoChanges {
			continue
		}
		base := groups[b]
		if plus, superset := extraLines(base.Changes, g.Changes); superset && len(plus) > 0 {
			g.Extends = base.ID
			g.Plus = plus
		}
		for j := range g.Changes {
			markDifference(&g.Changes[j], base)
		}
		for j := range g.Plus {
			markDifference(&g.Plus[j], base)
		}
	}
	return groups
}

func changeLines(changes []normalized) []ChangeLine {
	byKey := map[string]*ChangeLine{}
	var keys []string
	for _, c := range changes {
		if l, ok := byKey[c.Key]; ok {
			l.Count++
			continue
		}
		byKey[c.Key] = &ChangeLine{Line: c.Line, Action: c.Action, Count: 1, key: c.Key, norm: c}
		keys = append(keys, c.Key)
	}
	out := make([]ChangeLine, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].key < out[j].key
	})
	return out
}

// extraLines reports whether have contains all of base (counts included)
// and returns what it has beyond base.
func extraLines(base, have []ChangeLine) ([]ChangeLine, bool) {
	need := map[string]int{}
	for _, l := range base {
		need[l.key] += l.Count
	}
	var plus []ChangeLine
	for _, l := range have {
		n := need[l.key]
		switch {
		case n == 0:
			plus = append(plus, l)
		case l.Count > n:
			extra := l
			extra.Count = l.Count - n
			plus = append(plus, extra)
		}
		delete(need, l.key)
	}
	for _, n := range need {
		if n > 0 {
			return nil, false
		}
	}
	return plus, true
}

// markDifference names the attributes a change differs in from the
// baseline's change at the same address with the same action, when the
// baseline has one and does not have this exact change.
func markDifference(l *ChangeLine, base Group) {
	for _, b := range base.Changes {
		if b.key == l.key {
			return
		}
	}
	for _, b := range base.Changes {
		if b.norm.Address != l.norm.Address || b.norm.Action != l.norm.Action {
			continue
		}
		names := map[string]bool{}
		for k, v := range l.norm.Attrs {
			if b.norm.Attrs[k] != v {
				names[k] = true
			}
		}
		for k := range b.norm.Attrs {
			if _, ok := l.norm.Attrs[k]; !ok {
				names[k] = true
			}
		}
		for k := range names {
			l.DiffersIn = append(l.DiffersIn, k)
		}
		sort.Strings(l.DiffersIn)
		l.DiffersFrom = base.ID
		return
	}
}
