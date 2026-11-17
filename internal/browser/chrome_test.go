package browser

import (
	"testing"
)

func TestLocateChrome(t *testing.T) {
	// Attempt to locate Chrome on the system.
	// It's acceptable if Chrome is not installed in a headless CI environment,
	// but the function should handle it gracefully without crashing.
	path, err := LocateChrome()
	if err == nil {
		t.Logf("Found Chrome at: %s", path)
	} else {
		t.Logf("Chrome not found on this machine: %v", err)
	}
}

func TestChromeSessionInit(t *testing.T) {
	session := NewChromeSession()
	if session.IsRunning() {
		t.Errorf("Newly initialized ChromeSession should not be running")
	}
	_ = session.Close()
	if session.IsRunning() {
		t.Errorf("ChromeSession should still not be running after close")
	}
}
