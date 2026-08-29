// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"regexp"
	"strings"
)

// proposeCandidate is one type's parsed evidence out of
// `go run ./tools/row-gen -propose`'s printed report.
type proposeCandidate struct {
	Type        string
	Rule        string
	TrackRecord string
	Block       string
}

// candidateSeparator is renderProposeReport's own delimiter (propose.go)
// between two candidate blocks.
const candidateSeparator = "\n----------------------------------------------------------------\n"

// reportSeparator is renderProposeReport's own delimiter (propose.go)
// between the rule-class ledger and the candidates section - printed
// exactly once per report, whether or not any candidate follows.
const reportSeparator = "\n================================================================\n"

// candidateHeaderRE matches renderProposal's (render.go) own header line for
// every pastable bucket: "## aws_type -> CFN::Thing [proposed: ...]".
var candidateHeaderRE = regexp.MustCompile(`(?m)^## (\S+) ->`)

// ruleLineRE matches renderProposal's own "rule: ..." line.
var ruleLineRE = regexp.MustCompile(`(?m)^rule: (.+)$`)

// trackRecordLineRE matches renderProposeReport's own per-candidate line
// (propose.go's renderProposeReport): "rule class track record: 15/15
// (100%) admitted unchanged against internal/live/identity.DefaultTable;
// not recorded in tools/row-gen/rejected.json".
var trackRecordLineRE = regexp.MustCompile(`(?m)^rule class track record: (.+)$`)

// parseProposeCandidates extracts every candidate block from
// `go run ./tools/row-gen -propose`'s stdout, keyed by TF type. It parses
// the report renderProposeReport (tools/row-gen/propose.go) actually prints
// rather than re-deriving PROPOSE's own selection logic, so this generator
// stays a consumer of that stage rather than a second implementation of it.
//
// An empty report ("0 logical types proposed this run.") is not an error:
// it returns an empty map, the same as an unparseable-but-present report
// would be a bug worth surfacing loudly (the error return below), which a
// genuinely empty PROPOSE run is not.
func parseProposeCandidates(report string) (map[string]proposeCandidate, error) {
	idx := strings.Index(report, reportSeparator)
	if idx < 0 {
		return nil, fmt.Errorf("row-gen -propose's report has no %q separator; its own renderProposeReport format has changed", strings.TrimSpace(reportSeparator))
	}
	tail := report[idx+len(reportSeparator):]

	out := map[string]proposeCandidate{}
	if strings.HasPrefix(tail, "0 logical types proposed this run.") {
		return out, nil
	}

	chunks := strings.Split(tail, candidateSeparator)
	// chunks[0] is the "N logical type(s) proposed..." summary line, not a
	// candidate block.
	for _, chunk := range chunks[1:] {
		headerMatch := candidateHeaderRE.FindStringSubmatch(chunk)
		if headerMatch == nil {
			return nil, fmt.Errorf("row-gen -propose: a candidate block has no %q header line; its own renderProposal format has changed:\n%s", "## <type> ->", chunk)
		}
		tfType := headerMatch[1]

		rule := ""
		if m := ruleLineRE.FindStringSubmatch(chunk); m != nil {
			rule = m[1]
		}
		trackRecord := ""
		if m := trackRecordLineRE.FindStringSubmatch(chunk); m != nil {
			trackRecord = m[1]
		}

		if _, dup := out[tfType]; dup {
			return nil, fmt.Errorf("row-gen -propose printed %s more than once in the same report; PROPOSE's own candidates are supposed to be one block per type", tfType)
		}
		out[tfType] = proposeCandidate{
			Type:        tfType,
			Rule:        rule,
			TrackRecord: trackRecord,
			Block:       strings.TrimSpace(chunk),
		}
	}
	return out, nil
}
