package extractor

import (
	"strings"
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

func TestParseBatchResult(t *testing.T) {
	batchRaw := `{
		"questions": [
			{
				"index": 0,
				"text": "Question 1: What is DNA?",
				"choices": [
					{"label": "A", "text": "A nucleic acid"},
					{"label": "B", "text": "A lipid"}
				]
			},
			{
				"index": 1,
				"text": "Question 2: What is ATP?",
				"choices": [
					{"label": "A", "text": "Energy currency"},
					{"label": "B", "text": "Protein"}
				]
			}
		],
		"is_login": false
	}`

	batch, err := ParseBatchResult(batchRaw)
	if err != nil {
		t.Fatalf("unexpected error parsing batch: %v", err)
	}
	if len(batch.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(batch.Questions))
	}
	if batch.IsLogin {
		t.Errorf("expected is_login=false")
	}
	if batch.Questions[0].Choices[0].Text != "A nucleic acid" {
		t.Errorf("unexpected choice text: %s", batch.Questions[0].Choices[0].Text)
	}
	if batch.Questions[1].Index != 1 {
		t.Errorf("unexpected index: %d", batch.Questions[1].Index)
	}
}

func TestMarkAnswerJS(t *testing.T) {
	js := MarkAnswerJS(1, "B")
	if !strings.Contains(js, `data-mimir-q="1"`) {
		t.Errorf("expected selector to contain question index 1: %s", js)
	}
	if !strings.Contains(js, `data-mimir-opt="B"`) {
		t.Errorf("expected selector to contain option B: %s", js)
	}
	if !strings.Contains(js, "dispatchEvent(new Event('change'") {
		t.Errorf("expected bubbling change event dispatch in JS: %s", js)
	}
}

func TestFillAnswerJSUsesStructuredValuesAndReadback(t *testing.T) {
	q := Question{
		Index: 2, TargetID: "mimir-q-2", Type: TypeCheckbox,
		Choices: []Choice{{Label: "A", Text: "Alpha"}, {Label: "C", Text: "Gamma"}},
	}
	js := FillAnswerJS(q, []string{"A", "C"})
	for _, expected := range []string{`"values":["A","C"]`, "answer readback mismatch", "request.type === 'checkbox'"} {
		if !strings.Contains(js, expected) {
			t.Fatalf("structured fill script missing %q", expected)
		}
	}
	for _, forbidden := range []string{"requestSubmit", ".submit("} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("fill script must never submit forms; found %q", forbidden)
		}
	}
}

func TestAdvancePageJSRejectsFinalActions(t *testing.T) {
	for _, expected := range []string{"nextLabels", "submit|send|finish|done|complete|turn in", "no safe next control"} {
		if !strings.Contains(AdvancePageJS, expected) {
			t.Fatalf("advance script missing safety rule %q", expected)
		}
	}
	for _, forbidden := range []string{"requestSubmit", ".submit("} {
		if strings.Contains(AdvancePageJS, forbidden) {
			t.Fatalf("advance script must never submit forms; found %q", forbidden)
		}
	}
}

func TestParseFillResult(t *testing.T) {
	result, err := ParseFillResult(`"{\"ok\":true,\"values\":[\"B\"]}"`)
	if err != nil || !result.OK || len(result.Values) != 1 || result.Values[0] != "B" {
		t.Fatalf("unexpected fill result: result=%+v err=%v", result, err)
	}
}
