package overlay

import (
	"strings"
	"testing"

	"mimir/internal/extractor"
	"mimir/internal/solver"
)

func TestHTMLRendering(t *testing.T) {
	q := &extractor.Question{
		Text: "Which planet is closest to the Sun?",
		Choices: []extractor.Choice{
			{Label: "A", Text: "Venus"},
			{Label: "B", Text: "Mercury"},
			{Label: "C", Text: "Mars"},
		},
	}
	r := &solver.Result{
		Answer:     "B",
		Confidence: 94,
		Reason:     "Laya model decision",
		SearchUsed: true,
		Backend:    "laya-mlx",
	}

	html := HTML(q, r)
	if !strings.Contains(html, "Mark B") {
		t.Errorf("HTML should contain Mark B button, got:\n%s", html)
	}
	if !strings.Contains(html, `onclick=`) {
		t.Errorf("HTML should have inline onclick handler for HTML5 innerHTML support")
	}
	if !strings.Contains(html, "94% confidence") {
		t.Errorf("HTML should contain confidence percentage")
	}
	if !strings.Contains(html, "[search]") {
		t.Errorf("HTML should contain search badge when search is used")
	}
}
