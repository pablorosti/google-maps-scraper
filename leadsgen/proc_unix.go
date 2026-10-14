//go:build !windows

package leadsgen

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup runs the scraper in its own process group so stopping it
// also stops the Chromium processes it spawned.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		_ = syscall.Kill(pgid, syscall.SIGTERM)

		time.AfterFunc(killGracePeriod, func() { _ = syscall.Kill(pgid, syscall.SIGKILL) })

		return nil
	}
}
