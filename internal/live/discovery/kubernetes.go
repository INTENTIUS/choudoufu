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
// provider has a type for; every object that comes back carrying the
// estate's label and no owner reference is either declared - its type and
// NAMESPACE/NAME identity match a concrete resolution - or an orphan, and
// an orphan is filed the way the AWS legs file one, so [classifyOrphans]
// and [applyOrphanPolicy] decide its removal with no Kubernetes-specific
// rule of their own.
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
	// declared object is (type, import id), and a listed object of a kind
	// managed by more than one type name is declared if any of those
	// names declares it.
	declaredTypes := map[string]bool{}
	declaredIDs := map[string]map[string]bool{} // kind -> import id -> true
	kindOf := map[string]string{}
	for _, t := range req.KubernetesTypes {
		if kind, _, ok := kubesweep.KindOfType(t); ok {
			kindOf[t] = kind
		}
	}
	for _, r := range req.Resolutions {
		t := r.Addr.Resource.Resource.Type
		kind, ok := kindOf[t]
		if !ok {
			continue
		}
		declaredTypes[t] = true
		if r.Class != identity.ClassConcrete || r.ImportID == "" {
			continue
		}
		if declaredIDs[kind] == nil {
			declaredIDs[kind] = map[string]bool{}
		}
		declaredIDs[kind][r.ImportID] = true
	}

	kinds, unserved, err := req.Kubernetes.Kinds(ctx, req.KubernetesTypes)
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

	for _, k := range kinds {
		objects, ownerSkipped, err := req.Kubernetes.List(ctx, k, markers.TagEstate, req.Estate)
		if err != nil {
			for _, t := range k.TypeNames {
				res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapListFailed,
					Detail: fmt.Sprintf("listing %s across all namespaces failed: %s", k.GVR.String(), err)})
			}
			continue
		}
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
		res.KubernetesOwnerSkipped += ownerSkipped

		typeName, _ := kubesweep.TypeFor(kindTypes, declaredTypes, k.Kind)
		for _, o := range objects {
			if declaredIDs[k.Kind][o.ImportID] {
				continue
			}
			addr := addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: typeName,
				Name: kubesweep.OrphanResourceName(o.Namespace, o.Name),
			}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
			res.Orphans = append(res.Orphans, OwnedResource{
				TypeName:    typeName,
				ImportID:    o.ImportID,
				Marker:      req.Estate,
				Normalized:  markers.EscapeAddress(addr.String()),
				DisplayName: k.Kind + " " + o.ImportID,
				Tags:        o.Labels,
				Resource:    cty.NilVal,
				Swept:       true,
			})
		}
	}
	sort.SliceStable(res.Orphans, func(i, j int) bool {
		if res.Orphans[i].TypeName != res.Orphans[j].TypeName {
			return res.Orphans[i].TypeName < res.Orphans[j].TypeName
		}
		return res.Orphans[i].ImportID < res.Orphans[j].ImportID
	})
	return diags
}
