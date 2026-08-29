// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The stage protocol. A crossing script reports each stage on stdout as one
// line:
//
//	GAUNTLET stage=<id> verdict=<pass|fail|not_run> [duration_s=<seconds>] [detail=<free text>]
//
// duration_s is optional (a script sourcing an older live/e2e/lib/gauntlet.sh
// omits it, which is not an error) and, when present, must parse as a
// float64: the wall-clock seconds gauntlet_stage measured since the previous
// GAUNTLET line, i.e. this stage's own run time, not a cumulative total.
//
// and announces that it speaks the protocol with
//
//	GAUNTLET protocol=1
//
// before its first stage line. live/e2e/lib/gauntlet.sh emits both; a script
// sources it and calls `gauntlet_stage <id> <verdict> [detail]`. A script that
// never prints the protocol line is legacy: the runner records its exit code
// and leaves the imported verdicts alone.
//
// The grammar is deliberately one line per event with key=value pairs, no
// JSON, so a script can print it with printf and a human can grep it.
const (
	ProtocolPrefix  = "GAUNTLET "
	ProtocolVersion = "1"
)

// ProtocolResult is what the parser extracts from a script's stdout.
type ProtocolResult struct {
	Spoken  bool               // the protocol line was seen
	Stages  map[string]string  // stage id -> verdict
	Detail  map[string]string  // stage id -> detail, when given
	Seconds map[string]float64 // stage id -> wall-clock seconds, when the script reported duration_s (live/e2e/lib/gauntlet.sh emits it on every gauntlet_stage call; a script that sources an older copy simply omits the key, which is why this is a plain lookup miss, never an error)
	Unknown []string           // stage ids not in the registry, reported not silently dropped
}

// ParseProtocol reads stdout and returns the verdicts. Lines that do not
// start with the prefix are ignored; malformed protocol lines are errors,
// because a script that half-speaks the protocol is worse than one that does
// not speak it at all.
func ParseProtocol(r io.Reader) (*ProtocolResult, error) {
	res := &ProtocolResult{Stages: map[string]string{}, Detail: map[string]string{}, Seconds: map[string]float64{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimRight(sc.Text(), "\r")
		if !strings.HasPrefix(text, ProtocolPrefix) {
			continue
		}
		fields := parseKV(strings.TrimPrefix(text, ProtocolPrefix))
		if v, ok := fields["protocol"]; ok {
			if v != ProtocolVersion {
				return nil, fmt.Errorf("line %d: unsupported gauntlet protocol version %q", line, v)
			}
			res.Spoken = true
			continue
		}
		if _, ok := fields["end"]; ok {
			continue
		}
		id, ok := fields["stage"]
		if !ok {
			return nil, fmt.Errorf("line %d: GAUNTLET line without stage=, protocol= or end=: %q", line, text)
		}
		verdict := fields["verdict"]
		switch verdict {
		case VerdictPass, VerdictFail, VerdictNotRun:
		default:
			return nil, fmt.Errorf("line %d: stage %q has verdict %q, want pass, fail or not_run", line, id, verdict)
		}
		if _, known := StageByID(id); !known {
			res.Unknown = append(res.Unknown, id)
		}
		res.Stages[id] = verdict
		if s, ok := fields["duration_s"]; ok {
			secs, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: stage %q has invalid duration_s %q: %w", line, id, s, err)
			}
			res.Seconds[id] = secs
		}
		if d, ok := fields["detail"]; ok && d != "" {
			res.Detail[id] = d
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// parseKV splits "k=v k2=v2 detail=the rest of the line" into a map. detail
// is special: it runs to the end of the line, spaces included, so it must be
// last.
func parseKV(s string) map[string]string {
	out := map[string]string{}
	rest := s
	for rest != "" {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		if key == "detail" {
			out[key] = strings.TrimSpace(rest)
			break
		}
		sp := strings.IndexByte(rest, ' ')
		if sp < 0 {
			out[key] = rest
			break
		}
		out[key] = rest[:sp]
		rest = rest[sp:]
	}
	return out
}
