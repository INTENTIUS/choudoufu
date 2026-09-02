// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// renames pairs the live resources this estate owns at a for_each key the
// configuration no longer declares with the declared instances of the same
// resource block that nothing claimed.
//
// It reads [discovery.Verdicts.Orphans] and [discovery.Report.Unbound] and
// changes neither. Both lists keep every member they arrived with: the plan
// still proposes creating the declared instance, and the live resource is
// still an owned resource with no declared address. What this adds is the
// sentence an operator otherwise has to work out for themselves - that the two
// entries are probably one resource under a new key - and the command that
// makes it so.
//
// See [renameBlock] for the pairing rule and the exact, narrow part content
// plays in it.
func (c *classifier) renames(ctx context.Context) {
	blocks := c.renameBlocks()
	if len(blocks) == 0 {
		return
	}

	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		blocks[name].decide(ctx, c, c.res)
	}

	sort.Slice(c.res.Renames, func(i, j int) bool {
		return c.res.Renames[i].New.String() < c.res.Renames[j].New.String()
	})
	sort.Slice(c.res.Ambiguous, func(i, j int) bool {
		return c.res.Ambiguous[i].Block < c.res.Ambiguous[j].Block
	})
}

// renameBlocks groups the unbound declared instances and the orphaned live
// resources that could be the same resource, by the resource block they both
// belong to.
//
// A block is only looked at when something declared by it is unbound, because
// a rename needs both halves: an orphan with no unbound instance beside it is
// an ordinary removal (P5.1's business), and an unbound instance with no
// orphan beside it is an ordinary create.
func (c *classifier) renameBlocks() map[string]*renameBlock {
	blocks := make(map[string]*renameBlock)

	for _, addr := range c.req.Report.Unbound {
		if !addr.Module.IsRoot() {
			// v0 declares no modules, and a module instance key would make
			// "the same block" a question about two addresses rather than
			// one. Nothing to be conservative about later if it is refused
			// here.
			continue
		}
		key, ok := addr.Resource.Key.(addrs.StringKey)
		if !ok {
			// NoKey is a single instance, which cannot have had a key
			// renamed, and IntKey is a count member, whose membership is the
			// slot matcher's business: a live count member the configuration
			// no longer expands to is surplus, not a rename.
			continue
		}
		name := addr.Resource.Resource.String()
		rc, ok := c.forEachBlock(name)
		if !ok {
			continue
		}
		b := blocks[name]
		if b == nil {
			b = &renameBlock{name: name, typeName: rc.Type}
			blocks[name] = b
		}
		b.unbound = append(b.unbound, renameTarget{addr: addr, key: string(key)})
	}

	for name, b := range blocks {
		prefix := discovery.EscapeAddress(name) + ":"
		for i := range c.req.Orphans {
			o := &c.req.Orphans[i]
			if o.TypeName != b.typeName || !strings.HasPrefix(o.Normalized, prefix) {
				continue
			}
			if o.Removal {
				// Unreachable, and checked anyway because the two passes have
				// to agree: discovery withholds every orphan of a block with
				// an unclaimed declared instance from removal, precisely so
				// that this pass gets to call it a rename first. An orphan
				// that arrived here already marked for destruction would mean
				// that ordering had broken, and pairing it would produce a
				// plan that destroys the resource and a hint that renames it.
				continue
			}
			raw := o.Normalized[len(prefix):]
			// raw is still escaped (issue #178: a key containing "@", "."
			// or ":" carries "@@", "@d" or "@c" here, not the original
			// character), and readableKey's malformed check has to run
			// against that raw text - a genuinely unescapable marker
			// (predating the escaping rule entirely) is exactly what would
			// still contain a literal "." or ":" at this point. key itself
			// has to be the decoded value: it is compared against
			// renameTarget.key below, which addrs.StringKey supplies
			// undecoded, and it is what candidate() rebuilds the old
			// address's instance key from.
			b.orphans = append(b.orphans, renameSource{res: o, key: discovery.UnescapeKey(raw), readable: readableKey(raw)})
		}
		if len(b.orphans) == 0 {
			delete(blocks, name)
		}
	}

	for _, b := range blocks {
		sort.Slice(b.unbound, func(i, j int) bool { return b.unbound[i].key < b.unbound[j].key })
		sort.Slice(b.orphans, func(i, j int) bool {
			if b.orphans[i].key != b.orphans[j].key {
				return b.orphans[i].key < b.orphans[j].key
			}
			return b.orphans[i].res.ImportID < b.orphans[j].res.ImportID
		})
	}
	return blocks
}

// forEachBlock returns the configuration for a for_each resource block, and
// whether the address names one.
//
// count blocks are refused here rather than filtered out later, because the
// difference matters: a count block's cardinality is a set with slots, its
// live members past the declared count are surplus, and calling one of them a
// rename candidate would offer to move a marker that the scale-down rule has
// already accounted for.
func (c *classifier) forEachBlock(name string) (*configs.Resource, bool) {
	rc, ok := c.req.Config.Module.ManagedResources[name]
	if !ok || rc.ForEach == nil || rc.Count != nil {
		return nil, false
	}
	return rc, true
}

// renameBlock is one resource block's two halves: the live resources whose
// markers name keys of this block that no longer exist, and the declared
// instances of this block that nothing claimed.
type renameBlock struct {
	name     string
	typeName string
	unbound  []renameTarget
	orphans  []renameSource
}

// renameSource is one orphaned live resource that belongs to a block.
type renameSource struct {
	res *discovery.OwnedResource

	// key is the instance key its marker names, decoded through
	// [discovery.UnescapeKey] back to the value a for_each expression would
	// have produced - not the escaped text on the wire (issue #178: a key
	// containing "@", "." or ":" is not embedded raw). It has to be the
	// true key, because it is compared against renameTarget.key below,
	// which addrs.StringKey supplies undecoded, and because [candidate]
	// rebuilds the rename's old address from it.
	key string

	// readable is false when the RAW (still-escaped) text is not a key this
	// pass will turn back into an address at all: it carries a literal '.'
	// or ':', which cannot be a validly escaped key (see
	// [discovery.EscapeKey]) and so means a marker from before the escaping
	// rule existed at all -
	// unparseable rather than merely old-grammar. Such a marker still
	// counts against the one-to-one rule, because pretending it is not
	// there would make two live resources look like one.
	readable bool
}

// renameTarget is one declared instance nothing claimed.
type renameTarget struct {
	addr addrs.AbsResourceInstance
	key  string
}

// decide applies the pairing rule to one block.
//
// The rule, in full, and conservative in the same way P2.4's bind candidates
// are conservative:
//
//  1. Both sides are the same resource type and the same resource block. A
//     live aws_subnet marked aws_subnet.this:a can only ever be offered as a
//     rename of another instance of aws_subnet.this - never of
//     aws_subnet.other, and never of another type. This is the requirement;
//     everything below only narrows what it admits.
//  2. The block uses for_each. count members are excluded entirely; see
//     [classifier.forEachBlock].
//  3. The keys differ. Identical keys would have bound in discovery, so a
//     pair that agrees on its key is a state this pass does not understand
//     and declines to write a command for.
//  4. Content, where both sides have it, may remove a pairing but never
//     create one. A live resource whose identity-bearing arguments disagree
//     with the declared instance's is not offered as that instance's rename,
//     and a block with several candidates on each side is resolved only if
//     content leaves exactly one pairing standing and that pairing agrees
//     positively. Content that cannot be read on either side - a for_each
//     block whose arguments come from each.value, which is most of them -
//     leaves the pairing exactly where the block match left it.
//  5. What remains has to be one pairing. Two orphans against one unbound
//     instance says a resource moved, not which one, and is reported as
//     ambiguous with no command attached.
//
// The reason content is confined to this role is that it is evidence about
// sameness, not about identity. Two subnets in one block can carry identical
// arguments; a rename is a claim about which live resource *is* which
// declared instance, and only the marker answers that. Letting content create
// a pairing would mean adopting resources across blocks on a resemblance,
// which is the thing the match table exists to refuse.
func (b *renameBlock) decide(ctx context.Context, c *classifier, res *Result) {
	pairs := b.candidatePairs(ctx, c)

	switch {
	case len(pairs) != 1:
		res.Ambiguous = append(res.Ambiguous, b.ambiguity(ctx, c))
	case len(b.orphans) > 1 || len(b.unbound) > 1:
		// Content narrowed several possibilities to one. That is only worth
		// acting on when the survivor agrees positively: "everything else was
		// ruled out" plus "this one could not be compared" is not evidence
		// about the one that is left.
		if pairs[0].verdict != contentAgrees {
			res.Ambiguous = append(res.Ambiguous, b.ambiguity(ctx, c))
			return
		}
		res.Renames = append(res.Renames, b.candidate(pairs[0]))
	default:
		res.Renames = append(res.Renames, b.candidate(pairs[0]))
	}
}

// renamePair is one orphan offered for one unclaimed declared instance, with
// what content had to say about it.
type renamePair struct {
	orphan  renameSource
	target  renameTarget
	verdict contentVerdict
	matched []AttrMatch
}

// candidatePairs is every (orphan, unclaimed instance) combination this block
// admits: the block match, minus the pairings rule 3 and rule 4 remove.
func (b *renameBlock) candidatePairs(ctx context.Context, c *classifier) []renamePair {
	var out []renamePair
	for _, o := range b.orphans {
		if !o.readable {
			continue
		}
		for _, u := range b.unbound {
			if o.key == u.key {
				continue
			}
			v, matched := c.contentVerdict(ctx, b, o)
			if v == contentDisagrees {
				continue
			}
			out = append(out, renamePair{orphan: o, target: u, verdict: v, matched: matched})
		}
	}
	return out
}

func (b *renameBlock) candidate(p renamePair) RenameCandidate {
	o, u := p.orphan, p.target
	old := addrs.AbsResourceInstance{
		Module:   u.addr.Module,
		Resource: addrs.ResourceInstance{Resource: u.addr.Resource.Resource, Key: addrs.StringKey(o.key)},
	}
	return RenameCandidate{
		Resource: Resource{
			TypeName:    o.res.TypeName,
			LiveID:      o.res.ImportID,
			DisplayName: o.res.DisplayName,
			Tags:        sortTags(o.res.Tags),
		},
		Block:      b.name,
		Old:        old,
		New:        u.addr,
		Marker:     o.res.Marker,
		Normalized: o.res.Normalized,
		MatchedOn:  p.matched,
		Command:    renameCommand(old, u.addr),
	}
}

// contentVerdict is what the identity-bearing arguments say about one
// pairing.
type contentVerdict int

const (
	// contentUnknown is the usual answer, and the reason content is optional:
	// the type has no identity-bearing arguments stateless mode will match on, or
	// the declared instance's argument comes from each.value and cannot be
	// read from configuration alone, or the provider sent no object.
	contentUnknown contentVerdict = iota

	// contentAgrees is every argument present on both sides and equal.
	contentAgrees

	// contentDisagrees is at least one argument present on both sides and
	// different. It is the only verdict that changes an outcome on its own.
	contentDisagrees
)

// contentVerdict compares one orphan's live object with the configuration of
// the resource block both halves of the pairing belong to, on the same
// identity-bearing arguments a bind candidate is matched on.
//
// It is the block's configuration and not the declared instance's, because
// there is no such thing as the declared instance's: a for_each block has one
// body, and an argument written as each.value is not readable from
// configuration alone at all. So the comparison answers "could this live
// resource be an instance of this block" rather than "is it this particular
// key", and the shape of what it can decide follows from that. It can
// disqualify a pairing outright when the block fixes an argument and the live
// resource carries something else. It can tell two orphans apart against one
// unclaimed instance, because the two live objects differ. It cannot tell two
// unclaimed instances apart, because they share a body, and the code above
// does not ask it to.
//
// An argument missing from either side is not a mismatch: it is a question
// this comparison cannot answer, and the pairing falls back to what the block
// match said. Only a value present on both sides and different disqualifies.
func (c *classifier) contentVerdict(ctx context.Context, b *renameBlock, o renameSource) (contentVerdict, []AttrMatch) {
	attrs, ok := matchTable[b.typeName]
	if !ok || o.res.Resource == cty.NilVal {
		return contentUnknown, nil
	}
	rc, ok := c.req.Config.Module.ManagedResources[b.name]
	if !ok {
		return contentUnknown, nil
	}

	var matched []AttrMatch
	for _, name := range attrs {
		want, why := staticString(ctx, c.req.Config.Module, rc, name)
		if why != "" {
			continue
		}
		got, ok := liveString(o.res.Resource, name)
		if !ok {
			continue
		}
		if got != want {
			return contentDisagrees, nil
		}
		matched = append(matched, AttrMatch{Attr: name, Value: want})
	}
	if len(matched) == 0 {
		return contentUnknown, nil
	}
	return contentAgrees, matched
}

// ambiguity is what this block is reported as when the one-to-one rule does
// not hold: everything an operator needs to make the call themselves, and no
// command.
func (b *renameBlock) ambiguity(ctx context.Context, c *classifier) RenameAmbiguity {
	amb := RenameAmbiguity{Block: b.name, TypeName: b.typeName}
	for _, o := range b.orphans {
		amb.Live = append(amb.Live, RenameLive{
			LiveID:      o.res.ImportID,
			DisplayName: o.res.DisplayName,
			Marker:      o.res.Normalized,
		})
	}
	for _, u := range b.unbound {
		amb.Declared = append(amb.Declared, u.addr.String())
	}

	single := len(b.orphans) == 1 && len(b.unbound) == 1
	switch {
	case single && !b.orphans[0].readable:
		amb.Detail = fmt.Sprintf(
			"One live %s carries the marker %q and one declared instance of %s is unclaimed, but that marker's instance key contains a '.' or a ':', which the escaping rule in live/MARKERS.md cannot turn back into an address. Writing the rename command would mean guessing where the key ends.",
			b.typeName, b.orphans[0].res.Normalized, b.name)
	case single && b.orphans[0].key == b.unbound[0].key:
		amb.Detail = fmt.Sprintf(
			"One live %s and one declared instance of %s are both at the key %q, which discovery would have bound. Something rewrote a marker underneath this run; nothing is offered until the two agree.",
			b.typeName, b.name, b.orphans[0].key)
	case single:
		// The only way a readable orphan with a different key reaches an
		// ambiguity: content ruled the pairing out.
		amb.Detail = fmt.Sprintf(
			"One live %s carries the marker %q and one declared instance of %s is unclaimed, but %s. A resource whose identifying arguments are not the declared ones is not the declared one under a new key, so no rename is offered; the plan proposes creating the declared instance, and the live resource is still owned at an address nothing declares.",
			b.typeName, b.orphans[0].res.Normalized, b.name, b.contentMismatch(ctx, c, b.orphans[0]))
	default:
		amb.Detail = fmt.Sprintf(
			"%d live %s resources carry markers naming keys of %s that the configuration no longer declares, and %d declared instances of it are unclaimed. Which live resource became which key is not something a marker answers, and pairing them by position would be a guess. Rename them one at a time with choudoufu live-mv if you know which is which.",
			len(b.orphans), b.typeName, b.name, len(b.unbound))
	}
	return amb
}

// contentMismatch names the arguments that ruled a pairing out, so that an
// operator can see the comparison rather than be told its conclusion.
func (b *renameBlock) contentMismatch(ctx context.Context, c *classifier, o renameSource) string {
	rc, ok := c.req.Config.Module.ManagedResources[b.name]
	if !ok {
		return "its content does not match the declared instance"
	}
	var parts []string
	for _, name := range matchTable[b.typeName] {
		want, why := staticString(ctx, c.req.Config.Module, rc, name)
		if why != "" {
			continue
		}
		got, ok := liveString(o.res.Resource, name)
		if !ok || got == want {
			continue
		}
		parts = append(parts, fmt.Sprintf("its %s is %q where %s declares %q", name, got, b.name, want))
	}
	if len(parts) == 0 {
		return "its content does not match the declared instance"
	}
	return strings.Join(parts, " and ")
}

// readableKey reports whether a RAW (still-escaped) instance key can be
// turned back into an address at all. A validly escaped key - under either
// the current grammar or the pre-issue-#178 one - never contains a literal
// '.' or ':' at this point: both are always represented as "@d"/"@c" or, on
// a pre-#178 marker, simply never occurred in the first place, since lint
// refused the key before it could reach a marker. A raw '.' or ':' here
// means the marker predates the escaping rule entirely (or was hand-edited
// into an invalid shape), and is not decoded on a hunch.
func readableKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, ".:")
}

// renameCommand is the exact command that performs the rename: the marker
// rewrite live-mv does, with both addresses quoted so a shell hands the
// brackets and quotes through unharmed.
func renameCommand(old, new addrs.AbsResourceInstance) string {
	return fmt.Sprintf("choudoufu live-mv %s %s", shellQuote(old.String()), shellQuote(new.String()))
}

// shellQuote wraps a string in single quotes. An address contains '"' and
// '[' and ']', all of which a shell would otherwise interpret; it cannot
// contain a single quote, since an instance key carrying one could never have
// been written to an AWS tag value, but the escape is here rather than
// assumed because a command printed for copying has to run when it is pasted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
