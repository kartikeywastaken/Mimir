package solver

import (
	"testing"
	"time"

	"mimir/internal/extractor"
	"mimir/internal/von"
)

func TestSearchQuestionsEmpty(t *testing.T) {
	// Simple test with empty slice to ensure it doesn't panic
	v := von.New("http://127.0.0.1:9222") // mock client
	results := SearchQuestions(v, []extractor.Question{}, 2, 0*time.Second)
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}
