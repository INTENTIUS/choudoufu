// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestRegisteredSeverityMatchesWhatTheCodeRaises is the Azure epic's
// (GitHub issue #1120) follow-up to [Refusal.Warning]. live/LIMITATIONS.md's
// generated table called the foreign-resource-group warning an error,
// because this registry had nowhere to say otherwise; the field it gained
// is hand-written, so each warning is raised here through the code that
// raises it and its severity compared with the registry's.
//
// The pinned set below is the other half: every registered refusal not in
// it stays an error, so a row flipped to Warning by mistake fails here.
func TestRegisteredSeverityMatchesWhatTheCodeRaises(t *testing.T) {
	raised := map[string]tfdiags.Severity{}
	record := func(diags tfdiags.Diagnostics, summary string) {
		t.Helper()
		for _, d := range diags {
			if d.Description().Summary == summary {
				raised[summary] = d.Severity()
				return
			}
		}
		t.Fatalf("the code raised no %q diagnostic; the fixture no longer exercises it", summary)
	}

	// The foreign resource group, through ResolveWith, the path every
	// live plan takes.
	dir := writeTree(t, map[string]string{"main.tf": `
resource "azurerm_resource_group" "rg" {
  name     = "estate-rg"
  location = "westeurope"
}
resource "azurerm_storage_account" "sa" {
  name                = "estatesa"
  resource_group_name = "someone-elses-rg"
}
`})
	_, diags := ResolveWith(context.Background(), loadConfigTree(t, dir, nil), Context{})
	record(diags, SummaryForeignResourceGroup)

	// GitHub issue #107's orphan warning, on the fixture its own test uses.
	_, diags = ResolveWith(context.Background(), loadConfig(t, "testdata/schema-fallback", nil), Context{Schemas: fallbackSchemas()})
	record(diags, SummaryNoOrphanRecovery)

	// A schema finding renders as one summary or the other by Breaking.
	record(tfdiags.Diagnostics{Finding{Type: "aws_thing"}.Diagnostic()}, SummarySchemaDisagreement)
	record(tfdiags.Diagnostics{Finding{Type: "aws_thing", Breaking: true}.Diagnostic()}, SummarySchemaBreaking)

	for summary, sev := range raised {
		if _, ok := LookupRefusal(summary); !ok {
			t.Errorf("%q is raised but not registered", summary)
			continue
		}
		if got, want := IsWarning(summary), sev == tfdiags.Warning; got != want {
			t.Errorf("%q: the registry says warning=%v, the code raises it as %v", summary, got, sev)
		}
	}

	wantWarnings := []string{
		SummaryForeignResourceGroup,
		SummaryNoOrphanRecovery,
		SummarySchemaDisagreement,
	}
	sort.Strings(wantWarnings)
	var gotWarnings []string
	for _, r := range Refusals() {
		if r.Warning {
			gotWarnings = append(gotWarnings, r.Summary)
		}
	}
	if strings.Join(gotWarnings, "\n") != strings.Join(wantWarnings, "\n") {
		t.Errorf("registered warnings changed:\n got: %q\nwant: %q\nEvery other identity refusal stops the run; a new warning is raised in this test before it joins the list.", gotWarnings, wantWarnings)
	}
}
