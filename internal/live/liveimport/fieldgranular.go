// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// The field-granular carrier (GitHub issue #1863, the third of PR #1828's
// follow-ups to #1191).
//
// kubernetes_labels, kubernetes_annotations, kubernetes_env,
// kubernetes_config_map_v1_data, kubernetes_secret_v1_data and
// kubernetes_node_taint write fields of an object they do not own, and
// under a live block their ownership marker is the server-side-apply field
// manager the write is made under, "choudoufu:<estate>". A stock state
// file's instance of one wrote under the manager its block named - the
// provider's default, "Terraform", unless it set field_manager. Before
// this carrier existed, ratification found no marker surface on these
// types and reported them UNTAGGABLE ("no tags argument"), so a migration
// wrote nothing and the first live plan, reading under the estate's
// manager, proposed every one of them as a create.
//
// What a migration owes such an instance is the marker, and the marker is
// ownership: Approve moves ownership of exactly the fields the state
// records the instance writing from the manager the state names to the
// estate's (kubesweep.TransferFieldOwnership), with no change to any
// value. The first live plan then reads the instance under the estate's
// manager and finds it, as a migrated labelled object is found by its
// label. What is refused, by name, never written:
//
//   - a state naming another estate's manager, "choudoufu:<other>": a
//     migration never takes another estate's fields;
//   - a recorded object this run cannot name (no metadata.name, an
//     unknown kind) or whose written fields cannot be read;
//   - a run with no cluster client, or one that cannot move ownership;
//   - an object on which neither the state's manager nor the estate's owns
//     any of the recorded fields - something else took them, and handing
//     over nothing would report a migration that did not happen.

// fieldGranularType reports whether schema is the field-granular shape on
// the Kubernetes family's provider. It never reads the type name.
func fieldGranularType(providerAddr addrs.AbsProviderConfig, schema providers.Schema) bool {
	if s, ok := substrate.ForProvider(providerAddr.Provider.Type); !ok || s != substrate.Kubernetes {
		return false
	}
	_, ok := substrate.FieldGranularShape(schema.Block)
	return ok
}

// stateFieldManager is the field manager the state's object names, or
// the provider's default when it names none.
func stateFieldManager(obj cty.Value) string {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(substrate.FieldManagerAttr) {
		return kubesweep.DefaultFieldManager
	}
	v, _ := obj.GetAttr(substrate.FieldManagerAttr).Unmark()
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String || v.AsString() == "" {
		return kubesweep.DefaultFieldManager
	}
	return v.AsString()
}

// ratifyFieldGranular fills elig's field-granular carrier from the state's
// own recorded object and says in entry what -approve will do, so the
// read-only run tells an operator about a hand-over -approve will refuse.
func ratifyFieldGranular(ctx context.Context, req Request, entry *Entry, elig *eligible, providerAddr addrs.AbsProviderConfig, schema providers.Schema, typeName string, priorVal cty.Value) {
	elig.fieldGranular = true
	elig.fieldManager = stateFieldManager(priorVal)
	elig.fieldWrite, elig.fieldWriteOK = discovery.FieldGranularWriteOf(typeName, schema.Block, priorVal)
	elig.fieldWrite.Addr = entry.Addr
	if elig.fieldWriteOK {
		entry.LiveID = elig.fieldWrite.Object.String()
	}

	patcher, err := manifestPatcher(ctx, req, providerAddr)
	elig.patcherErr = err
	if patcher != nil {
		if t, ok := patcher.(kubesweep.FieldTransferer); ok {
			elig.transferer = t
		} else {
			elig.patcherErr = fmt.Errorf("the cluster client built for %s cannot move field ownership", providerAddr)
		}
	}

	if why := fieldGranularRefusal(req.Estate, elig); why != "" {
		entry.Detail += " " + why + " -approve will report it failed."
		return
	}
	if elig.fieldManager != markers.FieldManagerFor(req.Estate) {
		entry.Detail += fmt.Sprintf(" -approve hands the fields it writes on %s from field manager %q to this estate's, %q.", elig.fieldWrite.Object, elig.fieldManager, markers.FieldManagerFor(req.Estate))
	}
}

// fieldGranularRefusal is why -approve will not hand e's fields over, or
// "" when it may try. The live object's own answer is Approve's.
func fieldGranularRefusal(estate string, e *eligible) string {
	if why := markers.ValidFieldManagerEstate(estate); why != "" {
		return fmt.Sprintf("This estate's name cannot be carried as a field manager: %s.", why)
	}
	if other, ok := markers.EstateOfFieldManager(e.fieldManager); ok && other != estate {
		return fmt.Sprintf("The state records field_manager = %q, the estate %q's field manager. A migration never takes another estate's fields.", e.fieldManager, other)
	}
	if !e.fieldWriteOK {
		return fmt.Sprintf("The state's recorded %s does not name the object it patches (metadata.name, and the apiVersion and kind for a type that names its kind) or the fields it writes, so the fields cannot be handed to this estate's field manager.", e.typeName)
	}
	if len(e.fieldWrite.Writes) == 0 {
		return fmt.Sprintf("The state's recorded %s writes no field, so there is nothing to hand to this estate's field manager.", e.typeName)
	}
	if e.transferer == nil {
		return fmt.Sprintf("No cluster client that can move field ownership was built: %s.", e.patcherErr)
	}
	return ""
}

// approveFieldGranular is Approve's write for a field-granular instance;
// see this file's doc comment.
func approveFieldGranular(ctx context.Context, estate string, addr addrs.AbsResourceInstance, e *eligible) StampOutcome {
	out := StampOutcome{Addr: addr, TypeName: e.typeName}
	if why := fieldGranularRefusal(estate, e); why != "" {
		out.Outcome = OutcomeFailed
		out.Detail = why + " Nothing was written."
		return out
	}
	want := markers.FieldManagerFor(estate)
	ref := e.fieldWrite.Object

	if e.fieldManager != want {
		moved, err := e.transferer.TransferFields(ctx, ref, e.fieldManager, want, e.fieldWrite.Writes)
		if err != nil {
			out.Outcome = OutcomeFailed
			out.Detail = fmt.Sprintf("Handing the fields of %s from field manager %q to %q failed: %s. Nothing was written; rerun the same live-import -approve command.", ref, e.fieldManager, want, err)
			return out
		}
		if moved {
			out.Outcome = OutcomeStamped
			out.Detail = fmt.Sprintf("Handed %s's fields on %s (%s) from field manager %q to %q.", e.typeName, ref, fieldGranularMembers(e.fieldWrite.Writes), e.fieldManager, want)
			return out
		}
	}

	// Nothing to move: the estate's manager owns the fields already (a
	// rerun, or a state written under it), or nobody this run may take
	// them from does.
	live, found, err := e.transferer.ReadObject(ctx, ref)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The cluster could not be asked about %s: %s. Nothing was written.", ref, err)
		return out
	}
	if !found {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The cluster serves no object at %s, so there are no fields to hand over. Nothing was written.", ref)
		return out
	}
	for _, w := range e.fieldWrite.Writes {
		for _, o := range kubesweep.FieldOwners(live, w, "") {
			if o.Manager == want {
				out.Outcome = OutcomeAlreadyStamped
				out.Detail = fmt.Sprintf("This estate's field manager %q already owns the fields on %s; nothing written.", want, ref)
				return out
			}
		}
	}
	out.Outcome = OutcomeFailed
	out.Detail = fmt.Sprintf("Neither field manager %q, which the state records, nor this estate's %q owns any of the fields the state records on %s (%s). Something else holds them now, and a migration hands over only what the migrated block wrote. Nothing was written; the first live plan proposes writing them.", e.fieldManager, want, ref, fieldGranularMembers(e.fieldWrite.Writes))
	return out
}

// fieldGranularMembers names a write's members for a reader.
func fieldGranularMembers(writes []kubesweep.FieldWrite) string {
	var parts []string
	for _, w := range writes {
		parts = append(parts, w.Members...)
		if len(w.Members) == 0 {
			parts = append(parts, strings.Join(w.Root, "."))
		}
	}
	return strings.Join(parts, ", ")
}
