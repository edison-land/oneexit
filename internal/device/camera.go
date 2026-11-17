package device

import (
	"os/exec"
	"runtime"
	"strings"
)

type CameraInfo struct {
	Available bool   `json:"available"`
	Name      string `json:"name,omitempty"`
}

// DetectCamera checks if camera devices are connected to the host system
func DetectCamera() CameraInfo {
	switch runtime.GOOS {
	case "darwin":
		// Query macOS system_profiler for camera info without accessing feed
		cmd := exec.Command("system_profiler", "SPCameraDataType")
		out, err := cmd.Output()
		if err == nil {
			str := string(out)
			if strings.Contains(str, "Model ID:") || strings.Contains(str, "Camera") {
				lines := strings.Split(str, "\n")
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if strings.HasSuffix(line, ":") && !strings.Contains(line, "Camera:") && len(line) > 2 {
						return CameraInfo{
							Available: true,
							Name:      strings.TrimSuffix(line, ":"),
						}
					}
				}
				return CameraInfo{Available: true, Name: "FaceTime HD / Web Camera"}
			}
		}
	case "windows":
		// Query PowerShell WMI PnP devices
		cmd := exec.Command("powershell", "-NoProfile", "-Command", "Get-PnpDevice -Class 'Camera' -Status OK | Select-Object -First 1 -ExpandProperty FriendlyName")
		out, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return CameraInfo{
				Available: true,
				Name:      strings.TrimSpace(string(out)),
			}
		}
	}

	return CameraInfo{
		Available: false,
	}
}
