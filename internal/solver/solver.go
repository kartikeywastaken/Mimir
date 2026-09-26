package solver

import (
	"fmt"
	"regexp"
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

	if len(q.Choices) == 0 {
		return solveTextQuestion(q, searchSnippets)
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

func solveTextQuestion(q *extractor.Question, searchSnippets []string) (*Result, error) {
	if len(searchSnippets) > 0 {
		ans := extractAnswerFromSnippets(q.Text, searchSnippets)
		return &Result{
			Answer:     ans,
			Confidence: 88,
			Reason:     "Extracted answer from background Google search snippet",
			SearchUsed: true,
			Backend:    "search-extract",
		}, nil
	}

	return &Result{
		Answer:     "Answer for " + q.Text,
		Confidence: 50,
		Reason:     "Text input question (no background search snippets)",
		SearchUsed: false,
		Backend:    "mock",
	}, nil
}

func extractAnswerFromSnippets(query string, snippets []string) string {
	if len(snippets) == 0 {
		return ""
	}

	qLower := strings.ToLower(query)

	// Clean and normalize all snippets (strip date stamps, featured prefixes, URLs)
	var cleanedSnippets []string
	dateRe := regexp.MustCompile(`^(?:[A-Za-z]+\s+\d{1,2},?\s+\d{4}|\d{1,2}\s+[A-Za-z]+\s+\d{4}|\d+\s+(?:days?|hours?|mins?)\s+ago)\s*[·•-]\s*`)
	urlRe := regexp.MustCompile(`^https?://\S+\s+[›·-]\s*`)

	for _, raw := range snippets {
		s := strings.TrimPrefix(raw, "FEATURED: ")
		parts := strings.Split(s, " — ")
		if len(parts) > 1 {
			s = parts[1]
		}
		s = dateRe.ReplaceAllString(s, "")
		s = urlRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if len(s) > 0 {
			cleanedSnippets = append(cleanedSnippets, s)
		}
	}

	if len(cleanedSnippets) == 0 {
		return ""
	}

	joined := strings.Join(cleanedSnippets, " ")

	// 1. Long-form / Explanation questions ("why", "explain", "describe", "few sentences", "discuss")
	if strings.Contains(qLower, "explain") || strings.Contains(qLower, "few sentences") || strings.HasPrefix(qLower, "why ") || strings.Contains(qLower, "describe") {
		first := cleanedSnippets[0]
		sentences := strings.Split(first, ". ")
		if len(sentences) >= 2 {
			res := strings.TrimSpace(sentences[0]) + ". " + strings.TrimSpace(sentences[1]) + "."
			if len(res) <= 250 {
				return res
			}
		}
		if len(first) > 220 {
			return first[:220] + "."
		}
		return first
	}

	// 2. Quantity / Numeric questions ("how many", "how much", "what year", "in what year", "number of")
	if strings.Contains(qLower, "how many") || strings.Contains(qLower, "how much") || strings.Contains(qLower, "what year") || strings.Contains(qLower, "in what year") || strings.Contains(qLower, "number of") {
		unitRe := regexp.MustCompile(`how many\s+([a-zA-Z]+)`)
		if m := unitRe.FindStringSubmatch(qLower); len(m) > 1 {
			unit := m[1]
			pattern := fmt.Sprintf(`(?i)(?:less\s+than\s+|over\s+|about\s+|approximately\s+)?(\d+(?:\.\d+)?)\s*%s`, regexp.QuoteMeta(unit))
			re := regexp.MustCompile(pattern)
			if numMatch := re.FindStringSubmatch(joined); len(numMatch) > 1 {
				return numMatch[1]
			}
		}
		if strings.Contains(qLower, "year") {
			yearRe := regexp.MustCompile(`\b(1[0-9]{3}|20[0-9]{2})\b`)
			if ym := yearRe.FindString(joined); ym != "" {
				return ym
			}
		}
		numRe := regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
		if nm := numRe.FindString(cleanedSnippets[0]); nm != "" {
			return nm
		}
	}

	// 3. Name / Surname / Origin questions ("what was ... name", "real name", "original name", "surname", "called")
	if strings.Contains(qLower, "name") || strings.Contains(qLower, "surname") || strings.Contains(qLower, "called") {
		namePatterns := []*regexp.Regexp{
			regexp.MustCompile(`(?i)(?:real|original|birth)\s+name\s+(?:is|was)\s+["']?([A-Z][a-zA-Z0-9\-_]+(?:\s+[A-Z][a-zA-Z0-9\-_]+)*)["']?`),
			regexp.MustCompile(`(?i)(?:surname|name)\s+(?:was|is)\s+["']?([A-Z][a-zA-Z0-9\-_]+(?:\s+[A-Z][a-zA-Z0-9\-_]+)*)["']?`),
			regexp.MustCompile(`(?i)(?:originally|initially)\s+["']?([A-Z][a-zA-Z0-9\-_]+(?:\s+[A-Z][a-zA-Z0-9\-_]+)*)["']?`),
			regexp.MustCompile(`(?i)(?:given the name|born as|known as)\s+["']?([A-Z][a-zA-Z0-9\-_]+(?:\s+[A-Z][a-zA-Z0-9\-_]+)*)["']?`),
		}
		for _, pat := range namePatterns {
			if m := pat.FindStringSubmatch(joined); len(m) > 1 {
				ans := strings.Trim(m[1], `"'.,`)
				if len(ans) > 1 && len(ans) < 50 {
					return ans
				}
			}
		}
	}

	// 4. "Who" / Agent questions ("who does", "who was", "who is", "who discovered", "who wrote", "who sent")
	if strings.HasPrefix(qLower, "who ") || strings.Contains(qLower, " who ") || strings.Contains(qLower, "whom ") {
		byRe := regexp.MustCompile(`(?i)(?:by|via|through|from)\s+([A-Z0-9][a-zA-Z0-9\-_]+(?:\s+[A-Z0-9][a-zA-Z0-9\-_]+)*)`)
		if m := byRe.FindStringSubmatch(joined); len(m) > 1 {
			candidate := strings.Trim(m[1], `"'.,`)
			if !strings.EqualFold(candidate, "the") && !strings.EqualFold(candidate, "this") && len(candidate) > 1 && len(candidate) < 40 {
				return candidate
			}
		}
	}

	// 5. General Short-Answer fallback:
	// Extract the first clean sentence or clause from the top snippet
	first := cleanedSnippets[0]
	sentences := strings.Split(first, ". ")
	if len(sentences) > 0 && len(sentences[0]) > 0 {
		res := strings.TrimSpace(sentences[0])
		if len(res) > 80 {
			for _, sep := range []string{" is ", " was ", " — ", ", "} {
				if idx := strings.Index(res, sep); idx > 0 && idx < 60 {
					remainder := strings.TrimSpace(res[idx+len(sep):])
					if len(remainder) > 0 && len(remainder) <= 60 {
						return strings.Trim(remainder, `"'.,`)
					}
				}
			}
			res = res[:80]
		}
		return strings.Trim(res, `"'.,`)
	}

	return first
}


