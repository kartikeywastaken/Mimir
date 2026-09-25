package browser

import (
	"strings"
	"testing"
)

func TestFindBrowserBinary(t *testing.T) {
	name, path, err := FindBrowserBinary()
	if err != nil {
		t.Fatalf("expected browser binary to be found, got error: %v", err)
	}
	if name == "" || path == "" {
		t.Errorf("expected non-empty name and path, got name=%q path=%q", name, path)
	}
	if !strings.Contains(path, "Contents/MacOS") && !strings.Contains(path, "bin") {
		t.Errorf("expected executable binary path, got: %s", path)
	}
}

func TestDetectBrowsers(t *testing.T) {
	browsers := DetectBrowsers()
	if len(browsers) == 0 {
		t.Fatalf("expected at least one installed browser to be detected on this machine")
	}
	t.Logf("Detected %d browser(s):", len(browsers))
	for i, b := range browsers {
		t.Logf("  [%d] %s -> %s (default: %v)", i+1, b.Name, b.Path, b.IsDefault)
		if b.Name == "" || b.Executable == "" {
			t.Errorf("browser %d has empty name or executable", i)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"   ", ""},
		{"example.com/quiz", "https://example.com/quiz"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://platform.com/test", "https://platform.com/test"},
		{"file:///tmp/mock_quiz.html", "file:///tmp/mock_quiz.html"},
	}

	for _, tc := range tests {
		got := NormalizeURL(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeURL(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}
