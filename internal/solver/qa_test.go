package solver

import (
	"strings"
	"testing"
)

func TestExtractAnswerUniversalDomains(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		snippets []string
		expected string
	}{
		{
			name:  "Quantity - Biology Chromosomes",
			query: "How many chromosomes do human cells contain?",
			snippets: []string{
				"Humans have 46 chromosomes in each somatic cell, organized into 23 pairs.",
			},
			expected: "46",
		},
		{
			name:  "Quantity - Science Light speed run in parsecs",
			query: "He completed the run in how many parsecs?",
			snippets: []string{
				"The famous route was completed in 12 parsecs by flying close to the Maw cluster.",
			},
			expected: "12",
		},
		{
			name:  "Year - History Magna Carta",
			query: "In what year was the Magna Carta granted?",
			snippets: []string{
				"Magna Carta was issued in June 1215 at Runnymede by King John of England.",
			},
			expected: "1215",
		},
		{
			name:  "Who - Discovery of Penicillin",
			query: "WHO discovered penicillin in 1928?",
			snippets: []string{
				"Penicillin was discovered in 1928 by Alexander Fleming at St. Mary's Hospital in London.",
			},
			expected: "Alexander Fleming",
		},
		{
			name:  "Who - Delivery agent",
			query: "Who does the leader send to deliver the critical message?",
			snippets: []string{
				"The holographic plea was recorded and delivered via R2-D2 to General Kenobi.",
			},
			expected: "R2-D2",
		},
		{
			name:  "Name - Real/Original name",
			query: "What was the protagonist original name in early drafts?",
			snippets: []string{
				"In the earliest draft from 1974, his surname was Starkiller before Lucas revised it.",
			},
			expected: "Starkiller",
		},
		{
			name:  "Explanation - Photosynthesis",
			query: "In a few sentences, explain why plants require sunlight for survival.",
			snippets: []string{
				"Plants absorb sunlight through chlorophyll to convert carbon dioxide and water into glucose. This glucose serves as food and produces oxygen as a vital byproduct.",
			},
			expected: "Plants absorb sunlight through chlorophyll to convert carbon dioxide and water into glucose. This glucose serves as food and produces oxygen as a vital byproduct.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ans := extractAnswerFromSnippets(tc.query, tc.snippets)
			if !strings.EqualFold(ans, tc.expected) && !strings.Contains(strings.ToLower(ans), strings.ToLower(tc.expected)) {
				t.Errorf("extractAnswerFromSnippets(%q) = %q, want %q", tc.query, ans, tc.expected)
			}
		})
	}
}
