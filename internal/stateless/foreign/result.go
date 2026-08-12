// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opentofu/opentofu/internal/addrs"
)

// Class is the classification of one unclaimed live resource.
type Class string

const (
	// ClassForeign is a live resource carrying no tofu-estate tag that no
	// declared instance can be matched to. Report only, never deleted.
	ClassForeign Class = "FOREIGN"

	// ClassBindCandidate is a live resource carrying no tofu-estate tag
	// whose content exactly matches one declared, unbound instance of the
	// same type. Surfaced for explicit adoption; never bound here.
	ClassBindCandidate Class = "BIND_CANDIDATE"

	// ClassOtherEstate is a live resource carrying a tofu-estate tag naming
	// a different estate. Counted, not itemized.
	ClassOtherEstate Class = "OTHER_ESTATE"
)

// Result is one classification pass over one discovery result.
type Result struct {
	// Estate is the estate the classification was made against.
	Estate string

	// Foreign lists the unclaimed live resources that belong to nobody, in
	// type and live-ID order. Every one of them is report-only.
	Foreign []Resource

	// Candidates lists the unclaimed live resources that exactly match a
	// declared, unbound instance, in declared-address order. Nothing is
	// bound: each carries the marker pair an operator would stamp to adopt
	// it.
	Candidates []Candidate

	// Renames lists the live resources this estate owns whose marker names a
	// for_each key the configuration no longer declares, each paired with the
	// one declared instance of the same resource block that nothing claimed,
	// in declared-address order. Like a bind candidate it is an offer and
	// nothing more: the marker is not rewritten, the plan still proposes
	// creating the new key, and the live resource is still an orphan.
	Renames []RenameCandidate

	// Removals lists the live resources this estate owns and no longer
	// declares, which the plan therefore proposes destroying, in address
	// order. They are the one part of this pass that is not report-only:
	// every one of them is in the prior state the plan ran against, as an
	// instance with no configuration.
	//
	// It is a section of this report rather than of the plan's own output
	// because the plan can only say "this will be destroyed"; the fact that
	// makes it legitimate - that the resource carries this estate's marker
	// at an address the configuration used to declare - is a marker fact,
	// and this is where marker facts are printed.
	Removals []Removal

	// SweepGaps lists the resource types the estate-wide removal sweep could
	// not enumerate, carried through from discovery. An estate with a sweep
	// gap has resources it may own and cannot see, so an empty Removals list
	// is not a claim that nothing is undeclared.
	SweepGaps []SweepGap

	// SweepCovered lists the types the sweep did enumerate, sorted.
	SweepCovered []string

	// Ambiguous lists the resource blocks that have orphans and unclaimed
	// declared instances but no one-to-one pairing between them, sorted by
	// block address. They carry no command: which live resource became which
	// key is exactly what a marker cannot say.
	Ambiguous []RenameAmbiguity

	// OtherEstates counts the live resources carrying another estate's
	// marker, by estate name, sorted. An entry with an empty Estate is the
	// count discovery kept without recording which estate it belonged to
	// (see [EstateCount.Estate]).
	OtherEstates []EstateCount

	// Swept lists the resource types that were listed in full, so that
	// their unclaimed resources are the complete set the provider would
	// admit - subject to the provider-side filters described in the package
	// doc. Sorted.
	Swept []string

	// Unswept lists the types this pass cannot speak for at all, sorted by
	// type name. It is the difference between "there are none" and "nobody
	// looked", and it is never omitted from output on the grounds of being
	// empty-looking.
	Unswept []Unswept
}

// Resource is one classified live resource.
type Resource struct {
	// TypeName is the resource type it was listed as.
	TypeName string

	// LiveID is the identity the provider serves for it, which is what an
	// operator needs to find it in a console or a CLI. Empty when the
	// provider sent no usable identity.
	LiveID string

	// DisplayName is the provider's label for it. Display only.
	DisplayName string

	// Tags is the resource's whole tag set, sorted by key. It is the
	// evidence for the classification: an operator reading a foreign line
	// wants to see that no tofu-estate is there, and usually recognizes the
	// resource from whatever tags it does carry.
	Tags []Tag

	// Why is one sentence saying why this resource is in the class it is in
	// - for a foreign resource, why it is not a bind candidate.
	Why string
}

// Tag is one resource tag.
type Tag struct {
	Key   string
	Value string
}

// TagSummary renders at most n tags as "k=v" pairs, with a count of the
// remainder. n <= 0 renders all of them.
func (r Resource) TagSummary(n int) string {
	if len(r.Tags) == 0 {
		return "(no tags)"
	}
	shown := r.Tags
	rest := 0
	if n > 0 && len(shown) > n {
		rest = len(shown) - n
		shown = shown[:n]
	}
	parts := make([]string, 0, len(shown))
	for _, t := range shown {
		parts = append(parts, t.Key+"="+t.Value)
	}
	s := strings.Join(parts, ", ")
	if rest > 0 {
		s += fmt.Sprintf(" (+%d more)", rest)
	}
	return s
}

// String renders a classified resource on one line.
func (r Resource) String() string {
	id := r.LiveID
	if id == "" {
		id = "(no identity)"
	}
	s := r.TypeName + " " + id
	if r.DisplayName != "" && r.DisplayName != r.LiveID {
		s += " " + r.DisplayName
	}
	return s
}

// Candidate is an unclaimed live resource that exactly matches one declared,
// unbound instance. It is an offer, not a binding.
type Candidate struct {
	Resource

	// Addr is the declared instance it matches.
	Addr addrs.AbsResourceInstance

	// Matched are the identity-bearing arguments that matched, with the
	// value both sides agreed on, in argument order. This is the whole
	// evidence for the offer: an operator has to be able to see what the
	// match was made on before acting on it.
	Matched []AttrMatch

	// MarkerEstate and MarkerAddress are the two tag values that would
	// adopt this resource: tofu-estate and tofu-address, the latter already
	// escaped per stateless/MARKERS.md.
	MarkerEstate  string
	MarkerAddress string

	// Hint is a one-line command that stamps those two tags, for the
	// resource types whose adoption command this package knows. It is empty
	// for a type it does not, and the marker pair above is the contract
	// either way: any tool that writes those two tags adopts the resource.
	Hint string
}

// AttrMatch is one identity-bearing argument that matched exactly.
type AttrMatch struct {
	Attr  string
	Value string
}

// String renders a candidate on one line.
func (c Candidate) String() string {
	parts := make([]string, 0, len(c.Matched))
	for _, m := range c.Matched {
		parts = append(parts, m.Attr+"="+m.Value)
	}
	return c.Resource.String() + " ~ " + c.Addr.String() + " on " + strings.Join(parts, " ")
}

// RenameCandidate is one live resource this estate owns, sitting at a
// for_each key the configuration no longer declares, offered as the same
// resource as one declared instance nothing claimed. It is an offer, not a
// move: only [Command] performs it.
type RenameCandidate struct {
	Resource

	// Block is the resource block both sides belong to, which is the whole
	// basis of the pairing.
	Block string

	// Old is the address the live marker claims, unescaped: the address to
	// rename from.
	Old addrs.AbsResourceInstance

	// New is the declared instance nothing claimed: the address to rename to.
	New addrs.AbsResourceInstance

	// Marker is the tofu-address tag value exactly as the live resource
	// carries it, and Normalized is its escaped form - the string the block
	// match was made on.
	Marker     string
	Normalized string

	// MatchedOn are the identity-bearing arguments the live resource and the
	// block's configuration agreed on, when there were any. It is empty for
	// the ordinary pairing, which the block match alone decides; it is the
	// evidence when content is what resolved an ambiguity.
	MatchedOn []AttrMatch

	// Command is the exact, shell-quoted live-mv invocation that
	// rewrites the marker. Running it is the entire rename; nothing else has
	// to happen and no state is touched.
	Command string
}

// String renders a rename candidate on one line.
func (r RenameCandidate) String() string {
	return r.Old.String() + " (" + liveOrNone(r.LiveID) + ") -> " + r.New.String()
}

// Removal is one live resource this estate owns at an address the
// configuration no longer declares, which the plan proposes destroying.
type Removal struct {
	Resource

	// Addr is the address its marker names, unescaped: where it sits in the
	// prior state, and the address the plan's destroy line prints.
	Addr addrs.AbsResourceInstance

	// Marker is the tofu-address tag value as carried, and Normalized its
	// escaped form.
	Marker     string
	Normalized string

	// BlockGone is true when the whole resource block is missing from the
	// configuration, as opposed to the block still existing and no longer
	// expanding to this instance key. The two are the same operation to the
	// cloud and different sentences to an operator.
	BlockGone bool

	// Swept is true when the estate-wide sweep found it rather than a scan
	// of a type the configuration declares. It is exactly the case that was
	// invisible before removal exactness landed.
	Swept bool
}

// String renders a removal on one line.
func (r Removal) String() string {
	return r.Addr.String() + " (" + r.TypeName + " " + liveOrNone(r.LiveID) + ") will be destroyed"
}

// SweepGapReason is why one resource type could not be swept for resources
// this estate owns and no longer declares.
type SweepGapReason string

// SweepGap is one resource type the removal sweep could not cover, carried
// through from discovery so that a reader of this report sees the holes in it
// beside its contents.
type SweepGap struct {
	TypeName string
	Reason   SweepGapReason
	Detail   string
}

// String renders a sweep gap on one line.
func (g SweepGap) String() string {
	return g.TypeName + " [" + string(g.Reason) + "]: " + g.Detail
}

// RenameAmbiguity is one resource block whose orphans and unclaimed declared
// instances do not pair one-to-one, reported so that the operator can see the
// same thing this pass saw and decide for themselves.
type RenameAmbiguity struct {
	// Block is the resource block, and TypeName its resource type.
	Block    string
	TypeName string

	// Live are the orphaned live resources of that block.
	Live []RenameLive

	// Declared are the addresses of its unclaimed declared instances,
	// sorted.
	Declared []string

	// Detail is one sentence saying what the obstacle is.
	Detail string
}

// String renders an ambiguity on one line.
func (a RenameAmbiguity) String() string {
	ids := make([]string, 0, len(a.Live))
	for _, l := range a.Live {
		ids = append(ids, l.Marker+" ("+liveOrNone(l.LiveID)+")")
	}
	return a.Block + ": [" + strings.Join(ids, ", ") + "] vs [" + strings.Join(a.Declared, ", ") + "]"
}

// RenameLive is one orphaned live resource named in an ambiguity.
type RenameLive struct {
	LiveID      string
	DisplayName string

	// Marker is the escaped tofu-address value it carries.
	Marker string
}

func liveOrNone(id string) string {
	if id == "" {
		return "no identity"
	}
	return id
}

// EstateCount is how many live resources carry one other estate's marker.
type EstateCount struct {
	// Estate is the other estate's name, or empty when the count came from
	// discovery's per-type tally, which records that a resource belonged to
	// some other estate without recording which. A count with no name is
	// still worth printing: it says another estate shares this account.
	Estate string

	// Count is the number of live resources.
	Count int

	// Types are the resource types they were listed as, sorted.
	Types []string
}

// String renders an other-estate count on one line.
func (e EstateCount) String() string {
	name := e.Estate
	if name == "" {
		name = "(estate not recorded)"
	}
	return fmt.Sprintf("%s: %d resource(s) [%s]", name, e.Count, strings.Join(e.Types, ", "))
}

// UnsweptReason is why a resource type's live population is unknown to this
// classification.
type UnsweptReason string

const (
	// UnsweptNotListable is a type the provider cannot list at all.
	UnsweptNotListable UnsweptReason = "TYPE_NOT_LISTABLE"

	// UnsweptListFailed is a type whose list call errored.
	UnsweptListFailed UnsweptReason = "LIST_FAILED"

	// UnsweptEstateScoped is a type that was listed with the server-side
	// estate filter on, which hides unclaimed resources by construction.
	UnsweptEstateScoped UnsweptReason = "SCOPE_ESTATE"

	// UnsweptNotScanned is a type in the configuration that was never listed
	// because no instance of it needed discovery.
	UnsweptNotScanned UnsweptReason = "NOT_SCANNED"
)

// Unswept is one resource type this classification cannot speak for.
type Unswept struct {
	// TypeName is the type.
	TypeName string

	// Reason is the machine-readable why.
	Reason UnsweptReason

	// Detail is one sentence for an operator.
	Detail string
}

// String renders an unswept type on one line.
func (u Unswept) String() string {
	return u.TypeName + " [" + string(u.Reason) + "]: " + u.Detail
}

// Empty reports whether the classification found nothing to say about live
// resources at all: no foreign resources, no candidates, no other estates.
// It says nothing about [Result.Unswept], which is reportable on its own -
// a run that swept nothing has an empty result and the most to explain, and
// nothing about [Result.Renames], which is about resources this estate owns
// rather than about the ones it does not.
func (r *Result) Empty() bool {
	return len(r.Foreign) == 0 && len(r.Candidates) == 0 && len(r.OtherEstates) == 0
}

// SweptClean reports whether at least one type was swept in full and nothing
// unclaimed came back from any of them. This is the distinction the output
// must never blur: SweptClean means "looked, found none", while an empty
// result with no swept types means nobody looked.
func (r *Result) SweptClean() bool {
	return len(r.Swept) > 0 && r.Empty()
}

// OtherEstateTotal is the number of live resources carrying another estate's
// marker, across all estates.
func (r *Result) OtherEstateTotal() int {
	var n int
	for _, e := range r.OtherEstates {
		n += e.Count
	}
	return n
}

// UnsweptOf returns the entry for one type, and whether there is one.
func (r *Result) UnsweptOf(typeName string) (Unswept, bool) {
	for _, u := range r.Unswept {
		if u.TypeName == typeName {
			return u, true
		}
	}
	return Unswept{}, false
}

// CandidateFor returns the candidate offered for one declared address.
func (r *Result) CandidateFor(addr addrs.AbsResourceInstance) (Candidate, bool) {
	want := addr.String()
	for _, c := range r.Candidates {
		if c.Addr.String() == want {
			return c, true
		}
	}
	return Candidate{}, false
}

// RenameFor returns the rename candidate offered for one declared address.
func (r *Result) RenameFor(addr addrs.AbsResourceInstance) (RenameCandidate, bool) {
	want := addr.String()
	for _, c := range r.Renames {
		if c.New.String() == want {
			return c, true
		}
	}
	return RenameCandidate{}, false
}

// AmbiguousFor returns the ambiguity reported for one resource block.
func (r *Result) AmbiguousFor(block string) (RenameAmbiguity, bool) {
	for _, a := range r.Ambiguous {
		if a.Block == block {
			return a, true
		}
	}
	return RenameAmbiguity{}, false
}

// ForeignByID returns the foreign resource with one live ID.
func (r *Result) ForeignByID(id string) (Resource, bool) {
	for _, f := range r.Foreign {
		if f.LiveID == id {
			return f, true
		}
	}
	return Resource{}, false
}

// String renders a whole classification as a multi-line summary, for logs
// and test failure output.
func (r *Result) String() string {
	var b strings.Builder
	for _, t := range r.Swept {
		b.WriteString("SWEPT     " + t + "\n")
	}
	for _, f := range r.Foreign {
		b.WriteString("FOREIGN   " + f.String() + " tags=[" + f.TagSummary(0) + "] " + f.Why + "\n")
	}
	for _, c := range r.Candidates {
		b.WriteString("CANDIDATE " + c.String() + "\n")
	}
	for _, rm := range r.Removals {
		b.WriteString("REMOVAL   " + rm.String() + "\n")
	}
	for _, g := range r.SweepGaps {
		b.WriteString("SWEEPGAP  " + g.String() + "\n")
	}
	for _, rn := range r.Renames {
		b.WriteString("RENAME    " + rn.String() + "\n")
	}
	for _, a := range r.Ambiguous {
		b.WriteString("RENAME?   " + a.String() + "\n")
	}
	for _, e := range r.OtherEstates {
		b.WriteString("OTHER     " + e.String() + "\n")
	}
	for _, u := range r.Unswept {
		b.WriteString("UNSWEPT   " + u.String() + "\n")
	}
	return b.String()
}

func sortTags(tags map[string]string) []Tag {
	if len(tags) == 0 {
		return nil
	}
	out := make([]Tag, 0, len(tags))
	for k, v := range tags {
		out = append(out, Tag{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
