package browser

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"mimir/internal/von"
)

const (
	DefaultCDPURL   = "http://127.0.0.1:9222"
	BraveProfileDir = "/tmp/brave-mimir"
)

// FindBrowserBinary locates Brave Browser or falls back to Google Chrome
func FindBrowserBinary() (string, string, error) {
	candidates := []struct {
		name string
		path string
	}{
		{"Brave Browser", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
		{"Brave Browser (User)", os.Getenv("HOME") + "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
		{"Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		{"Google Chrome (User)", os.Getenv("HOME") + "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
	}

	for _, c := range candidates {
		if _, err := os.Stat(c.path); err == nil {
			return c.name, c.path, nil
		}
	}
	return "", "", fmt.Errorf("no supported browser found (looked for Brave Browser and Google Chrome in /Applications)")
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

// LaunchBrowser launches Brave Browser with remote debugging on port 9222 and opens targetURL
func LaunchBrowser(targetURL string) error {
	name, binPath, err := FindBrowserBinary()
	if err != nil {
		return err
	}

	targetURL = NormalizeURL(targetURL)

	// If CDP is already running, open or navigate to targetURL via CDP
	if IsCDPAvailable(DefaultCDPURL) {
		if targetURL != "" {
			v := von.New(DefaultCDPURL)
			t, err := v.ActiveTarget()
			if err == nil && t != nil {
				// Navigate active tab to targetURL
				_, _ = v.Evaluate(t.WebSocketURL, fmt.Sprintf("window.location.href = %q;", targetURL))
				return nil
			}
			// Or create new background tab and activate
			_, _ = v.CreateBackgroundTab(targetURL)
		}
		return nil
	}

	args := []string{
		"--remote-debugging-port=9222",
		"--user-data-dir=" + BraveProfileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}

	if targetURL != "" {
		args = append(args, targetURL)
	}

	cmd := exec.Command(binPath, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch %s: %w", name, err)
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
	return nil, fmt.Errorf("timed out waiting for browser CDP at %s (is Brave Browser starting?)", cdpURL)
}

// NavigateTo navigates the active target to the given URL
func NavigateTo(v *von.Client, targetURL string) error {
	targetURL = NormalizeURL(targetURL)
	if targetURL == "" {
		return nil
	}
	t, err := v.ActiveTarget()
	if err != nil {
		// Try to create target
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

// Helper to open a URL via /json/new
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
