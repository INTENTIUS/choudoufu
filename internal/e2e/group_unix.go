//go:build unix

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

// watchdog is a shell that leads one process group and outlives this test
// process by up to a second. Every binary the e2e tests start joins that
// group. When `go test` kills a run on its timeout it exits without running
// any cleanup, so the choudoufu processes it started and the provider
// plugins under them are orphaned to init. The watchdog sees the test
// process gone and signals the whole group.
var (
	watchdogOnce sync.Once
	watchdogPgid int
)

func joinTestProcessGroup(cmd *exec.Cmd) {
	watchdogOnce.Do(func() {
		script := fmt.Sprintf(`while kill -0 %d 2>/dev/null; do sleep 1; done; kill -KILL -$$`, os.Getpid())
		wd := exec.Command("sh", "-c", script)
		wd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := wd.Start(); err != nil {
			return
		}
		watchdogPgid = wd.Process.Pid
		go func() { _ = wd.Wait() }()
	})
	if watchdogPgid == 0 {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: watchdogPgid}
}
