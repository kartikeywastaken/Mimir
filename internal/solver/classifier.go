package solver

import (
	"fmt"
	"regexp"
	"strings"

	"mimir/internal/extractor"
	"mimir/internal/laya"
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
