package solver

import (
	"context"
	"fmt"
	"strings"

	"mimir/internal/extractor"
	"mimir/internal/llm"
)

// llmMaxConfidence caps self-reported LLM confidence, which is not calibrated.
const llmMaxConfidence = 92

// llmBackend answers via an OpenAI-compatible chat API (Groq by default).
// Unlike Laya it can write short and paragraph answers.
type llmBackend struct {
	client *llm.Client
	cfg    llm.Config
}

func newLLMBackend(cfg Config) *llmBackend {
	return &llmBackend{client: llm.New(cfg.LLM), cfg: cfg.LLM}
}

func (b *llmBackend) Name() string { return "llm:" + b.client.Model() }

func (b *llmBackend) CanAnswer(q *extractor.Question) bool {
	if q.Type == extractor.TypeDropdown && len(q.Choices) == 0 {
		return false // nothing to choose from
	}
	return strings.TrimSpace(q.Text) != ""
}

type llmAnswer struct {
	Values     []string `json:"values"`
	Answer     string   `json:"answer"`
	Confidence int      `json:"confidence"`
	Reason     string   `json:"reason"`
}

const llmSystemPrompt = `You answer objective quiz and form questions.
Reply with a single JSON object and nothing else:
{"values": [...], "answer": "...", "confidence": 0-100, "reason": "one short sentence"}

Rules:
- Multiple choice: "values" holds exactly one option label (e.g. ["B"]).
- Checkbox (select all that apply): "values" holds every correct label.
- Free text: put the answer in "answer" and leave "values" empty. Keep short answers to a few words; paragraph answers to 2-4 sentences.
- If evidence is provided, rely on it. If the evidence does not support an answer, give your best answer with confidence below 50.
- If you are unsure, say so with a low confidence. Never invent options.`

func (b *llmBackend) Answer(ctx context.Context, q *extractor.Question, evidence []Evidence) (*Result, error) {
	if !b.cfg.Configured() {
		return nil, fmt.Errorf("llm backend not configured: set GROQ_API_KEY or MIMIR_LLM_API_KEY")
	}
	var reply llmAnswer
	err := b.client.ChatJSON(ctx, []llm.Message{
		{Role: "system", Content: llmSystemPrompt},
		{Role: "user", Content: buildLLMPrompt(q, evidence)},
	}, &reply)
	if err != nil {
		return nil, fmt.Errorf("llm answer: %w", err)
	}

	values, err := validateLLMValues(q, reply)
	if err != nil {
		return nil, err
	}
	confidence := reply.Confidence
	if confidence > llmMaxConfidence {
		confidence = llmMaxConfidence
	}
	reason := strings.TrimSpace(reply.Reason)
	if reason == "" {
		reason = "LLM answer"
	}
	return &Result{
		Values:     values,
		Confidence: clampConfidence(confidence),
		Reason:     fmt.Sprintf("%s — %s", b.client.Model(), reason),
		SearchUsed: len(evidence) > 0,
		Backend:    "llm",
		Evidence:   evidence,
	}, nil
}

func buildLLMPrompt(q *extractor.Question, evidence []Evidence) string {
	var sb strings.Builder
	kind := "multiple choice (pick one)"
	switch {
	case q.Type == extractor.TypeCheckbox:
		kind = "checkbox (select all that apply)"
	case q.Type == extractor.TypeParagraph:
		kind = "paragraph answer"
	case isFreeText(q):
		kind = "short answer"
	}
	fmt.Fprintf(&sb, "Question type: %s\n", kind)
	if ctx := strings.TrimSpace(q.Context); ctx != "" && ctx != strings.TrimSpace(q.Text) {
		fmt.Fprintf(&sb, "Context: %s\n", truncateText(ctx, 600))
	}
	if q.Row != "" {
		fmt.Fprintf(&sb, "Row: %s\n", q.Row)
	}
	fmt.Fprintf(&sb, "Question: %s\n", q.Text)
	if len(q.Choices) > 0 {
		sb.WriteString("Options:\n")
		for _, c := range q.Choices {
			fmt.Fprintf(&sb, "  %s. %s\n", c.Label, c.Text)
		}
	}
	if len(evidence) > 0 {
		sb.WriteString("\nEvidence from web sources:\n")
		sb.WriteString(truncateText(evidenceText(evidence), 6000))
	}
	return sb.String()
}

func validateLLMValues(q *extractor.Question, reply llmAnswer) ([]string, error) {
	if isFreeText(q) {
		answer := strings.TrimSpace(reply.Answer)
		if answer == "" && len(reply.Values) > 0 {
			answer = strings.TrimSpace(reply.Values[0])
		}
		if answer == "" {
			return nil, fmt.Errorf("llm returned an empty answer")
		}
		if answerEchoesQuestion(answer, q.Text) {
			return nil, fmt.Errorf("llm repeated the question instead of answering it")
		}
		return []string{answer}, nil
	}

	values := make([]string, 0, len(reply.Values))
	seen := map[string]bool{}
	for _, raw := range reply.Values {
		label := strings.ToUpper(strings.Trim(strings.TrimSpace(raw), ".)"))
		if !hasChoiceLabel(q, label) {
			return nil, fmt.Errorf("llm returned unknown option %q", raw)
		}
		if !seen[label] {
			seen[label] = true
			values = append(values, label)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("llm selected no option")
	}
	if q.Type != extractor.TypeCheckbox && len(values) > 1 {
		return nil, fmt.Errorf("llm selected %d options for a single-choice question", len(values))
	}
	return values, nil
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
