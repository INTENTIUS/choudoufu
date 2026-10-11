// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// GitHub issue #1591. A cell in live/smoke/claims.json whose scenario
// reaches a real account cannot run on every pull request (CLAUDE.md: a
// paid run is the maintainer's to start), so it carries dated evidence
// instead: the claims table must not call a cell proven on the strength of
// a hand run that does not say when it ran, the way claim 28 stayed red for
// eighteen days while its own row said "proven" (#1379). Which kind a cell
// is comes from the cell itself (real_service), not from its provider name,
// so this keeps working the day a second real-account provider exists.

// provenCellEvidenceDate matches a lastrun evidence file's "date" field:
// YYYY-MM-DD, the spelling every existing evidence file under
// live/smoke/evidence uses.
var provenCellEvidenceDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// cellRunsItsOwnScenario is true for a scenario proof in a cell whose
// status rests on it: proven, or restated (the promise holds in a weaker
// form, and the scenario is what shows the weaker form holds, #1599).
func cellRunsItsOwnScenario(c smokeScenarioCell) bool {
	return c.Cell.Scenario != "" && (c.Status == "proven" || c.Status == "restated")
}

// TestRealServiceProvenCellsCarryDatedEvidence: every proven or restated
// cell that carries its own scenario and is real_service (a real account,
// never run on every pull request) carries an evidence file with a
// YYYY-MM-DD date.
func TestRealServiceProvenCellsCarryDatedEvidence(t *testing.T) {
	f := readSmokeClaims(t)

	realService := 0
	for _, s := range smokeScenarioCells(f) {
		if !cellRunsItsOwnScenario(s) || !s.Cell.RealService {
			continue
		}
		realService++
		if len(s.Cell.Evidence) == 0 {
			t.Errorf("%s: %s and real_service, so it never runs in a workflow, but carries no evidence file", s, s.Status)
			continue
		}
		dated := false
		for _, ev := range s.Cell.Evidence {
			raw, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(ev)))
			if err != nil {
				// live/smoke_claims_test.go already reports a missing
				// evidence file; this test only checks the date.
				continue
			}
			var rec struct {
				Date string `json:"date"`
			}
			if err := json.Unmarshal(raw, &rec); err != nil {
				continue
			}
			if provenCellEvidenceDate.MatchString(rec.Date) {
				dated = true
			}
		}
		if !dated {
			t.Errorf("%s: %s and real_service, but none of %v carries a YYYY-MM-DD \"date\" field; a claim resting on a hand run has to say when it ran", s, s.Status, s.Cell.Evidence)
		}
	}
	if realService == 0 {
		t.Fatal("no proven or restated cell is real_service; this guard is checking nothing")
	}
}
