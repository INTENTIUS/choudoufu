// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// jointRootMarker is the file that makes a composed root a joint root
// (GitHub issue #2213): one choudoufu wrote to join the per-unit invocations
// of a tool run. Its module calls may carry arguments.
const jointRootMarker = ".choudoufu-joint-root"

// jointEnv is the explicit switch for a joint run: "off" disables it, any
// other value is the run id to join, for a driver that is not Terragrunt or
// Atmos.
const jointEnv = "CHOUDOUFU_JOINT_RUN"

// jointStubMagic opens the file a unit's "plan -out" leaves behind.
const jointStubMagic = "choudoufu_joint_plan"

// jointSentinelRE finds the placeholder `output -json` hands a tool for an
// upstream unit's output that is not known until apply.
var jointSentinelRE = regexp.MustCompile(`\(known after apply: ([A-Za-z_][A-Za-z0-9_-]*)\.([A-Za-z0-9_-]+)\)`)

// jointDebugf says why a unit took the ordinary path, when asked to.
func jointDebugf(format string, a ...any) {
	if os.Getenv("CHOUDOUFU_JOINT_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "choudoufu joint: "+format+"\n", a...)
	}
}

func jointSentinel(unit, output string) string {
	return "(known after apply: " + unit + "." + output + ")"
}

// jointTool is the Terragrunt or Atmos process that is invoking this binary
// once per unit, found from the parent process.
type jointTool struct {
	Kind  string // "terragrunt", "atmos" or "env"
	PID   int
	ID    string
	Phase string // "plan", "apply" or ""
}

// detectJointTool reports the multi-unit tool run this invocation belongs
// to, or nil when it is a run of one unit (the common case, left untouched).
func detectJointTool() *jointTool {
	switch v := os.Getenv(jointEnv); v {
	case "off", "0", "false":
		return nil
	case "", "on", "1", "true":
		// look at the parent process
	default:
		return &jointTool{Kind: "env", ID: "env-" + jointSafe(v), Phase: "plan"}
	}
	ppid := os.Getppid()
	if ppid <= 1 {
		return nil
	}
	if argv, start, ok := jointParentFromProc(ppid); ok {
		jointDebugf("parent %d (from /proc): %s", ppid, strings.Join(argv, " "))
		return jointToolFromArgs(ppid, start, argv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	args, err := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(ppid)).Output()
	if err != nil {
		jointDebugf("ps failed: %v", err)
		return nil
	}
	jointDebugf("parent %d: %s", ppid, strings.TrimSpace(string(args)))
	start, _ := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(ppid)).Output()
	return jointToolFromArgs(ppid, strings.TrimSpace(string(start)), strings.Fields(string(args)))
}

// jointParentFromProc reads the parent's arguments and start time from /proc,
// which exists on Linux even in images without procps. ok is false when /proc
// is not there or cannot be read, and the caller falls back to ps.
func jointParentFromProc(pid int) (argv []string, start string, ok bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	cmdline, err := os.ReadFile(dir + "/cmdline")
	if err != nil {
		return nil, "", false
	}
	argv = jointArgvFromCmdline(cmdline)
	if len(argv) == 0 {
		return nil, "", false
	}
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return nil, "", false
	}
	start, ok = jointStartFromStat(string(stat))
	if !ok {
		return nil, "", false
	}
	return argv, start, true
}

// jointArgvFromCmdline splits the NUL-separated content of /proc/<pid>/cmdline.
func jointArgvFromCmdline(b []byte) []string {
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}

// jointStartFromStat returns field 22 (starttime, in clock ticks since boot)
// of a /proc/<pid>/stat line. The comm field is in parentheses and may hold
// spaces and parentheses, so the fields are counted after the last ')'.
func jointStartFromStat(line string) (string, bool) {
	i := strings.LastIndex(line, ")")
	if i < 0 {
		return "", false
	}
	// Fields after the comm start at field 3 (state), so starttime is the
	// 20th of them.
	f := strings.Fields(line[i+1:])
	if len(f) < 20 {
		return "", false
	}
	return f[19], true
}

func jointToolFromArgs(pid int, start string, argv []string) *jointTool {
	if len(argv) == 0 {
		return nil
	}
	kind := strings.TrimSuffix(strings.ToLower(filepath.Base(argv[0])), ".exe")
	if kind != "terragrunt" && kind != "atmos" {
		return nil
	}
	multi := false
	phase := ""
	for _, a := range argv[1:] {
		switch a {
		case "--all", "run-all", "plan-all", "apply-all", "--affected", "--graph":
			multi = true
		case "plan":
			phase = "plan"
		case "apply", "deploy":
			if phase == "" {
				phase = "apply"
			}
		}
	}
	if !multi {
		return nil
	}
	sum := sha256.Sum256([]byte(start))
	return &jointTool{Kind: kind, PID: pid, Phase: phase, ID: fmt.Sprintf("%s-%d-%s", kind, pid, hex.EncodeToString(sum[:4]))}
}

var jointUnsafeRE = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// jointSafe makes s usable as a module name and a file name.
func jointSafe(s string) string {
	s = strings.Trim(jointUnsafeRE.ReplaceAllString(s, "_"), "_")
	if s == "" {
		return "unit"
	}
	if c := s[0]; c >= '0' && c <= '9' || c == '-' {
		s = "u_" + s
	}
	return s
}

// jointUnitName is the readable name of a unit: the Terragrunt unit
// directory (the working directory is a cache directory below it), or the
// stem of the var file Atmos generated for the component.
func jointUnitName(dir string, varFiles []string) string {
	d := filepath.ToSlash(dir)
	if i := strings.Index(d, "/.terragrunt-cache/"); i >= 0 {
		return jointSafe(filepath.Base(d[:i]))
	}
	for _, f := range varFiles {
		b := filepath.Base(f)
		for _, suf := range []string{".terraform.tfvars.json", ".tfvars.json", ".tfvars"} {
			if strings.HasSuffix(b, suf) {
				return jointSafe(strings.TrimSuffix(b, suf))
			}
		}
	}
	return jointSafe(filepath.Base(d))
}
