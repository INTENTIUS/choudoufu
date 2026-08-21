// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"
)

// TestNotImportableRoster covers the rule itself: importable=false vetoes,
// importable=true never does regardless of any other signal, and the one
// named exemption is spared even though its own signal reads importable=false
// - the same shape TestMarkerlessRosterNeedsSurveyMembership uses to pin its
// own veto's edge cases.
func TestNotImportableRoster(t *testing.T) {
	survey := map[string]surveyEntry{
		"aws_has_importer": {Type: "aws_has_importer", Signals: surveySignals{Importable: true}},
		"aws_no_importer":  {Type: "aws_no_importer", Signals: surveySignals{Importable: false}},
		"aws_acm_certificate_validation": {
			Type:    "aws_acm_certificate_validation",
			Signals: surveySignals{Importable: false},
		},
	}

	got := notImportableRoster(survey)
	want := []string{"aws_no_importer"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notImportableRoster = %v, want %v - either a real gap was missed or the exemption did not fire", got, want)
	}
}

// TestNotImportableExemptIsExactlyTheRuledType pins the escape hatch itself,
// the same discipline TestGeneratedTypeLiteralExemptionIsExact holds row-gen
// to elsewhere: notImportableExempt is one hand-written input this
// derivation carries, and it must name exactly the type issue #331's own
// text says is out of scope - aws_acm_certificate_validation, ruled on the
// nameability axis by classify.go's 2026-08-17 decision - and nothing else.
// A second entry here would be exactly the hand-list the veto's own doc
// comment says this file must not become.
func TestNotImportableExemptIsExactlyTheRuledType(t *testing.T) {
	if len(notImportableExempt) != 1 {
		t.Fatalf("notImportableExempt has %d entries, want exactly 1: %v", len(notImportableExempt), notImportableExempt)
	}
	const want = "aws_acm_certificate_validation"
	if _, ok := notImportableExempt[want]; !ok {
		t.Errorf("notImportableExempt does not name %s: %v", want, notImportableExempt)
	}
	for tf, reason := range notImportableExempt {
		if reason == "" {
			t.Errorf("notImportableExempt[%s] carries no reason - a bare exemption is the hand-list this file exists not to be", tf)
		}
	}
}
