package extractor

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

type QuestionType string

const (
	TypeChoice    QuestionType = "choice"
	TypeCheckbox  QuestionType = "checkbox"
	TypeText      QuestionType = "text"
	TypeParagraph QuestionType = "paragraph"
	TypeDropdown  QuestionType = "dropdown"
)

// Question represents a single question pulled from the visible tab
type Question struct {
	Index         int          `json:"index"`
	TargetID      string       `json:"target_id,omitempty"`
	Type          QuestionType `json:"type,omitempty"` // "choice", "text", "dropdown"
	Text          string       `json:"text"`
	Row           string       `json:"row,omitempty"`
	Context       string       `json:"context,omitempty"`
	Choices       []Choice     `json:"choices,omitempty"`
	RawHTML       string       `json:"raw_html,omitempty"`
	SolvedAns     string       `json:"solved_ans,omitempty"`
	SolvedValues  []string     `json:"solved_values,omitempty"`
	Confidence    int          `json:"confidence,omitempty"`
	Marked        bool         `json:"marked,omitempty"`
	LowConfidence bool         `json:"low_confidence,omitempty"`
	SolveError    string       `json:"solve_error,omitempty"`
	Skipped       bool         `json:"skipped,omitempty"`
	SkipReason    string       `json:"skip_reason,omitempty"`
}

type Choice struct {
	Label string `json:"label"` // A, B, C, D
	Text  string `json:"text"`
}

type BatchResult struct {
	Questions       []Question `json:"questions"`
	IsLogin         bool       `json:"is_login"`
	EmbeddedQuizURL string     `json:"embedded_quiz_url,omitempty"`
}

// ExtractAllJS detects all questions on the page, groups choices, and stamps
// data-mimir attributes for zero-popup, high-precision DOM marking.
//
//go:embed extract.js
var ExtractAllJS string

// Legacy single-question script kept for backward compatibility
var ExtractJS = ExtractAllJS

// MarkAnswerJS is the legacy single-choice entry point. New callers should
// provide the extracted question to FillAnswerJS for type-aware verification.
func MarkAnswerJS(qIndex int, answer string) string {
	return FillAnswerJS(Question{Index: qIndex, TargetID: fmt.Sprintf("mimir-q-%d", qIndex), Type: TypeChoice}, []string{answer})
}

// ParseBatchResult parses the JSON string returned by ExtractAllJS
func ParseBatchResult(jsonStr string) (*BatchResult, error) {
	var inner string
	if err := json.Unmarshal([]byte(jsonStr), &inner); err == nil {
		jsonStr = inner
	}

	var batch BatchResult
	if err := json.Unmarshal([]byte(jsonStr), &batch); err != nil {
		return nil, fmt.Errorf("parse batch extractor result: %w | raw: %s", err, jsonStr)
	}

	for i := range batch.Questions {
		batch.Questions[i].Index = i
		if batch.Questions[i].TargetID == "" {
			batch.Questions[i].TargetID = fmt.Sprintf("mimir-q-%d", i)
		}
		if batch.Questions[i].Type == "" {
			if len(batch.Questions[i].Choices) > 0 {
				batch.Questions[i].Type = TypeChoice
			} else {
				batch.Questions[i].Type = TypeText
			}
		}
		for j := range batch.Questions[i].Choices {
			if batch.Questions[i].Choices[j].Label == "" {
				batch.Questions[i].Choices[j].Label = string(rune('A' + j))
			}
			batch.Questions[i].Choices[j].Label = strings.ToUpper(strings.TrimSpace(batch.Questions[i].Choices[j].Label))
		}
	}
	return &batch, nil
}

// ParseResult maintains compatibility with single-question callers
func ParseResult(jsonStr string) (*Question, error) {
	batch, err := ParseBatchResult(jsonStr)
	if err == nil && len(batch.Questions) > 0 {
		return &batch.Questions[0], nil
	}

	var inner string
	if err := json.Unmarshal([]byte(jsonStr), &inner); err == nil {
		jsonStr = inner
	}
	var q Question
	if err := json.Unmarshal([]byte(jsonStr), &q); err != nil {
		return nil, fmt.Errorf("parse extractor result: %w | raw: %s", err, jsonStr)
	}
	for i := range q.Choices {
		if q.Choices[i].Label == "" {
			q.Choices[i].Label = string(rune('A' + i))
		}
		q.Choices[i].Label = strings.ToUpper(strings.TrimSpace(q.Choices[i].Label))
	}
	return &q, nil
}
