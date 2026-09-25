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
	if name != "Brave Browser" && name != "Google Chrome" {
		t.Errorf("unexpected browser name: %s", name)
	}
	if !strings.Contains(path, "Contents/MacOS") {
		t.Errorf("expected macOS binary path, got: %s", path)
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
