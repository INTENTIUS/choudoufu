// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #2051: a downstream estate planned on its upstream's PLANNED
// outputs, read from the upstream's "show -json" document, rather than on
// what the upstream recorded at its last apply. "plan -estate-outputs-from
// <estate>=<plan.json>" hands a plan one such document per estate, and
// terraform_estate_outputs answers from it for that estate instead of the
// record store.
//
// Only a planned value that is wholly known and not sensitive crosses, the
// same rule [WriteRootOutputValues] keeps for a recorded one. An output only
// known after apply is refused naming it: a plan made on an unknown or a
// placeholder is not the plan that will apply, which is the gap #2051
// closes.

const (
	// SummaryEstateOutputUnknownInPlan is a planned output whose value is
	// only known after the upstream applies.
	SummaryEstateOutputUnknownInPlan = "Another estate's planned output is known only after apply"

	// SummaryEstateOutputSensitiveInPlan is a planned output marked
	// sensitive.
	SummaryEstateOutputSensitiveInPlan = "Another estate's planned output is sensitive"

	// SummaryEstateOutputNotInPlan is a named output the upstream's plan
	// does not carry.
	SummaryEstateOutputNotInPlan = "Another estate's plan has no such output"

	// SummaryEstateOutputsFromPlan is the warning a read from a plan
	// raises, in place of [SummaryEstateOutputsAsOf].
	SummaryEstateOutputsFromPlan = "Values from another estate are as that estate's plan proposes them"
)

// PlannedEstateOutputs is one upstream estate's planned root outputs, read
// from its "show -json" document.
type PlannedEstateOutputs struct {
	// Estate is the upstream estate the document was handed for.
	Estate string
	// Path is the document, for diagnostics.
	Path string
	// Outputs are keyed by output name.
	Outputs map[string]PlannedOutput
}

// PlannedOutput is one output in a plan's planned_values.
type PlannedOutput struct {
	// Value is the planned value, cty.NilVal when Known is false.
	Value cty.Value
	// Known is false for an output "show -json" prints without a value:
	// one only known after apply.
	Known bool
	// Sensitive is the output's own sensitive flag.
	Sensitive bool
}

// ParsePlannedEstateOutputs reads planned_values.outputs from a "show
// -json" document. An output the plan deletes is not in it, and a read of
// one is refused as not in the plan.
func ParsePlannedEstateOutputs(estate, path string, data []byte) (*PlannedEstateOutputs, error) {
	var doc struct {
		FormatVersion string `json:"format_version"`
		PlannedValues *struct {
			Outputs map[string]struct {
				Sensitive bool            `json:"sensitive"`
				Type      json.RawMessage `json:"type"`
				Value     json.RawMessage `json:"value"`
			} `json:"outputs"`
		} `json:"planned_values"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not a JSON document: %w", err)
	}
	if doc.FormatVersion == "" {
		return nil, fmt.Errorf("no format_version, so this is not what \"choudoufu show -json PLANFILE\" prints")
	}
	out := &PlannedEstateOutputs{Estate: estate, Path: path, Outputs: map[string]PlannedOutput{}}
	if doc.PlannedValues == nil {
		return out, nil
	}
	for name, o := range doc.PlannedValues.Outputs {
		po := PlannedOutput{Sensitive: o.Sensitive}
		// "show -json" leaves both value and type out for an output that
		// is not wholly known (jsonplan's marshalPlannedOutputs).
		if len(o.Value) > 0 && len(o.Type) > 0 {
			ty, err := ctyjson.UnmarshalType(o.Type)
			if err != nil {
				return nil, fmt.Errorf("output %q: its type does not decode: %w", name, err)
			}
			v, err := ctyjson.Unmarshal(o.Value, ty)
			if err != nil {
				return nil, fmt.Errorf("output %q: its value does not decode: %w", name, err)
			}
			po.Value, po.Known = v, true
		}
		out.Outputs[name] = po
	}
	return out, nil
}

// ReadPlannedEstateOutputs answers a terraform_estate_outputs read from a
// plan. It returns the values keyed by name, or error diagnostics and no
// values: every output named must be in the plan, known and not sensitive.
func ReadPlannedEstateOutputs(p *PlannedEstateOutputs, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	values := make(map[string]cty.Value, len(sorted))
	for _, name := range sorted {
		o, ok := p.Outputs[name]
		switch {
		case !ok:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputNotInPlan, fmt.Sprintf(
				"This plan reads estate %q's outputs from its plan, %s, and that plan has no output %q: %q declares none by that name, or its plan removes it.",
				p.Estate, p.Path, name, p.Estate)))
		case o.Sensitive:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputSensitiveInPlan, fmt.Sprintf(
				"Estate %q's planned output %q (in %s) is sensitive. A sensitive output never crosses to another estate, planned or recorded, so this plan cannot be made on it.",
				p.Estate, name, p.Path)))
		case !o.Known:
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryEstateOutputUnknownInPlan, fmt.Sprintf(
				"Estate %q's planned output %q (in %s) is known only after %q applies. This plan is not made on an unknown or a placeholder, because that would not be the plan that applies. Apply %q first, then plan this estate on its recorded outputs, without -estate-outputs-from %s=...",
				p.Estate, name, p.Path, p.Estate, p.Estate, p.Estate)))
		default:
			values[name] = o.Value
		}
	}
	if diags.HasErrors() {
		return nil, diags
	}
	subject := "The value of " + joinQuoted(sorted) + " was"
	if len(sorted) > 1 {
		subject = "The values of " + joinQuoted(sorted) + " were"
	}
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryEstateOutputsFromPlan, fmt.Sprintf(
		"%s read from estate %q's plan, %s, not from what it recorded at its last apply. A saved plan records this, and \"choudoufu apply\" of it refuses unless %q has since applied and recorded these same values.",
		subject, p.Estate, p.Path, p.Estate)))
	return values, diags
}

// CanonicalOutputJSON renders v the way a saved plan records a planned
// estate output and the way an apply compares the recorded one against it:
// JSON with object keys sorted, so two values compare by value and not by
// encoding.
func CanonicalOutputJSON(v cty.Value) (string, error) {
	raw, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return "", err
	}
	b, err := json.Marshal(x)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
