// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Issue #1299: a floci container started with `docker run -d --rm` and lost
// mid-run is erased by dockerd the instant it exits - before the script's own
// fail(), before its EXIT trap, before any human. No exit code, no
// State.OOMKilled, no `docker logs`, not even a row in `docker ps -a`. A run
// whose emulator died and a run that finished cleanly then look identical
// afterwards, which is why #1141's "docker ps showed no port conflict and no
// container left running" could neither rule the sighting in nor out: that
// observation is what you see either way.
//
// The fix is two halves and this file guards both, because each is useless
// alone. Dropping `--rm` keeps the corpse; routing teardown through
// gauntlet_floci_teardown is what reads it before the trap buries it. Put
// `--rm` back and there is nothing to read; go back to a bare `docker rm -f`
// and nobody reads it.

// flociScriptSources is the population these guards scan: every shell script
// in live/ that could plausibly start a floci container.
//
// Wider than CrossingScriptSources in awspagequery_baseline.go, deliberately,
// and each addition is a script that actually starts one:
//
//	e2e/run.sh          the stateless-mode harness, which e2e/*/run.sh misses
//	                    because it sits one directory up.
//	live-cert/*.sh      reference-ec2-vpc.sh and terralith-scale.sh both do,
//	                    and a paid real-AWS run is the LAST place to discover
//	                    that a dead container left no trace.
//
// Read with os.ReadFile rather than grep, for #1219's reason:
// e2e/corpus-mastino-dns/run.sh carries an embedded NUL byte and some greps
// silently skip it as binary. That script starts three floci containers, so
// the blind spot would be load-bearing here too.
func flociScriptSources(t *testing.T) map[string]string {
	t.Helper()
	globs := []string{
		filepath.Join("e2e", "*", "run.sh"),
		filepath.Join("e2e", "run.sh"),
		filepath.Join("e2e", "lib", "*.sh"),
		filepath.Join("live-cert", "*.sh"),
		filepath.Join("live-cert", "lib", "*.sh"),
	}
	out := map[string]string{}
	for _, g := range globs {
		paths, err := filepath.Glob(g)
		if err != nil {
			t.Fatalf("globbing live/%s: %v", g, err)
		}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("reading live/%s: %v", p, err)
			}
			out[filepath.ToSlash(p)] = string(data)
		}
	}
	if len(out) == 0 {
		t.Fatal("no scripts matched - this guard checked nothing, which is worse than not existing")
	}
	return out
}

// codeLines yields the (1-based) line number and text of every line that is
// not blank and not a whole-line comment. Comments matter here: the library's
// own explanation of this fix quotes `docker run -d --rm` verbatim, as does a
// live-cert selftest recommending an ad-hoc emulator to a human - where `--rm`
// is the right answer, because nothing is going to inspect that container.
func codeLines(src string) func(func(int, string) bool) {
	return func(yield func(int, string) bool) {
		for i, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !yield(i+1, line) {
				return
			}
		}
	}
}

// TestNoScriptStartsADetachedContainerWithRm is the first half of #1299's
// fix, held in place. `--rm` on a detached floci container buys nothing the
// EXIT trap was not already doing, and costs the only evidence that
// distinguishes a dead emulator from a wrong assertion.
func TestNoScriptStartsADetachedContainerWithRm(t *testing.T) {
	scripts := flociScriptSources(t)
	var found []string
	scanned := 0
	for _, rel := range flociSortedKeys(scripts) {
		scanned++
		for n, line := range codeLines(scripts[rel]) {
			if !strings.Contains(line, "docker run") || !strings.Contains(line, " -d") {
				continue
			}
			if strings.Contains(line, "--rm") {
				found = append(found, fmt.Sprintf("live/%s:%d: %s", rel, n, strings.TrimSpace(line)))
			}
		}
	}
	for _, f := range found {
		t.Errorf("a detached container is started with --rm, so it is erased the instant it dies and nothing can inspect it (#1299): %s", f)
	}
	t.Logf("scanned %d script(s) for `docker run -d ... --rm`", scanned)
}

// TestFlociTeardownGoesThroughTheLibrary is the second half. Keeping the
// corpse is pointless if the EXIT trap removes it without looking: the trap
// is the last moment the container exists, so the reading has to happen
// inside the same call as the removal. That call is gauntlet_floci_teardown.
//
// Scoped to containers named by a FLOCI_* variable on purpose. Not every
// `docker rm -f` in these scripts is a floci teardown - corpus-eks-basic
// removes the child containers its in-emulator Kubernetes leaves behind, and
// live-cert/selftest-kill.sh removes a container by literal name to prove the
// harness did not - and sweeping those into this rule would be a guard that
// names instances rather than the class.
func TestFlociTeardownGoesThroughTheLibrary(t *testing.T) {
	scripts := flociScriptSources(t)
	var found []string
	for _, rel := range flociSortedKeys(scripts) {
		for n, line := range codeLines(scripts[rel]) {
			if !strings.Contains(line, "docker rm") {
				continue
			}
			if strings.Contains(line, "$FLOCI_") || strings.Contains(line, "${FLOCI_") {
				found = append(found, fmt.Sprintf("live/%s:%d: %s", rel, n, strings.TrimSpace(line)))
			}
		}
	}
	for _, f := range found {
		t.Errorf("a floci container is removed with a bare `docker rm`, so whatever killed it is discarded unread; call gauntlet_floci_teardown instead (#1299): %s", f)
	}
}

// TestEveryScriptThatStartsFlociCanReadItBack pairs the two halves per script,
// which is the property that actually matters: a script that starts a
// container and never routes its teardown through the library still loses the
// evidence, even though neither guard above fires on it on its own.
//
// The count is asserted by value as well. A guard over a population that
// silently shrank to nothing reads exactly like a guard that passed, and this
// one's population comes from a glob.
func TestEveryScriptThatStartsFlociCanReadItBack(t *testing.T) {
	scripts := flociScriptSources(t)
	// 61 was measured 2026-09-18, not guessed: 63 scripts carried the
	// `docker run -d --rm` string before #1299, but two of those are
	// comments - the library's own explanation of the fix, and a live-cert
	// selftest telling a human how to bring up an ad-hoc emulator.
	//
	// 62 since #1312, and the extra one is a blind spot found, not a script
	// added: corpus-eks-basic's three starts were `docker run -d --network
	// ... \` with `--name` on a later line, so the one-line detection above
	// never counted it. Routing every start through gauntlet_floci_start put
	// the name on the same line as the call, and the guard now sees it.
	const wantStarters = 62

	var starters, blind []string
	for _, rel := range flociSortedKeys(scripts) {
		if rel == flociLibrary {
			// The library holds the one `docker run -d` since #1312 and
			// defines the helper; it is not a script that starts an
			// estate's emulator.
			continue
		}
		src := scripts[rel]
		startsOne := false
		for _, line := range linesOnly(codeLines(src)) {
			if startsFloci(line) {
				startsOne = true
				break
			}
		}
		if !startsOne {
			continue
		}
		starters = append(starters, rel)
		if !strings.Contains(src, "gauntlet_floci_teardown") {
			blind = append(blind, rel)
		}
	}

	for _, rel := range blind {
		t.Errorf("live/%s starts a detached container but never calls gauntlet_floci_teardown, so if that container dies mid-run its exit code and logs go with it (#1299)", rel)
	}
	if len(starters) != wantStarters {
		t.Errorf("%d script(s) start a detached container, expected %d - if that is a real change, move the number here and say why in the commit; if it is not, the glob in flociScriptSources has stopped matching: %v",
			len(starters), wantStarters, starters)
	}
}

// TestFlociTeardownReadsBeforeItRemoves pins the one ordering the whole fix
// rests on. Inspect and log after `docker rm -f` returns nothing at all, and
// the mistake would be invisible: the helper would still be called from every
// trap, the guards above would still pass, and every postmortem would be
// empty.
func TestFlociTeardownReadsBeforeItRemoves(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("e2e", "lib", "gauntlet.sh"))
	if err != nil {
		t.Fatalf("reading live/e2e/lib/gauntlet.sh: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "gauntlet_floci_teardown() {")
	if start < 0 {
		t.Fatal("live/e2e/lib/gauntlet.sh does not define gauntlet_floci_teardown")
	}
	body = body[start:]

	inspect := strings.Index(body, "docker inspect")
	logs := strings.Index(body, "docker logs")
	remove := strings.Index(body, "docker rm")
	if inspect < 0 || logs < 0 || remove < 0 {
		t.Fatalf("gauntlet_floci_teardown is missing one of inspect/logs/rm (inspect=%d logs=%d rm=%d) - it cannot both read and remove", inspect, logs, remove)
	}
	if inspect > remove {
		t.Error("gauntlet_floci_teardown runs `docker inspect` after `docker rm`, so it inspects nothing")
	}
	if logs > remove {
		t.Error("gauntlet_floci_teardown runs `docker logs` after `docker rm`, so it collects no logs")
	}
	// The prefix is what makes a postmortem greppable out of a multi-thousand
	// line estate log, and it must not be the runner's own "GAUNTLET " grammar,
	// where an unrecognised line is a parse error (tools/gauntlet/protocol.go).
	if !strings.Contains(body, "FLOCI-POSTMORTEM") {
		t.Error("gauntlet_floci_teardown's output carries no FLOCI-POSTMORTEM prefix, so it cannot be found in an estate log")
	}
	if strings.Contains(body, `printf 'GAUNTLET `) {
		t.Error("gauntlet_floci_teardown prints a GAUNTLET-prefixed line; that prefix is the runner's parsed grammar, not a place for diagnostics")
	}
}

// flociLibrary is live/e2e/lib/gauntlet.sh as flociScriptSources keys it.
const flociLibrary = "e2e/lib/gauntlet.sh"

// startsFloci says whether a code line starts a floci container: through
// the library's gauntlet_floci_start (#1312), or with the bare
// `docker run -d ... --name` that TestFlociStartGoesThroughTheLibrary
// forbids outside the library. Both count, so a script that regresses to
// the bare form is still a starter here and still has to read its corpse.
func startsFloci(line string) bool {
	if strings.Contains(line, "gauntlet_floci_start ") && !strings.Contains(line, "() {") {
		return true
	}
	return strings.Contains(line, "docker run") && strings.Contains(line, " -d") && strings.Contains(line, "--name")
}

// TestFlociStartGoesThroughTheLibrary is issue #1312's guard. A container
// started with a bare `docker run -d` carries no ownership labels, so once
// the script that started it is SIGKILLed nothing can tell it from a
// concurrent run's container and it is unsweepable for the rest of its
// life. gauntlet_floci_start labels it with the owner's pid and that pid's
// start time, and sweeps the estate's leaks first.
//
// Scoped like TestFlociTeardownGoesThroughTheLibrary, to lines that name a
// FLOCI_* variable: corpus-eks-basic's per-command `docker run --rm`
// helpers are neither detached nor floci, and belong outside this rule.
func TestFlociStartGoesThroughTheLibrary(t *testing.T) {
	scripts := flociScriptSources(t)
	var found []string
	scanned := 0
	for _, rel := range flociSortedKeys(scripts) {
		if rel == flociLibrary {
			continue
		}
		scanned++
		for n, line := range codeLines(scripts[rel]) {
			if !strings.Contains(line, "docker run") || !strings.Contains(line, " -d") {
				continue
			}
			if strings.Contains(line, "$FLOCI_") || strings.Contains(line, "${FLOCI_") {
				found = append(found, fmt.Sprintf("live/%s:%d: %s", rel, n, strings.TrimSpace(line)))
			}
		}
	}
	for _, f := range found {
		t.Errorf("a floci container is started with a bare `docker run -d`, so it carries no ownership labels and can never be swept; call gauntlet_floci_start <name> <docker run args...> instead (#1312): %s", f)
	}
	if scanned == 0 {
		t.Fatal("no script outside the library was scanned; the population is broken rather than clean")
	}

	// And the library's helper must actually write both owner labels and
	// sweep before it starts, or every script is calling a function that
	// labels nothing. Read from the function's own body.
	lib, ok := scripts[flociLibrary]
	if !ok {
		t.Fatalf("%s is not in the scanned population", flociLibrary)
	}
	start := strings.Index(lib, "gauntlet_floci_start() {")
	if start < 0 {
		t.Fatal("live/e2e/lib/gauntlet.sh does not define gauntlet_floci_start")
	}
	body := lib[start:]
	sweep := strings.Index(body, "gauntlet_sweep_leaked_floci ")
	run := strings.Index(body, "docker run -d")
	if sweep < 0 || run < 0 {
		t.Fatalf("gauntlet_floci_start is missing its sweep or its docker run (sweep=%d run=%d)", sweep, run)
	}
	if sweep > run {
		t.Error("gauntlet_floci_start runs `docker run` before it sweeps, so a leak on this estate's port fails the health check before the sweep could have cleared it")
	}
	for _, label := range []string{"$GAUNTLET_FLOCI_LABEL_PID=$$", "$GAUNTLET_FLOCI_LABEL_STARTED=$started", "$GAUNTLET_FLOCI_LABEL_ESTATE=$estate"} {
		if !strings.Contains(body, `--label "`+label+`"`) {
			t.Errorf("gauntlet_floci_start does not write --label %q, so the sweeper has nothing to check", label)
		}
	}
}

func flociSortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func linesOnly(seq func(func(int, string) bool)) []string {
	var out []string
	seq(func(_ int, line string) bool {
		out = append(out, line)
		return true
	})
	return out
}
