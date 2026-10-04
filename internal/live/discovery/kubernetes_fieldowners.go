// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The estate boundary for a resource that owns fields rather than an
// object (GitHub issue #1191, ruled 2026-10-03; #1106 section 3's second
// control). A field-granular Kubernetes block writes under its estate's
// field manager, "choudoufu:<estate>" ([markers.FieldManagerFor]), so the
// API server's own metadata.managedFields says which estate wrote each
// field, and two estates can meet on one object - an object neither owns,
// one field each - which no whole-object type can (the estate label stops
// the second estate two layers before server-side apply is reached).
//
// What this pass decides, per planned create or update, after the plan
// exists and before anything is applied:
//
//   - force across estates is refused, by name. A planned write with
//     force = true over a field another estate's manager owns would take
//     that field from the other estate with a flag; a boundary a flag can
//     cross is a suggestion. An error, naming the owning estate.
//   - force against any other manager - a controller, kubectl, the
//     provider's default "Terraform" - keeps its ordinary meaning and is
//     not this pass's business.
//   - a write without force over another estate's field is a warning
//     naming the estate: the API server will refuse the apply with a 409
//     naming the same manager, so the operator learns whose field it is
//     before the apply rather than from it.
//   - two blocks of ONE estate patching one object are refused: both write
//     under the one manager, and server-side apply removes a manager's
//     fields its next apply leaves out, so each apply would erase the
//     other's writes.

// The pass's diagnostics.
const (
	// SummaryFieldForceAcrossEstates is the refusal: force = true over a
	// field another estate's field manager owns.
	SummaryFieldForceAcrossEstates = "Force refused over another estate's field"

	// SummaryFieldOwnedByEstate is the warning: a write without force over
	// a field another estate owns, which the API server will refuse.
	SummaryFieldOwnedByEstate = "Field owned by another estate"

	// SummaryFieldGranularSameObject is the refusal: two field-granular
	// blocks of one estate patch one object under one field manager.
	SummaryFieldGranularSameObject = "Two field-granular blocks patch one object"

	// SummaryFieldSharedWithStock is the warning: a planned create writes
	// fields stock's default field manager, "Terraform", already owns on
	// the object, and nothing records this instance as migrated, so they
	// are not taken over (GitHub issue #1863).
	SummaryFieldSharedWithStock = "Field shared with the stock field manager"

	// SummaryFieldOwnersUnavailable is the warning: the object could not be
	// read back, so whose fields the write meets is unknown. The apply is
	// the next thing that asks, and the API server answers it.
	SummaryFieldOwnersUnavailable = "Field owners unavailable"
)

// FieldGranularWrite is one planned create or update of a field-granular
// instance: the object it patches and where in it the write lands.
type FieldGranularWrite struct {
	Addr   addrs.AbsResourceInstance
	Object kubesweep.ObjectRef
	Writes []kubesweep.FieldWrite
	Force  bool
	// Create says the plan creates the instance: no prior under any
	// manager. A create over fields stock's default manager owns is
	// warned about (GitHub issue #1863).
	Create bool
	// HandoverFrom is the stock field manager a planned migration
	// hand-over takes the fields from (GitHub issue #1863); empty for
	// every other write.
	HandoverFrom string
}

// SameObjectFieldWrites refuses every object more than one of writes
// patches. The caller passes the writes of one cluster - one provider
// configuration - since the same name on two clusters is two objects.
func SameObjectFieldWrites(root *configs.Config, estate string, writes []FieldGranularWrite) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	byObject := map[string][]addrs.AbsResourceInstance{}
	var keys []string
	for _, w := range writes {
		k := w.Object.String()
		if _, seen := byObject[k]; !seen {
			keys = append(keys, k)
		}
		byObject[k] = append(byObject[k], w.Addr)
	}
	sort.Strings(keys)
	for _, k := range keys {
		addrsHere := byObject[k]
		if len(addrsHere) < 2 {
			continue
		}
		names := make([]string, len(addrsHere))
		for i, a := range addrsHere {
			names[i] = a.String()
		}
		sort.Strings(names)
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  SummaryFieldGranularSameObject,
			Detail: fmt.Sprintf(
				"%s all patch %s, and every field-granular write of the estate %q is made under one field manager, %q. Server-side apply removes the fields a manager's next apply leaves out, so each of these blocks would erase what the others wrote on every apply. Keep each object to one field-granular block per estate, or move the others to a second estate.",
				strings.Join(names, ", "), k, estate, markers.FieldManagerFor(estate)),
			Subject: manifestBlockRange(root, addrsHere[0]),
		})
	}
	return diags
}

// CheckKubernetesFieldOwners reads each planned write's object back and
// judges it against the other managers that own the fields it writes; see
// this file's doc comment for what each finding is.
func CheckKubernetesFieldOwners(ctx context.Context, reader kubesweep.ObjectReader, root *configs.Config, estate string, writes []FieldGranularWrite) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if reader == nil || estate == "" || len(writes) == 0 {
		return diags
	}
	ours := markers.FieldManagerFor(estate)
	sorted := append([]FieldGranularWrite(nil), writes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Addr.String() < sorted[j].Addr.String() })
	for _, w := range sorted {
		obj, found, err := reader.ReadObject(ctx, w.Object)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryFieldOwnersUnavailable,
				fmt.Sprintf("%s patches %s, which could not be read back for its metadata.managedFields: %s. Whose fields this write meets is unknown; the apply is the next thing that asks, and the API server answers it.", w.Addr, w.Object, err)))
			continue
		}
		if !found {
			// The object this block patches does not exist yet: nothing
			// owns any field of it, and the provider's own apply reports
			// the absence.
			continue
		}
		owners := map[string][]string{}
		var estates []string
		var stock []string
		for _, write := range w.Writes {
			for _, o := range kubesweep.FieldOwners(obj, write, ours) {
				other, ok := markers.EstateOfFieldManager(o.Manager)
				if !ok && w.Create && o.Manager == kubesweep.DefaultFieldManager {
					what := o.Members
					if o.Atomic {
						what = []string{strings.Join(write.Root, ".") + " as a whole"}
					}
					stock = append(stock, what...)
				}
				if !ok {
					// Not an estate's manager: force keeps its ordinary
					// meaning against it (#1106 section 3).
					continue
				}
				if _, seen := owners[other]; !seen {
					estates = append(estates, other)
				}
				what := o.Members
				if o.Atomic {
					what = []string{strings.Join(write.Root, ".") + " as a whole"}
				}
				owners[other] = append(owners[other], what...)
			}
		}
		if len(stock) > 0 {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  SummaryFieldSharedWithStock,
				Detail: fmt.Sprintf(
					"%s creates fields of %s that field manager %q already owns (%s), and nothing in this estate's records says the block was migrated from a stock state file, so they are not taken over: the apply shares them with %q, which keeps its claim. If this block is the one stock applied, run choudoufu live-import -approve against that state file first, and the next plan hands the fields over instead.",
					w.Addr, w.Object, kubesweep.DefaultFieldManager, strings.Join(stock, ", "), kubesweep.DefaultFieldManager),
				Subject: manifestBlockRange(root, w.Addr),
			})
		}
		sort.Strings(estates)
		for _, other := range estates {
			fields := strings.Join(owners[other], ", ")
			if w.Force {
				diags = diags.Append(&hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  SummaryFieldForceAcrossEstates,
					Detail: fmt.Sprintf(
						"%s sets force = true and writes fields of %s that the estate %q owns (field manager %q: %s). Forcing would take them from that estate with a flag, and an estate boundary a flag can cross is not a boundary, so the plan stops with nothing applied. Force keeps its ordinary meaning against any manager that is not an estate's. To move the fields between estates, remove them from %q's configuration and apply there first.",
						w.Addr, w.Object, other, markers.FieldManagerFor(other), fields, other),
					Subject: manifestBlockRange(root, w.Addr),
				})
				continue
			}
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Summary:  SummaryFieldOwnedByEstate,
				Detail: fmt.Sprintf(
					"%s writes fields of %s that the estate %q owns (field manager %q: %s). Where the planned value differs from the live one, the API server refuses the apply with a conflict naming that manager; force = true would be refused here rather than sent.",
					w.Addr, w.Object, other, markers.FieldManagerFor(other), fields),
				Subject: manifestBlockRange(root, w.Addr),
			})
		}
	}
	return diags
}
