//go:build !windows

package browser

import (
	"os/exec"
	"regexp"
)

// killProfile kills helper processes Chrome left behind for this profile. On
// Unix the browser forks helpers that outlive the main process.
func killProfile(dir string) {
	if dir == "" {
		return
	}
	_ = exec.Command("pkill", "-9", "-f", regexp.QuoteMeta("--user-data-dir="+dir)).Run()
}

// killTree ends the browser process and everything it spawned.
func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
