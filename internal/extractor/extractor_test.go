package extractor

import (
	"testing"
)

func TestParseResult(t *testing.T) {
	raw := `{"text": "What is the capital of France?", "choices": [{"label": "A", "text": "London"}, {"label": "B", "text": "Paris"}]}`
	q, err := ParseResult(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Text != "What is the capital of France?" {
		t.Errorf("expected question text, got %q", q.Text)
	}
	if len(q.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(q.Choices))
	}
	if q.Choices[0].Label != "A" || q.Choices[0].Text != "London" {
		t.Errorf("choice 0 mismatch: %+v", q.Choices[0])
	}
	if q.Choices[1].Label != "B" || q.Choices[1].Text != "Paris" {
		t.Errorf("choice 1 mismatch: %+v", q.Choices[1])
	}
}

func TestParseResultDoubleEncoded(t *testing.T) {
	// CDP Runtime.evaluate sometimes returns a JSON-encoded string
	doubleEncoded := `"{\"text\": \"2+2?\", \"choices\": [{\"label\": \"A\", \"text\": \"4\"}]}"`
	q, err := ParseResult(doubleEncoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Text != "2+2?" {
		t.Errorf("expected '2+2?', got %q", q.Text)
	}
	if len(q.Choices) != 1 || q.Choices[0].Label != "A" {
		t.Errorf("unexpected choices: %+v", q.Choices)
	}
}
