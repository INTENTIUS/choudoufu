// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The Kubernetes leg of the estate sweep (GitHub issue #1065, under
// #1016's ruling). One cluster-wide, label-selected list per kind the
// cluster serves; every object that comes back carrying the estate's
// label and no owner reference is either declared - its kind and
// NAMESPACE/NAME natural key match a concrete resolution, whether that
// resolution is a built-in type's block or a kubernetes_manifest block
// naming the same object - or an orphan, and an orphan is filed the way
// the AWS legs file one, so [classifyOrphans] and [applyOrphanPolicy]
// decide its removal with no Kubernetes-specific rule of their own. A
// kind no built-in type manages (every CRD; GitHub issue #1079's third
// unit) is listed under [Request.KubernetesManifestType] and its orphan
// imports by the manifest id ([kubesweep.ManifestImportID]).
//
// The one thing an AWS orphan has that a Kubernetes orphan does not is an
// address: the AWS marker carries tofu-address, and classifyOrphans plans
// the removal at that address. The Kubernetes marker is the estate alone
// (live/MARKERS.md, "Kubernetes: one label"), so the removal is planned at
// a synthetic address, <type>.orphan_<namespace>_<name>
// ([kubesweep.OrphanResourceName]): a resource instance with no
// configuration, which is exactly what stock plans a destroy for. The
// plan names the object by its kind, namespace and name, which is what a
// reader needs; the address is bookkeeping and says so in its name.

// SourceKubernetes marks a [TypeScan] the Kubernetes leg made.
const SourceKubernetes EnumerationSource = "KUBERNETES_API"

// SummaryKubernetesSweepUnavailable is the warning the leg raises when it
// cannot list the cluster at all: a gap in removal coverage, never a wrong
// plan, the same severity [SummaryIncompleteSweep] carries.
const SummaryKubernetesSweepUnavailable = "Kubernetes sweep unavailable"

// sweepKubernetes runs the leg when [Request.Kubernetes] is set.
func sweepKubernetes(ctx context.Context, req Request, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if req.Kubernetes == nil {
		return diags
	}

	// The provider's object-metadata types are the universe, and the
	// configuration's concrete resolutions are what is declared: a
	// declared object is (kind, natural key), whichever type it was
	// declared through - see [DeclaredKubernetesObjects].
	manifestType := req.KubernetesManifestType
	declared := DeclaredKubernetesObjects(req.Resolutions, req.KubernetesTypes, manifestType)
	declaredTypes := declared.Types
	declaredIDs := declared.Objects

	kinds, unserved, err := req.Kubernetes.Kinds(ctx, req.KubernetesTypes, manifestType)
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryKubernetesSweepUnavailable,
			fmt.Sprintf("The cluster's API discovery failed, so no Kubernetes object owned by estate %q could be listed this run: %s. An object whose block was deleted is not proposed for removal until a run can list it.", req.Estate, err)))
		for _, t := range req.KubernetesTypes {
			res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapListFailed, Detail: "API discovery failed: " + err.Error()})
		}
		return diags
	}
	for _, t := range unserved {
		res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapNotListable,
			Detail: "the cluster serves no kind this type manages, or serves it without list and delete verbs, so nothing of this type can exist there to sweep"})
	}
	kindTypes := kubesweep.KindTypes(req.KubernetesTypes)

	// The manifest type is one scan over every kind it lists, not one per
	// kind: a reader of the scan table asks "was kubernetes_manifest
	// swept", and the kinds are the detail of the answer.
	manifestKinds, manifestDeclared := 0, declared.Count()
	for _, k := range kinds {
		objects, ownerSkipped, err := req.Kubernetes.List(ctx, k, markers.TagEstate, req.Estate)
		if err != nil {
			for _, t := range k.TypeNames {
				res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapListFailed,
					Detail: fmt.Sprintf("listing %s across all namespaces failed: %s", k.GVR.String(), err)})
			}
			continue
		}
		if k.Manifest {
			manifestKinds++
		} else {
			for _, t := range k.TypeNames {
				res.Scans = append(res.Scans, TypeScan{
					TypeName:  t,
					Filtering: FilterServerSide,
					Scope:     ScopeEstate,
					Sweep:     true,
					Source:    SourceKubernetes,
					Declared:  len(declaredIDs[k.Kind]),
				})
				res.SweepCovered = append(res.SweepCovered, t)
			}
		}
		res.KubernetesOwnerSkipped += ownerSkipped

		typeName := manifestType
		if !k.Manifest {
			typeName, _ = kubesweep.TypeFor(kindTypes, declaredTypes, k.Kind)
		}
		for _, o := range objects {
			if _, isDeclared := declared.Declares(k.Kind, kubesweep.NaturalKey(o.Namespace, o.Name)); isDeclared {
				continue
			}
			name := kubesweep.OrphanResourceName(o.Namespace, o.Name)
			if k.Manifest {
				name = kubesweep.ManifestOrphanResourceName(k.Kind, o.Namespace, o.Name)
			}
			addr := addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: typeName,
				Name: name,
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
			res.Orphans = append(res.Orphans, OwnedResource{
				TypeName:    typeName,
				ImportID:    o.ImportID,
				Marker:      req.Estate,
				Normalized:  markers.EscapeAddress(addr.String()),
				DisplayName: k.Kind + " " + kubesweep.NaturalKey(o.Namespace, o.Name),
				Tags:        o.Labels,
				Resource:    cty.NilVal,
				Swept:       true,
			})
		}
	}
	if manifestKinds > 0 {
		res.Scans = append(res.Scans, TypeScan{
			TypeName:  manifestType,
			Filtering: FilterServerSide,
			Scope:     ScopeEstate,
			Sweep:     true,
			Source:    SourceKubernetes,
			Declared:  manifestDeclared,
		})
		res.SweepCovered = append(res.SweepCovered, manifestType)
	}
	sort.SliceStable(res.Orphans, func(i, j int) bool {
		if res.Orphans[i].TypeName != res.Orphans[j].TypeName {
			return res.Orphans[i].TypeName < res.Orphans[j].TypeName
		}
		return res.Orphans[i].ImportID < res.Orphans[j].ImportID
	})
	return diags
}

// KubernetesDeclared is what a configuration declares on a cluster, by
// the join a listed object is matched on: the kinds and natural keys of
// every concrete resolution of a Kubernetes type, each naming the block
// that declares it, and the set of Kubernetes types the configuration
// declares at all (which is what [kubesweep.TypeFor] files an undeclared
// object under).
type KubernetesDeclared struct {
	// Types is every Kubernetes resource type the configuration declares
	// a block of, the manifest type included.
	Types map[string]bool
	// Objects is kind -> natural key ([kubesweep.NaturalKey]) -> the
	// declaring instance's address. A block declared through the manifest
	// type is filed under the kind its manifest names, so a ConfigMap
	// declared that way meets a listed ConfigMap exactly as a
	// kubernetes_config_map block's would.
	Objects map[string]map[string]addrs.AbsResourceInstance
}

// Declares reports whether the configuration declares the object of kind
// at key, and which block does.
func (d KubernetesDeclared) Declares(kind, key string) (addrs.AbsResourceInstance, bool) {
	addr, ok := d.Objects[kind][key]
	return addr, ok
}

// Count is how many objects the configuration declares, across kinds.
func (d KubernetesDeclared) Count() int {
	n := 0
	for _, keys := range d.Objects {
		n += len(keys)
	}
	return n
}

// DeclaredKubernetesObjects is the one rule for what a configuration
// declares on a cluster, shared by the sweep (an object nothing declares
// is an orphan) and by live-ls (an object a block declares is reported
// under that block's address). typeNames is the provider's Kubernetes
// type universe, [Request.KubernetesTypes]; manifestType names the
// manifest type among them, or is empty when the provider has none.
//
// A built-in type's resolution declares (kind recovered from the type
// name by [kubesweep.KindOfType], its import id NAMESPACE/NAME or NAME);
// a manifest type's resolution declares (the kind its manifest names, the
// natural key read back from the manifest import id). A resolution that
// is not concrete declares nothing yet - its object cannot be named until
// the run that creates it - but still counts its type as declared.
func DeclaredKubernetesObjects(resolutions []identity.Resolution, typeNames []string, manifestType string) KubernetesDeclared {
	out := KubernetesDeclared{
		Types:   map[string]bool{},
		Objects: map[string]map[string]addrs.AbsResourceInstance{},
	}
	kindOf := map[string]string{}
	for _, t := range typeNames {
		if t == manifestType {
			continue
		}
		if kind, _, ok := kubesweep.KindOfType(t); ok {
			kindOf[t] = kind
		}
	}
	declare := func(kind, key string, addr addrs.AbsResourceInstance) {
		if out.Objects[kind] == nil {
			out.Objects[kind] = map[string]addrs.AbsResourceInstance{}
		}
		out.Objects[kind][key] = addr
	}
	for _, r := range resolutions {
		t := r.Addr.Resource.Resource.Type
		if manifestType != "" && t == manifestType {
			out.Types[t] = true
			if r.Class != identity.ClassConcrete {
				continue
			}
			if _, kind, namespace, name, ok := kubesweep.ParseManifestImportID(r.ImportID); ok {
				declare(kind, kubesweep.NaturalKey(namespace, name), r.Addr)
			}
			continue
		}
		kind, ok := kindOf[t]
		if !ok {
			continue
		}
		out.Types[t] = true
		if r.Class != identity.ClassConcrete || r.ImportID == "" {
			continue
		}
		declare(kind, r.ImportID, r.Addr)
	}
	return out
}
