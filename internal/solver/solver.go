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
	evidence := make([]Evidence, 0, len(searchSnippets))
	for _, snippet := range searchSnippets {
		evidence = append(evidence, Evidence{Passage: snippet})
	}
	if len(q.Choices) == 0 {
		return SolveWeb(q, evidence)
	}
	return SolveLocal(q, evidence, cfg)
}

// SolveLocal makes a real typed decision with Laya. It never returns a mock or
// default answer. Evidence is optional and is used by Hybrid mode.
func SolveLocal(q *extractor.Question, evidence []Evidence, cfg Config) (*Result, error) {
	client := laya.New()
	if cfg.Model != "" {
		client.Model = cfg.Model
	}

	// Build state: question + background research (hidden tab, like recursive Von trick)
	state := q.Text
	if len(evidence) > 0 {
		state += "\n\nBackground research (isolated browser):\n" + evidenceText(evidence)
	}
	// Truncate for ANE 96-token limit vs 1024 for general
	// laya client does its own truncation, but we also keep it short
	if len(state) > 2000 {
		state = state[:2000] + "..."
	}

	if len(q.Choices) == 0 {
		return nil, fmt.Errorf("local Laya cannot generate free text")
	}

	if q.Type == extractor.TypeCheckbox {
		return solveCheckboxLocal(client, q, state, evidence, cfg)
	}

	choices := make([]laya.Choice, len(q.Choices))
	for i, c := range q.Choices {
		choices[i] = laya.Choice{Label: c.Label, Text: c.Text}
	}

	resp, err := client.Predict(state, q.Text, choices, cfg.Instructions)
	if err != nil {
		return nil, fmt.Errorf("laya decision: %w", err)
	}
	if resp.Mock || resp.Answer == "" {
		return nil, fmt.Errorf("laya returned no real answer")
	}
	valid := false
	for _, choice := range q.Choices {
		if choice.Label == resp.Answer {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("laya returned unknown choice %q", resp.Answer)
	}

	// Laya confidence is 0-1, convert to 0-100
	conf := int(resp.Confidence*100 + 0.5)
	// Clamp real model confidence without inventing a default.
	if conf > 98 {
		conf = 98
	}
	if conf < 1 {
		conf = 1
	}

	reason := fmt.Sprintf("Laya %s (%.0f%%) — typed decision over %d options. %s",
		resp.Backend, resp.Confidence*100, len(choices),
		map[bool]string{true: "with isolated-browser evidence", false: "without web evidence"}[len(evidence) > 0],
	)

	return normalizeResult(&Result{
		Values:     []string{resp.Answer},
		Answer:     resp.Answer,
		Confidence: conf,
		Reason:     reason,
		SearchUsed: len(evidence) > 0,
		Backend:    resp.Backend,
		RawChoice:  resp.Choice,
		Evidence:   evidence,
	}), nil
}

func solveCheckboxLocal(client *laya.Client, q *extractor.Question, state string, evidence []Evidence, cfg Config) (*Result, error) {
	selected := make([]string, 0, len(q.Choices))
	confidence := 100
	for _, option := range q.Choices {
		choices := []laya.Choice{{Label: "Y", Text: "Yes, select this option"}, {Label: "N", Text: "No, do not select this option"}}
		question := fmt.Sprintf("For %q, should %q be selected?", q.Text, option.Text)
		resp, err := client.Predict(state, question, choices, "Decide whether this individual option is correct. Select Yes only when supported.")
		if err != nil {
			return nil, fmt.Errorf("laya checkbox decision for %s failed: %w", option.Label, err)
		}
		if resp.Mock || (resp.Answer != "Y" && resp.Answer != "N") {
			return nil, fmt.Errorf("laya checkbox decision for %s was invalid", option.Label)
		}
		if resp.Answer == "Y" {
			selected = append(selected, option.Label)
		}
		optionConfidence := int(resp.Confidence*100 + 0.5)
		if optionConfidence < confidence {
			confidence = optionConfidence
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("laya selected no checkbox options")
	}
	return normalizeResult(&Result{Values: selected, Confidence: confidence, Reason: "Laya independently evaluated each checkbox option", SearchUsed: len(evidence) > 0, Backend: "laya", Evidence: evidence}), nil
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
		answer := strings.TrimSpace(extractAnswerFromSnippets(q.Text, passages))
		if answer == "" {
			return nil, fmt.Errorf("could not extract a text answer from web evidence")
		}
		if answerEchoesQuestion(answer, q.Text) {
			return nil, fmt.Errorf("web candidate repeated the question instead of answering it")
		}
		support := 0
		answerLower := strings.ToLower(answer)
		for _, passage := range passages {
			if strings.Contains(strings.ToLower(passage), answerLower) {
				support++
			}
		}
		confidence := 72
		if support >= 2 {
			confidence = 86
		}
		return normalizeResult(&Result{Values: []string{answer}, Confidence: confidence, Reason: "Answer extracted from isolated-browser evidence", SearchUsed: true, Backend: "web-evidence", Evidence: evidence}), nil
	}

	corpus := strings.ToLower(strings.Join(passages, " "))
	type scoredChoice struct {
		label string
		score int
	}
	scores := make([]scoredChoice, 0, len(q.Choices))
	tokenFrequency := choiceTokenFrequency(q.Choices)
	for _, choice := range q.Choices {
		score := evidenceScore(corpus, choice.Text, tokenFrequency)
		scores = append(scores, scoredChoice{label: choice.Label, score: score})
	}
	best := 0
	for _, score := range scores {
		if score.score > best {
			best = score.score
		}
	}
	if best == 0 {
		return nil, fmt.Errorf("web evidence did not support any answer option")
	}
	bestCount := 0
	for _, score := range scores {
		if score.score == best {
			bestCount++
		}
	}
	if q.Type != extractor.TypeCheckbox && bestCount != 1 {
		return nil, fmt.Errorf("web evidence was ambiguous across %d answer options", bestCount)
	}
	values := make([]string, 0, 1)
	for _, score := range scores {
		if score.score == best || (q.Type == extractor.TypeCheckbox && score.score*4 >= best*3) {
			values = append(values, score.label)
			if q.Type != extractor.TypeCheckbox {
				break
			}
		}
	}
	confidence := 60 + best*5
	if confidence > 92 {
		confidence = 92
	}
	return normalizeResult(&Result{Values: values, Confidence: confidence, Reason: "Answer option matched isolated-browser evidence", SearchUsed: true, Backend: "web-evidence", Evidence: evidence}), nil
}

func normalizeResult(result *Result) *Result {
	if len(result.Values) > 0 {
		result.Answer = result.Values[0]
	}
	result.LowConfidence = result.Confidence < 70
	return result
}

func evidenceText(evidence []Evidence) string {
	parts := make([]string, 0, len(evidence))
	for _, item := range evidence {
		parts = append(parts, strings.TrimSpace(item.Title+" — "+item.Passage))
	}
	return strings.Join(parts, "\n---\n")
}

func choiceTokenFrequency(choices []extractor.Choice) map[string]int {
	frequency := make(map[string]int)
	for _, choice := range choices {
		seen := make(map[string]bool)
		for _, token := range strings.Fields(strings.ToLower(choice.Text)) {
			token = strings.Trim(token, ".,:;!?()[]{}\"'")
			if token != "" && !seen[token] {
				frequency[token]++
				seen[token] = true
			}
		}
	}
	return frequency
}

func evidenceScore(corpus, choice string, tokenFrequency map[string]int) int {
	choice = strings.ToLower(strings.TrimSpace(choice))
	if choice == "" {
		return 0
	}
	score := strings.Count(corpus, choice) * 12
	for _, token := range strings.Fields(choice) {
		token = strings.Trim(token, ".,:;!?()[]{}\"'")
		if (len(token) >= 4 || regexp.MustCompile(`^\d+$`).MatchString(token)) && tokenFrequency[token] == 1 {
			score += strings.Count(corpus, token) * 3
		}
	}
	return score
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
