package solver

import (
	"fmt"
	"strings"

	"mimir/internal/extractor"
	"mimir/internal/laya"
)

// Result from Laya (local, no API key)
type Result struct {
	Answer     string `json:"answer"`     // e.g. "B"
	Confidence int    `json:"confidence"` // 0-100
	Reason     string `json:"reason"`
	SearchUsed bool   `json:"search_used"`
	Backend    string `json:"backend"` // laya-coreml / laya-mlx / mock
	RawChoice  string `json:"raw_choice,omitempty"`
}

type Config struct {
	Model        string
	Instructions string
	Backend      string
}

func ConfigFromEnv() Config {
	// LAYA_MODEL env — same as laya-browser and laya-coreml
	// Default: aac6fef/laya-multilingual-coreml-ane (96 tokens, ~5ms ANE)
	// For 1024 tokens: aac6fef/laya-multilingual-coreml or aac6fef/laya-mlx
	return Config{
		Model:        "",
		Instructions: "Which answer correctly answers the question?",
	}
}

// Solve via local Laya typed decision (no OpenAI key).
// state = question + search snippets, criteria = choices
// This mirrors layaForWeb's choice type but runs natively via laya-coreml/mlx.
// See: https://vishalmysore.github.io/layaForWeb/ (ONNX WASM) vs laya-coreml (CoreML) vs laya-mlx (laya-browser)
func Solve(q *extractor.Question, searchSnippets []string, cfg Config) (*Result, error) {
	client := laya.New()
	if cfg.Model != "" {
		client.Model = cfg.Model
	}

	// Build state: question + background research (hidden tab, like recursive Von trick)
	state := q.Text
	if len(searchSnippets) > 0 {
		state += "\n\nBackground research (hidden tab):\n" + strings.Join(searchSnippets, "\n---\n")
	}
	// Truncate for ANE 96-token limit vs 1024 for general
	// laya client does its own truncation, but we also keep it short
	if len(state) > 2000 {
		state = state[:2000] + "..."
	}

	choices := make([]laya.Choice, len(q.Choices))
	for i, c := range q.Choices {
		choices[i] = laya.Choice{Label: c.Label, Text: c.Text}
	}

	resp, err := client.Predict(state, q.Text, choices, cfg.Instructions)
	if err != nil {
		// Fallback to mock if Laya not installed or failed — still shows overlay, never blocks
		return mockSolve(q, searchSnippets, fmt.Sprintf("laya error: %v", err)), nil
	}

	// Laya confidence is 0-1, convert to 0-100
	conf := int(resp.Confidence*100 + 0.5)
	if conf == 0 {
		conf = 62
	}
	// Clamp
	if conf > 98 {
		conf = 98
	}
	if conf < 10 {
		conf = 10
	}

	reason := fmt.Sprintf("Laya %s (%.0f%%) — typed decision over %d options. %s",
		resp.Backend, resp.Confidence*100, len(choices),
		map[bool]string{true: "with background search", false: "no search"}[len(searchSnippets) > 0],
	)
	if resp.Mock {
		reason = "Mock (no laya model installed) — install laya-coreml or laya-mlx for real local decisions."
	}

	return &Result{
		Answer:     resp.Answer,
		Confidence: conf,
		Reason:     reason,
		SearchUsed: len(searchSnippets) > 0,
		Backend:    resp.Backend,
		RawChoice:  resp.Choice,
	}, nil
}

func mockSolve(q *extractor.Question, snippets []string, why string) *Result {
	ans := "A"
	if len(q.Choices) >= 2 {
		ans = "B"
		if strings.Contains(strings.ToLower(q.Text), "not") && len(q.Choices) > 0 {
			max := 0
			for _, c := range q.Choices {
				if len(c.Text) > max {
					max = len(c.Text)
					ans = c.Label
				}
			}
		}
	}
	reason := "Mock — no laya model. " + why
	if len(snippets) > 0 {
		reason = "Mock with background snippets: " + snippets[0][:min(120, len(snippets[0]))]
	}
	return &Result{Answer: ans, Confidence: 62, Reason: reason, SearchUsed: len(snippets) > 0, Backend: "mock"}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
