// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is the leg that makes internal/live/check's identity golden
// non-silenceable.
//
// TestIdentityGolden pins the rendered identity of every managed resource
// instance the in-repo fixtures resolve. It is the only instrument in this
// repository that measures the value a marker will carry rather than whether
// something refused - every other one counts refusals, and a marker can be
// wrong without anything refusing. Six defects shipped green through that gap.
//
// But the golden regenerates with -update, which is one word, and the diff it
// produces is a changed testdata file among thirteen hundred lines. Nobody
// reads that as an alarm. So the rule "explain a moved line, do not silence
// it" lived only in prose, in two files, and prose is what this project keeps
// discovering was stale.
//
// The golden is therefore pinned again here, in Go, where moving it is a
// one-line diff in a file named for the purpose. -update alone no longer
// makes the tree green: it makes THIS test fail, and the only way past is to
// edit a line next to a comment asking why.
//
// Two legs, and it took an audit to find out that one of them was not enough.
//
// The counts are the first. They catch a regression that adds or drops an
// instance or moves one between classes: reverting #251's conversion
// fabricated three identities and lost two correct ones, which is CONCRETE
// 658 -> 659. An exact pin catches that; a floor does not, and neither does
// any aggregate this repository was recording at the time, because the
// instance count went UP.
//
// The digest is the second, and it exists because the counts alone were
// defeated on 2026-08-16 by injecting a defect that rewrote 35 rendered
// ImportIDs and running -update. Every count was byte-identical afterwards and
// both tests went green over 35 changed markers - which is the very defect
// shape the golden's own doc says it catches eight times out of eleven. A
// count cannot see a value move.

// identityGoldenPin is the shape of internal/live/check/testdata/identity-golden.txt.
//
// TO CHANGE A NUMBER HERE, say why in the commit message, naming what moved
// and in which direction. A rising CONCRETE is usually the campaign working.
// A falling one, or a NEEDS_DISCOVERY that became CONCRETE in a fixture
// nobody touched, is the shape of a fabricated identity - which is worse than
// a refusal, because it is a marker this tool writes into a real cloud tag.
//
// Recompute with:
//
//	env -u PWD go test ./internal/live/check -run TestIdentityGolden -update
//
// then read the "# shape:" block at the top of the regenerated file.
var identityGoldenPin = map[string]int{
	// 716, up from 713 (issue #274): three ADDED rows, not a moved one -
	// internal/live/identity/testdata/markerless-veto-two-source-agreement's
	// new fixture, one instance each of aws_cognito_risk_configuration,
	// aws_detective_member and aws_lambda_function_event_invoke_config,
	// three types the markerless-veto two-source exception newly admits.
	// Every other CONCRETE row in the golden is byte-identical; see the
	// digest below.
	"CONCRETE":        716,
	"NEEDS_DISCOVERY": 557,
	"PARENT_DERIVED":  95,
	"RECORD_BACKED":   17,
}

// identityGoldenPinBodyDigest is sha256 over the golden's rows, and it is the
// leg that covers the VALUES rather than the counts.
//
// The counts above cannot see an identity move. An audit proved it by
// injecting a one-line defect into identity resolution that rewrote 35
// rendered ImportIDs, running -update, and watching both this test and
// TestIdentityGolden go green with a byte-identical "# shape:" count block.
// That is exactly the defect shape the golden's own doc says it catches eight
// times out of eleven - a MODIFIED line - and it was the one shape the pin
// could not see, because a changed value moves no count.
//
// So this number changes on any change to any rendered identity, which is
// deliberate and is the whole guarantee: -update alone leaves the tree red,
// and the only way past is to edit this line next to a comment asking why.
//
// TO CHANGE IT, read the diff first. `git diff` on the golden separates
// modified rows from added ones and they mean opposite things: an added row is
// the campaign working, a modified row on a fixture nobody touched is a marker
// this tool would write into a real cloud tag having silently changed.
//
// Recompute with:
//
//	env -u PWD go test ./internal/live/check -run TestIdentityGolden -update
//
// then copy the body-sha256 from the regenerated file's header.
// 2026-08-17 (issue #274): three ADDED rows (see identityGoldenPin's own
// comment above) - a fresh fixture directory, not an edit to an existing
// one, so every pre-existing row's digest contribution is unchanged.
const identityGoldenPinBodyDigest = "9033545f6134524ad57f414783c8b8374afe68e0df5c4814a294977b945ca311"

// 2026-08-17 (issue #270): dirs 412 -> 413, instances unchanged at 1385 and
// the body digest unchanged. The new directory is
// internal/live/check/testdata/stamp-untaggable-record-located, the
// onboarded half of the stamp-gate split: a markerless type under a
// record_store, which resolves RECORD_LOCATED. It contributes no row
// because this sweep runs SCHEMA-LESS by design, and
// identity.LocatedType fails closed with no schema - the credential
// exclusion is readable only from a schema and a predicate that cannot run
// must refuse. So the located class is a class this instrument cannot see,
// which is stated here rather than discovered later from a suspiciously
// stable digest.
const (
	identityGoldenPinInstances = 1385
	identityGoldenPinDirs      = 413

	// identityGoldenSweepFloor is the anti-tamper leg, in the same spirit as
	// universeFloor in admission_coverage_test.go.
	//
	// Every pin above can be satisfied by making the sweep smaller: drop a
	// root, tighten the file filter, and the numbers fall to something you
	// then re-pin, with each individual edit looking reasonable. This is the
	// number that must never go down. It is deliberately well below the
	// current 400 so that removing fixtures is not an event, and far enough
	// above zero that narrowing the walk to nothing is.
	identityGoldenSweepFloor = 300
)

// TestIdentityGoldenShapeIsPinned fails in both directions.
//
// Forwards: the golden's shape moved and nobody recorded why, which is what a
// bare -update produces.
//
// Backwards: this pin names a class the golden no longer emits, or the header
// disagrees with a recount of the body beneath it. Both mean the pin has
// stopped describing the thing it pins, and a pin that describes nothing
// passes forever.
func TestIdentityGoldenShapeIsPinned(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "live", "check", "testdata", "identity-golden.txt")

	header, bodyCounts, bodyInstances, bodyDigest, headerDigest := readIdentityGolden(t, path)

	// The value leg, checked first because it is the one that covers what a
	// marker will actually say. The counts below cover how many there are.
	if headerDigest == "" {
		t.Error("the golden's header carries no \"# shape: body-sha256=\" line.\n" +
			"Without it the pin covers counts only, and a defect that rewrites a rendered identity without\n" +
			"adding or removing one is invisible to every check in this repository. Regenerate with -update.")
	} else if headerDigest != bodyDigest {
		t.Errorf("the golden's header says body-sha256=%s, a rehash of its rows gives %s: the header was edited without regenerating.",
			headerDigest, bodyDigest)
	}
	if bodyDigest != identityGoldenPinBodyDigest {
		t.Errorf("the golden's rows hash to %s, pinned at %s.\n"+
			"Some rendered identity changed. That is not settled by re-running -update: the counts can be identical\n"+
			"while every marker in the file is different, which is how this leg came to exist.\n"+
			"Read `git diff internal/live/check/testdata/identity-golden.txt`. A MODIFIED row on a fixture nobody\n"+
			"touched is a marker this tool would write into a real cloud tag having silently moved; an ADDED row is\n"+
			"the campaign working. Say which, then re-pin.",
			bodyDigest, identityGoldenPinBodyDigest)
	}

	// The header is written by the same walk that writes the body, so these
	// agreeing proves only that the file was not hand-edited afterwards -
	// which is exactly the edit this test would otherwise miss, since the
	// header is where a reader looks and the body is where the truth is.
	if got, want := header["instances"], bodyInstances; got != want {
		t.Errorf("header says instances=%d, body holds %d rows: the header was edited without regenerating", got, want)
	}
	for class, n := range bodyCounts {
		if got := header["class "+class]; got != n {
			t.Errorf("header says class %s=%d, body holds %d: the header was edited without regenerating", class, got, n)
		}
	}

	if got := header["dirs"]; got < identityGoldenSweepFloor {
		t.Errorf("the golden swept %d configuration directories, below the floor of %d.\n"+
			"Every count in identityGoldenPin can be satisfied by sweeping less, so this is the leg that is not allowed to move.\n"+
			"If the tree genuinely shrank, lower the floor in its own commit that says so - not in the commit that shrank it.",
			got, identityGoldenSweepFloor)
	}
	if got := header["dirs"]; got != identityGoldenPinDirs {
		t.Errorf("the golden sweeps %d configuration directories, pinned at %d.\n"+
			"Adding fixtures moves this and is fine; say so and re-pin.",
			got, identityGoldenPinDirs)
	}
	if bodyInstances != identityGoldenPinInstances {
		t.Errorf("the golden holds %d instances, pinned at %d.\n"+
			"An instance is a marker this tool writes into a cloud tag. Rising is usually the campaign working; falling means something that resolved no longer does.\n"+
			"Neither is settled by re-running -update.",
			bodyInstances, identityGoldenPinInstances)
	}

	for _, class := range sortedKeys(identityGoldenPin) {
		want := identityGoldenPin[class]
		got, present := bodyCounts[class]
		if !present {
			t.Errorf("identityGoldenPin pins class %s at %d, but the golden no longer emits that class at all.\n"+
				"A pin on something that does not exist passes forever. Remove it, or find out why the class vanished.",
				class, want)
			continue
		}
		if got != want {
			t.Errorf("class %s: golden has %d, pinned at %d.\n"+
				"%s", class, got, want, identityGoldenClassAdvice(class, got, want))
		}
	}
	for _, class := range sortedKeys(bodyCounts) {
		if _, pinned := identityGoldenPin[class]; !pinned {
			t.Errorf("the golden emits class %s (%d instances) and identityGoldenPin does not mention it.\n"+
				"A new class is a new way for a marker to be rendered, and it is unpinned until it is listed here.",
				class, bodyCounts[class])
		}
	}
}

// identityGoldenClassAdvice says which direction of movement is the alarming
// one, per class, because they are not symmetric.
func identityGoldenClassAdvice(class string, got, want int) string {
	switch class {
	case "CONCRETE":
		if got > want {
			return "CONCRETE rose. That is usually the campaign working - but it is also exactly what a fabricated identity looks like,\n" +
				"and a fabricated marker is worse than a refusal. Read the added lines' rendered values before re-pinning."
		}
		return "CONCRETE fell. Something that rendered a real identity no longer does, which is a regression unless a fixture was deleted."
	case "NEEDS_DISCOVERY":
		if got < want {
			return "NEEDS_DISCOVERY fell. If CONCRETE rose by the same amount, resources became identifiable offline, which is the goal.\n" +
				"If it fell on its own, instances went missing."
		}
		return "NEEDS_DISCOVERY rose. Something that used to resolve offline now defers to a live read."
	default:
		return "Read the diff and say which fixtures moved."
	}
}

// readIdentityGolden parses the "# shape:" header and independently recounts
// the body, so the two can be compared against each other.
func readIdentityGolden(t *testing.T, path string) (header map[string]int, bodyCounts map[string]int, bodyInstances int, bodyDigest, headerDigest string) {
	t.Helper()

	// Rehashed here rather than read from the header, so a hand-edited header
	// and a hand-edited body are two different failures.
	digest := sha256.New()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading the identity golden: %s\n"+
			"This pin exists to guard that file; with the file absent it guards nothing, so this is a failure rather than a skip.", err)
	}
	defer f.Close()

	header = map[string]int{}
	bodyCounts = map[string]int{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			for _, kv := range parseShapeLine(line) {
				header[kv.key] = kv.n
			}
			if d, ok := parseShapeDigest(line); ok {
				headerDigest = d
			}
			continue
		}
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			t.Fatalf("malformed golden row (%d tab-separated fields, want at least 3): %q", len(fields), line)
		}
		bodyCounts[fields[2]]++
		bodyInstances++
		// The same bytes the writer hashed: each row plus its newline, and
		// nothing else.
		digest.Write([]byte(line))
		digest.Write([]byte("\n"))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanning the identity golden: %s", err)
	}
	if len(header) == 0 {
		t.Fatal("the identity golden carries no \"# shape:\" header.\n" +
			"That block is what makes a silenced regression visible in the first fifteen lines of a diff; regenerate with -update.")
	}
	return header, bodyCounts, bodyInstances, hex.EncodeToString(digest.Sum(nil)), headerDigest
}

// parseShapeDigest reads "# shape: body-sha256=<hex>". It is separate from
// parseShapeLine because that one Atoi's every value and silently drops what
// does not parse, which is how a hex digest would have gone unnoticed.
func parseShapeDigest(line string) (string, bool) {
	const marker = "# shape:"
	if !strings.HasPrefix(line, marker) {
		return "", false
	}
	for _, field := range strings.Fields(strings.TrimPrefix(line, marker)) {
		if v, ok := strings.CutPrefix(field, "body-sha256="); ok {
			return v, true
		}
	}
	return "", false
}

type shapeKV struct {
	key string
	n   int
}

// parseShapeLine reads "# shape: dirs=375 instances=1320" and
// "# shape: class CONCRETE=658".
func parseShapeLine(line string) []shapeKV {
	const marker = "# shape:"
	if !strings.HasPrefix(line, marker) {
		return nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(line, marker))

	var prefix string
	if after, ok := strings.CutPrefix(rest, "class "); ok {
		prefix, rest = "class ", after
	}

	var out []shapeKV
	for _, field := range strings.Fields(rest) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			continue
		}
		out = append(out, shapeKV{key: prefix + key, n: n})
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
