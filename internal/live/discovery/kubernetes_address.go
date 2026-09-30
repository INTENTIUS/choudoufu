// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1640, step 2 of the ruling on #1605: the Kubernetes leg
// binds a listed object to the declared instance its address annotation
// names (kubesweep.AddressAnnotation, which #1639 stamps).
//
// Before this, the leg's only join was the natural key: an object whose
// kind and NAMESPACE/NAME a concrete resolution names is declared, and
// every other object is an orphan. Two shapes fall through that join
// although the object IS a declared instance's, and the annotation says
// which:
//
//   - #1539: the instance's identity reads something the static evaluator
//     cannot follow (a CRD's spec field), so it is refused statically and
//     handed to the plan node, and is absent from the resolutions. Its
//     object was filed as orphan_<namespace>_<name>, and the node, finding
//     nothing, planned a create over it (now #1617's refusal).
//   - #1541: the configuration renamed the object (a content-hashed
//     ConfigMap under create_before_destroy). The resolution names the new
//     object, which does not exist yet; the old one was an orphan beside a
//     create, where stock plans one replace.
//
// Here such an object becomes a [Binding] and a concrete resolution at the
// annotated address, so the projection imports it there and the node's
// marker index answers for it. It binds only when nothing else could be
// meant; every other shape stays exactly the orphan it was:
//
//   - the annotation names an instance this configuration declares: a
//     resolution the caller handed in, or an instance the static evaluator
//     refused ([Request.NodeRefused]). "Names" is [moved.Accepts]: the
//     escaped annotation compared with the instance's escaped address
//     under [markers.AddressMatches], or with an address a honoured moved
//     block says the instance used to have. It is never the annotation
//     decoded and compared as an address, because decoding cannot tell a
//     for_each key made of digits from a count index (GitHub issue #1737):
//     x["0"] stamps as x:0, which decodes to x[0]. An address nothing
//     declares is a deleted block's, and its object is the orphan it
//     always was;
//   - that address's type manages the object's kind: one of the kind's
//     provider types, or the manifest type, which manages any kind;
//   - the address has no object already: when its configuration names a
//     natural key, no listed object of that kind has it. The declared
//     object wins over one that merely carries the address;
//   - exactly one listed object claims the address. Two or more are the
//     collision refusal (GitHub issue #1641), and none of them is an
//     orphan either;
//   - the object is not terminating;
//   - on a multi-provider run, the address's block is this pass's
//     provider configuration's ([Request.ScopeProvider]).
//
// Nothing here reads CarriesAddress. Since #1641 flipped it, #1617's
// refusal at the node stands for an instance this cannot bind only where
// this leg found an object that could be that instance's and carries no
// annotation, or could not list a kind the instance's type manages: see
// [Verdicts.KubernetesUnaddressed].

// UndeclaredObject is one listed object no natural key declares, with the
// kind it was listed under and the type an orphan of it is filed at.
// Exported (GitHub issue #1677) so live-ls can build the same slice the
// sweep does and hand it to [KubernetesAddressBindings], rather than
// forking the rule this file's comment states.
type UndeclaredObject struct {
	Kind     kubesweep.Kind
	TypeName string
	Object   kubesweep.Object
}

// ListedObjects is every object the leg listed, by kind and natural key.
// Exported alongside [UndeclaredObject] for the same reason.
type ListedObjects map[string]map[string]bool

// Add records that an object of kind at key was listed.
func (l ListedObjects) Add(kind, key string) {
	if l[kind] == nil {
		l[kind] = map[string]bool{}
	}
	l[kind][key] = true
}

// KubernetesAddressBindings reports, for each undeclared object, the
// declared instance its address annotation binds it to under this file's
// rule - by index into undeclared, since an undeclared object carries no
// identity of its own until it is bound. An index absent from the result
// is unbound: either nothing claims it, or more than one object does and
// the annotation cannot say which is the instance's. The sweep refuses
// that second case as a collision (GitHub issue #1641) and binds neither
// object, so neither is bound here either.
//
// This is the one place the rule is evaluated. The sweep turns a binding
// into a concrete resolution and a [Binding] ([bindByAddress]); live-ls
// only needs the address, to report the object as declared under it
// (GitHub issue #1677) - a second copy of the rule there would drift from
// this one the first time either changed.
func KubernetesAddressBindings(req Request, manifestType string, declared KubernetesDeclared, listed ListedObjects, undeclared []UndeclaredObject) map[int]addrs.AbsResourceInstance {
	claims, _ := addressClaims(req, manifestType, declared, listed, undeclared)
	out := make(map[int]addrs.AbsResourceInstance, len(claims))
	for idx, c := range claims {
		out[idx] = c.addr
	}
	return out
}

// addressClaims is [KubernetesAddressBindings]'s work, kept unexported
// because the sweep also needs the import id and identity values a bare
// address does not carry, to build the concrete resolution it replaces the
// orphan with. collisions is every address two or more undeclared objects
// claim, each as its claimants' indexes, in address order: the sweep's
// collision refusal (GitHub issue #1641).
func addressClaims(req Request, manifestType string, declared KubernetesDeclared, listed ListedObjects, undeclared []UndeclaredObject) (bound map[int]addressClaim, collisions []addressCollisionSet) {
	join := newAddressJoin(req, manifestType, undeclared)

	byKey := map[string][]int{}
	claimed := map[int]addressClaim{}
	for i, u := range undeclared {
		c, ok := addressCandidate(req, manifestType, declared, listed, join, u)
		if !ok {
			continue
		}
		key := c.addr.String()
		byKey[key] = append(byKey[key], i)
		claimed[i] = c
	}

	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	bound = map[int]addressClaim{}
	for _, key := range keys {
		idxs := byKey[key]
		if len(idxs) != 1 {
			// Two objects carry one address, and neither is at the
			// namespace and name the configuration names for it: which
			// is the instance's is not something the annotation can say.
			// #1640 left both as orphans, which destroyed both. A
			// create_before_destroy replacement interrupted between its
			// create and its destroy leaves exactly this, and where the
			// node reads the name (#1539's shape) the replacement would
			// be destroyed beside the old object while the node planned
			// a create at the replacement's own name. So the sweep raises
			// the collision refusal AWS raises for two objects carrying
			// one tofu-address (GitHub issue #1641), and binds neither.
			collisions = append(collisions, addressCollisionSet{addr: claimed[idxs[0]].addr, idxs: idxs})
			continue
		}
		bound[idxs[0]] = claimed[idxs[0]]
	}
	return bound, collisions
}

// addressCollisionSet is the undeclared objects, by index, whose
// annotations all name addr.
type addressCollisionSet struct {
	addr addrs.AbsResourceInstance
	idxs []int
}

// bindByAddress binds what the annotation settles, records it in res, and
// reports which of undeclared it settled, by index: bound, or held back as
// one of several claimants of one address (the collision refusal, GitHub
// issue #1641), which are never orphans.
func bindByAddress(req Request, leg KubernetesSweep, declared KubernetesDeclared, listed ListedObjects, undeclared []UndeclaredObject, res *Result) (map[int]bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	claims, collisions := addressClaims(req, leg.ManifestType, declared, listed, undeclared)
	bound := map[int]bool{}
	for _, c := range collisions {
		for _, idx := range c.idxs {
			bound[idx] = true
		}
		diags = diags.Append(problemDiag(res, addressCollision(req, declared, undeclared, c.addr, c.idxs)))
	}
	for idx, u := range undeclared {
		c, ok := claims[idx]
		if !ok {
			continue
		}
		bound[idx] = true
		key := c.addr.String()
		res.Bindings = append(res.Bindings, Binding{
			Addr:        c.addr,
			TypeName:    c.addr.Resource.Resource.Type,
			ImportID:    c.importID,
			Marker:      u.Object.Address,
			DisplayName: u.Kind.Kind + " " + kubesweep.NaturalKey(u.Object.Namespace, u.Object.Name),
		})
		if res.KubernetesAddressBound == nil {
			res.KubernetesAddressBound = map[string]bool{}
		}
		res.KubernetesAddressBound[key] = true
		r := identity.Resolution{
			Addr:           c.addr,
			Class:          identity.ClassConcrete,
			ImportID:       c.importID,
			IdentityValues: c.values,
		}
		replaced := false
		for i := range res.Resolutions {
			if res.Resolutions[i].Addr.String() == key && !res.Resolutions[i].Undeclared {
				res.Resolutions[i] = r
				replaced = true
			}
		}
		if !replaced {
			res.Resolutions = append(res.Resolutions, r)
		}
	}
	return bound, diags
}

// addressClaim is one undeclared object's claim on the declared instance
// its annotation names: the instance, and the import id and identity
// values the object is imported by.
type addressClaim struct {
	addr     addrs.AbsResourceInstance
	importID string
	values   map[string]string
}

// addressCandidate reports the declared instance u's annotation binds it
// to, when every condition in this file's comment holds.
func addressCandidate(req Request, manifestType string, declared KubernetesDeclared, listed ListedObjects, join *addressJoin, u UndeclaredObject) (addressClaim, bool) {
	o, k := u.Object, u.Kind
	if o.Address == "" || o.DeletionTimestamp != "" {
		return addressClaim{}, false
	}
	addr, ok := join.match(o.Address)
	if !ok {
		return addressClaim{}, false
	}
	key := addr.String()

	claim := addressClaim{addr: addr}
	typeName := addr.Resource.Resource.Type
	switch {
	case k.Manifest:
		if typeName != manifestType {
			return addressClaim{}, false
		}
		claim.importID = o.ImportID
	case manifestType != "" && typeName == manifestType:
		// A manifest block may declare a kind a built-in type manages; its
		// object imports by the manifest id all the same.
		claim.importID = kubesweep.ManifestImportID(k.APIVersion, k.Kind, o.Namespace, o.Name)
	case slices.Contains(k.TypeNames, typeName):
		claim.importID = o.ImportID
		claim.values = map[string]string{"name": o.Name}
		if k.Namespaced {
			claim.values["namespace"] = o.Namespace
		}
	default:
		return addressClaim{}, false
	}

	if want, named := declared.Keys[key]; named {
		if want.Kind != k.Kind || listed[want.Kind][want.Key] {
			return addressClaim{}, false
		}
	}

	if !ownsInstance(req, addr) {
		return addressClaim{}, false
	}
	return claim, true
}

// addressCollision is the [ProblemCollision] for two or more listed
// objects whose address annotations name one declared instance that no
// listed object holds by its natural key.
func addressCollision(req Request, declared KubernetesDeclared, undeclared []UndeclaredObject, addr addrs.AbsResourceInstance, idxs []int) Problem {
	ids := make([]string, 0, len(idxs))
	for _, idx := range idxs {
		o := undeclared[idx].Object
		ids = append(ids, kubesweep.NaturalKey(o.Namespace, o.Name))
	}
	sort.Strings(ids)
	kind := undeclared[idxs[0]].Kind.Kind

	var named string
	if want, ok := declared.Keys[addr.String()]; ok {
		named = fmt.Sprintf("The configuration names %s %s for it, which is not listed", want.Kind, want.Key)
	} else {
		named = "The configuration names its object only at plan time, from a value the static evaluator cannot read"
	}
	return Problem{
		Kind:     ProblemCollision,
		TypeName: addr.Resource.Resource.Type,
		Addr:     addr,
		Marker:   markers.EscapeAddress(addr.String()),
		LiveIDs:  ids,
		Detail: fmt.Sprintf(
			"%d live %s objects carry estate %q and the annotation %s=%q at once: %s. %s, so the annotation is the only thing tying these objects to %s, and it cannot say which one is the block's. A create_before_destroy replacement interrupted after creating the new object and before destroying the old one leaves exactly this. Planning both as orphans would destroy the object the block still needs, so neither is proposed for anything. Delete the one the block should not keep (kubectl delete %s -n <namespace> <name>) and re-run; see live/MARKERS.md, \"Ownership semantics\".",
			len(idxs), kind, req.Estate, markers.AddressAnnotation, markers.EscapeAddress(addr.String()), strings.Join(ids, ", "),
			named, addr, strings.ToLower(kind)),
	}
}

// manages reports whether a block of typeName can declare an object of
// kind k: one of the kind's provider types, or the manifest type, which
// can declare any kind.
func manages(manifestType string, k kubesweep.Kind, typeName string) bool {
	if manifestType != "" && typeName == manifestType {
		return true
	}
	return !k.Manifest && slices.Contains(k.TypeNames, typeName)
}

// accountUnaddressed records [Verdicts.KubernetesUnaddressed] for every
// instance in [Request.NodeRefused] this pass owns and did not settle.
// unlisted is every kind whose list failed; settled is bindByAddress's.
func accountUnaddressed(req Request, leg KubernetesSweep, unlisted []kubesweep.Kind, undeclared []UndeclaredObject, settled map[int]bool, res *Result) {
	keys := make([]string, 0, len(req.NodeRefused))
	for key, refused := range req.NodeRefused {
		if refused {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	stmts := moved.Honoured(req.Config)

refused:
	for _, key := range keys {
		if res.KubernetesAddressBound[key] {
			continue
		}
		addr, diags := addrs.ParseAbsResourceInstanceStr(key)
		if diags.HasErrors() || addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		typeName := addr.Resource.Resource.Type
		if !slices.Contains(leg.Types, typeName) && (leg.ManifestType == "" || typeName != leg.ManifestType) {
			continue
		}
		if !ownsInstance(req, addr) {
			continue
		}
		for _, k := range unlisted {
			if manages(leg.ManifestType, k, typeName) {
				// A kind this instance's type can declare was not
				// listed, so whether an unannotated object of it exists
				// is unknown. Absent, and the node's refusal stands.
				continue refused
			}
		}
		objects := []string{}
		for i, u := range undeclared {
			if settled[i] || u.Object.DeletionTimestamp != "" || !manages(leg.ManifestType, u.Kind, typeName) {
				continue
			}
			if u.Object.Address != "" && !namesInstance(stmts, addr, u.Object.Address) {
				if _, ok := UnescapeAddress(u.Object.Address); ok {
					// It names a block, and not this one.
					continue
				}
			}
			// No annotation, one that does not parse, or one that names
			// this very instance and did not bind (GitHub issue #1737):
			// each could be this instance's object, and the last is the
			// likeliest to be. Skipping that one as "annotated for
			// another block" is what let a missed bind lift the refusal
			// and plan a create beside an orphan destroy of the object.
			objects = append(objects, u.Kind.Kind+" "+kubesweep.NaturalKey(u.Object.Namespace, u.Object.Name))
		}
		if res.KubernetesUnaddressed == nil {
			res.KubernetesUnaddressed = map[string][]string{}
		}
		res.KubernetesUnaddressed[key] = objects
	}
}

// ownsInstance reports whether this pass's provider configuration is the
// one addr's block uses: always, on a run with one pass
// ([Request.ScopeProvider] unset). The same question addressCandidate asks.
func ownsInstance(req Request, addr addrs.AbsResourceInstance) bool {
	if req.ScopeProvider.Provider.Type == "" {
		return true
	}
	modCfg, ok := identity.ConfigForModule(req.Config, addr.Module)
	if !ok || modCfg == nil || modCfg.Module == nil {
		return false
	}
	rc := modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	return rc != nil && inScope(req.ScopeProvider, rc, modCfg)
}

// namesInstance reports whether an address annotation names addr: by
// [moved.Accepts], the one definition of "this marker names this
// instance", or - kept so a refusal can only ever be held by this, never
// lifted - by decoding to addr exactly.
func namesInstance(stmts []moved.Statement, addr addrs.AbsResourceInstance, annotation string) bool {
	if moved.Accepts(stmts, addr, annotation) {
		return true
	}
	decoded, ok := UnescapeAddress(annotation)
	return ok && decoded.String() == addr.String()
}

// addressJoin is what an address annotation is compared with: every
// instance the configuration declares (a resolution the caller handed in,
// or an instance [Request.NodeRefused] names), and the addresses the
// honoured moved blocks say each used to have.
//
// It compares escaped strings, as the AWS legs do, and never decodes the
// annotation into an address to compare: decoding reads a for_each key
// made of digits as a count index, so x["0"]'s annotation x:0 would come
// back as x[0], which nothing declares (GitHub issue #1737).
type addressJoin struct {
	declared []addrs.AbsResourceInstance
	// exact is each declared instance's current-grammar escaped address,
	// the common case, answered without a scan.
	exact   map[string][]int
	aliases [][]addrs.AbsResourceInstance
}

// newAddressJoin indexes the declared instances of the types undeclared's
// kinds can be declared by: an annotation naming an instance of any other
// type could not bind to an object of these kinds anyway, and the
// resolutions carry every provider's instances.
func newAddressJoin(req Request, manifestType string, undeclared []UndeclaredObject) *addressJoin {
	types := map[string]bool{}
	if manifestType != "" {
		types[manifestType] = true
	}
	for _, u := range undeclared {
		for _, t := range u.Kind.TypeNames {
			types[t] = true
		}
	}

	seen := map[string]bool{}
	var declared []addrs.AbsResourceInstance
	add := func(a addrs.AbsResourceInstance) {
		key := a.String()
		if seen[key] || a.Resource.Resource.Mode != addrs.ManagedResourceMode || !types[a.Resource.Resource.Type] {
			return
		}
		seen[key] = true
		declared = append(declared, a)
	}
	for _, r := range req.Resolutions {
		if !r.Undeclared {
			add(r.Addr)
		}
	}
	refused := make([]string, 0, len(req.NodeRefused))
	for key, ok := range req.NodeRefused {
		if ok {
			refused = append(refused, key)
		}
	}
	sort.Strings(refused)
	for _, key := range refused {
		if a, diags := addrs.ParseAbsResourceInstanceStr(key); !diags.HasErrors() {
			add(a)
		}
	}

	stmts := moved.Honoured(req.Config)
	j := &addressJoin{declared: declared, exact: map[string][]int{}, aliases: make([][]addrs.AbsResourceInstance, len(declared))}
	for i, a := range declared {
		esc := markers.EscapeAddress(a.String())
		j.exact[esc] = append(j.exact[esc], i)
		j.aliases[i] = moved.Aliases(stmts, a)
	}
	return j
}

// match reports the one declared instance annotation names. A declared
// instance's own address wins over a moved alias, as it does on the AWS
// legs (an alias never displaces an address the configuration still
// declares); two instances named at one level is no match, since the
// annotation cannot say which.
func (j *addressJoin) match(annotation string) (addrs.AbsResourceInstance, bool) {
	if annotation == "" {
		return addrs.AbsResourceInstance{}, false
	}
	if idxs := j.exact[annotation]; len(idxs) > 0 {
		if len(idxs) == 1 {
			return j.declared[idxs[0]], true
		}
		return addrs.AbsResourceInstance{}, false
	}
	one := func(names func(int) bool) (addrs.AbsResourceInstance, int) {
		var hit addrs.AbsResourceInstance
		n := 0
		for i := range j.declared {
			if names(i) {
				hit = j.declared[i]
				n++
			}
		}
		return hit, n
	}
	// An older escaping grammar of a declared address.
	if hit, n := one(func(i int) bool { return markers.AddressMatches(annotation, j.declared[i].String()) }); n > 0 {
		return hit, n == 1
	}
	hit, n := one(func(i int) bool {
		for _, alias := range j.aliases[i] {
			if markers.AddressMatches(annotation, alias.String()) {
				return true
			}
		}
		return false
	})
	return hit, n == 1
}
