package solver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mimir/internal/extractor"
	"mimir/internal/llm"
)

// fakeLLM serves an OpenAI-compatible /chat/completions endpoint that always
// replies with content, and records the last request's messages.
func fakeLLM(t *testing.T, content string) (*httptest.Server, *[]llm.Message) {
	t.Helper()
	var last []llm.Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var req struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		last = req.Messages
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func llmConfig(url string) Config {
	return Config{Engine: EngineLLM, LLM: llm.Config{BaseURL: url, APIKey: "test-key", Model: "test-model"}}
}

var capitalQuestion = extractor.Question{
	Type: extractor.TypeChoice,
	Text: "What is the capital of France?",
	Choices: []extractor.Choice{
		{Label: "A", Text: "Berlin"}, {Label: "B", Text: "Paris"}, {Label: "C", Text: "Rome"},
	},
}

func TestLLMBackendChoice(t *testing.T) {
	srv, last := fakeLLM(t, `{"values":["b"],"confidence":99,"reason":"Paris is the capital"}`)
	q := capitalQuestion
	res, err := SolveLocal(&q, nil, llmConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "B" || res.Backend != "llm" {
		t.Fatalf("got answer=%q backend=%q", res.Answer, res.Backend)
	}
	if res.Confidence != llmMaxConfidence {
		t.Fatalf("confidence should be capped at %d, got %d", llmMaxConfidence, res.Confidence)
	}
	if prompt := (*last)[1].Content; !strings.Contains(prompt, "B. Paris") {
		t.Fatalf("prompt missing options: %s", prompt)
	}
}

func TestLLMBackendRejectsUnknownOption(t *testing.T) {
	srv, _ := fakeLLM(t, `{"values":["E"],"confidence":80}`)
	q := capitalQuestion
	if _, err := SolveLocal(&q, nil, llmConfig(srv.URL)); err == nil {
		t.Fatal("expected error for option not on the form")
	}
}

func TestLLMBackendRejectsMultipleForSingleChoice(t *testing.T) {
	srv, _ := fakeLLM(t, `{"values":["A","B"],"confidence":80}`)
	q := capitalQuestion
	if _, err := SolveLocal(&q, nil, llmConfig(srv.URL)); err == nil {
		t.Fatal("expected error for two answers on a single-choice question")
	}
}

func TestLLMBackendCheckbox(t *testing.T) {
	srv, _ := fakeLLM(t, "```json\n{\"values\":[\"A\",\"C\",\"A\"],\"confidence\":81}\n```")
	q := extractor.Question{
		Type: extractor.TypeCheckbox,
		Text: "Select all prime numbers",
		Choices: []extractor.Choice{
			{Label: "A", Text: "2"}, {Label: "B", Text: "4"}, {Label: "C", Text: "5"},
		},
	}
	res, err := SolveLocal(&q, nil, llmConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Values, ",") != "A,C" {
		t.Fatalf("values = %v", res.Values)
	}
}

func TestLLMBackendFreeTextAndEvidence(t *testing.T) {
	srv, last := fakeLLM(t, `{"answer":"Carbon dioxide","confidence":40,"reason":"weak evidence"}`)
	q := extractor.Question{Type: extractor.TypeText, Text: "Which gas do humans exhale?"}
	ev := []Evidence{{Title: "Biology", URL: "https://example.test", Passage: "Humans exhale carbon dioxide."}}
	res, err := SolveLocal(&q, ev, llmConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "Carbon dioxide" || !res.LowConfidence || !res.SearchUsed {
		t.Fatalf("unexpected result %+v", res)
	}
	if !strings.Contains((*last)[1].Content, "Humans exhale carbon dioxide.") {
		t.Fatal("evidence was not sent to the model")
	}
}

func TestLLMBackendHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
	}))
	defer srv.Close()
	q := capitalQuestion
	_, err := SolveLocal(&q, nil, llmConfig(srv.URL))
	if err == nil || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Fatalf("expected provider error message, got %v", err)
	}
}

func TestLocalBackendSelection(t *testing.T) {
	text := extractor.Question{Type: extractor.TypeText, Text: "Explain photosynthesis."}

	noKey := Config{Engine: EngineAuto, LLM: llm.Config{BaseURL: llm.DefaultBaseURL}}
	if _, ok := LocalBackend(noKey).(*layaBackend); !ok {
		t.Fatal("auto without a key should use Laya")
	}
	if LocalCanAnswer(&text, noKey) {
		t.Fatal("Laya must not claim it can write free text")
	}

	withKey := Config{Engine: EngineAuto, LLM: llm.Config{BaseURL: llm.DefaultBaseURL, APIKey: "k"}}
	if _, ok := LocalBackend(withKey).(*llmBackend); !ok {
		t.Fatal("auto with a key should use the LLM")
	}
	if !LocalCanAnswer(&text, withKey) {
		t.Fatal("LLM backend should answer free text")
	}

	forced := Config{Engine: EngineLaya, LLM: withKey.LLM}
	if _, ok := LocalBackend(forced).(*layaBackend); !ok {
		t.Fatal("explicit laya engine should win over a configured key")
	}
}

func TestClassifierUsesLLMForAmbiguousFields(t *testing.T) {
	srv, _ := fakeLLM(t, `{"label":"N"}`)
	q := extractor.Question{Type: extractor.TypeText, Text: "Section two covers the remaining topics in detail"}
	c, err := ClassifyQuestion(&q, llmConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if c.Disposition != DispositionNonQuestion || c.Backend != "llm" {
		t.Fatalf("got %+v", c)
	}
}
