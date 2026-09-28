package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"mimir/internal/extractor"
	"mimir/internal/solver"
	"mimir/internal/von"
)

func sizedModel(t *testing.T, width, height int) Model {
	t.Helper()
	t.Setenv("MIMIR_CONFIG_DIR", t.TempDir())
	m := New(nil, solver.Config{Model: "mock"}, "")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func TestTUIShellAndCoreStates(t *testing.T) {
	model := sizedModel(t, 100, 30)

	view := model.View()
	for _, expected := range []string{"█▀▄▀█", "Local intelligence for browser forms", "Paste a quiz URL"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("setup view missing %q:\n%s", expected, view)
		}
	}

	model.state = StateIdle
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	model = updated.(Model)
	if model.state != StateModePicker {
		t.Fatalf("m should open mode picker, got state %d", model.state)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(Model)
	if model.solveMode != ModeWebSearch || model.state != StateIdle {
		t.Fatalf("mode shortcut should select Web Search and return to dashboard; mode=%d state=%d", model.solveMode, model.state)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	if model.state != StateSlashModal || !strings.Contains(model.View(), "/mode") {
		t.Fatal("slash should open the command composer")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.state != StateIdle {
		t.Fatalf("escape should restore the dashboard, got state %d", model.state)
	}

	model.state = StateBrowserPicker
	if !strings.Contains(model.View(), "Browser") {
		t.Fatal("browser picker should render inside the shell")
	}

	model.state = StateIdle
	model.questions = []extractor.Question{{
		Index:      1,
		Text:       "What is the capital of France?",
		Choices:    []extractor.Choice{{Label: "A", Text: "London"}, {Label: "B", Text: "Paris"}},
		SolvedAns:  "B",
		Confidence: 95,
		Marked:     true,
	}}
	dashboard := model.View()
	if !strings.Contains(dashboard, "Q1/1") || strings.Contains(dashboard, "What is the capital of France?") {
		t.Fatalf("minimal dashboard should keep question detail out of the center and count it in the footer:\n%s", dashboard)
	}
}

func TestModeCommandAcceptsOptionalOrRepeatedSlash(t *testing.T) {
	for _, command := range []string{"mode", "/mode", "//mode"} {
		t.Run(command, func(t *testing.T) {
			model := sizedModel(t, 80, 24)
			model.state = StateIdle
			model.prevState = StateIdle
			updated, _ := model.executeSlashCommand(command)
			result := updated.(Model)
			if result.state != StateModePicker {
				t.Fatalf("%q resolved to state %d, want StateModePicker", command, result.state)
			}
		})
	}

	model := sizedModel(t, 80, 24)
	model.urlInput.SetValue("/mode")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if result := updated.(Model); result.state != StateModePicker {
		t.Fatalf("/mode entered directly in the main composer resolved to state %d", result.state)
	}
}

func TestModePersistenceAndSearchCommandRemoval(t *testing.T) {
	t.Setenv("MIMIR_CONFIG_DIR", t.TempDir())
	if err := savePersistedMode(ModeHybrid); err != nil {
		t.Fatalf("save mode: %v", err)
	}
	model := New(nil, solver.Config{}, "")
	if model.solveMode != ModeHybrid {
		t.Fatalf("persisted mode = %v, want Hybrid", model.solveMode)
	}
	for _, command := range allSlashCmds {
		if command.Name == "/search" {
			t.Fatal("/search must not remain in the command list")
		}
	}
}

func TestTUILayoutNeverExceedsTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{60, 18}, {80, 24}, {120, 40}, {36, 9}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := sizedModel(t, size.width, size.height)
			for _, state := range []State{StateURLInput, StateModePicker, StateBrowserPicker, StateSlashModal, StateIdle} {
				model.state = state
				view := model.View()
				lines := strings.Split(view, "\n")
				if len(lines) > size.height {
					t.Fatalf("state %d rendered %d rows into a %d-row terminal:\n%s", state, len(lines), size.height, view)
				}
				for index, line := range lines {
					if got := lipgloss.Width(line); got > size.width {
						t.Fatalf("state %d row %d rendered %d cells into a %d-column terminal", state, index, got, size.width)
					}
				}
			}
		})
	}
}

func TestTextInputSurfacesUsePurpleBackground(t *testing.T) {
	t.Setenv("MIMIR_CONFIG_DIR", t.TempDir())
	model := New(nil, solver.Config{Model: "mock"}, "")
	styles := []struct {
		name string
		got  lipgloss.TerminalColor
	}{
		{"url prompt", model.urlInput.PromptStyle.GetBackground()},
		{"url text", model.urlInput.TextStyle.GetBackground()},
		{"url placeholder", model.urlInput.PlaceholderStyle.GetBackground()},
		{"url completion", model.urlInput.CompletionStyle.GetBackground()},
		{"url cursor", model.urlInput.Cursor.TextStyle.GetBackground()},
		{"slash prompt", model.slashInput.PromptStyle.GetBackground()},
		{"slash text", model.slashInput.TextStyle.GetBackground()},
		{"slash placeholder", model.slashInput.PlaceholderStyle.GetBackground()},
	}
	for _, style := range styles {
		if style.got != colorInputBg {
			t.Errorf("%s background = %v, want %v", style.name, style.got, colorInputBg)
		}
	}
}

func TestInitialURLAndASCIIWordmark(t *testing.T) {
	t.Setenv("MIMIR_ASCII", "1")
	t.Setenv("MIMIR_CONFIG_DIR", t.TempDir())
	model := New(nil, solver.Config{Model: "mock"}, "https://example.test/quiz")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	view := model.View()
	if model.state != StateURLInput || model.urlInput.Value() != "https://example.test/quiz" {
		t.Fatalf("initial URL should populate the shared setup composer; state=%d value=%q", model.state, model.urlInput.Value())
	}
	if strings.Contains(view, "ᛗ") || !strings.Contains(view, "M I M I R") {
		t.Fatalf("ASCII wordmark fallback was not applied:\n%s", view)
	}
}

func TestBrowserWaitsForExplicitSolveShortcut(t *testing.T) {
	model := sizedModel(t, 100, 30)
	updated, _ := model.Update(browserLaunchedMsg{v: von.New("http://127.0.0.1:9222")})
	model = updated.(Model)
	if model.state != StateWaitingForStart {
		t.Fatalf("browser launch immediately entered state %v; want explicit-start wait", model.state)
	}
	view := model.View()
	if !strings.Contains(view, "Ctrl+P") || !strings.Contains(view, "LATEST ACTIVITY") || !strings.Contains(view, "WAIT") {
		t.Fatalf("waiting UI must advertise Ctrl+P:\n%s", view)
	}
}

func TestDecisionTraceShowsUsefulSignals(t *testing.T) {
	model := sizedModel(t, 100, 30)
	model.traceEvent("THINK", "Q1 · classify → Hybrid")
	model.traceEvent("SOURCE", "Example reference")
	model.traceEvent("FILL", "Q1 · B · web-evidence · 86%")
	view := model.View()
	for _, expected := range []string{"LATEST ACTIVITY", "THINK", "SOURCE", "86%"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("decision trace missing %q:\n%s", expected, view)
		}
	}
}

func TestQuestionPacingIsAccuracyFirst(t *testing.T) {
	if questionPace != 2*time.Second {
		t.Fatalf("question pace=%s, want 2s", questionPace)
	}
}

func TestViewportWheelAndAutoFollow(t *testing.T) {
	model := sizedModel(t, 60, 12)
	model.state = StateIdle
	model.logs = nil
	for index := 0; index < 30; index++ {
		model.logs = append(model.logs, fmt.Sprintf("[%02d:00:00] activity line %02d", index%24, index))
	}
	model.syncViewport(true)
	bottom := model.viewport.YOffset
	if bottom == 0 {
		t.Fatal("expected overflowing activity to produce a scroll offset")
	}

	updated, _ := model.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	model = updated.(Model)
	if model.viewport.YOffset >= bottom {
		t.Fatalf("wheel up did not move the app viewport: before=%d after=%d", bottom, model.viewport.YOffset)
	}

	readingOffset := model.viewport.YOffset
	model.log("new line while reading history")
	if model.viewport.YOffset != readingOffset {
		t.Fatalf("new activity should preserve a reader's offset: before=%d after=%d", readingOffset, model.viewport.YOffset)
	}

	model.viewport.GotoBottom()
	model.log("new line while following")
	if !model.viewport.AtBottom() {
		t.Fatal("new activity should remain pinned when already at the bottom")
	}
}

func TestStepByStepSolving(t *testing.T) {
	t.Setenv("MIMIR_CONFIG_DIR", t.TempDir())
	m := New(nil, solver.Config{Model: "mock"}, "")
	m.questions = []extractor.Question{
		{Index: 0, Text: "Q1?", Choices: []extractor.Choice{{Label: "A", Text: "Yes"}, {Label: "B", Text: "No"}}},
		{Index: 1, Text: "Q2?", Choices: []extractor.Choice{{Label: "A", Text: "1"}, {Label: "B", Text: "2"}}},
	}
	m.state = StateSolving

	updated, cmd := m.Update(questionStepSolvedMsg{Index: 0, Total: 2, Values: []string{"A"}, Confidence: 80, Marked: true})
	model := updated.(Model)
	if !model.questions[0].Marked || model.questions[0].SolvedAns != "A" || cmd == nil {
		t.Fatalf("expected Q1 to be marked and the next step scheduled, got %+v", model.questions[0])
	}

	updated, cmd = model.Update(questionStepSolvedMsg{Index: 1, Total: 2, Values: []string{"B"}, Confidence: 90, Marked: true})
	model = updated.(Model)
	if !model.questions[1].Marked || model.questions[1].SolvedAns != "B" || model.state != StateAdvancing || cmd == nil {
		t.Fatalf("expected final question to be marked and navigation check scheduled, got question=%+v state=%v", model.questions[1], model.state)
	}
	updated, _ = model.Update(pageAdvancedMsg{Result: extractor.AdvanceResult{Final: true}})
	model = updated.(Model)
	if model.state != StateDone || !strings.Contains(model.summaryLine, "stopped before submit") {
		t.Fatalf("expected run to stop before submission, got state=%v summary=%q", model.state, model.summaryLine)
	}
}

func TestNewInstallDefaultsToHybrid(t *testing.T) {
	t.Setenv("MIMIR_CONFIG_DIR", t.TempDir())
	if loadPersistedMode() != ModeHybrid {
		t.Fatal("new installs must default to web-first hybrid")
	}
}

func TestUnresolvedPageStaysForReview(t *testing.T) {
	m := sizedModel(t, 100, 30)
	m.questions = []extractor.Question{{Text: "What is the answer?"}}
	updated, cmd := m.Update(questionStepSolvedMsg{Index: 0, Total: 1, Err: fmt.Errorf("conflicting sources")})
	if updated.(Model).state != StateDone || cmd != nil {
		t.Fatal("unresolved page must not advance")
	}
}
