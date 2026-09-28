package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mimir/internal/browser"
	"mimir/internal/extractor"
	"mimir/internal/solver"
	"mimir/internal/von"
)

type State int

const (
	StateModePicker State = iota
	StateURLInput
	StateBrowserPicker
	StateSlashModal
	StateLaunchingBrowser
	StateWaitingForStart
	StateLoginPolling
	StateIdle
	StateExtracting
	StateSearching
	StateReadingSources
	StateSolving
	StateFallback
	StateFilling
	StateAdvancing
	StateDone
	StateError
)

type SolveMode int

const (
	ModeLocalAI SolveMode = iota
	ModeWebSearch
	ModeHybrid
)

const questionPace = 2 * time.Second

type SlashCmd struct {
	Name string
	Desc string
}

var allSlashCmds = []SlashCmd{
	{Name: "/link [url]", Desc: "Open a quiz link in your chosen browser & start auto-polling"},
	{Name: "/browser", Desc: "Select active browser (Brave, Chrome, Edge, Arc, Chromium)"},
	{Name: "/mode", Desc: "Change solving mode (Local AI, Web Search, Hybrid)"},
	{Name: "/clear", Desc: "Clear activity log history"},
	{Name: "/help", Desc: "Show available slash commands"},
	{Name: "/quit", Desc: "Exit Mimir"},
}

// Theme tokens adapted from the Tosen reference. Mimir cannot choose the
// terminal's font, so the visual system uses stable-width glyphs and a compact
// hierarchy that remains legible in any modern monospace terminal.
var (
	colorBgPurple     = lipgloss.Color("#11111B")
	colorCardPurple   = lipgloss.Color("#181825")
	colorInputBg      = lipgloss.Color("#1E1E2E")
	colorBorderPurple = lipgloss.Color("#7D56F4")
	colorBorderDim    = lipgloss.Color("#45475A")
	colorLogoPurple   = lipgloss.Color("#A78BFA")
	colorSubPurple    = lipgloss.Color("#CDD6F4")
	colorMutedPurple  = lipgloss.Color("#6C7086")
	colorDimPurple    = lipgloss.Color("#585B70")
	colorWhite        = lipgloss.Color("#CDD6F4")
	colorGreen        = lipgloss.Color("#00FF9D")
	colorCyan         = lipgloss.Color("#00D7D7")
	colorYellow       = lipgloss.Color("#F9E2AF")
	colorRed          = lipgloss.Color("#F38BA8")
	colorSelected     = lipgloss.Color("#2A283E")
)

type Model struct {
	von       *von.Client
	searchVon *von.Client
	solverCfg solver.Config

	state      State
	prevState  State
	spinner    spinner.Model
	viewport   viewport.Model
	urlInput   textinput.Model
	slashInput textinput.Model
	initialURL string
	pendingURL string

	solveMode    SolveMode
	selectedMode int

	// Browser selection
	detectedBrowsers []browser.BrowserInfo
	selectedBrowser  int

	// Slash modal
	slashFilteredIdx int
	slashFiltered    []SlashCmd

	// Questions & solving
	questions             []extractor.Question
	currentQIdx           int
	lastSolvedFingerprint string
	autoMode              bool
	errMsg                string
	statusDetail          string
	runSummary            RunSummary
	summaryLine           string
	decisionTrace         []string
	logs                  []string
	width, height         int
}

type RunSummary struct {
	Filled        int
	LowConfidence int
	Unresolved    int
	Skipped       int
	Fallbacks     int
	Pages         int
}

func New(v *von.Client, sc solver.Config, initialURL string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorYellow).Background(colorCardPurple)
	vp := viewport.New(76, 12)
	vp.Style = lipgloss.NewStyle().Background(colorBgPurple)
	vp.MouseWheelDelta = 3

	ti := textinput.New()
	ti.Placeholder = "Paste a quiz URL, or press Enter for the active tab"
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 68
	ti.Prompt = "› "
	styleTextInput(&ti)

	si := textinput.New()
	si.Placeholder = "Type a command"
	si.CharLimit = 256
	si.Width = 68
	si.Prompt = "› "
	styleTextInput(&si)

	detected := browser.DetectBrowsers()

	startState := StateURLInput
	logs := []string{"Mimir ready — Local Laya + Universal Browser Support"}
	if initialURL != "" {
		ti.SetValue(initialURL)
		ti.CursorEnd()
		logs = append(logs, "Link queued: "+initialURL)
	} else {
		logs = append(logs, "Enter a quiz link below, or press Enter to use the active tab.")
	}

	mode := loadPersistedMode()
	return Model{
		von:              v,
		solverCfg:        sc,
		state:            startState,
		prevState:        StateURLInput,
		spinner:          s,
		viewport:         vp,
		urlInput:         ti,
		slashInput:       si,
		initialURL:       initialURL,
		pendingURL:       initialURL,
		detectedBrowsers: detected,
		selectedBrowser:  0,
		slashFiltered:    allSlashCmds,
		solveMode:        mode,
		selectedMode:     int(mode),
		autoMode:         true,
		logs:             logs,
	}
}

func styleTextInput(input *textinput.Model) {
	input.PromptStyle = lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Background(colorInputBg)
	input.TextStyle = lipgloss.NewStyle().Foreground(colorWhite).Background(colorInputBg)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorInputBg)
	input.CompletionStyle = lipgloss.NewStyle().Foreground(colorMutedPurple).Background(colorInputBg)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(colorBgPurple).Background(colorGreen)
	input.Cursor.TextStyle = lipgloss.NewStyle().Foreground(colorWhite).Background(colorInputBg)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

// Messages
type shortcutPollMsg struct {
	targetID string
	client   *von.Client
}

type tickMsg time.Time
type pollMsg time.Time
type browserLaunchedMsg struct {
	v   *von.Client
	err error
}
type batchExtractedMsg struct {
	batch *extractor.BatchResult
	err   error
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.syncViewport(false)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		m.syncViewport(false)
		return m, tea.ClearScreen

	case browserLaunchedMsg:
		if msg.err != nil {
			m.state = StateError
			m.errMsg = msg.err.Error()
			m.log("x browser launch failed: " + msg.err.Error())
			return m, nil
		}
		m.von = msg.v
		m.autoMode = true
		m.state = StateWaitingForStart
		bName := "Browser"
		if len(m.detectedBrowsers) > m.selectedBrowser {
			bName = m.detectedBrowsers[m.selectedBrowser].Name
		}
		m.log(fmt.Sprintf("✓ %s connected on port 9222", bName))

		// Every mode may need web fallback (and Local uses it for free text).
		// The actual command verifies the isolated browser before each lookup.
		m.searchVon = von.New(browser.SearchCDPURL)

		m.statusDetail = "Ctrl+P to start"
		m.log("Ready. Fill respondent details, then press Ctrl+P to solve.")
		m.traceEvent("WAIT", "Fill respondent details, then press Ctrl+P")
		return m, tea.Batch(m.spinner.Tick, pollShortcutCmd(m.von))

	case shortcutPollMsg:
		if msg.client != m.von {
			return m, nil
		}
		next := pollShortcutCmd(m.von)
		if msg.targetID != "" && (m.state == StateWaitingForStart || m.state == StateDone || m.state == StateIdle || m.state == StateError) {
			m.von.PinTarget(msg.targetID)
			updated, cmd := m.handleKeyPress(tea.KeyMsg{Type: tea.KeyCtrlP})
			return updated, tea.Batch(cmd, next)
		}
		return m, next

	case batchExtractedMsg:
		if msg.err != nil {
			m.log("! extract check: " + msg.err.Error())
			if m.autoMode {
				m.state = StateLoginPolling
				return m, tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
			}
			m.state = StateIdle
			return m, nil
		}

		// Check for embedded quiz iframes (e.g. Google Forms embedded in blog/LMS)
		if msg.batch.EmbeddedQuizURL != "" {
			m.log("→ Detected embedded quiz iframe: " + msg.batch.EmbeddedQuizURL)
			m.log("-> Automatically navigating directly into the quiz...")
			if m.von != nil {
				_ = browser.NavigateTo(m.von, msg.batch.EmbeddedQuizURL)
			}
			m.state = StateExtracting
			m.lastSolvedFingerprint = ""
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
		}

		// Check if it's a login screen or empty page
		if msg.batch.IsLogin || len(msg.batch.Questions) == 0 {
			if m.state != StateLoginPolling {
				m.state = StateLoginPolling
				m.log("· Waiting for login or questions to load; checking every 10s")
			}
			// Schedule silent 10-second poll
			return m, tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
		}

		// Questions found! Check if question set has changed
		fingerprint := makeFingerprint(msg.batch.Questions)
		if fingerprint == m.lastSolvedFingerprint && m.lastSolvedFingerprint != "" {
			m.finishRun("next page did not change")
			return m, nil
		}

		m.questions = msg.batch.Questions
		m.currentQIdx = 0
		m.runSummary.Pages++
		m.state = stateForQuestion(m.solveMode, m.questions[0])
		m.statusDetail = fmt.Sprintf("Q1/%d · starts in 2s", len(m.questions))
		m.log(fmt.Sprintf("✓ Detected %d question(s) on page %d", len(m.questions), m.runSummary.Pages))
		m.traceEvent("SCAN", fmt.Sprintf("Page %d · %d fields detected", m.runSummary.Pages, len(m.questions)))
		return m, tea.Tick(questionPace, func(time.Time) tea.Msg { return nextQuestionMsg{Index: 0} })

	case questionStepSolvedMsg:
		idx := msg.Index
		if idx >= 0 && idx < len(m.questions) {
			m.questions[idx].SolvedValues = append([]string(nil), msg.Values...)
			if len(msg.Values) > 0 {
				m.questions[idx].SolvedAns = strings.Join(msg.Values, ", ")
			}
			m.questions[idx].Confidence = msg.Confidence
			m.questions[idx].Marked = msg.Marked
			m.questions[idx].LowConfidence = msg.LowConfidence
			m.questions[idx].Skipped = msg.Skipped
			m.questions[idx].SkipReason = msg.SkipReason
			if msg.Err != nil {
				m.questions[idx].SolveError = msg.Err.Error()
			}
		}
		if msg.LowConfidence {
			m.runSummary.LowConfidence++
		}
		if msg.Skipped {
			m.runSummary.Skipped++
			m.log(fmt.Sprintf("[Q%d/%d] Left for you: %s", idx+1, msg.Total, msg.SkipReason))
			m.traceEvent("LEAVE", fmt.Sprintf("Q%d · %s", idx+1, msg.SkipReason))
		} else if msg.Marked {
			m.runSummary.Filled++
			fallback := ""
			if msg.FallbackPath != "" {
				m.runSummary.Fallbacks++
				m.state = StateFallback
				m.statusDetail = msg.FallbackPath
				fallback = " · fallback " + msg.FallbackPath
			}
			m.log(fmt.Sprintf("[Q%d/%d] Filled %s (%d%%)%s", idx+1, msg.Total, strings.Join(msg.Values, ", "), msg.Confidence, fallback))
			m.traceEvent("FILL", fmt.Sprintf("Q%d · %s · %s · %d%%", idx+1, strings.Join(msg.Values, ", "), msg.Backend, msg.Confidence))
			if msg.Reason != "" {
				m.traceEvent("WHY", msg.Reason)
			}
			if len(msg.Sources) > 0 {
				m.traceEvent("SOURCE", strings.Join(msg.Sources, " · "))
			}
		} else {
			m.runSummary.Unresolved++
			reason := "no verified answer"
			if msg.Err != nil {
				reason = msg.Err.Error()
			}
			m.log(fmt.Sprintf("[Q%d/%d] Unresolved: %s", idx+1, msg.Total, reason))
			m.traceEvent("HOLD", fmt.Sprintf("Q%d · %s", idx+1, reason))
		}

		if idx+1 < msg.Total {
			m.currentQIdx = idx + 1
			m.statusDetail = fmt.Sprintf("Q%d/%d · waiting 2s", idx+2, len(m.questions))
			return m, tea.Tick(questionPace, func(t time.Time) tea.Msg {
				return nextQuestionMsg{Index: idx + 1}
			})
		}

		// Keep unresolved objective answers visible for review before navigation.
		for _, question := range m.questions {
			if !question.Marked && !question.Skipped {
				m.finishRun("unresolved answers remain on this page")
				return m, nil
			}
		}
		// This page is complete. Advance only through an explicitly safe Next control.
		m.state = StateAdvancing
		m.lastSolvedFingerprint = makeFingerprint(m.questions)
		m.statusDetail = "Checking for next page"
		return m, advancePageCmd(m.von)

	case nextQuestionMsg:
		if msg.Index < len(m.questions) {
			m.state = stateForQuestion(m.solveMode, m.questions[msg.Index])
			m.statusDetail = fmt.Sprintf("Q%d/%d", msg.Index+1, len(m.questions))
			m.traceEvent("THINK", fmt.Sprintf("Q%d · classify → %s", msg.Index+1, m.modeName()))
			return m, solveSingleQuestionCmd(m.von, m.searchVon, m.solverCfg, m.questions, msg.Index, m.solveMode)
		}
		return m, nil

	case pageAdvancedMsg:
		if msg.Err != nil {
			m.finishRun("navigation stopped: " + msg.Err.Error())
			return m, nil
		}
		if !msg.Result.Advanced {
			m.traceEvent("STOP", "Final page reached · submission left to you")
			m.finishRun("stopped before submit")
			return m, nil
		}
		m.state = StateExtracting
		m.statusDetail = "Loading next page"
		m.traceEvent("NEXT", "Safe Next control clicked · loading page")
		return m, tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg { return pollMsg(time.Now()) })

	case pollMsg:
		if (m.autoMode || m.state == StateExtracting) && m.von != nil {
			return m, extractBatchCmd(m.von)
		}
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlP && m.von != nil && (m.state == StateWaitingForStart || m.state == StateDone || m.state == StateIdle || m.state == StateError) {
			m.von.PinTarget("")
		}
		return m.handleKeyPress(msg)

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	// Update text inputs based on active state
	var cmd tea.Cmd
	if m.state == StateURLInput {
		m.urlInput, cmd = m.urlInput.Update(msg)
		return m, cmd
	} else if m.state == StateSlashModal {
		m.slashInput, cmd = m.slashInput.Update(msg)
		m.filterSlashCmds()
		return m, cmd
	}

	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	return m, vpCmd
}

func (m *Model) filterSlashCmds() {
	val := strings.TrimSpace(strings.ToLower(m.slashInput.Value()))
	val = strings.TrimLeft(val, "/")
	if val == "" {
		m.slashFiltered = allSlashCmds
		m.slashFilteredIdx = 0
		return
	}
	var filtered []SlashCmd
	for _, c := range allSlashCmds {
		cmdName := strings.ToLower(c.Name)
		if strings.Contains(cmdName, val) || strings.Contains(strings.ToLower(c.Desc), val) {
			filtered = append(filtered, c)
		}
	}
	m.slashFiltered = filtered
	if m.slashFilteredIdx >= len(filtered) {
		m.slashFilteredIdx = max(0, len(filtered)-1)
	}
}

func (m Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global Quit
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if msg.Type == tea.KeyCtrlP && (m.state == StateWaitingForStart || m.state == StateDone || m.state == StateIdle || m.state == StateError) {
		if m.von == nil || !browser.IsCDPAvailable(m.von.HTTPBase) {
			m.state = StateError
			m.errMsg = "no browser attached; enter a link first"
			return m, nil
		}
		m.resetRun()
		m.state = StateExtracting
		m.statusDetail = "Scanning after Ctrl+P"
		m.log("Ctrl+P received — starting solve run")
		m.traceEvent("START", fmt.Sprintf("%s mode · accuracy-first run", m.modeName()))
		return m, extractBatchCmd(m.von)
	}

	// 0. Mode Picker Modal
	if m.state == StateModePicker {
		switch msg.String() {
		case "esc":
			m.state = m.pickerReturnState()
			m.restoreInputFocus()
			return m, nil
		case "up":
			if m.selectedMode > 0 {
				m.selectedMode--
			}
			return m, nil
		case "down":
			if m.selectedMode < 2 {
				m.selectedMode++
			}
			return m, nil
		case "enter":
			m.solveMode = SolveMode(m.selectedMode)
			_ = savePersistedMode(m.solveMode)
			m.state = m.pickerReturnState()
			m.restoreInputFocus()
			return m, nil
		case "1", "2", "3":
			idx := int(msg.String()[0] - '1')
			m.solveMode = SolveMode(idx)
			m.selectedMode = idx
			_ = savePersistedMode(m.solveMode)
			m.state = m.pickerReturnState()
			m.restoreInputFocus()
			return m, nil
		}
		return m, nil
	}

	// 1. Slash Command Modal
	if m.state == StateSlashModal {
		switch msg.String() {
		case "esc":
			m.state = m.prevState
			m.slashInput.Blur()
			m.restoreInputFocus()
			return m, nil
		case "up":
			if m.slashFilteredIdx > 0 {
				m.slashFilteredIdx--
			}
			return m, nil
		case "down":
			if m.slashFilteredIdx < len(m.slashFiltered)-1 {
				m.slashFilteredIdx++
			}
			return m, nil
		case "enter":
			inputVal := strings.TrimSpace(m.slashInput.Value())
			return m.executeSlashCommand(inputVal)
		}
		var cmd tea.Cmd
		m.slashInput, cmd = m.slashInput.Update(msg)
		m.filterSlashCmds()
		return m, cmd
	}

	// 2. Browser Picker Modal
	if m.state == StateBrowserPicker {
		switch msg.String() {
		case "esc":
			m.state = m.pickerReturnState()
			m.restoreInputFocus()
			return m, nil
		case "up":
			if m.selectedBrowser > 0 {
				m.selectedBrowser--
			}
			return m, nil
		case "down":
			if m.selectedBrowser < len(m.detectedBrowsers)-1 {
				m.selectedBrowser++
			}
			return m, nil
		case "enter":
			if len(m.detectedBrowsers) == 0 {
				m.state = StateError
				m.errMsg = "no supported browsers detected on system"
				return m, nil
			}
			chosen := m.detectedBrowsers[m.selectedBrowser]
			m.state = StateLaunchingBrowser
			m.log(fmt.Sprintf("Launching %s to: %s...", chosen.Name, m.pendingURL))
			return m, tea.Batch(m.spinner.Tick, launchBrowserWithCmd(chosen, m.pendingURL))
		}
		// Number shortcuts: 1, 2, 3...
		if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
			idx := int(msg.String()[0] - '1')
			if idx < len(m.detectedBrowsers) {
				m.selectedBrowser = idx
				chosen := m.detectedBrowsers[idx]
				m.state = StateLaunchingBrowser
				m.log(fmt.Sprintf("Launching %s to: %s...", chosen.Name, m.pendingURL))
				return m, tea.Batch(m.spinner.Tick, launchBrowserWithCmd(chosen, m.pendingURL))
			}
		}
		return m, nil
	}

	// 3. Initial URL Input Box
	if m.state == StateURLInput {
		switch msg.String() {
		case "esc":
			m.state = StateIdle
			m.urlInput.Blur()
			return m, nil
		case "/":
			m.openSlashModal()
			return m, nil
		case "enter":
			raw := strings.TrimSpace(m.urlInput.Value())
			if strings.HasPrefix(raw, "/") {
				m.urlInput.SetValue("")
				m.prevState = StateURLInput
				return m.executeSlashCommand(raw)
			}
			m.pendingURL = raw
			m.resetRun()
			m.prevState = StateURLInput
			m.state = StateBrowserPicker
			m.urlInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.urlInput, cmd = m.urlInput.Update(msg)
		return m, cmd
	}

	// 4. Default / Idle Keys
	switch msg.String() {
	case "/":
		m.openSlashModal()
		return m, nil
	case "c":
		m.logs = []string{"Logs cleared."}
		m.viewport.SetContent("")
	case "m":
		m.prevState = m.state
		m.selectedMode = int(m.solveMode)
		m.state = StateModePicker
		m.viewport.GotoTop()
	case "b":
		m.prevState = m.state
		m.state = StateBrowserPicker
		m.viewport.GotoTop()
	case "q":
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *Model) openSlashModal() {
	m.prevState = m.state
	m.state = StateSlashModal
	m.urlInput.Blur()
	m.slashInput.SetValue("/")
	m.slashInput.CursorEnd()
	m.slashInput.Focus()
	m.slashFiltered = allSlashCmds
	m.slashFilteredIdx = 0
	m.viewport.GotoTop()
}

func (m Model) pickerReturnState() State {
	if m.prevState == StateModePicker || m.prevState == StateBrowserPicker || m.prevState == StateSlashModal {
		return StateIdle
	}
	return m.prevState
}

func (m *Model) restoreInputFocus() {
	if m.state == StateURLInput {
		m.urlInput.Focus()
	}
}

func (m *Model) executeSlashCommand(input string) (tea.Model, tea.Cmd) {
	input = strings.TrimSpace(input)
	normalized := strings.TrimLeft(input, "/")
	if normalized != "" {
		normalized = "/" + normalized
	}
	cmdName := normalized
	cmdArg := ""
	if normalized != "" {
		parts := strings.SplitN(normalized, " ", 2)
		cmdName = parts[0]
		if len(parts) > 1 {
			cmdArg = strings.TrimSpace(parts[1])
		}
	} else if len(m.slashFiltered) > 0 && m.slashFilteredIdx < len(m.slashFiltered) {
		// Use highlighted command from list
		selected := m.slashFiltered[m.slashFilteredIdx].Name
		parts := strings.SplitN(selected, " ", 2)
		cmdName = parts[0]
	}

	returnState := m.prevState
	if returnState == StateSlashModal || returnState == StateModePicker || returnState == StateBrowserPicker {
		returnState = StateIdle
	}
	m.state = returnState
	m.slashInput.Blur()
	m.restoreInputFocus()

	switch cmdName {
	case "/link":
		if cmdArg != "" {
			m.pendingURL = cmdArg
			m.resetRun()
			m.state = StateBrowserPicker
			return *m, nil
		}
		m.state = StateURLInput
		m.urlInput.SetValue("")
		m.urlInput.Focus()
		return *m, nil

	case "/browser":
		m.prevState = returnState
		m.state = StateBrowserPicker
		m.viewport.GotoTop()
		return *m, nil

	case "/mode":
		m.prevState = returnState
		m.state = StateModePicker
		m.selectedMode = int(m.solveMode)
		m.viewport.GotoTop()
		return *m, nil

	case "/clear":
		m.logs = []string{"Logs cleared."}
		m.viewport.SetContent("")
		return *m, nil

	case "/help":
		m.log("Available Slash Commands:")
		for _, c := range allSlashCmds {
			m.log(fmt.Sprintf("  %-12s - %s", c.Name, c.Desc))
		}
		return *m, nil

	case "/quit":
		return *m, tea.Quit
	}

	m.log(fmt.Sprintf("! unknown slash command: %s (type /help for commands)", input))
	return *m, nil
}

func (m *Model) resize(width, height int) {
	m.width = max(1, width)
	m.height = max(1, height)
	workspaceWidth := m.workspaceWidth()
	chromeHeight := 6
	if m.height < 10 {
		chromeHeight = 2
	}
	m.viewport.Width = workspaceWidth
	m.viewport.Height = max(1, m.height-chromeHeight)
	inputWidth := max(1, workspaceWidth-4)
	m.urlInput.Width = inputWidth
	m.slashInput.Width = inputWidth
}

func (m Model) workspaceWidth() int {
	width := m.width
	if width <= 0 {
		width = 80
	}
	gutter := 4
	if width < 48 {
		gutter = 2
	}
	return max(1, min(96, width-gutter))
}

func (m *Model) syncViewport(follow bool) {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.viewport.SetContent(strings.Join(m.logs, "\n"))
	if follow {
		m.viewport.GotoBottom()
	}
}

func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	footer := m.renderMinimalFooter(m.width)
	if m.height == 1 {
		return footer
	}

	brand := m.brandView()
	panel := m.renderMinimalPanel()
	gap := lipgloss.NewStyle().Background(colorBgPurple).Render(" ")
	centerParts := []string{brand, gap, panel}
	if trace := m.renderDecisionTrace(); trace != "" {
		centerParts = append(centerParts, gap, trace)
	}
	if m.summaryLine != "" {
		summary := lipgloss.NewStyle().Foreground(colorMutedPurple).Background(colorBgPurple).Render(truncateCells(m.summaryLine, max(1, m.workspaceWidth())))
		centerParts = append(centerParts, gap, summary)
	}
	center := lipgloss.JoinVertical(lipgloss.Center, centerParts...)
	body := lipgloss.Place(
		m.width,
		m.height-1,
		lipgloss.Center,
		lipgloss.Center,
		center,
		lipgloss.WithWhitespaceBackground(colorBgPurple),
		lipgloss.WithWhitespaceForeground(colorDimPurple),
	)
	return body + "\n" + footer
}

func (m Model) renderDecisionTrace() string {
	if len(m.decisionTrace) == 0 || m.height < 20 {
		return ""
	}
	panelWidth := max(12, min(68, m.width-6))
	contentWidth := max(1, panelWidth-4)
	limit := 3
	start := max(0, len(m.decisionTrace)-limit)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(colorSubPurple).Background(colorBgPurple).Render("LATEST ACTIVITY")}
	for _, entry := range m.decisionTrace[start:] {
		parts := strings.SplitN(entry, "  ", 2)
		stage := parts[0]
		detail := ""
		if len(parts) == 2 {
			detail = parts[1]
		}
		stageWidth := min(8, contentWidth)
		stageLabel := truncateCells(stage, stageWidth)
		stageView := lipgloss.NewStyle().Bold(true).Foreground(traceStageColor(stage)).Background(colorBgPurple).Width(stageWidth).Render(stageLabel)
		detailWidth := max(0, contentWidth-stageWidth)
		detailView := lipgloss.NewStyle().Foreground(colorMutedPurple).Background(colorBgPurple).Width(detailWidth).Render(truncateCells(detail, detailWidth))
		lines = append(lines, stageView+detailView)
	}
	return lipgloss.NewStyle().
		Foreground(colorWhite).
		Background(colorBgPurple).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderDim).
		Padding(0, 1).
		Width(contentWidth + 2).
		Render(strings.Join(lines, "\n"))
}

func traceStageColor(stage string) lipgloss.Color {
	switch stage {
	case "FILL", "START", "NEXT":
		return colorGreen
	case "THINK", "SOURCE", "SCAN":
		return colorCyan
	case "HOLD", "LEAVE", "WHY", "WAIT", "STOP":
		return colorYellow
	default:
		return colorLogoPurple
	}
}

func (m Model) renderMinimalPanel() string {
	panelWidth := max(12, min(68, m.width-6))
	if panelWidth > m.width {
		panelWidth = m.width
	}
	contentWidth := max(1, panelWidth-4)
	content := m.renderMinimalPanelContent(contentWidth)

	return lipgloss.NewStyle().
		Foreground(colorWhite).
		Background(colorInputBg).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderPurple).
		Padding(0, 1).
		Width(contentWidth + 2).
		Render(content)
}

func (m Model) renderMinimalPanelContent(width int) string {
	switch m.state {
	case StateURLInput:
		input := m.urlInput
		input.Width = max(1, width-2)
		return input.View()
	case StateSlashModal:
		return m.renderMinimalCommands(width)
	case StateModePicker:
		return m.renderMinimalModes(width)
	case StateBrowserPicker:
		return m.renderMinimalBrowsers(width)
	case StateWaitingForStart:
		return lipgloss.NewStyle().Foreground(colorGreen).Background(colorInputBg).Width(width).Render(truncateCells("●  READY  •  Fill personal fields, then Ctrl+P", width))
	default:
		return lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorInputBg).Width(width).Render(truncateCells("›  Ctrl+P solve  •  / commands", width))
	}
}

func (m Model) renderMinimalCommands(width int) string {
	input := m.slashInput
	input.Width = max(1, width-2)
	lines := []string{input.View()}
	if len(m.slashFiltered) == 0 {
		lines = append(lines, minimalPanelLine("No matching commands", width, false, colorDimPurple))
		return strings.Join(lines, "\n")
	}

	limit := min(5, max(1, m.height-7))
	start := 0
	if m.slashFilteredIdx >= limit {
		start = m.slashFilteredIdx - limit + 1
	}
	end := min(len(m.slashFiltered), start+limit)
	for index := start; index < end; index++ {
		command := m.slashFiltered[index]
		label := fmt.Sprintf("  %-13s %s", command.Name, command.Desc)
		selected := index == m.slashFilteredIdx
		if selected {
			label = "› " + strings.TrimLeft(label, " ")
		}
		lines = append(lines, minimalPanelLine(label, width, selected, colorWhite))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderMinimalModes(width int) string {
	options := []string{"Local AI · Laya; web fallback for text", "Web Search · isolated browser evidence", "Hybrid · web first, Laya checks agreement"}
	lines := []string{minimalPanelLine("Choose a solving mode", width, false, colorSubPurple)}
	for index, option := range options {
		selected := index == m.selectedMode
		marker := "  "
		if selected {
			marker = "› "
		}
		lines = append(lines, minimalPanelLine(fmt.Sprintf("%s%d  %s", marker, index+1, option), width, selected, colorWhite))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderMinimalBrowsers(width int) string {
	lines := []string{minimalPanelLine("Choose a browser", width, false, colorSubPurple)}
	if len(m.detectedBrowsers) == 0 {
		return strings.Join(append(lines, minimalPanelLine("! No supported browser detected", width, false, colorRed)), "\n")
	}
	limit := min(5, len(m.detectedBrowsers))
	start := 0
	if m.selectedBrowser >= limit {
		start = m.selectedBrowser - limit + 1
	}
	end := min(len(m.detectedBrowsers), start+limit)
	for index := start; index < end; index++ {
		selected := index == m.selectedBrowser
		marker := "  "
		if selected {
			marker = "› "
		}
		label := fmt.Sprintf("%s%d  %s", marker, index+1, m.detectedBrowsers[index].Name)
		lines = append(lines, minimalPanelLine(label, width, selected, colorWhite))
	}
	return strings.Join(lines, "\n")
}

func minimalPanelLine(text string, width int, selected bool, foreground lipgloss.Color) string {
	background := colorInputBg
	if selected {
		background = colorSelected
		foreground = colorLogoPurple
	}
	style := lipgloss.NewStyle().Foreground(foreground).Background(background).Width(width).MaxWidth(width)
	if selected {
		style = style.Bold(true)
	}
	return style.Render(truncateCells(text, width))
}

func (m Model) renderMinimalFooter(width int) string {
	left := fmt.Sprintf(" %s  •  %s", strings.ToUpper(m.modeName()), m.browserName())
	if len(m.questions) > 0 {
		left += fmt.Sprintf("  •  Q%d/%d", min(m.currentQIdx+1, len(m.questions)), len(m.questions))
	}
	status, statusColor := m.minimalStatus()
	if m.statusDetail != "" && m.state != StateDone {
		status += " • " + m.statusDetail
	}
	right := "● " + status + "   / commands  •  ^c quit "
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > width {
		right = "● " + status + " "
	}
	right = truncateCells(right, width)
	availableLeft := max(0, width-lipgloss.Width(right)-1)
	left = truncateCells(left, availableLeft)

	leftView := lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render(left)
	rightView := lipgloss.NewStyle().Foreground(statusColor).Background(colorCardPurple).Render(right)
	gap := max(0, width-lipgloss.Width(leftView)-lipgloss.Width(rightView))
	return lipgloss.NewStyle().Background(colorCardPurple).Width(width).MaxWidth(width).Render(leftView + strings.Repeat(" ", gap) + rightView)
}

func (m Model) minimalStatus() (string, lipgloss.Color) {
	switch m.state {
	case StateLaunchingBrowser:
		return "Launching browser", colorYellow
	case StateWaitingForStart:
		return "Waiting · Ctrl+P starts", colorGreen
	case StateLoginPolling:
		return "Waiting for quiz", colorYellow
	case StateExtracting:
		return "Scanning page", colorCyan
	case StateSearching:
		return "Searching", colorCyan
	case StateReadingSources:
		return "Reading sources", colorCyan
	case StateSolving:
		return "Solving", colorCyan
	case StateFallback:
		return "Fallback", colorYellow
	case StateFilling:
		return "Filling", colorYellow
	case StateAdvancing:
		return "Advancing", colorYellow
	case StateDone:
		return "Solved", colorGreen
	case StateError:
		return "Error", colorRed
	case StateModePicker:
		return "Select mode", colorSubPurple
	case StateBrowserPicker:
		return "Select browser", colorSubPurple
	case StateSlashModal:
		return "Command", colorSubPurple
	default:
		return "Ready", colorGreen
	}
}

func (m Model) modeName() string {
	switch m.solveMode {
	case ModeWebSearch:
		return "Web Search"
	case ModeHybrid:
		return "Hybrid"
	default:
		return "Local AI"
	}
}

func (m Model) browserName() string {
	if m.selectedBrowser >= 0 && m.selectedBrowser < len(m.detectedBrowsers) {
		return m.detectedBrowsers[m.selectedBrowser].Name
	}
	return "No browser"
}

func (m Model) brandView() string {
	logoStyle := lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorBgPurple).Align(lipgloss.Center)
	if os.Getenv("MIMIR_ASCII") != "" {
		return logoStyle.Render("M I M I R")
	}
	if m.width < 46 || m.height < 14 {
		return logoStyle.Render("ᛗ  M I M I R")
	}
	const wordmark = `█▀▄▀█ █ █▄ ▄█ █ █▀█
█ ▀ █ █ █ ▀ █ █ █▀▄
▀   ▀ ▀ ▀   ▀ ▀ ▀ ▀`
	parts := make([]string, 0, 4)
	if m.height >= 22 {
		parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Background(colorBgPurple).Render("ᛗ"))
	}
	parts = append(parts, logoStyle.Render(wordmark))
	if m.height >= 18 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorMutedPurple).Background(colorBgPurple).Render("Local intelligence for browser forms"))
	}
	if m.height >= 24 {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorBgPurple).Render("laya  •  isolated research  •  verified fill"))
	}
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}

func truncateCells(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	var builder strings.Builder
	for _, r := range text {
		candidate := builder.String() + string(r)
		if lipgloss.Width(candidate)+1 > width {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String() + "…"
}

func (m *Model) log(s string) {
	m.syncViewport(false)
	follow := m.viewport.AtBottom()
	ts := time.Now().Format("15:04:05")
	m.logs = append(m.logs, fmt.Sprintf("[%s] %s", ts, s))
	if len(m.logs) > 200 {
		m.logs = m.logs[len(m.logs)-200:]
	}
	m.syncViewport(follow)
}

func (m *Model) resetRun() {
	m.runSummary = RunSummary{}
	m.summaryLine = ""
	m.statusDetail = ""
	m.lastSolvedFingerprint = ""
	m.decisionTrace = nil
}

func (m *Model) finishRun(reason string) {
	m.state = StateDone
	m.statusDetail = ""
	m.summaryLine = fmt.Sprintf("%d filled · %d left for you · %d low confidence · %d unresolved · %d fallbacks · %s",
		m.runSummary.Filled, m.runSummary.Skipped, m.runSummary.LowConfidence, m.runSummary.Unresolved, m.runSummary.Fallbacks, reason)
	m.log("✓ " + m.summaryLine)
}

func (m *Model) traceEvent(stage, detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return
	}
	m.decisionTrace = append(m.decisionTrace, stage+"  "+detail)
	if len(m.decisionTrace) > 8 {
		m.decisionTrace = m.decisionTrace[len(m.decisionTrace)-8:]
	}
}

func stateForQuestion(mode SolveMode, q extractor.Question) State {
	if mode == ModeWebSearch || q.Type == extractor.TypeText || q.Type == extractor.TypeParagraph {
		return StateSearching
	}
	return StateSolving
}

type persistedSettings struct {
	Mode SolveMode `json:"mode"`
}

func settingsPath() string {
	if dir := os.Getenv("MIMIR_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "mimir", "settings.json")
}

func loadPersistedMode() SolveMode {
	path := settingsPath()
	if path == "" {
		return ModeHybrid
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ModeHybrid
	}
	var settings persistedSettings
	if json.Unmarshal(data, &settings) != nil || settings.Mode < ModeLocalAI || settings.Mode > ModeHybrid {
		return ModeHybrid
	}
	return settings.Mode
}

func savePersistedMode(mode SolveMode) error {
	path := settingsPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(persistedSettings{Mode: mode})
	return os.WriteFile(path, data, 0o600)
}

func makeFingerprint(qs []extractor.Question) string {
	var sb strings.Builder
	for _, q := range qs {
		sb.WriteString(q.Text)
		for _, c := range q.Choices {
			sb.WriteString(c.Label)
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Commands

func launchBrowserWithCmd(b browser.BrowserInfo, targetURL string) tea.Cmd {
	return func() tea.Msg {
		v, err := browser.EnsureBrowserWith(b, targetURL)
		if err != nil {
			return browserLaunchedMsg{nil, err}
		}
		time.Sleep(3 * time.Second) // Brave needs time to register page target after CDP is up
		return browserLaunchedMsg{v, nil}
	}
}

func extractBatchCmd(v *von.Client) tea.Cmd {
	return func() tea.Msg {
		if v == nil {
			return batchExtractedMsg{nil, fmt.Errorf("no browser connected")}
		}
		t, err := v.ActiveTarget()
		if err != nil {
			return batchExtractedMsg{nil, err}
		}
		v.PinTarget(t.ID)
		var res string
		for attempt := 0; attempt < 3; attempt++ {
			res, err = v.Evaluate(t.WebSocketURL, extractor.ExtractAllJS)
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "context") {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			return batchExtractedMsg{nil, err}
		}
		batch, err := extractor.ParseBatchResult(res)
		if err != nil {
			return batchExtractedMsg{nil, err}
		}
		return batchExtractedMsg{batch, nil}
	}
}

type questionStepSolvedMsg struct {
	Index         int
	Total         int
	Values        []string
	Confidence    int
	Marked        bool
	LowConfidence bool
	Backend       string
	FallbackPath  string
	Reason        string
	Sources       []string
	Skipped       bool
	SkipReason    string
	Err           error
}

type nextQuestionMsg struct {
	Index int
}

type pageAdvancedMsg struct {
	Result extractor.AdvanceResult
	Err    error
}

func solveSingleQuestionCmd(quizVon, searchVon *von.Client, sc solver.Config, questions []extractor.Question, idx int, solveMode SolveMode) tea.Cmd {
	return func() tea.Msg {
		if idx >= len(questions) {
			return questionStepSolvedMsg{Index: idx, Total: len(questions), Err: fmt.Errorf("question index out of range")}
		}

		q := questions[idx]
		classification, classifyErr := solver.ClassifyQuestion(&q, sc)
		if classification.Disposition != solver.DispositionObjective {
			reason := classification.Reason
			if classifyErr != nil {
				reason += "; " + classifyErr.Error()
			}
			return questionStepSolvedMsg{Index: idx, Total: len(questions), Skipped: true, SkipReason: reason}
		}
		res, err := solveWithMode(&q, searchVon, sc, solveMode)
		message := questionStepSolvedMsg{Index: idx, Total: len(questions), Err: err}
		if err != nil || res == nil || len(res.Values) == 0 {
			return message
		}
		message.Values = append([]string(nil), res.Values...)
		message.Confidence = res.Confidence
		message.LowConfidence = res.LowConfidence
		message.Backend = res.Backend
		message.FallbackPath = res.FallbackPath
		message.Reason = res.Reason
		for _, evidence := range res.Evidence {
			source := strings.TrimSpace(evidence.Title)
			if source == "" {
				source = strings.TrimSpace(evidence.URL)
			}
			if source != "" {
				message.Sources = append(message.Sources, truncate(source, 40))
				if len(message.Sources) == 2 {
					break
				}
			}
		}
		if res.LowConfidence {
			message.Err = fmt.Errorf("answer confidence %d%% is below the 70%% safety threshold", res.Confidence)
			return message
		}

		if quizVon == nil {
			message.Err = fmt.Errorf("quiz browser is not connected")
			return message
		}
		target, err := quizVon.ActiveTarget()
		if err != nil {
			message.Err = fmt.Errorf("find quiz page: %w", err)
			return message
		}
		raw, err := quizVon.Evaluate(target.WebSocketURL, extractor.FillAnswerJS(q, res.Values))
		if err != nil {
			message.Err = fmt.Errorf("fill answer: %w", err)
			return message
		}
		fill, err := extractor.ParseFillResult(raw)
		if err != nil {
			message.Err = err
			return message
		}
		if !fill.OK {
			message.Err = fmt.Errorf("fill verification failed: %s", fill.Error)
			return message
		}
		message.Marked = true
		message.Err = nil
		return message
	}
}

func solveWithMode(q *extractor.Question, searchVon *von.Client, sc solver.Config, mode SolveMode) (*solver.Result, error) {
	research := func(query string) ([]solver.Evidence, error) {
		client := searchVon
		if client == nil || client.HTTPBase != browser.SearchCDPURL || !browser.IsCDPAvailable(browser.SearchCDPURL) {
			isolated, err := browser.EnsureHeadlessSearchBrowser(browser.BrowserInfo{})
			if err != nil {
				return nil, fmt.Errorf("isolated search browser unavailable: %w", err)
			}
			client = isolated
		}
		found, err := client.AdaptiveResearch(query, 60*time.Second)
		if err != nil {
			return nil, err
		}
		evidence := make([]solver.Evidence, 0, len(found))
		for _, item := range found {
			evidence = append(evidence, solver.Evidence{Title: item.Title, URL: item.URL, Passage: item.Passage})
		}
		return evidence, nil
	}

	isText := q.Type == extractor.TypeText || q.Type == extractor.TypeParagraph || len(q.Choices) == 0
	switch mode {
	case ModeLocalAI:
		var localErr error
		if !isText {
			if result, err := solver.SolveLocal(q, nil, sc); err == nil {
				return result, nil
			} else {
				localErr = err
			}
		}
		evidence, webErr := research(q.Text)
		if webErr != nil {
			return nil, fmt.Errorf("Local → Web failed: local=%v; web=%w", localErr, webErr)
		}
		result, err := solver.SolveWeb(q, evidence)
		if err != nil {
			return nil, fmt.Errorf("Local → Web failed: %w", err)
		}
		if !isText {
			result.FallbackPath = "Local → Web"
		}
		return result, nil

	case ModeWebSearch, ModeHybrid:
		if q.Type == extractor.TypeDropdown && len(q.Choices) == 0 {
			return nil, fmt.Errorf("dropdown options could not be read")
		}
		evidence, err := research(q.Text)
		if err != nil {
			return nil, fmt.Errorf("web research required: %w", err)
		}
		web, err := solver.SolveWeb(q, evidence)
		if err != nil || web.LowConfidence {
			query := strings.TrimSpace(q.Row + " " + q.Context)
			if query == "" || query == q.Text {
				query = q.Text + " explanation facts"
			}
			more, retryErr := research(query)
			if retryErr == nil {
				evidence = append(evidence, more...)
				web, err = solver.SolveWeb(q, evidence)
			}
		}
		if err != nil {
			return nil, err
		}
		if mode == ModeWebSearch || isText || web.LowConfidence {
			return web, nil
		}
		local, localErr := solver.SolveLocal(q, solver.RelevantEvidence(q, evidence), sc)
		return solver.ReconcileHybrid(web, local, localErr)

	default:
		return nil, fmt.Errorf("unknown solve mode")
	}
}

func advancePageCmd(v *von.Client) tea.Cmd {
	return func() tea.Msg {
		if v == nil {
			return pageAdvancedMsg{Err: fmt.Errorf("quiz browser is not connected")}
		}
		target, err := v.ActiveTarget()
		if err != nil {
			return pageAdvancedMsg{Err: err}
		}
		raw, err := v.Evaluate(target.WebSocketURL, extractor.AdvancePageJS)
		if err != nil {
			return pageAdvancedMsg{Err: err}
		}
		result, err := extractor.ParseAdvanceResult(raw)
		return pageAdvancedMsg{Result: result, Err: err}
	}
}

func pollShortcutCmd(v *von.Client) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(750 * time.Millisecond)
		if v == nil {
			return shortcutPollMsg{}
		}
		return shortcutPollMsg{targetID: v.PollShortcut(), client: v}
	}
}
