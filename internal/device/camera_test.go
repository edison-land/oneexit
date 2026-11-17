package device

import (
	"testing"
)

func TestDetectCamera(t *testing.T) {
	// Should run cleanly without panic on macOS/Windows/Linux
	info := DetectCamera()
	t.Logf("Camera detection result: available=%v name=%s", info.Available, info.Name)
}
