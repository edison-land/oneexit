//go:build windows

package browser

import (
	"os/exec"
	"strconv"
)

// killProfile stops Chrome processes that still hold the temporary profile.
// Windows has no pkill, and Process.Kill only ends the browser process, so ask
// the OS which processes were started with this profile directory.
func killProfile(dir string) {
	if dir == "" {
		return
	}
	script := "Get-CimInstance Win32_Process | " +
		"Where-Object { $_.CommandLine -like '*--user-data-dir=" + dir + "*' } | " +
		"ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

// killTree ends the browser process and its renderer/GPU children, which would
// otherwise keep the profile directory locked.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/F", "/T", "/PID", pid).Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
