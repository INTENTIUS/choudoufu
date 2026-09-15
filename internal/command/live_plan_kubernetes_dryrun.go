// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The command layer's half of the server-side dry run (GitHub issue
// #1081, item 3; the engine half is discovery.DryRunKubernetesManifests).
// This is the one seam where a planned Kubernetes object exists as a
// value: the plan is in hand, and each change's After is the planned
// resource, the manifest inside it already carrying the estate label the
// node stamp wrote. The cluster client is the sweep's, built from the
// provider block at the plan's first cluster contact and kept on
// statelessProviders for this moment, since the provider plugins that
// projected the prior state are closed by now and the sweep's client is
// not a plugin.

// statelessKubernetesDryRun submits every planned create or update of a
// manifest-shaped instance to the cluster its provider configuration
// names, one dry run each, and returns the evidence for the view and the
// refusals for the run. An instance is manifest-shaped by its schema
// (markers.ManifestSurface), never by its type name. A provider
// configuration with no client (the sweep already said so) keeps its
// instances out of the evidence rather than raising a second warning
// about the same cluster.
func statelessKubernetesDryRun(ctx context.Context, sweepers map[string]kubesweep.Sweeper, config *configs.Config, plan *plans.Plan, schemas *tofu.Schemas) ([]views.StatelessKubernetesDryRun, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if plan == nil || plan.Changes == nil || schemas == nil || len(sweepers) == 0 {
		return nil, diags
	}
	// A namespace this same plan creates does not exist when the dry run
	// is asked, and the server would answer 404 for every object inside
	// it - the apply's ordering, not the object's validity. Those objects
	// are reported as not submitted rather than as refused.
	plannedNamespaces := map[string]bool{}
	for _, rc := range plan.Changes.Resources {
		if rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode || rc.Action != plans.Create {
			continue
		}
		schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
		if name, ok := plannedNamespaceName(rc, schema); ok {
			plannedNamespaces[name] = true
		}
	}
	byProvider := map[string][]discovery.DryRunObject{}
	var keys []string
	for _, rc := range plan.Changes.Resources {
		if rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if rc.Action != plans.Create && rc.Action != plans.Update {
			// A replace is a delete and a create the server would judge
			// in that order; a PUT of the new object against the old one
			// would answer the wrong question (an immutable field, most
			// often, which is why it is a replace at all).
			continue
		}
		schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
		if schema == nil || !markers.ManifestSurface(schema.Block) {
			continue
		}
		key := providerCacheKey(rc.ProviderAddr)
		if sweepers[key] == nil {
			continue
		}
		obj, err := kubernetesDryRunObject(rc, schema)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, discovery.SummaryKubernetesDryRunUnavailable,
				fmt.Sprintf("The planned value of %s could not be read back as an API object for a dry run: %s. The plan stands on the provider's own validation.", rc.Addr, err)))
			continue
		}
		if ns := manifestNamespace(obj.Manifest); ns != "" && plannedNamespaces[ns] {
			obj.Manifest = nil
			obj.NotSubmitted = fmt.Sprintf("its namespace %s is created by this same plan, and the server cannot judge an object in a namespace that does not exist yet; apply the namespace first to see the server's answer", ns)
		}
		if _, seen := byProvider[key]; !seen {
			keys = append(keys, key)
		}
		byProvider[key] = append(byProvider[key], obj)
	}
	sort.Strings(keys)
	var out []views.StatelessKubernetesDryRun
	for _, key := range keys {
		evidence, dryDiags := discovery.DryRunKubernetesManifests(ctx, sweepers[key], config, byProvider[key])
		diags = diags.Append(dryDiags)
		for _, e := range evidence {
			out = append(out, views.StatelessKubernetesDryRun{
				Addr:         e.Addr.String(),
				Kind:         e.Kind,
				Namespace:    e.Namespace,
				Name:         e.Name,
				Update:       e.Update,
				Defaulted:    e.Defaulted,
				NotSubmitted: e.NotSubmitted,
			})
		}
	}
	return out, diags
}

// kubernetesDryRunObject reads one planned change's manifest back as the
// API object it is: the After value decoded against the schema, its
// marks dropped (a sensitive value is still the value the server would
// be sent), the manifest attribute rendered through cty's own JSON
// encoding and read back as plain values, which is exactly what the
// provider sends. A manifest with any value not yet known reports
// Unknown rather than a half-object.
func kubernetesDryRunObject(rc *plans.ResourceInstanceChangeSrc, schema *providers.Schema) (discovery.DryRunObject, error) {
	obj := discovery.DryRunObject{Addr: rc.Addr, Update: rc.Action == plans.Update}
	change, err := rc.Decode(schema)
	if err != nil {
		return obj, err
	}
	after, _ := change.After.UnmarkDeep()
	if after.IsNull() || !after.IsKnown() || !after.Type().IsObjectType() || !after.Type().HasAttribute(markers.ManifestSurfaceAttr) {
		return obj, fmt.Errorf("the planned value carries no %s attribute", markers.ManifestSurfaceAttr)
	}
	manifest := after.GetAttr(markers.ManifestSurfaceAttr)
	if manifest.IsNull() {
		return obj, fmt.Errorf("the planned %s is null", markers.ManifestSurfaceAttr)
	}
	if !manifest.IsWhollyKnown() {
		obj.NotSubmitted = "the planned manifest holds values not known until apply, so there is no object to submit yet"
		return obj, nil
	}
	if !manifest.Type().IsObjectType() && !manifest.Type().IsMapType() {
		return obj, fmt.Errorf("the planned %s is a %s, not an object", markers.ManifestSurfaceAttr, manifest.Type().FriendlyName())
	}
	raw, err := ctyjson.Marshal(manifest, manifest.Type())
	if err != nil {
		return obj, fmt.Errorf("encoding the planned %s: %w", markers.ManifestSurfaceAttr, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return obj, fmt.Errorf("reading the planned %s back: %w", markers.ManifestSurfaceAttr, err)
	}
	obj.Manifest = m
	return obj, nil
}

// manifestNamespace reads metadata.namespace off a decoded manifest, or
// "" for a cluster-scoped object.
func manifestNamespace(manifest map[string]any) string {
	meta, _ := manifest["metadata"].(map[string]any)
	ns, _ := meta["namespace"].(string)
	return ns
}

// plannedNamespaceName reports the name of the Namespace one planned
// create makes, whichever shape declares it: a manifest-shaped instance
// whose manifest's kind is Namespace, or a metadata-shaped instance whose
// type manages that kind (kubesweep.KindOfType, the sweep's own join),
// with metadata[0].name. ok is false for any other change, and for a name
// not yet known.
func plannedNamespaceName(rc *plans.ResourceInstanceChangeSrc, schema *providers.Schema) (string, bool) {
	if schema == nil || schema.Block == nil {
		return "", false
	}
	if markers.ManifestSurface(schema.Block) {
		obj, err := kubernetesDryRunObject(rc, schema)
		if err != nil || obj.Manifest == nil {
			return "", false
		}
		if kind, _ := obj.Manifest["kind"].(string); kind != "Namespace" {
			return "", false
		}
		meta, _ := obj.Manifest["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		return name, name != ""
	}
	if _, ok := markers.LabelSurface(schema.Block); !ok {
		return "", false
	}
	if kind, _, ok := kubesweep.KindOfType(rc.Addr.Resource.Resource.Type); !ok || kind != "Namespace" {
		return "", false
	}
	change, err := rc.Decode(schema)
	if err != nil {
		return "", false
	}
	after, _ := change.After.UnmarkDeep()
	if after.IsNull() || !after.IsKnown() || !after.Type().IsObjectType() || !after.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return "", false
	}
	meta := after.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || !meta.CanIterateElements() || meta.LengthInt() != 1 {
		return "", false
	}
	it := meta.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() || !elem.Type().HasAttribute("name") {
		return "", false
	}
	name := elem.GetAttr("name")
	if name.IsNull() || !name.IsKnown() || name.Type() != cty.String {
		return "", false
	}
	return name.AsString(), name.AsString() != ""
}

// kubernetesSweepers is a copy of every client kept, for a caller that
// outlives the providers.
func (p *statelessProviders) kubernetesSweepers() map[string]kubesweep.Sweeper {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.kubeSweepers) == 0 {
		return nil
	}
	out := make(map[string]kubesweep.Sweeper, len(p.kubeSweepers))
	for k, v := range p.kubeSweepers {
		out[k] = v
	}
	return out
}

// AfterPlan implements [local.StatelessRun] (GitHub issue #1081, item 3):
// the server-side dry run of every planned kubernetes_manifest create or
// update, through the sweep's own cluster clients captured in PriorState,
// printed above the plan by this run's view. A rejection is an error and
// the backend stops with nothing rendered and nothing applied.
func (r *statelessRunner) AfterPlan(ctx context.Context, config *configs.Config, plan *plans.Plan, schemas *tofu.Schemas) tfdiags.Diagnostics {
	evidence, diags := statelessKubernetesDryRun(ctx, r.kubeSweepers, config, plan, schemas)
	if r.view != nil {
		r.view.KubernetesDryRun(evidence)
	}
	return diags
}

// rememberKubernetesSweeper keeps the sweep's client for the post-plan dry
// run, keyed the way every configured provider instance is.
func (p *statelessProviders) rememberKubernetesSweeper(addr addrs.AbsProviderConfig, sweeper kubesweep.Sweeper) {
	if sweeper == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kubeSweepers == nil {
		p.kubeSweepers = map[string]kubesweep.Sweeper{}
	}
	p.kubeSweepers[providerCacheKey(addr)] = sweeper
}
