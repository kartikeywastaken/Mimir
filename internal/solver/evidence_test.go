package solver

import (
	"mimir/internal/extractor"
	"strings"
	"testing"
)

func TestSymbolEvidenceRequiresWholeWordsAndRowSubject(t *testing.T) {
	q := &extractor.Question{Type: extractor.TypeCheckbox, Text: "Match the chemical element to its symbol — Potassium", Row: "Potassium", Choices: []extractor.Choice{{Label: "A", Text: "P"}, {Label: "B", Text: "K"}, {Label: "C", Text: "Na"}, {Label: "D", Text: "Fe"}}}
	evidence := []Evidence{{URL: "https://one.test/potassium", Passage: "Potassium is a chemical element with symbol K. Iron has symbol Fe."}, {URL: "https://two.test/potassium", Passage: "Potassium has the symbol K."}}
	result, err := SolveWeb(q, evidence)
	if err != nil || strings.Join(result.Values, ",") != "B" || result.LowConfidence {
		t.Fatalf("got %+v %v", result, err)
	}
	evidence[1].URL = "https://one.test/duplicate"
	result, err = SolveWeb(q, evidence)
	if err != nil || !result.LowConfidence {
		t.Fatalf("same site inflated confidence: %+v %v", result, err)
	}
}

func TestWebRejectsDistractorListsAndNegation(t *testing.T) {
	q := &extractor.Question{Type: extractor.TypeChoice, Text: "What is the capital of France?", Choices: []extractor.Choice{{Label: "A", Text: "Paris"}, {Label: "B", Text: "London"}}}
	for _, passage := range []string{"What is the capital of France: Paris or London?", "Paris is not the capital of France.", "Paris has excellent museums."} {
		if result, err := SolveWeb(q, []Evidence{{Passage: passage}}); err == nil {
			t.Fatalf("accepted %q: %+v", passage, result)
		}
	}
}

func TestHybridDisagreementCannotOverrideWeb(t *testing.T) {
	web := &Result{Values: []string{"A"}, Confidence: 86, Evidence: []Evidence{{Passage: "source"}}}
	local := &Result{Values: []string{"B"}, Confidence: 98}
	if _, err := ReconcileHybrid(web, local, nil); err == nil {
		t.Fatal("disagreement accepted")
	}
	local.Values = []string{"A"}
	result, err := ReconcileHybrid(web, local, nil)
	if err != nil || result.Backend != "hybrid" || result.Confidence != 86 {
		t.Fatalf("got %+v %v", result, err)
	}
	if _, err := ReconcileHybrid(nil, local, nil); err == nil {
		t.Fatal("model memory replaced web support")
	}
}

func TestEvidenceExtractsFormTextAnswers(t *testing.T) {
	cases := []struct{ q, passage, want string }{
		{"What is the primary gas that humans exhale as a waste product of respiration?", "Humans exhale carbon dioxide as a waste product of respiration.", "carbon dioxide"},
		{"What is the speed of light in a vacuum in meters per second?", "The speed of light in a vacuum is 299,792,458 meters per second.", "299792458"},
		{"What is the term for the biological study of how traits are passed from parents to offspring?", "Genetics is the study of how traits are passed from parents to offspring.", "Genetics"},
	}
	for _, tc := range cases {
		q := &extractor.Question{Type: extractor.TypeText, Text: tc.q}
		result, err := SolveWeb(q, []Evidence{{URL: "https://one.test", Passage: tc.passage}, {URL: "https://two.test", Passage: tc.passage}})
		if err != nil || !strings.EqualFold(result.Answer, tc.want) {
			t.Fatalf("%s: %+v %v", tc.q, result, err)
		}
	}
}
