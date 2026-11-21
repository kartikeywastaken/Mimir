package solver

import (
	"context"
	"fmt"
	"strings"

	"mimir/internal/extractor"
	"mimir/internal/laya"
)

// layaBackend runs the on-device Laya typed-decision model (Core ML / MLX).
// It can choose among options but cannot generate free text.
type layaBackend struct {
	client       *laya.Client
	instructions string
}

func newLayaBackend(cfg Config) *layaBackend {
	client := laya.New()
	if cfg.Model != "" {
		client.Model = cfg.Model
	}
	return &layaBackend{client: client, instructions: cfg.Instructions}
}

func (b *layaBackend) Name() string { return "laya" }

func (b *layaBackend) CanAnswer(q *extractor.Question) bool { return len(q.Choices) > 0 }

func (b *layaBackend) Answer(_ context.Context, q *extractor.Question, evidence []Evidence) (*Result, error) {
	client := *b.client
	if len(evidence) > 0 && strings.HasSuffix(client.Model, "-ane") {
		// The 96-token ANE variant would discard most research context.
		client.Model = strings.TrimSuffix(client.Model, "-ane")
	}

	state := q.Text
	if len(evidence) > 0 {
		state += "\n\nBackground research (isolated browser):\n" + evidenceText(evidence)
	}
	if len(state) > 2000 {
		state = state[:2000] + "..."
	}

	if q.Type == extractor.TypeCheckbox {
		return b.answerCheckbox(&client, q, state, evidence)
	}

	choices := make([]laya.Choice, len(q.Choices))
	for i, c := range q.Choices {
		choices[i] = laya.Choice{Label: c.Label, Text: c.Text}
	}
	resp, err := client.Predict(state, q.Text, choices, b.instructions)
	if err != nil {
		return nil, fmt.Errorf("laya decision: %w", err)
	}
	if resp.Mock || resp.Answer == "" {
		return nil, fmt.Errorf("laya returned no real answer")
	}
	if !hasChoiceLabel(q, resp.Answer) {
		return nil, fmt.Errorf("laya returned unknown choice %q", resp.Answer)
	}

	grounding := "without web evidence"
	if len(evidence) > 0 {
		grounding = "with isolated-browser evidence"
	}
	return &Result{
		Values:     []string{resp.Answer},
		Confidence: clampConfidence(int(resp.Confidence*100 + 0.5)),
		Reason:     fmt.Sprintf("Laya %s (%.0f%%) — typed decision over %d options %s", resp.Backend, resp.Confidence*100, len(choices), grounding),
		SearchUsed: len(evidence) > 0,
		Backend:    resp.Backend,
		RawChoice:  resp.Choice,
		Evidence:   evidence,
	}, nil
}

// answerCheckbox evaluates each option independently as a yes/no decision.
func (b *layaBackend) answerCheckbox(client *laya.Client, q *extractor.Question, state string, evidence []Evidence) (*Result, error) {
	yesNo := []laya.Choice{{Label: "Y", Text: "Yes, select this option"}, {Label: "N", Text: "No, do not select this option"}}
	selected := make([]string, 0, len(q.Choices))
	confidence := 100
	for _, option := range q.Choices {
		question := fmt.Sprintf("For %q, should %q be selected?", q.Text, option.Text)
		resp, err := client.Predict(state, question, yesNo, "Decide whether this individual option is correct. Select Yes only when supported.")
		if err != nil {
			return nil, fmt.Errorf("laya checkbox decision for %s failed: %w", option.Label, err)
		}
		if resp.Mock || (resp.Answer != "Y" && resp.Answer != "N") {
			return nil, fmt.Errorf("laya checkbox decision for %s was invalid", option.Label)
		}
		if resp.Answer == "Y" {
			selected = append(selected, option.Label)
		}
		if c := int(resp.Confidence*100 + 0.5); c < confidence {
			confidence = c
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("laya selected no checkbox options")
	}
	return &Result{Values: selected, Confidence: confidence, Reason: "Laya independently evaluated each checkbox option", SearchUsed: len(evidence) > 0, Backend: "laya", Evidence: evidence}, nil
}

func hasChoiceLabel(q *extractor.Question, label string) bool {
	for _, choice := range q.Choices {
		if choice.Label == label {
			return true
		}
	}
	return false
}
