package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"mimir/internal/extractor"
	"mimir/internal/solver"
)

func TestTUIViewRendering(t *testing.T) {
	m := New(nil, solver.Config{Model: "mock"}, "")
	// Simulate window size
	mUpdated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model := mUpdated.(Model)

	// 0. StateModePicker (new default)
	view := model.View()
	t.Log("\n--- Rendered StateModePicker ---\n" + view + "\n--- End ---")
	if !strings.Contains(view, "M I M I R") {
		t.Errorf("expected View() to contain large ASCII banner 'M I M I R', got: %s", view)
	}
	if !strings.Contains(view, "Select Solving Mode:") {
		t.Errorf("expected View() to contain 'Select Solving Mode:', got: %s", view)
	}
	
	// Mode transitions
	mUpdated2, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = mUpdated2.(Model)
	if model.solveMode != ModeWebSearch {
		t.Errorf("expected mode to be ModeWebSearch (1) after pressing '2', got %d", model.solveMode)
	}
	if model.state != StateURLInput {
		t.Errorf("expected state to be StateURLInput after mode selection, got %d", model.state)
	}

	// 1. StateURLInput
	model.state = StateURLInput
	view = model.View()
	if !strings.Contains(view, "Enter Quiz / Exam URL:") {
		t.Errorf("expected View() to contain 'Enter Quiz / Exam URL:', got: %s", view)
	}

	// 2. StateSlashModal
	model.state = StateSlashModal
	slashView := model.View()
	if !strings.Contains(slashView, "Slash Commands:") {
		t.Errorf("expected View() in StateSlashModal to contain 'Slash Commands:', got: %s", slashView)
	}
	if !strings.Contains(slashView, "M I M I R") {
		t.Errorf("expected View() in StateSlashModal to contain large ASCII banner, got: %s", slashView)
	}

	// 3. StateBrowserPicker
	model.state = StateBrowserPicker
	pickerView := model.View()
	if !strings.Contains(pickerView, "Select Browser to Launch:") {
		t.Errorf("expected View() in StateBrowserPicker to contain 'Select Browser to Launch:', got: %s", pickerView)
	}
	if !strings.Contains(pickerView, "M I M I R") {
		t.Errorf("expected View() in StateBrowserPicker to contain large ASCII banner, got: %s", pickerView)
	}

	// 4. StateIdle / Main Dashboard
	model.state = StateIdle
	model.questions = []extractor.Question{
		{
			Index: 1,
			Text:  "What is the capital of France?",
			Choices: []extractor.Choice{
				{Label: "A", Text: "London"},
				{Label: "B", Text: "Paris"},
			},
			SolvedAns:  "B",
			Confidence: 95,
			Marked:     true,
		},
	}
	dashView := model.View()
	if !strings.Contains(dashView, "What is the capital of France?") {
		t.Errorf("expected View() in StateIdle to contain question text, got: %s", dashView)
	}
	if !strings.Contains(dashView, "→ B ✓ (95%)") {
		t.Errorf("expected View() in StateIdle to contain marked answer, got: %s", dashView)
	}
}

func TestStepByStepSolving(t *testing.T) {
	m := New(nil, solver.Config{Model: "mock"}, "")
	m.questions = []extractor.Question{
		{
			Index:   0,
			Text:    "Q1?",
			Choices: []extractor.Choice{{Label: "A", Text: "Yes"}, {Label: "B", Text: "No"}},
		},
		{
			Index:   1,
			Text:    "Q2?",
			Choices: []extractor.Choice{{Label: "A", Text: "1"}, {Label: "B", Text: "2"}},
		},
	}
	m.state = StateSolving

	// Step 1: Q0 solved
	mUpdated, cmd := m.Update(questionStepSolvedMsg{
		Index:      0,
		Total:      2,
		SolvedAns:  "A",
		Confidence: 80,
	})
	model := mUpdated.(Model)
	if !model.questions[0].Marked || model.questions[0].SolvedAns != "A" {
		t.Errorf("expected Q0 to be marked A, got %+v", model.questions[0])
	}
	if cmd == nil {
		t.Errorf("expected tick command to proceed to Q1")
	}

	// Step 2: Q1 solved (final)
	mUpdated2, _ := model.Update(questionStepSolvedMsg{
		Index:      1,
		Total:      2,
		SolvedAns:  "B",
		Confidence: 90,
	})
	model2 := mUpdated2.(Model)
	if !model2.questions[1].Marked || model2.questions[1].SolvedAns != "B" {
		t.Errorf("expected Q1 to be marked B, got %+v", model2.questions[1])
	}
	if model2.state != StateDone {
		t.Errorf("expected state to be StateDone after final question, got %v", model2.state)
	}
}

