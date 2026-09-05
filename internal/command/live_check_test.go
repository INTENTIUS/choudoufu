// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/terminal"
)

// GitHub issue #790: "choudoufu live-check -json" prints the declared
// roster - every instance's address, type and rung, and every cross-estate
// data-source reference live/OUTPUTS.md's pattern makes visible - instead
// of the prose [LiveCheckCommand.Run] otherwise prints. These tests run the
// real command end to end against real fixtures, the way #790's own "Done
// when" states it: "live-check -json live/e2e/estate lists the demo
// estate's instances with rungs, a fixture with a marker-filtered data
// source yields one entry in references[], and the text and JSON agree on
// every count."

func newLiveCheckCommand(t *testing.T) (*LiveCheckCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()
	view, done := testView(t)
	c := &LiveCheckCommand{Meta: Meta{View: view, WorkingDir: workdir.NewDir(".")}}
	return c, done
}

// TestLiveCheckJSON_ParsesArgs is #114's own rule ("live-check accepts no
// options") widened by exactly one flag rather than opened up: -json is
// recognized, a directory is still optional and at most one, and every
// other flag still refuses.
func TestLiveCheckJSON_ParsesArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantDir    string
		wantJSON   bool
		wantErr    bool
		errContain string
	}{
		{name: "no args", args: nil, wantDir: ".", wantJSON: false},
		{name: "dir only", args: []string{"some/dir"}, wantDir: "some/dir", wantJSON: false},
		{name: "json only", args: []string{"-json"}, wantDir: ".", wantJSON: true},
		{name: "json then dir", args: []string{"-json", "some/dir"}, wantDir: "some/dir", wantJSON: true},
		{name: "dir then json", args: []string{"some/dir", "-json"}, wantDir: "some/dir", wantJSON: true},
		{name: "unknown flag refused", args: []string{"-verbose"}, wantErr: true, errContain: "-json"},
		{name: "two dirs refused", args: []string{"a", "b"}, wantErr: true, errContain: "at most one argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, jsonOutput, diags := parseLiveCheckArgs(tt.args)
			if tt.wantErr {
				if !diags.HasErrors() {
					t.Fatalf("parseLiveCheckArgs(%v) succeeded, want an error", tt.args)
				}
				if !strings.Contains(diags.Err().Error(), tt.errContain) {
					t.Errorf("error %q does not contain %q", diags.Err().Error(), tt.errContain)
				}
				return
			}
			if diags.HasErrors() {
				t.Fatalf("parseLiveCheckArgs(%v) failed: %s", tt.args, diags.Err())
			}
			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if jsonOutput != tt.wantJSON {
				t.Errorf("jsonOutput = %v, want %v", jsonOutput, tt.wantJSON)
			}
		})
	}
}

// liveCheckJSONDoc is this test file's own decode target: only the fields
// these tests assert on, kept separate from views.liveCheckDocument (an
// unexported type in another package this one cannot import) rather than
// exported for testing's sake alone.
type liveCheckJSONDoc struct {
	Dir      string `json:"dir"`
	Estate   string `json:"estate"`
	Blocked  bool   `json:"blocked"`
	ExitCode int    `json:"exit_code"`

	Instances []struct {
		Address string `json:"address"`
		Type    string `json:"type"`
		Rung    string `json:"rung"`
		Refused bool   `json:"refused"`
		Rule    string `json:"rule"`
		Reason  string `json:"reason"`
	} `json:"instances"`

	References []struct {
		From    string   `json:"from"`
		Estate  string   `json:"estate"`
		Address string   `json:"address"`
		ReadBy  []string `json:"read_by"`
	} `json:"references"`

	Checked   []string `json:"checked"`
	Partial   []string `json:"partial"`
	Unchecked []string `json:"unchecked"`
}

// TestLiveCheckJSON_DemoEstateListsInstancesWithRungs is #790's own primary
// "Done when" target: "live-check -json live/e2e/estate lists the demo
// estate's instances with rungs."
func TestLiveCheckJSON_DemoEstateListsInstancesWithRungs(t *testing.T) {
	c, done := newLiveCheckCommand(t)
	code := c.Run([]string{"-json", "../../live/e2e/estate"})
	out := done(t)
	if out.Stderr() != "" {
		t.Errorf("unexpected stderr:\n%s", out.Stderr())
	}

	var doc liveCheckJSONDoc
	if err := json.Unmarshal([]byte(out.Stdout()), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %s\nstdout:\n%s", err, out.Stdout())
	}

	if len(doc.Instances) == 0 {
		t.Fatal("live/e2e/estate reported zero instances; the demo estate declares many managed resources")
	}
	var rungless int
	for _, inst := range doc.Instances {
		if inst.Address == "" {
			t.Errorf("an instance has no address: %+v", inst)
		}
		if inst.Rung == "" {
			rungless++
		}
		switch inst.Rung {
		case "", "tag-governable", "declaration-carried", "record-only":
		default:
			t.Errorf("instance %s has an unrecognized rung %q", inst.Address, inst.Rung)
		}
	}
	if rungless == len(doc.Instances) {
		t.Errorf("every instance is missing a rung; the demo estate's own admitted types should classify")
	}

	// The exit code has to match what a bare (non-JSON) run of the same
	// directory would return, since #790 does not change what "blocked"
	// means - only how the verdict is presented.
	wantCode := 0
	if doc.Blocked {
		wantCode = 1
	}
	if code != wantCode {
		t.Errorf("exit code %d does not match doc.Blocked=%v (want %d)", code, doc.Blocked, wantCode)
	}
	if doc.ExitCode != wantCode {
		t.Errorf("doc.ExitCode = %d, want %d (matching the command's own exit code)", doc.ExitCode, wantCode)
	}
}

// TestLiveCheckJSON_CrossEstateFixtureYieldsOneReference is #790's other
// "Done when" clause: "a fixture with a marker-filtered data source yields
// one entry in references[]."
func TestLiveCheckJSON_CrossEstateFixtureYieldsOneReference(t *testing.T) {
	c, done := newLiveCheckCommand(t)
	code := c.Run([]string{"-json", "../../live/e2e/estate-references"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (the fixture is not meant to be refused):\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}

	var doc liveCheckJSONDoc
	if err := json.Unmarshal([]byte(out.Stdout()), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %s\nstdout:\n%s", err, out.Stdout())
	}

	if len(doc.References) != 1 {
		t.Fatalf("got %d references, want exactly 1: %+v", len(doc.References), doc.References)
	}
	ref := doc.References[0]
	if ref.From != "data.aws_vpc.network" {
		t.Errorf("From = %q, want %q", ref.From, "data.aws_vpc.network")
	}
	if ref.Estate != "estate-references-network" {
		t.Errorf("Estate = %q, want the fixture's tag:tofu-estate filter value", ref.Estate)
	}
	if ref.Address != "aws_vpc.main" {
		t.Errorf("Address = %q, want the fixture's tag:tofu-address filter value", ref.Address)
	}
	if len(ref.ReadBy) != 1 || ref.ReadBy[0] != "aws_subnet.app" {
		t.Errorf("ReadBy = %v, want [\"aws_subnet.app\"]", ref.ReadBy)
	}
}

// TestLiveCheckJSON_AgreesWithTextOnInstanceCount is the second half of
// #790's "the text and JSON agree on every count": run the same directory
// through both renderers and check that the JSON roster's resolved-instance
// count matches the number the text report prints, and that Blocked agrees
// with the exit code both runs return.
func TestLiveCheckJSON_AgreesWithTextOnInstanceCount(t *testing.T) {
	dir := "../../live/e2e/estate-references"

	jsonCmd, jsonDone := newLiveCheckCommand(t)
	jsonCode := jsonCmd.Run([]string{"-json", dir})
	jsonOut := jsonDone(t)

	textCmd, textDone := newLiveCheckCommand(t)
	textCode := textCmd.Run([]string{dir})
	textOut := textDone(t)

	if jsonCode != textCode {
		t.Errorf("exit codes disagree: json=%d text=%d", jsonCode, textCode)
	}

	var doc liveCheckJSONDoc
	if err := json.Unmarshal([]byte(jsonOut.Stdout()), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %s\nstdout:\n%s", err, jsonOut.Stdout())
	}

	var resolved int
	for _, inst := range doc.Instances {
		if !inst.Refused {
			resolved++
		}
	}

	// The text report's own headline names the resolved-instance count
	// directly ("%d managed resource instance(s) resolved."); a substring
	// match on that number is what ties the two together without re-parsing
	// the whole prose report.
	wantSubstr := strconv.Itoa(resolved) + " managed resource instance(s) resolved."
	if !strings.Contains(textOut.Stdout(), wantSubstr) {
		t.Errorf("text report does not say %q, but the JSON roster carries %d resolved instance(s):\ntext:\n%s\njson:\n%s",
			wantSubstr, resolved, textOut.Stdout(), jsonOut.Stdout())
	}
}
