// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Server-side dry run as evidence (GitHub issue #1081, item 3). Once the
// plan exists, every kubernetes_manifest instance it proposes to create
// or update is one API object - the planned manifest, with the estate
// label the node stamp wrote into metadata.labels - and the API server
// can be asked about exactly that object without persisting it:
// dryRun=All validates it against the kind's schema, applies the
// server's defaults and runs admission, the estate boundary's
// ValidatingAdmissionPolicy included. That is evidence a locally
// computed plan cannot give (the provider validates a manifest against
// the schema it fetched, and nothing else), and something AWS has no
// equivalent for. A server that says no is a refusal by name, in the
// server's own words, and the plan exits non-zero with nothing applied;
// a server that cannot answer is a coverage gap and a warning, exactly
// as the sweep's own failures are.
//
// Only the manifest shape is reachable. A built-in type's block
// (kubernetes_config_map and the rest) is the provider's schema shape -
// metadata[0], spec blocks - and the mapping from it to the API object
// is the provider's own; this fork does not reproduce it, so those
// instances are not submitted and the documentation says so rather than
// pretending. The caller decides which instances are manifest-shaped by
// schema (markers.ManifestSurface), never by type name.

// SummaryKubernetesDryRunRejected is the refusal: the API server, asked
// to write the planned object with dryRun=All, refused it. An error: the
// apply would fail at this object with the same answer, after writing
// whatever the plan ordered before it.
const SummaryKubernetesDryRunRejected = "Kubernetes API server rejected the planned object"

// SummaryKubernetesDryRunUnavailable is the warning raised when the
// server could not answer a dry run at all - a transport failure, a 5xx,
// a manifest this pass could not submit. The plan stands: the apply is
// the next thing that asks, and it reports its own answer.
const SummaryKubernetesDryRunUnavailable = "Kubernetes dry run unavailable"

// DryRunObject is one planned create or update of a manifest-shaped
// instance: the address and the planned manifest as the API object it
// is, decoded to plain JSON values.
type DryRunObject struct {
	Addr     addrs.AbsResourceInstance
	Manifest map[string]any
	// Update is a planned in-place update (a PUT); a create otherwise.
	Update bool
	// NotSubmitted, when non-empty, is the caller's reason there is no
	// object to submit (Manifest is then nil): the planned manifest holds
	// values not known until apply, or its namespace is created by this
	// same plan and the server cannot judge an object in a namespace that
	// does not exist yet. The evidence says so instead of claiming an
	// answer, and no diagnostic is raised: neither is a gap in the
	// server's answer, both are the plan's own order.
	NotSubmitted string
}

// DryRunEvidence is what the server said about one object, for the view
// to print above the plan: one line per object.
type DryRunEvidence struct {
	Addr      addrs.AbsResourceInstance
	Kind      string
	Namespace string
	Name      string
	Update    bool
	// Defaulted is the count of fields the server's answer added outside
	// metadata and status ([kubesweep.DryRunResult.Defaulted]).
	Defaulted int
	// NotSubmitted, when non-empty, says why no answer exists for this
	// object; every other field but Addr is then meaningless.
	NotSubmitted string
}

// DryRunKubernetesManifests submits every object to sweeper and returns
// the evidence in address order, with a refusal by name for each object
// the server rejected - pointed at the block that declares it when root
// is given - and one warning for each it could not answer for.
func DryRunKubernetesManifests(ctx context.Context, sweeper kubesweep.Sweeper, root *configs.Config, objects []DryRunObject) ([]DryRunEvidence, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if sweeper == nil || len(objects) == 0 {
		return nil, diags
	}
	sorted := append([]DryRunObject(nil), objects...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Addr.String() < sorted[j].Addr.String() })
	var out []DryRunEvidence
	for _, o := range sorted {
		ev := DryRunEvidence{Addr: o.Addr, Update: o.Update}
		if o.NotSubmitted != "" || o.Manifest == nil {
			ev.NotSubmitted = o.NotSubmitted
			if ev.NotSubmitted == "" {
				ev.NotSubmitted = "the planned manifest holds no object to submit"
			}
			out = append(out, ev)
			continue
		}
		ev.Kind, _ = o.Manifest["kind"].(string)
		if meta, ok := o.Manifest["metadata"].(map[string]any); ok {
			ev.Namespace, _ = meta["namespace"].(string)
			ev.Name, _ = meta["name"].(string)
		}
		verb := "create"
		if o.Update {
			verb = "update"
		}
		res, err := sweeper.DryRun(ctx, o.Manifest, o.Update)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryKubernetesDryRunUnavailable,
				fmt.Sprintf("The API server could not answer a dry run of the %s %s plans (%s %s): %s. The plan stands on the provider's own validation; the apply is the next thing that asks the server, and it reports the server's answer.",
					verb, o.Addr, ev.Kind, kubesweep.NaturalKey(ev.Namespace, ev.Name), err)))
			ev.NotSubmitted = "the server could not answer: " + err.Error()
			out = append(out, ev)
			continue
		}
		if !res.Accepted {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  SummaryKubernetesDryRunRejected,
				Detail: fmt.Sprintf("The API server refused the %s %s plans, asked with dryRun=All so nothing was written: %s. The apply would fail at this object with the same answer, after writing whatever the plan ordered before it; nothing is applied until the manifest is one the server accepts.",
					verb, o.Addr, res.Message),
				Subject: manifestBlockRange(root, o.Addr),
			})
			ev.NotSubmitted = "rejected: " + res.Message
			out = append(out, ev)
			continue
		}
		ev.Defaulted = res.Defaulted
		out = append(out, ev)
	}
	return out, diags
}
