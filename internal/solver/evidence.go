package solver

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"mimir/internal/extractor"
)

var wordPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)
var sentencePattern = regexp.MustCompile(`[.!?\n]+\s*`)
var negativePattern = regexp.MustCompile(`(?i)\b(not|incorrect|false|neither|except|isn't|aren't)\b`)

func words(s string) []string { return wordPattern.FindAllString(strings.ToLower(s), -1) }
func phrasePresent(text, phrase string) bool {
	return strings.Contains(" "+strings.Join(words(text), " ")+" ", " "+strings.Join(words(phrase), " ")+" ")
}

// RelevantEvidence excludes unrelated sentences before option matching. A symbol
// like P or K must be a whole token, never a substring of arbitrary prose.
func RelevantEvidence(q *extractor.Question, evidence []Evidence) []Evidence {
	anchor := q.Text
	if q.Row != "" {
		anchor = q.Row
	}
	stop := map[string]bool{}
	for _, w := range strings.Fields("what which who when where how why these those this that their following correctly matches match pairs primary field study respective select all answer question numbers options choices does used with from into have are was were the and for its has can") {
		stop[w] = true
	}
	var anchors []string
	for _, w := range words(anchor) {
		if !stop[w] && len(w) > 2 {
			anchors = append(anchors, w)
		}
	}
	var out []Evidence
	for _, e := range evidence {
		var parts []string
		for _, sentence := range sentencePattern.Split(e.Passage, -1) {
			hits := 0
			for _, w := range anchors {
				if phrasePresent(sentence, w) {
					hits++
				}
			}
			if (q.Row != "" && phrasePresent(sentence, q.Row)) || (q.Row == "" && hits >= minEvidenceHits(len(anchors))) {
				parts = append(parts, sentence)
			}
		}
		if len(parts) > 0 {
			e.Passage = strings.Join(parts, ". ")
			out = append(out, e)
		}
	}
	return out
}
func minEvidenceHits(n int) int {
	if n > 1 {
		return 2
	}
	return 1
}

// Independent hosts provide corroboration; repeated snippets or pages on one
// site cannot inflate the score. URL-less passages can inform but not corroborate.
func sourceKey(e Evidence) string {
	u, err := url.Parse(e.URL)
	if err != nil || u.Hostname() == "" {
		return "unattributed"
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func solveOptionsFromEvidence(q *extractor.Question, evidence []Evidence) (*Result, error) {
	relevant := RelevantEvidence(q, evidence)
	if len(relevant) == 0 {
		return nil, fmt.Errorf("no evidence relates the question subject to an answer")
	}
	values := []string{}
	confidence := 86
	for _, choice := range q.Choices {
		supports := map[string]bool{}
		contradicted := false
		for _, e := range relevant {
			for _, sentence := range sentencePattern.Split(e.Passage, -1) {
				if !phrasePresent(sentence, choice.Text) {
					continue
				}
				if negativePattern.MatchString(sentence) {
					contradicted = true
					continue
				}
				// A page merely listing all single-choice alternatives is not an answer.
				matches := 0
				for _, other := range q.Choices {
					if phrasePresent(sentence, other.Text) {
						matches++
					}
				}
				if q.Type != extractor.TypeCheckbox && matches != 1 {
					continue
				}
				supports[sourceKey(e)] = true
			}
		}
		if len(supports) > 0 && !contradicted {
			values = append(values, choice.Label)
			delete(supports, "unattributed")
			if len(supports) < 2 {
				confidence = 65
			}
		}
	}
	if len(values) == 0 || (q.Type != extractor.TypeCheckbox && len(values) != 1) {
		return nil, fmt.Errorf("web evidence is missing, conflicting, or ambiguous")
	}
	return normalizeResult(&Result{Values: values, Confidence: confidence, Reason: "Answer supported by subject-specific passages; independent sources required for automatic filling", SearchUsed: true, Backend: "web-evidence", Evidence: relevant}), nil
}

// ReconcileHybrid never lets a local prediction replace missing web support.
func ReconcileHybrid(web, local *Result, localErr error) (*Result, error) {
	if web == nil {
		return nil, fmt.Errorf("hybrid requires a web-supported answer")
	}
	if localErr != nil || local == nil || local.LowConfidence {
		web.FallbackPath = "Hybrid → Web (local validation unavailable)"
		return web, nil
	}
	a, b := append([]string(nil), web.Values...), append([]string(nil), local.Values...)
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		return nil, fmt.Errorf("web evidence and local validation disagree; answer left for review")
	}
	web.Backend = "hybrid"
	web.Reason = "Web-supported answer agrees with local validation"
	return web, nil
}
