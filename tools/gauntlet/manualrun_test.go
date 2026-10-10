// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunGoesAheadOnlyWhenAPersonTypesRun(t *testing.T) {
	for _, c := range []struct {
		in   string
		runs bool
	}{
		{"run\n", true},
		{"  run  \n", true},
		{"", false},
		{"y\n", false},
		{"yes\n", false},
		{"RUN\n", false},
	} {
		var out bytes.Buffer
		err := confirmFrom(&out, strings.NewReader(c.in), time.Second)
		if (err == nil) != c.runs {
			t.Errorf("answer %q: err=%v, want runs=%v", c.in, err, c.runs)
		}
	}
}

func TestRunRefusesWithNoTerminal(t *testing.T) {
	// go test has no controlling terminal under CI or an agent's shell, which
	// is exactly the case that must refuse. Skip only when a person is
	// sitting at one, since the test would then block on their answer.
	if tty, err := openTTY(); err == nil {
		tty.Close()
		t.Skip("a terminal is attached; this checks the no-terminal refusal")
	}
	var out bytes.Buffer
	err := confirmManualRun(&out)
	if err == nil || !strings.Contains(err.Error(), "REFUSED") {
		t.Fatalf("ran with no terminal: err=%v", err)
	}
	if !strings.Contains(out.String(), "DO NOT RETRY") {
		t.Fatalf("refusal banner not printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "STOP AND THINK") {
		t.Fatalf("header not printed before refusing:\n%s", out.String())
	}
}

func TestRunTimesOutWhenNobodyAnswers(t *testing.T) {
	r, w := io.Pipe() // never written: nobody at the terminal
	defer w.Close()
	var out bytes.Buffer
	start := time.Now()
	err := confirmFrom(&out, r, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("silence did not time out as a refusal: err=%v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("timeout did not fire promptly: %s", time.Since(start))
	}
	if !strings.Contains(out.String(), "DO NOT RETRY. DO NOT RESTART.") {
		t.Fatalf("refusal banner not printed on timeout:\n%s", out.String())
	}
}

func TestEstateScriptRunsOnlyWithTheRunnersConfirmation(t *testing.T) {
	if tty, err := openTTY(); err == nil {
		tty.Close()
		t.Skip("a terminal is attached; the script would ask it instead of refusing")
	}
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	lib, err := os.ReadFile(filepath.Join(repo, "live", "e2e", "lib", "gauntlet.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFile := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("live/e2e/lib/gauntlet.sh", string(lib))
	writeFile("live/e2e/x/run.sh", "source \"$(dirname \"$0\")/../lib/gauntlet.sh\"\necho RAN\n")

	run := func(confirm bool) (string, error) {
		cmd := exec.Command("bash", "live/e2e/x/run.sh")
		cmd.Dir = root
		if confirm {
			release, err := handConfirmation(cmd)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(true); err != nil || !strings.Contains(out, "RAN") {
		t.Fatalf("with the runner's confirmation the script did not run: err=%v\n%s", err, out)
	}
	out, err := run(false)
	if err == nil || strings.Contains(out, "RAN") || !strings.Contains(out, "DO NOT RETRY") {
		t.Fatalf("started directly with no terminal, the script ran or did not refuse: err=%v\n%s", err, out)
	}
}
