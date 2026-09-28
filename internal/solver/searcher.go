package solver

import (
	"sync"
	"time"

	"mimir/internal/extractor"
	"mimir/internal/von"
)

type SearchResult struct {
	QuestionIdx int
	Query       string
	Snippets    []string
	Error       error
}

func SearchQuestions(v *von.Client, questions []extractor.Question, maxConcurrent int, delayBetween time.Duration) []SearchResult {
	results := make([]SearchResult, len(questions))
	if len(questions) == 0 {
		return results
	}

	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}

	for i := 0; i < len(questions); i += maxConcurrent {
		end := i + maxConcurrent
		if end > len(questions) {
			end = len(questions)
		}

		var wg sync.WaitGroup

		for j := i; j < end; j++ {
			wg.Add(1)
			go func(idx int, q extractor.Question) {
				defer wg.Done()

				query := q.Text

				snippets, err := v.BackgroundSearch(query, 60*time.Second)
				results[idx] = SearchResult{
					QuestionIdx: idx,
					Query:       query,
					Snippets:    snippets,
					Error:       err,
				}
			}(j, questions[j])
		}

		wg.Wait()

		if end < len(questions) {
			time.Sleep(delayBetween)
		}
	}

	return results
}
