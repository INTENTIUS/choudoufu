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
//   - the annotation parses as an instance address this configuration
//     declares: a resolution the caller handed in, or an instance the
//     static evaluator refused ([Request.NodeRefused]). An address nothing
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

// undeclaredObject is one listed object no natural key declares, with the
// kind it was listed under and the type an orphan of it is filed at.
type undeclaredObject struct {
	kind     kubesweep.Kind
	typeName string
	object   kubesweep.Object
}

// listedObjects is every object the leg listed, by kind and natural key.
type listedObjects map[string]map[string]bool

func (l listedObjects) add(kind, key string) {
	if l[kind] == nil {
		l[kind] = map[string]bool{}
	}
	l[kind][key] = true
}

// bindByAddress binds what the annotation settles, records it in res, and
// reports which of undeclared it settled, by index: bound, or held back as
// one of several claimants of one address (the collision refusal, GitHub
// issue #1641), which are never orphans.
func bindByAddress(req Request, leg KubernetesSweep, declared KubernetesDeclared, listed listedObjects, undeclared []undeclaredObject, res *Result) (map[int]bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	// What the configuration declares, by address: every resolution it
	// handed in, and every instance the static evaluator refused.
	resolved := make(map[string]identity.Resolution, len(req.Resolutions))
	for _, r := range req.Resolutions {
		if !r.Undeclared {
			resolved[r.Addr.String()] = r
		}
	}

	claims := map[string][]addressClaim{}
	var order []string
	for i, u := range undeclared {
		c, ok := addressCandidate(req, leg.ManifestType, declared, listed, resolved, u)
		if !ok {
			continue
		}
		key := c.addr.String()
		if _, seen := claims[key]; !seen {
			order = append(order, key)
		}
		c.idx = i
		claims[key] = append(claims[key], c)
	}

	bound := map[int]bool{}
	for _, key := range order {
		cs := claims[key]
		if len(cs) != 1 {
			// Two objects carry one address, and neither is at the
			// namespace and name the configuration names for it: which
			// is the instance's is not something the annotation can say.
			// #1640 left both as orphans, which destroyed both. A
			// create_before_destroy replacement interrupted between its
			// create and its destroy leaves exactly this, and where the
			// node reads the name (#1539's shape) the replacement would
			// be destroyed beside the old object while the node planned
			// a create at the replacement's own name. So it is the
			// collision refusal AWS raises for two objects carrying one
			// tofu-address (GitHub issue #1641), and neither object is
			// proposed for anything.
			for _, c := range cs {
				bound[c.idx] = true
			}
			diags = diags.Append(problemDiag(res, addressCollision(req, declared, undeclared, cs)))
			continue
		}
		c := cs[0]
		u := undeclared[c.idx]
		bound[c.idx] = true
		res.Bindings = append(res.Bindings, Binding{
			Addr:        c.addr,
			TypeName:    c.addr.Resource.Resource.Type,
			ImportID:    c.importID,
			Marker:      u.object.Address,
			DisplayName: u.kind.Kind + " " + kubesweep.NaturalKey(u.object.Namespace, u.object.Name),
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

// addressCollision is the [ProblemCollision] for two or more listed
// objects whose address annotations name one declared instance that no
// listed object holds by its natural key.
func addressCollision(req Request, declared KubernetesDeclared, undeclared []undeclaredObject, cs []addressClaim) Problem {
	addr := cs[0].addr
	ids := make([]string, 0, len(cs))
	for _, c := range cs {
		o := undeclared[c.idx].object
		ids = append(ids, kubesweep.NaturalKey(o.Namespace, o.Name))
	}
	sort.Strings(ids)
	kind := undeclared[cs[0].idx].kind.Kind

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
			len(cs), kind, req.Estate, markers.AddressAnnotation, markers.EscapeAddress(addr.String()), strings.Join(ids, ", "),
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
func accountUnaddressed(req Request, leg KubernetesSweep, unlisted []kubesweep.Kind, undeclared []undeclaredObject, settled map[int]bool, res *Result) {
	keys := make([]string, 0, len(req.NodeRefused))
	for key, refused := range req.NodeRefused {
		if refused {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

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
			if settled[i] || u.object.DeletionTimestamp != "" || !manages(leg.ManifestType, u.kind, typeName) {
				continue
			}
			if u.object.Address != "" {
				if _, ok := UnescapeAddress(u.object.Address); ok {
					// It names a block, and not this one: this instance
					// was not bound to it.
					continue
				}
			}
			objects = append(objects, u.kind.Kind+" "+kubesweep.NaturalKey(u.object.Namespace, u.object.Name))
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

// addressClaim is one undeclared object's claim on the declared instance
// its annotation names: the instance, and the import id and identity
// values the object is imported by.
type addressClaim struct {
	idx      int
	addr     addrs.AbsResourceInstance
	importID string
	values   map[string]string
}

// addressCandidate reports the declared instance u's annotation binds it
// to, when every condition in this file's comment holds.
func addressCandidate(req Request, manifestType string, declared KubernetesDeclared, listed listedObjects, resolved map[string]identity.Resolution, u undeclaredObject) (addressClaim, bool) {
	o, k := u.object, u.kind
	if o.Address == "" || o.DeletionTimestamp != "" {
		return addressClaim{}, false
	}
	addr, ok := UnescapeAddress(o.Address)
	if !ok {
		return addressClaim{}, false
	}
	key := addr.String()
	if _, isResolved := resolved[key]; !isResolved && !req.NodeRefused[key] {
		return addressClaim{}, false
	}

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
