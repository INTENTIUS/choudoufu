// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// `gauntlet run` only runs when a person runs it.
//
// On 2026-10-09/10 an agent drove one estate to clear by running the
// gauntlet, reading the failure, editing, and running it again, all night,
// when the maintainer had asked for estates to be built first and tested
// after. Instructions, goal files, memories and hooks saying "do not" were
// all read and routed around. So the runner asks a person, at the terminal,
// every time. There is no flag, environment variable or condition that skips
// the question.

const runHeader = `================ gauntlet run: STOP AND THINK ================
A gauntlet run measures finished work. It is not a development loop.
Why is this running at all? If you are an agent: you do not run this.
Build what was asked for, and tell the maintainer it is ready to test.
==============================================================
`

// manualRunTimeout is how long the prompt waits for a person's answer.
const manualRunTimeout = 60 * time.Second

// refusalBanner is what every refusal prints: no terminal, a timeout, or a
// wrong answer. It is written for whoever started the run without being the
// person who should have.
const refusalBanner = `
##############################################################
##############################################################
###                                                        ###
###   GAUNTLET RUN REFUSED. DO NOT RETRY. DO NOT RESTART.  ###
###                                                        ###
##############################################################
##############################################################

The gauntlet runs ONLY when a person starts it at a terminal and
types "run". It did not get that, so nothing ran and nothing was
measured.

If you are an agent: you started this, and you must not have.
  - Do NOT run it again, in the background, with a pipe, with
    "script", "expect", "yes", a pty, or any other way of faking
    a terminal.
  - Do NOT run the estate's run.sh directly instead.
  - STOP. Finish building what was asked for, and tell the
    maintainer it is ready for them to test.
##############################################################
`

// confirmManualRun prints the header to out and asks the person at the
// controlling terminal. The run goes ahead only if they type "run" within
// manualRunTimeout. No terminal - an agent's shell, a pipe, a background
// job - is a refusal, as is silence.
func confirmManualRun(out io.Writer) error {
	fmt.Fprint(out, runHeader)
	tty, err := openTTY()
	if err != nil {
		fmt.Fprint(out, refusalBanner)
		return fmt.Errorf("gauntlet run: REFUSED - no terminal to ask a person on (%v). Do not retry", err)
	}
	defer tty.Close()
	return confirmFrom(out, tty, manualRunTimeout)
}

// confirmFrom asks on out and reads the answer from in, giving up after
// timeout.
func confirmFrom(out io.Writer, in io.Reader, timeout time.Duration) error {
	fmt.Fprintf(out, "Type \"run\" within %s to start, anything else to stop: ", timeout)
	answer := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer <- line
	}()
	select {
	case line := <-answer:
		if strings.TrimSpace(line) == "run" {
			return nil
		}
		fmt.Fprint(out, refusalBanner)
		return fmt.Errorf("gauntlet run: REFUSED - the answer was not \"run\". Do not retry")
	case <-time.After(timeout):
		fmt.Fprint(out, refusalBanner)
		return fmt.Errorf("gauntlet run: REFUSED - no answer within %s, so no person is at this terminal. Do not retry", timeout)
	}
}

// openTTY opens the controlling terminal.
func openTTY() (*os.File, error) { return os.Open("/dev/tty") }

// confirmedLine is what the runner hands an estate script on file
// descriptor 3 once the person has typed "run", so the script does not ask
// them a second time. live/e2e/lib/gauntlet.sh reads it; anything else on
// fd 3, or nothing, and the script asks the terminal itself.
const confirmedLine = "choudoufu-gauntlet-confirmed"

// handConfirmation gives cmd the confirmation on its file descriptor 3. The
// returned close releases this process's end once the child has started.
func handConfirmation(cmd *exec.Cmd) (func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	if _, err := w.WriteString(confirmedLine + "\n"); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	w.Close()
	cmd.ExtraFiles = append([]*os.File{r}, cmd.ExtraFiles...)
	return func() { r.Close() }, nil
}
