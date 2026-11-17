package browser

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const profilePrefix = "temproom-chrome-"

type ChromeSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	tempDir string
	exited  chan struct{}
}

func NewChromeSession() *ChromeSession { return &ChromeSession{} }

// LocateChrome finds the Google Chrome binary.
func LocateChrome() (string, error) {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			filepath.Join(os.Getenv("HOME"), "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
		}
	case "windows":
		candidates = []string{
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
		}
	default:
		candidates = []string{"google-chrome", "google-chrome-stable", "chromium"}
	}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("google chrome executable not found")
}

// Args builds the Chrome command line. Every request goes to the SOCKS5 tunnel
// port; hostnames are resolved by A's proxy, WebRTC may not use direct UDP, and
// if the tunnel is down pages fail instead of falling back to B's network.
func Args(profileDir, socksAddr, startURL string) []string {
	host := socksAddr
	if i := strings.LastIndex(socksAddr, ":"); i > 0 {
		host = socksAddr[:i]
	}
	return []string{
		"--user-data-dir=" + profileDir,
		"--proxy-server=socks5://" + socksAddr,
		"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE " + host,
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-component-update",
		"--new-window",
		startURL,
	}
}

// Launch starts an isolated Chrome that can only reach the network via socksAddr.
func (s *ChromeSession) Launch(startURL, socksAddr string) error {
	if socksAddr == "" {
		return errors.New("refusing to start Chrome without the tunnel proxy")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return errors.New("chrome session already running")
	}
	binPath, err := LocateChrome()
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", profilePrefix+"*")
	if err != nil {
		return fmt.Errorf("failed to create temporary profile dir: %w", err)
	}
	if startURL == "" {
		startURL = "https://www.google.com"
	}
	cmd := exec.Command(binPath, Args(tmpDir, socksAddr, startURL)...)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to start chrome: %w", err)
	}
	exited := make(chan struct{})
	s.cmd, s.tempDir, s.exited = cmd, tmpDir, exited
	go func() {
		_ = cmd.Wait()
		close(exited)
		s.mu.Lock()
		if s.cmd == cmd { // user quit Chrome themselves
			s.cmd = nil
			_ = os.RemoveAll(s.tempDir)
			s.tempDir = ""
		}
		s.mu.Unlock()
	}()
	return nil
}

// Close kills Chrome, waits for it to exit, and deletes the profile. It returns
// an error if the profile directory could not be removed.
func (s *ChromeSession) Close() error {
	s.mu.Lock()
	cmd, dir, exited := s.cmd, s.tempDir, s.exited
	s.cmd, s.tempDir = nil, ""
	s.mu.Unlock()
	if cmd == nil {
		return nil
	}
	killTree(cmd)
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
	}
	killProfile(dir) // helper processes that outlived the main process
	if err := removeProfileDir(dir); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if _, err := os.Stat(dir); err == nil {
		return errors.New("profile directory still exists")
	}
	return nil
}

func (s *ChromeSession) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}

// removeProfileDir deletes the temporary profile. Windows keeps file handles
// open for a moment after the processes exit, so retry briefly before failing.
func removeProfileDir(dir string) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// SweepStale removes profiles (and kills their Chrome) left by a crashed or
// force-quit TempRoom. Call once at app startup.
func SweepStale() int {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), profilePrefix+"*"))
	for _, d := range dirs {
		killProfile(d)
		_ = os.RemoveAll(d)
	}
	return len(dirs)
}
