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
// A run that declines to go on - not a stage that failed, a run that will
// not attempt the rung at all - says so with one more line (#1151):
//
//	GAUNTLET refused=1 [scale=<n>] [needed=<n> limit=<n> unit=<token>] detail=<reason>
//
// emitted by gauntlet_refused. That is a fourth outcome beside pass, fail
// and not_run, and it is the difference between "choudoufu could not do this"
// and "this was never attempted, here is the arithmetic that says why".
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
	// PreApply is the addresses the run actually pre-applied, reported by
	// gauntlet_pre_apply as its own protocol line (#1173). It is how the
	// runner checks a declared pre-apply per address WITHOUT requiring the
	// verdict line to spell out every one of them: the verdict says how
	// many and where the list is declared, and this says what actually ran.
	// Empty for the ordinary estate, which declares none and emits no line.
	PreApply []string
	// PreApplySides names the sides that pre-applied, comma separated, in
	// the order they ran - "estate,oracle".
	PreApplySides string
	// Refusal is set when the run declined the rung outright (#1151): a
	// ceiling it cannot raise, a precondition it will not fake. Nil for
	// every ordinary run, including one whose stages failed - a failure is
	// a measurement and a refusal is the absence of one.
	Refusal *ProtocolRefusal
}

// ProtocolRefusal is one `GAUNTLET refused=1 ...` line: why a run would not
// attempt what it was asked for, and the arithmetic behind it.
//
// Reason is required - a refusal with no reason is worth nothing on the
// record, which is the whole point of recording it rather than skipping the
// rung silently. The numbers are optional because not every refusal has
// any: "the s3 record store is unsound (#1145)" is a reason with no
// arithmetic, while "10,070 records against a hard 10,000 cap" is both.
type ProtocolRefusal struct {
	Reason string
	// Scale is the terralith-gen -scale the refused run was for, when the
	// run has one. It matters because a refusal usually happens before any
	// stage speaks, so there is no cold_deploy detail to read the scale off
	// - and a refusal that cannot name its scale cannot be placed on the
	// ladder at all (see cmdLiveCert).
	Scale int
	// Needed and Limit are the two sides of the arithmetic, in Unit. Either
	// both are present or neither is.
	Needed *int
	Limit  *int
	Unit   string
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
		// GAUNTLET refused=1 [scale=N] [needed=N limit=N unit=token] detail=<reason>
		if _, ok := fields["refused"]; ok {
			ref, rerr := parseRefusal(line, fields)
			if rerr != nil {
				return nil, rerr
			}
			if res.Refusal != nil {
				return nil, fmt.Errorf("line %d: a second GAUNTLET refused= line; a run refuses once, for one reason, or the record cannot say which reason it carries", line)
			}
			res.Refusal = ref
			continue
		}
		// GAUNTLET pre_apply=<addr>[,<addr>...] sides=<label>[,<label>...]
		// Values carry no spaces, because parseKV only lets `detail` run to
		// the end of the line. Several lines accumulate rather than
		// replacing: a run that pre-applied twice pre-applied both times.
		if v, ok := fields["pre_apply"]; ok {
			for _, a := range strings.Split(v, ",") {
				if a = strings.TrimSpace(a); a != "" {
					res.PreApply = append(res.PreApply, a)
				}
			}
			if sides := fields["sides"]; sides != "" {
				res.PreApplySides = sides
			}
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

// parseRefusal reads one `GAUNTLET refused=1 ...` line's fields. Every
// malformation is an error rather than a tolerated shape, for the same
// reason a malformed stage line is: a half-spoken refusal would be recorded
// as a refusal with a missing reason, which is worse than no line at all.
func parseRefusal(line int, fields map[string]string) (*ProtocolRefusal, error) {
	ref := &ProtocolRefusal{Reason: strings.TrimSpace(fields["detail"]), Unit: fields["unit"]}
	if ref.Reason == "" {
		return nil, fmt.Errorf("line %d: GAUNTLET refused= carries no detail= - a refusal's whole value is the reason it names, so one without a reason is refused here rather than recorded empty", line)
	}
	if s, ok := fields["scale"]; ok {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("line %d: GAUNTLET refused= has invalid scale %q, want a positive integer", line, s)
		}
		ref.Scale = n
	}
	needed, hasNeeded := fields["needed"]
	limit, hasLimit := fields["limit"]
	if hasNeeded != hasLimit {
		return nil, fmt.Errorf("line %d: GAUNTLET refused= carries needed= or limit= but not both - one side of an arithmetic is not an arithmetic", line)
	}
	if hasNeeded {
		n, err := strconv.Atoi(needed)
		if err != nil {
			return nil, fmt.Errorf("line %d: GAUNTLET refused= has invalid needed %q: %w", line, needed, err)
		}
		l, err := strconv.Atoi(limit)
		if err != nil {
			return nil, fmt.Errorf("line %d: GAUNTLET refused= has invalid limit %q: %w", line, limit, err)
		}
		if ref.Unit == "" {
			return nil, fmt.Errorf("line %d: GAUNTLET refused= gives needed=%d limit=%d with no unit= - two bare numbers say nothing a later reader can check", line, n, l)
		}
		ref.Needed, ref.Limit = &n, &l
	}
	return ref, nil
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
