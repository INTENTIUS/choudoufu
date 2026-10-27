// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package modcompat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// artifact is the part of live/gauntlet.json the gauntlet tier reads.
// tools/gauntlet is a main package, so its types are mirrored by JSON name.
type artifact struct {
	Emulator        string           `json:"emulator"`
	Oracle          Oracle           `json:"oracle"`
	Providers       artifactPins     `json:"providers"`
	UpstreamVersion string           `json:"upstream_version"`
	Estates         []artifactEstate `json:"estates"`
}

type artifactPins struct {
	AWS string `json:"aws"`
}

type artifactEstate struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	URL       string   `json:"url"`
	Pin       string   `json:"pin"`
	Lane      string   `json:"lane"`
	Substrate string   `json:"substrate"`
	Clear     bool     `json:"clear"`
	LastRun   *lastRun `json:"last_run"`
}

type lastRun struct {
	Commit             string  `json:"commit"`
	Date               string  `json:"date"`
	Emulator           string  `json:"emulator"`
	AWSProviderVersion string  `json:"aws_provider_version"`
	Oracle             *Oracle `json:"oracle"`
	UpstreamVersion    string  `json:"upstream_version"`
}

// board is the part of live/gauntlet-board.json the gauntlet tier reads:
// the script staleness `gauntlet render` computed from git, and each row's
// stage table in board order.
type board struct {
	Estates []boardEstate `json:"estates"`
}

type boardEstate struct {
	Name        string  `json:"name"`
	ScriptStale string  `json:"script_stale"`
	StageRows   []Stage `json:"stage_rows"`
}

// examplePath reads the example directory out of an estate's source text,
// "terraform-aws-modules/terraform-aws-alb examples/complete-alb (tag ...)".
var examplePath = regexp.MustCompile(`\b(examples/[A-Za-z0-9_./-]+)`)

// GauntletRows returns the gauntlet tier: every terraform-popular estate of
// a terraform-aws-modules repository whose last run is current.
//
// Current means all of: a recorded run, on the floci substrate, against the
// emulator image, the hashicorp/aws release, the stock oracle and the engine
// base the artifact pins now, with a script the board does not mark stale.
// That is tools/gauntlet's pinCurrent plus its provider and script checks,
// restated over the committed files, so a row the board marks **Stale** in
// any way is never published here.
func GauntletRows(root string) ([]GauntletRow, error) {
	var a artifact
	if err := readJSON(root, ArtifactRel, &a); err != nil {
		return nil, err
	}
	var b board
	if err := readJSON(root, BoardRel, &b); err != nil {
		return nil, err
	}
	boardRows := map[string]boardEstate{}
	for _, e := range b.Estates {
		boardRows[e.Name] = e
	}

	rows := []GauntletRow{}
	for _, e := range a.Estates {
		if e.Lane != Lane {
			continue
		}
		mod, ok := ModuleFromRepo(e.URL)
		if !ok {
			continue
		}
		br, ok := boardRows[e.Name]
		if !ok {
			return nil, fmt.Errorf("%s has estate %s and %s does not; run %s", ArtifactRel, e.Name, BoardRel, GauntletCommand)
		}
		if !current(a, e, br) {
			continue
		}
		ex := ""
		if m := examplePath.FindStringSubmatch(e.Source); m != nil {
			ex = m[1]
		}
		stages := make([]Stage, 0, len(br.StageRows))
		for _, s := range br.StageRows {
			stages = append(stages, Stage{ID: s.ID, Title: s.Title, Verdict: s.Verdict})
		}
		rows = append(rows, GauntletRow{
			Tier:        TierGauntlet,
			Module:      mod,
			Version:     e.Pin,
			Example:     ex,
			Estate:      e.Name,
			Date:        e.LastRun.Date,
			Commit:      e.LastRun.Commit,
			Emulator:    e.LastRun.Emulator,
			AWSProvider: e.LastRun.AWSProviderVersion,
			Engine:      e.LastRun.UpstreamVersion,
			Oracle:      *e.LastRun.Oracle,
			Clear:       e.Clear,
			Stages:      stages,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Module != rows[j].Module {
			return rows[i].Module < rows[j].Module
		}
		return rows[i].Example < rows[j].Example
	})
	return rows, nil
}

// current is the gauntlet tier's admission rule; see [GauntletRows].
func current(a artifact, e artifactEstate, br boardEstate) bool {
	r := e.LastRun
	switch {
	case r == nil, r.Oracle == nil:
		return false
	case e.Substrate != "":
		// Every terraform-popular row runs on floci today. A row on
		// another substrate records a different image field, and this
		// rule does not judge it.
		return false
	case r.Emulator == "" || r.Emulator != a.Emulator:
		return false
	case r.AWSProviderVersion == "" || r.AWSProviderVersion != a.Providers.AWS:
		return false
	case *r.Oracle != a.Oracle:
		return false
	case r.UpstreamVersion == "" || r.UpstreamVersion != a.UpstreamVersion:
		return false
	case br.ScriptStale != "":
		return false
	}
	return true
}

func readJSON(root, rel string, v any) error {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decoding %s: %w", rel, err)
	}
	return nil
}
