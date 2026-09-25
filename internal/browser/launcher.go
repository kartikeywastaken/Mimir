package browser

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"mimir/internal/von"
)

const (
	DefaultCDPURL   = "http://127.0.0.1:9222"
	DefaultProfileDir = "/tmp/brave-mimir"
)

// BrowserInfo represents an installed browser detected on the system
type BrowserInfo struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Executable string `json:"executable"`
	IsDefault  bool   `json:"is_default"`
}

// DetectBrowsers scans the host system for all installed Chromium-compatible browsers
func DetectBrowsers() []BrowserInfo {
	var results []BrowserInfo
	home := os.Getenv("HOME")

	type candidate struct {
		name       string
		macPaths   []string
		linuxCmds  []string
	}

	candidates := []candidate{
		{
			name: "Brave Browser",
			macPaths: []string{
				"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
				home + "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			},
			linuxCmds: []string{"brave-browser", "brave"},
		},
		{
			name: "Google Chrome",
			macPaths: []string{
				"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
				home + "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			},
			linuxCmds: []string{"google-chrome", "google-chrome-stable"},
		},
		{
			name: "Microsoft Edge",
			macPaths: []string{
				"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
				home + "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			},
			linuxCmds: []string{"microsoft-edge", "microsoft-edge-stable"},
		},
		{
			name: "Arc Browser",
			macPaths: []string{
				"/Applications/Arc.app/Contents/MacOS/Arc",
				home + "/Applications/Arc.app/Contents/MacOS/Arc",
			},
			linuxCmds: []string{"arc"},
		},
		{
			name: "Chromium",
			macPaths: []string{
				"/Applications/Chromium.app/Contents/MacOS/Chromium",
				home + "/Applications/Chromium.app/Contents/MacOS/Chromium",
			},
			linuxCmds: []string{"chromium", "chromium-browser"},
		},
		{
			name: "Google Chrome Canary",
			macPaths: []string{
				"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
				home + "/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			},
			linuxCmds: []string{"google-chrome-canary"},
		},
	}

	seen := make(map[string]bool)

	for _, c := range candidates {
		if runtime.GOOS == "darwin" {
			for _, p := range c.macPaths {
				if _, err := os.Stat(p); err == nil && !seen[p] {
					seen[p] = true
					results = append(results, BrowserInfo{
						Name:       c.name,
						Path:       p,
						Executable: p,
					})
					break
				}
			}
		} else {
			for _, cmd := range c.linuxCmds {
				if path, err := exec.LookPath(cmd); err == nil && !seen[path] {
					seen[path] = true
					results = append(results, BrowserInfo{
						Name:       c.name,
						Path:       path,
						Executable: path,
					})
					break
				}
			}
		}
	}

	// Mark the first found browser as default if any exist
	if len(results) > 0 {
		results[0].IsDefault = true
	}

	return results
}

// FindBrowserBinary locates the primary browser (Brave or fallback)
func FindBrowserBinary() (string, string, error) {
	browsers := DetectBrowsers()
	if len(browsers) == 0 {
		return "", "", fmt.Errorf("no supported Chromium-based browser found (Brave, Chrome, Edge, Arc, Chromium)")
	}
	return browsers[0].Name, browsers[0].Executable, nil
}

// IsCDPAvailable checks if port 9222 is responding
func IsCDPAvailable(cdpURL string) bool {
	if cdpURL == "" {
		cdpURL = DefaultCDPURL
	}
	client := http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(cdpURL + "/json/version")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// NormalizeURL ensures URL has a proper scheme
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "file://") {
		return "https://" + raw
	}
	return raw
}

// LaunchBrowser launches the default detected browser
func LaunchBrowser(targetURL string) error {
	browsers := DetectBrowsers()
	if len(browsers) == 0 {
		return fmt.Errorf("no supported browser found on system")
	}
	return LaunchBrowserWith(browsers[0], targetURL)
}

// LaunchBrowserWith launches a specific browser with remote debugging on port 9222 and opens targetURL
func LaunchBrowserWith(b BrowserInfo, targetURL string) error {
	if b.Executable == "" {
		return fmt.Errorf("no executable provided for browser: %s", b.Name)
	}

	targetURL = NormalizeURL(targetURL)

	// If CDP is already running on port 9222, open or navigate to targetURL
	if IsCDPAvailable(DefaultCDPURL) {
		if targetURL != "" {
			v := von.New(DefaultCDPURL)
			t, err := v.ActiveTarget()
			if err == nil && t != nil {
				_, _ = v.Evaluate(t.WebSocketURL, fmt.Sprintf("window.location.href = %s;", strconvQuote(targetURL)))
				return nil
			}
			_, _ = v.CreateBackgroundTab(targetURL)
		}
		return nil
	}

	profileDir := DefaultProfileDir
	if b.Name != "" {
		safeName := strings.ToLower(strings.ReplaceAll(b.Name, " ", "-"))
		profileDir = fmt.Sprintf("/tmp/mimir-%s", safeName)
	}

	args := []string{
		"--remote-debugging-port=9222",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}

	if targetURL != "" {
		args = append(args, targetURL)
	}

	cmd := exec.Command(b.Executable, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch %s (%s): %w", b.Name, b.Executable, err)
	}

	return nil
}

// WaitForCDP polls until CDP port 9222 is active or times out
func WaitForCDP(cdpURL string, timeout time.Duration) (*von.Client, error) {
	if cdpURL == "" {
		cdpURL = DefaultCDPURL
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if IsCDPAvailable(cdpURL) {
			v := von.New(cdpURL)
			// Wait briefly for at least one target to be created
			for i := 0; i < 5; i++ {
				targets, err := v.ListTargets()
				if err == nil && len(targets) > 0 {
					return v, nil
				}
				time.Sleep(300 * time.Millisecond)
			}
			return v, nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out waiting for browser CDP at %s (is the browser starting?)", cdpURL)
}

// EnsureBrowser launches the default browser and connects to CDP
func EnsureBrowser(targetURL string) (*von.Client, error) {
	browsers := DetectBrowsers()
	if len(browsers) == 0 {
		return nil, fmt.Errorf("no supported browser found")
	}
	return EnsureBrowserWith(browsers[0], targetURL)
}

// EnsureBrowserWith launches the given browser and connects to CDP
func EnsureBrowserWith(b BrowserInfo, targetURL string) (*von.Client, error) {
	if !IsCDPAvailable(DefaultCDPURL) {
		if err := LaunchBrowserWith(b, targetURL); err != nil {
			return nil, err
		}
	} else if targetURL != "" {
		_ = OpenURL(DefaultCDPURL, targetURL)
	}
	return WaitForCDP(DefaultCDPURL, 15*time.Second)
}

// NavigateTo navigates the active target to the given URL
func NavigateTo(v *von.Client, targetURL string) error {
	targetURL = NormalizeURL(targetURL)
	if targetURL == "" {
		return nil
	}
	t, err := v.ActiveTarget()
	if err != nil {
		_, err = v.CreateBackgroundTab(targetURL)
		return err
	}
	_, err = v.Evaluate(t.WebSocketURL, fmt.Sprintf("window.location.href = %s;", strconvQuote(targetURL)))
	return err
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// OpenURL opens a URL in a new tab via /json/new
func OpenURL(cdpURL, rawURL string) error {
	rawURL = NormalizeURL(rawURL)
	endpoint := fmt.Sprintf("%s/json/new?%s", cdpURL, url.QueryEscape(rawURL))
	resp, err := http.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
