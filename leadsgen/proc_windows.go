//go:build windows

package leadsgen

import "os/exec"

// setProcessGroup is a no-op on Windows; the default cancel kills the scraper.
func setProcessGroup(_ *exec.Cmd) {}
