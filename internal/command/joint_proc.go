package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// jointParentProcess returns the parent's command line and a start-time token
// that tells two processes with one pid apart. On Linux it reads /proc, so a
// minimal image with no ps still works; elsewhere, and if /proc has no answer,
// it asks ps.
func jointParentProcess(pid int) (args, start string, err error) {
	if a, s, ok := jointProcInfo(pid); ok {
		return a, s, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", "", fmt.Errorf("ps: %w", err)
	}
	st, _ := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	return string(out), string(st), nil
}

// jointProcInfo reads /proc/<pid>/cmdline (NUL separated) and the start time
// (field 22 of /proc/<pid>/stat, in clock ticks since boot).
func jointProcInfo(pid int) (args, start string, ok bool) {
	cmd, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil || len(cmd) == 0 {
		return "", "", false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", "", false
	}
	// The command name in field 2 is parenthesised and may hold spaces, so
	// count fields from the last ")".
	i := strings.LastIndex(string(stat), ")")
	if i < 0 {
		return "", "", false
	}
	f := strings.Fields(string(stat)[i+1:])
	// After ")", field 3 (state) is f[0], so field 22 (starttime) is f[19].
	if len(f) < 20 {
		return "", "", false
	}
	return strings.Join(strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00"), " "), f[19], true
}
