package solver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mimir/internal/extractor"
	"mimir/internal/laya"
	"mimir/internal/llm"
)

type QuestionDisposition string

const (
	DispositionObjective   QuestionDisposition = "objective"
	DispositionRespondent  QuestionDisposition = "respondent-owned"
	DispositionNonQuestion QuestionDisposition = "non-question"
)

type Classification struct {
	Disposition QuestionDisposition
	Reason      string
	Backend     string
}

var respondentDataPattern = regexp.MustCompile(`(?i)\b(your|you are|respondent|participant|full name|first name|last name|e-?mail|phone|address|date of birth|birth date|student id|employee id|username|password|roll number)\b`)
var objectiveLeadPattern = regexp.MustCompile(`(?i)^\s*(who|what|when|where|why|how|which|select|choose|identify|calculate|solve|explain|describe|determine|find|list|match|given)\b`)
var objectiveVerbPattern = regexp.MustCompile(`(?i)\b(calculate|solve|explain|identify|determine|find|select all|choose all|match)\b`)

// ClassifyQuestion prevents respondent-owned fields from ever reaching search
// or answer generation. The deterministic layer handles clear cases; ambiguous
// prompts use Laya as a semantic classifier and fail closed when it is absent.
func ClassifyQuestion(q *extractor.Question, cfg Config) (Classification, error) {
	text := strings.TrimSpace(q.Text)
	context := strings.TrimSpace(q.Context)
	combined := strings.TrimSpace(text + " " + context)
	lower := strings.ToLower(combined)

	if respondentDataPattern.MatchString(combined) {
		return Classification{Disposition: DispositionRespondent, Reason: "asks for respondent-provided information", Backend: "safety-rules"}, nil
	}
	if (strings.Contains(text, "?") || strings.Contains(context, "?")) && !strings.Contains(lower, " your ") {
		return Classification{Disposition: DispositionObjective, Reason: "objective interrogative", Backend: "safety-rules"}, nil
	}
	if (objectiveLeadPattern.MatchString(text) || objectiveVerbPattern.MatchString(text)) && !strings.Contains(lower, " your ") {
		return Classification{Disposition: DispositionObjective, Reason: "objective task instruction", Backend: "safety-rules"}, nil
	}

	// Short label-shaped prompts are overwhelmingly profile, routing, or form
	// metadata. They remain untouched unless surrounding context establishes an
	// objective question.
	if len(strings.Fields(text)) <= 6 && (strings.HasSuffix(text, ":") || context == text) {
		return Classification{Disposition: DispositionRespondent, Reason: "label-shaped field without an objective question", Backend: "safety-rules"}, nil
	}

	if b, ok := LocalBackend(cfg).(*llmBackend); ok {
		return b.classify(combined)
	}

	client := laya.New()
	if cfg.Model != "" {
		client.Model = cfg.Model
	}
	choices := []laya.Choice{
		{Label: "O", Text: "Objective knowledge or reasoning question with an answer derivable from facts or supplied context"},
		{Label: "R", Text: "Respondent-owned information, identity, contact detail, preference, experience, or personal selection"},
		{Label: "N", Text: "Instruction, section heading, decoration, or non-question content"},
	}
	resp, err := client.Predict(combined, text, choices, "Classify the form field. Protect respondent-owned data: choose Objective only when an external solver can answer it without knowing the respondent.")
	if err != nil {
		return Classification{Disposition: DispositionRespondent, Reason: "ambiguous field skipped because semantic classification was unavailable", Backend: "fail-closed"}, fmt.Errorf("classify field: %w", err)
	}
	if resp.Mock {
		return Classification{Disposition: DispositionRespondent, Reason: "ambiguous field skipped because classifier was not real", Backend: "fail-closed"}, fmt.Errorf("classifier returned a mock decision")
	}
	switch resp.Answer {
	case "O":
		return Classification{Disposition: DispositionObjective, Reason: "Laya classified it as objective", Backend: resp.Backend}, nil
	case "R":
		return Classification{Disposition: DispositionRespondent, Reason: "Laya classified it as respondent-owned", Backend: resp.Backend}, nil
	case "N":
		return Classification{Disposition: DispositionNonQuestion, Reason: "Laya classified it as non-question content", Backend: resp.Backend}, nil
	default:
		return Classification{Disposition: DispositionRespondent, Reason: "ambiguous classifier response; skipped safely", Backend: "fail-closed"}, fmt.Errorf("classifier returned unknown label %q", resp.Answer)
	}
}

const llmClassifyPrompt = `Classify a form field. Reply with JSON {"label": "O"|"R"|"N"}.
O = objective knowledge or reasoning question answerable from facts or supplied context.
R = respondent-owned: identity, contact details, preferences, experience, opinions, or personal choices.
N = instruction, section heading, decoration, or other non-question content.
Protect respondent data: choose O only when someone other than the respondent could answer it.`

func (b *llmBackend) classify(field string) (Classification, error) {
	failClosed := Classification{Disposition: DispositionRespondent, Reason: "ambiguous field skipped because semantic classification was unavailable", Backend: "fail-closed"}
	if !b.cfg.Configured() {
		return failClosed, fmt.Errorf("llm classifier not configured")
	}
	var reply struct {
		Label string `json:"label"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := b.client.ChatJSON(ctx, []llm.Message{
		{Role: "system", Content: llmClassifyPrompt},
		{Role: "user", Content: field},
	}, &reply); err != nil {
		return failClosed, fmt.Errorf("classify field: %w", err)
	}
	switch strings.ToUpper(strings.TrimSpace(reply.Label)) {
	case "O":
		return Classification{Disposition: DispositionObjective, Reason: "LLM classified it as objective", Backend: "llm"}, nil
	case "R":
		return Classification{Disposition: DispositionRespondent, Reason: "LLM classified it as respondent-owned", Backend: "llm"}, nil
	case "N":
		return Classification{Disposition: DispositionNonQuestion, Reason: "LLM classified it as non-question content", Backend: "llm"}, nil
	default:
		failClosed.Reason = "ambiguous classifier response; skipped safely"
		return failClosed, fmt.Errorf("classifier returned unknown label %q", reply.Label)
	}
}
