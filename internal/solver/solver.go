// Package solver turns extracted questions into answers. Answers come from a
// pluggable local Backend (Laya or an LLM), from isolated-browser web
// evidence, or from both reconciled together (Hybrid).
package solver

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"mimir/internal/extractor"
	"mimir/internal/llm"
)

// LowConfidenceThreshold is the minimum confidence for automatic filling.
const LowConfidenceThreshold = 70

// Result is a structured answer from any backend.
type Result struct {
	Values        []string   `json:"values"`
	Answer        string     `json:"answer,omitempty"` // compatibility: first value
	Confidence    int        `json:"confidence"`
	Reason        string     `json:"reason"`
	SearchUsed    bool       `json:"search_used"`
	Backend       string     `json:"backend"`
	RawChoice     string     `json:"raw_choice,omitempty"`
	Evidence      []Evidence `json:"evidence,omitempty"`
	FallbackPath  string     `json:"fallback_path,omitempty"`
	LowConfidence bool       `json:"low_confidence,omitempty"`
}

type Evidence struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Passage string `json:"passage"`
}

// Engine selects which local backend answers questions.
type Engine string

const (
	EngineAuto Engine = "auto" // LLM when configured, otherwise Laya
	EngineLaya Engine = "laya"
	EngineLLM  Engine = "llm"
)

type Config struct {
	Model        string // Laya model override
	Instructions string // Laya decision instructions
	Engine       Engine
	LLM          llm.Config
}

// ConfigFromEnv builds a Config from MIMIR_ENGINE, LAYA_MODEL and the
// MIMIR_LLM_* / GROQ_API_KEY variables.
func ConfigFromEnv() Config {
	engine := Engine(strings.ToLower(strings.TrimSpace(os.Getenv("MIMIR_ENGINE"))))
	switch engine {
	case EngineLaya, EngineLLM:
	default:
		engine = EngineAuto
	}
	return Config{
		Model:        os.Getenv("LAYA_MODEL"),
		Instructions: "Which answer correctly answers the question?",
		Engine:       engine,
		LLM:          llm.ConfigFromEnv(),
	}
}

// SolveLocal answers with the configured local backend. Evidence is optional
// and is supplied by Hybrid mode. It never returns a mock or default answer.
func SolveLocal(q *extractor.Question, evidence []Evidence, cfg Config) (*Result, error) {
	return SolveLocalContext(context.Background(), q, evidence, cfg)
}

// SolveLocalContext is SolveLocal with cancellation.
func SolveLocalContext(ctx context.Context, q *extractor.Question, evidence []Evidence, cfg Config) (*Result, error) {
	backend := LocalBackend(cfg)
	if !backend.CanAnswer(q) {
		return nil, fmt.Errorf("%s cannot answer %s questions", backend.Name(), q.Type)
	}
	result, err := backend.Answer(ctx, q, evidence)
	if err != nil {
		return nil, err
	}
	return normalizeResult(result), nil
}

// LocalCanAnswer reports whether the configured local backend handles q.
func LocalCanAnswer(q *extractor.Question, cfg Config) bool {
	return LocalBackend(cfg).CanAnswer(q)
}

func isFreeText(q *extractor.Question) bool {
	return q.Type == extractor.TypeText || q.Type == extractor.TypeParagraph || len(q.Choices) == 0
}

func clampConfidence(c int) int {
	if c > 98 {
		return 98
	}
	if c < 1 {
		return 1
	}
	return c
}

// SolveWeb derives an answer only from real isolated-browser evidence.
func SolveWeb(q *extractor.Question, evidence []Evidence) (*Result, error) {
	if len(evidence) == 0 {
		return nil, fmt.Errorf("no web evidence found")
	}
	passages := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if strings.TrimSpace(item.Passage) != "" {
			passages = append(passages, item.Passage)
		}
	}
	if len(passages) == 0 {
		return nil, fmt.Errorf("web evidence was empty")
	}

	if len(q.Choices) == 0 {
		relevant := RelevantEvidence(q, evidence)
		passages = passages[:0]
		for _, item := range relevant {
			passages = append(passages, item.Passage)
		}
		if len(passages) == 0 {
			return nil, fmt.Errorf("no evidence addresses the text question")
		}
		evidence = relevant
		answer := strings.TrimSpace(extractAnswerFromSnippets(q.Text, passages))
		if answer == "" {
			return nil, fmt.Errorf("could not extract a text answer from web evidence")
		}
		if answerEchoesQuestion(answer, q.Text) {
			return nil, fmt.Errorf("web candidate repeated the question instead of answering it")
		}
		support := make(map[string]bool)
		answerLower := strings.ToLower(strings.ReplaceAll(answer, ",", ""))
		for _, item := range evidence {
			if strings.Contains(strings.ToLower(strings.ReplaceAll(item.Passage, ",", "")), answerLower) && !negativePattern.MatchString(item.Passage) {
				support[sourceKey(item)] = true
			}
		}
		delete(support, "unattributed")
		confidence := 65
		if len(support) >= 2 {
			confidence = 86
		}
		return normalizeResult(&Result{Values: []string{answer}, Confidence: confidence, Reason: "Answer extracted from isolated-browser evidence", SearchUsed: true, Backend: "web-evidence", Evidence: evidence}), nil
	}

	return solveOptionsFromEvidence(q, evidence)
}

func normalizeResult(result *Result) *Result {
	if len(result.Values) > 0 {
		result.Answer = result.Values[0]
	}
	result.LowConfidence = result.Confidence < LowConfidenceThreshold
	return result
}

func evidenceText(evidence []Evidence) string {
	parts := make([]string, 0, len(evidence))
	for _, item := range evidence {
		parts = append(parts, strings.TrimSpace(item.Title+" — "+item.Passage))
	}
	return strings.Join(parts, "\n---\n")
}

func answerEchoesQuestion(answer, question string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(value)
		value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, " ")
		return strings.TrimSpace(value)
	}
	a := normalize(answer)
	q := normalize(question)
	if a == "" || q == "" {
		return true
	}
	return a == q || (len(a) > 20 && strings.Contains(a, q))
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
	if strings.Contains(qLower, "term") || strings.Contains(qLower, "study") {
		re := regexp.MustCompile(`(?i)([a-z][a-z -]{1,40}) is (?:the )?(?:scientific |biological )?study of`)
		if match := re.FindStringSubmatch(joined); len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
	}
	if strings.Contains(qLower, "gas") {
		patterns := []string{`(?i)([a-z]+(?: [a-z]+)?) is (?:the |a )?(?:primary |main )?(?:waste |byproduct)`, `(?i)(?:exhale|exhaled gas is|breathe out) (?:mainly |primarily )?([a-z]+(?: dioxide| monoxide| gas)?)`}
		for _, pattern := range patterns {
			if match := regexp.MustCompile(pattern).FindStringSubmatch(joined); len(match) > 1 {
				return strings.TrimSpace(match[1])
			}
		}
	}
	if strings.Contains(qLower, "meters per second") || strings.Contains(qLower, "metres per second") {
		re := regexp.MustCompile(`(?i)([0-9][0-9,]*(?:\.[0-9]+)?(?:\s*(?:×|x)\s*10\^?[0-9]+)?)\s*(?:meters per second|metres per second|m/s)`)
		if match := re.FindStringSubmatch(joined); len(match) > 1 {
			return strings.ReplaceAll(match[1], ",", "")
		}
	}

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
		if strings.HasPrefix(qLower, "what ") || strings.HasPrefix(qLower, "which ") || strings.HasPrefix(qLower, "where ") || strings.HasPrefix(qLower, "when ") {
			for _, sep := range []string{" is ", " was ", " are ", " were "} {
				if idx := strings.LastIndex(strings.ToLower(res), sep); idx >= 0 {
					remainder := strings.TrimSpace(res[idx+len(sep):])
					if len(remainder) > 0 && len(remainder) <= 100 {
						return strings.Trim(remainder, `"'.,`)
					}
				}
			}
		}
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
