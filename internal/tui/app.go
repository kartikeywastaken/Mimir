package tui

import (
	"fmt"
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
	StateLoginPolling
	StateIdle
	StateExtracting
	StateSearching
	StateSolving
	StateDone
	StateError
)

type SolveMode int

const (
	ModeLocalAI SolveMode = iota
	ModeWebSearch
	ModeHybrid
)

type SlashCmd struct {
	Name string
	Desc string
}

var allSlashCmds = []SlashCmd{
	{Name: "/link [url]", Desc: "Open a quiz link in your chosen browser & start auto-polling"},
	{Name: "/browser", Desc: "Select active browser (Brave, Chrome, Edge, Arc, Chromium)"},
	{Name: "/solve", Desc: "Force immediate extraction & solving of current page"},
	{Name: "/search", Desc: "Toggle background search augmentation ON/OFF"},
	{Name: "/mode", Desc: "Change solving mode (Local AI, Web Search, Hybrid)"},
	{Name: "/clear", Desc: "Clear activity log history"},
	{Name: "/help", Desc: "Show available slash commands"},
	{Name: "/quit", Desc: "Exit Mimir"},
}

// Purple Theme Palette
var (
	colorBgPurple     = lipgloss.Color("#110722") // deep obsidian purple canvas
	colorCardPurple   = lipgloss.Color("#1c0d38") // card & modal container surface
	colorBorderPurple = lipgloss.Color("#a855f7") // vibrant purple border
	colorBorderDim    = lipgloss.Color("#4c1d95") // subtle divider / inner border
	colorLogoPurple   = lipgloss.Color("#e879f9") // neon magenta / fuchsia logo
	colorSubPurple    = lipgloss.Color("#c084fc") // soft lavender subtitle
	colorMutedPurple  = lipgloss.Color("#8b5cf6") // secondary text
	colorDimPurple    = lipgloss.Color("#7e629f") // hints / placeholders
	colorInputBg      = lipgloss.Color("#251244") // textinput field background
	colorWhite        = lipgloss.Color("#f8fafc") // bright text
	colorGreen        = lipgloss.Color("#4ade80") // success / marked
	colorYellow       = lipgloss.Color("#fde047") // spinner / warning
	colorRed          = lipgloss.Color("#f87171") // error
)

const asciiBanner = `
 M I M I R
`

type Model struct {
	von       *von.Client
	searchVon *von.Client
	solverCfg solver.Config

	state        State
	prevState    State
	spinner      spinner.Model
	viewport     viewport.Model
	urlInput     textinput.Model
	slashInput   textinput.Model
	initialURL   string
	pendingURL   string

	solveMode    SolveMode
	selectedMode int

	// Browser selection
	detectedBrowsers []browser.BrowserInfo
	selectedBrowser  int

	// Slash modal
	slashFilteredIdx int
	slashFiltered    []SlashCmd

	// Questions & solving
	questions          []extractor.Question
	currentQIdx        int
	lastSolvedFingerprint string
	useSearch          bool
	autoMode           bool
	errMsg             string
	logs               []string
	width, height      int
}

func New(v *von.Client, sc solver.Config, initialURL string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(colorYellow)
	vp := viewport.New(70, 6)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	ti := textinput.New()
	ti.Placeholder = "https://your-quiz.com (or press Enter for active tab)"
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 54
	ti.Prompt = "► "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(colorLogoPurple).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Foreground(colorWhite)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorDimPurple)

	si := textinput.New()
	si.Placeholder = "Type a command (e.g. /link, /browser, /solve)..."
	si.CharLimit = 256
	si.Width = 50
	si.Prompt = "► "
	si.PromptStyle = lipgloss.NewStyle().Foreground(colorLogoPurple).Bold(true)
	si.TextStyle = lipgloss.NewStyle().Foreground(colorWhite)
	si.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorDimPurple)

	detected := browser.DetectBrowsers()

	startState := StateModePicker
	logs := []string{"Mimir ready — Local Laya + Universal Browser Support"}
	if initialURL != "" {
		startState = StateBrowserPicker
		logs = append(logs, "Link queued: "+initialURL)
	} else {
		logs = append(logs, "Press '/' for commands, or enter a quiz link below.")
	}

	return Model{
		von:              v,
		solverCfg:        sc,
		state:            startState,
		prevState:        StateIdle,
		spinner:          s,
		viewport:         vp,
		urlInput:         ti,
		slashInput:       si,
		initialURL:       initialURL,
		pendingURL:       initialURL,
		detectedBrowsers: detected,
		selectedBrowser:  0,
		slashFiltered:    allSlashCmds,
		useSearch:        true,
		autoMode:         true,
		logs:             logs,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

// Messages
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
type multiSolvedMsg struct {
	questions []extractor.Question
	err       error
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		bw := max(56, min(74, msg.Width-8))
		m.viewport.Width = max(40, bw-4)
		m.viewport.Height = max(4, min(8, msg.Height-20))
		m.urlInput.Width = max(36, bw-12)
		m.slashInput.Width = max(36, bw-12)
		return m, nil

	case browserLaunchedMsg:
		if msg.err != nil {
			m.state = StateError
			m.errMsg = msg.err.Error()
			m.log("x browser launch failed: " + msg.err.Error())
			return m, nil
		}
		m.von = msg.v
		m.autoMode = true
		m.state = StateExtracting
		bName := "Browser"
		if len(m.detectedBrowsers) > m.selectedBrowser {
			bName = m.detectedBrowsers[m.selectedBrowser].Name
		}
		m.log(fmt.Sprintf("✓ %s connected on port 9222!", bName))

		// Ensure headless search browser is configured on port 9223 for stealth searching
		if m.solveMode != ModeLocalAI {
			var b browser.BrowserInfo
			if len(m.detectedBrowsers) > m.selectedBrowser {
				b = m.detectedBrowsers[m.selectedBrowser]
			}
			go func() {
				_, _ = browser.EnsureHeadlessSearchBrowser(b)
			}()
			m.searchVon = von.New(browser.SearchCDPURL)
			m.log("✓ Headless search browser configured on port 9223 (zero search tabs in quiz browser)")
		}

		m.log("-> checking page for questions or login...")
		return m, tea.Batch(m.spinner.Tick, extractBatchCmd(m.von))

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
			m.log("🎯 Detected embedded quiz iframe: " + msg.batch.EmbeddedQuizURL)
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
				m.log("⏳ Waiting for login / questions to load (silent check every 10s)...")
			}
			// Schedule silent 10-second poll
			return m, tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
		}

		// Questions found! Check if question set has changed
		fingerprint := makeFingerprint(msg.batch.Questions)
		if fingerprint == m.lastSolvedFingerprint && m.lastSolvedFingerprint != "" {
			m.state = StateDone
			m.log(fmt.Sprintf("● questions unchanged (%d question(s) already solved & marked). Waiting for new questions...", len(m.questions)))
			if m.autoMode {
				return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
			}
			return m, nil
		}

		m.questions = msg.batch.Questions
		m.currentQIdx = 0
		if m.solveMode != ModeLocalAI {
			m.state = StateSearching
			m.log(fmt.Sprintf("✓ detected %d quiz question(s) on page! Starting step-by-step solving...", len(m.questions)))
			m.log(fmt.Sprintf("[Q1/%d] 🌐 Searching Google in headless background browser (port 9223)...", len(m.questions)))
		} else {
			m.state = StateSolving
			m.log(fmt.Sprintf("✓ detected %d quiz question(s) on page! Starting step-by-step solving...", len(m.questions)))
			m.log(fmt.Sprintf("[Q1/%d] 🧠 Evaluating choices with Local Laya...", len(m.questions)))
		}
		return m, solveSingleQuestionCmd(m.von, m.searchVon, m.solverCfg, m.questions, 0, m.solveMode)

	case questionStepSolvedMsg:
		idx := msg.Index
		if idx >= 0 && idx < len(m.questions) {
			m.questions[idx].SolvedAns = msg.SolvedAns
			m.questions[idx].Confidence = msg.Confidence
			m.questions[idx].Marked = true
		}

		if m.solveMode != ModeLocalAI {
			if len(msg.Snippets) > 0 {
				m.log(fmt.Sprintf("[Q%d/%d] 🌐 Google snippets found -> Laya chose %s (%d%%) ✓ marked in DOM", idx+1, msg.Total, msg.SolvedAns, msg.Confidence))
			} else {
				m.log(fmt.Sprintf("[Q%d/%d] ! No Google snippets found -> Local Laya fallback chose %s (%d%%) ✓ marked in DOM", idx+1, msg.Total, msg.SolvedAns, msg.Confidence))
			}
		} else {
			m.log(fmt.Sprintf("[Q%d/%d] 🧠 Local Laya chose %s (%d%%) ✓ marked in DOM", idx+1, msg.Total, msg.SolvedAns, msg.Confidence))
		}

		if idx+1 < msg.Total {
			m.currentQIdx = idx + 1
			delay := 400 * time.Millisecond
			if m.solveMode != ModeLocalAI {
				delay = 2 * time.Second // 2s delay between background search requests
			}
			return m, tea.Tick(delay, func(t time.Time) tea.Msg {
				return nextQuestionMsg{Index: idx + 1}
			})
		}

		// All questions completed!
		m.state = StateDone
		m.lastSolvedFingerprint = makeFingerprint(m.questions)
		m.log(fmt.Sprintf("✓ All %d question(s) solved & automatically marked in page DOM! (no popups)", len(m.questions)))

		if m.autoMode {
			return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
		}
		return m, nil

	case nextQuestionMsg:
		if msg.Index < len(m.questions) {
			if m.solveMode != ModeLocalAI {
				m.state = StateSearching
				m.log(fmt.Sprintf("[Q%d/%d] 🌐 Searching Google in headless background browser...", msg.Index+1, len(m.questions)))
			} else {
				m.state = StateSolving
				m.log(fmt.Sprintf("[Q%d/%d] 🧠 Evaluating choices with Local Laya...", msg.Index+1, len(m.questions)))
			}
			return m, solveSingleQuestionCmd(m.von, m.searchVon, m.solverCfg, m.questions, msg.Index, m.solveMode)
		}
		return m, nil

	case pollMsg:
		if m.autoMode && m.von != nil {
			return m, extractBatchCmd(m.von)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyPress(msg)
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
	val = strings.TrimPrefix(val, "/")
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

	// 0. Mode Picker Modal
	if m.state == StateModePicker {
		switch msg.String() {
		case "esc":
			m.solveMode = ModeLocalAI
			m.state = StateURLInput
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
			m.state = StateURLInput
			return m, nil
		case "1", "2", "3":
			idx := int(msg.String()[0] - '1')
			m.solveMode = SolveMode(idx)
			m.state = StateURLInput
			return m, nil
		}
		return m, nil
	}

	// 1. Slash Command Modal
	if m.state == StateSlashModal {
		switch msg.String() {
		case "esc":
			m.state = m.prevState
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
			m.state = StateIdle
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
			return m, nil
		case "/":
			m.openSlashModal()
			return m, nil
		case "enter":
			raw := strings.TrimSpace(m.urlInput.Value())
			m.pendingURL = raw
			m.state = StateBrowserPicker
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
	case "e":
		if m.von != nil {
			m.state = StateExtracting
			m.lastSolvedFingerprint = ""
			m.log("-> extracting questions from page...")
			return m, extractBatchCmd(m.von)
		}
	case "s":
		if m.von != nil && len(m.questions) > 0 {
			m.currentQIdx = 0
			for i := range m.questions {
				m.questions[i].Marked = false
			}
			if m.solveMode != ModeLocalAI {
				m.state = StateSearching
				m.log(fmt.Sprintf("-> re-solving %d question(s) with Web Search...", len(m.questions)))
				m.log(fmt.Sprintf("[Q1/%d] 🌐 Searching Google in headless background browser (port 9223)...", len(m.questions)))
			} else {
				m.state = StateSolving
				m.log(fmt.Sprintf("-> re-solving %d question(s) with Local Laya...", len(m.questions)))
				m.log(fmt.Sprintf("[Q1/%d] 🧠 Evaluating choices with Local Laya...", len(m.questions)))
			}
			return m, solveSingleQuestionCmd(m.von, m.searchVon, m.solverCfg, m.questions, 0, m.solveMode)
		}
	case "a":
		m.autoMode = !m.autoMode
		m.log(fmt.Sprintf("Auto-pilot mode toggled: %v", m.autoMode))
		if m.autoMode && m.von != nil {
			return m, extractBatchCmd(m.von)
		}
	case "g":
		m.useSearch = !m.useSearch
		m.log(fmt.Sprintf("Background search toggled: %v", m.useSearch))
	case "c":
		m.logs = []string{"Logs cleared."}
		m.viewport.SetContent("")
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
	m.slashInput.SetValue("/")
	m.slashInput.CursorEnd()
	m.slashInput.Focus()
	m.slashFiltered = allSlashCmds
	m.slashFilteredIdx = 0
}

func (m *Model) executeSlashCommand(input string) (tea.Model, tea.Cmd) {
	input = strings.TrimSpace(input)
	cmdName := input
	cmdArg := ""
	if strings.HasPrefix(input, "/") {
		parts := strings.SplitN(input, " ", 2)
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

	m.state = StateIdle

	switch cmdName {
	case "/link":
		if cmdArg != "" {
			m.pendingURL = cmdArg
			m.state = StateBrowserPicker
			return m, nil
		}
		m.state = StateURLInput
		m.urlInput.SetValue("")
		m.urlInput.Focus()
		return m, nil

	case "/browser":
		m.state = StateBrowserPicker
		return m, nil

	case "/solve":
		if m.von != nil {
			m.state = StateExtracting
			m.log("-> /solve: extracting and solving...")
			return m, extractBatchCmd(m.von)
		}
		m.log("! no browser attached yet. Use /link <url> first.")
		return m, nil

	case "/search":
		m.useSearch = !m.useSearch
		m.log(fmt.Sprintf("✓ background search toggled: %v", m.useSearch))
		return m, nil

	case "/mode":
		m.state = StateModePicker
		m.selectedMode = int(m.solveMode)
		return m, nil

	case "/clear":
		m.logs = []string{"Logs cleared."}
		m.viewport.SetContent("")
		return m, nil

	case "/help":
		m.log("Available Slash Commands:")
		for _, c := range allSlashCmds {
			m.log(fmt.Sprintf("  %-12s - %s", c.Name, c.Desc))
		}
		return m, nil

	case "/quit":
		return m, tea.Quit
	}

	m.log(fmt.Sprintf("! unknown slash command: %s (type /help for commands)", input))
	return m, nil
}

func (m Model) renderHeader() string {
	logo := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorLogoPurple).
		Background(colorBgPurple).
		Render(asciiBanner)

	sub := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorSubPurple).
		Background(colorBgPurple).
		Render("Universal Browser Auto-Answer  •  Local Laya AI  •  Zero Popups")

	return lipgloss.JoinVertical(lipgloss.Center, logo, "", sub)
}

func (m Model) placeCentered(content string) string {
	w := m.width
	h := m.height
	cw := lipgloss.Width(content)
	ch := lipgloss.Height(content)
	if w < cw {
		w = cw
	}
	if h < ch {
		h = ch
	}
	return lipgloss.Place(
		w, h,
		lipgloss.Center, lipgloss.Center,
		content,
		lipgloss.WithWhitespaceBackground(colorBgPurple),
	)
}

func (m Model) renderModePicker(header string) string {
	boxWidth := max(58, min(72, m.width-8))
	
	options := []struct{ name, desc string }{
		{"1. 🧠 Local AI", "(Laya on-device, fully offline)"},
		{"2. 🌐 Web Search", "(Google lookup via your browser, needs internet)"},
		{"3. ⚡ Hybrid", "(search + AI, best accuracy)"},
	}
	
	var items []string
	for i, opt := range options {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorWhite).Background(colorCardPurple)
		if i == m.selectedMode {
			cursor = "► "
			style = lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple)
		}
		items = append(items, style.Render(fmt.Sprintf("%s%s %s", cursor, opt.name, opt.desc)))
	}

	pickerBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderPurple).
		Background(colorCardPurple).
		Padding(1, 2).
		Width(boxWidth).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("Select Solving Mode:"),
				"",
				strings.Join(items, "\n"),
				"",
				lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("↑/↓ select  •  Enter confirm  •  1-3 shortcuts  •  Esc skip (defaults to Local AI)"),
			),
		)

	content := lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		pickerBox,
	)
	return m.placeCentered(content)
}

func (m Model) renderURLInput(header string) string {
	boxWidth := max(58, min(72, m.width-8))

	inputBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderPurple).
		Background(colorCardPurple).
		Padding(1, 2).
		Width(boxWidth).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("Enter Quiz / Exam URL:"),
				"",
				lipgloss.NewStyle().Background(colorInputBg).Padding(0, 1).Render(m.urlInput.View()),
				"",
				lipgloss.NewStyle().Foreground(colorSubPurple).Background(colorCardPurple).Render("• Press Enter to select browser and launch directly to this URL."),
				lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("• Press '/' for slash commands  •  Esc to cancel"),
			),
		)

	// Activity log view
	m.viewport.Width = boxWidth - 4
	m.viewport.Height = 4
	m.viewport.SetContent(strings.Join(m.logs, "\n"))
	m.viewport.GotoBottom()

	logBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderDim).
		Background(colorCardPurple).
		Padding(0, 1).
		Width(boxWidth).
		Render(m.viewport.View())

	footer := lipgloss.NewStyle().
		Foreground(colorDimPurple).
		Render("Enter submit  •  / commands  •  Esc / Ctrl+C quit")

	content := lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		inputBox,
		"",
		logBox,
		"",
		footer,
	)

	return m.placeCentered(content)
}

func (m Model) renderSlashModal(header string) string {
	boxWidth := max(58, min(72, m.width-8))
	var items []string
	for i, c := range m.slashFiltered {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorWhite).Background(colorCardPurple)
		if i == m.slashFilteredIdx {
			cursor = "► "
			style = lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple)
		}
		items = append(items, style.Render(fmt.Sprintf("%s%-14s %s", cursor, c.Name, c.Desc)))
	}

	modalBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderPurple).
		Background(colorCardPurple).
		Padding(1, 2).
		Width(boxWidth).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("Slash Commands:"),
				"",
				lipgloss.NewStyle().Background(colorInputBg).Padding(0, 1).Render(m.slashInput.View()),
				"",
				strings.Join(items, "\n"),
				"",
				lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("↑/↓ navigate  •  Enter select  •  Esc close"),
			),
		)

	content := lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		modalBox,
	)
	return m.placeCentered(content)
}

func (m Model) renderBrowserPicker(header string) string {
	boxWidth := max(58, min(72, m.width-8))
	var items []string
	for i, b := range m.detectedBrowsers {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorWhite).Background(colorCardPurple)
		if i == m.selectedBrowser {
			cursor = "► "
			style = lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Background(colorCardPurple)
		}
		items = append(items, style.Render(fmt.Sprintf("%s%d. %s  (%s)", cursor, i+1, b.Name, b.Path)))
	}

	if len(items) == 0 {
		items = append(items, lipgloss.NewStyle().Foreground(colorRed).Background(colorCardPurple).Render("No Chromium-based browsers detected!"))
	}

	pickerBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderPurple).
		Background(colorCardPurple).
		Padding(1, 2).
		Width(boxWidth).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("Select Browser to Launch:"),
				lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("Mimir runs cleanly in the background with zero popups."),
				"",
				strings.Join(items, "\n"),
				"",
				lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("↑/↓ select  •  Enter launch  •  1-9 key shortcut  •  Esc cancel"),
			),
		)

	content := lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		pickerBox,
	)
	return m.placeCentered(content)
}

func (m Model) View() string {
	header := m.renderHeader()

	if m.state == StateModePicker {
		return m.renderModePicker(header)
	}
	if m.state == StateSlashModal {
		return m.renderSlashModal(header)
	}
	if m.state == StateBrowserPicker {
		return m.renderBrowserPicker(header)
	}
	if m.state == StateURLInput {
		return m.renderURLInput(header)
	}

	dashWidth := max(60, min(84, m.width-6))

	// Status line text
	status := ""
	switch m.state {
	case StateLaunchingBrowser:
		status = m.spinner.View() + " Launching browser on port 9222..."
	case StateLoginPolling:
		status = m.spinner.View() + " Waiting for quiz page to load (silent check every 10s)..."
	case StateIdle:
		status = "● Ready"
	case StateExtracting:
		status = m.spinner.View() + " Scanning page for questions..."
	case StateSearching:
		status = m.spinner.View() + " Searching Google in headless background browser (port 9223)..."
	case StateSolving:
		status = m.spinner.View() + " Evaluating answers with Local Laya..."
	case StateDone:
		status = fmt.Sprintf("✓ %d question(s) solved & marked in DOM", len(m.questions))
	case StateError:
		status = "x " + m.errMsg
	}

	// 1. Title bar inside the unified card
	browserName := "Browser Connected"
	if m.selectedBrowser < len(m.detectedBrowsers) {
		browserName = m.detectedBrowsers[m.selectedBrowser].Name
	}
	modeStr := "Local AI"
	if m.solveMode == ModeWebSearch {
		modeStr = "Web Search (Headless 9223)"
	} else if m.solveMode == ModeHybrid {
		modeStr = "Hybrid (Headless 9223)"
	}

	titleBar := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("M I M I R"),
		lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("  ❖  "),
		lipgloss.NewStyle().Foreground(colorSubPurple).Background(colorCardPurple).Render(browserName),
		lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("  ❖  "),
		lipgloss.NewStyle().Foreground(colorWhite).Background(colorCardPurple).Render("Mode: "+modeStr),
	)

	statusLine := lipgloss.NewStyle().Foreground(colorYellow).Background(colorCardPurple).Render("Status: " + status)
	divider := lipgloss.NewStyle().Foreground(colorBorderDim).Background(colorCardPurple).Render(strings.Repeat("─", dashWidth-4))

	// 2. Questions Section (Clean & Compact)
	var qLines []string
	qHeader := lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("QUESTIONS")
	qLines = append(qLines, qHeader)

	if len(m.questions) == 0 {
		qLines = append(qLines, lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("  (no questions detected yet - waiting for quiz page load)"))
	} else {
		for i, q := range m.questions {
			qText := q.Text
			if len(qText) > 60 {
				qText = qText[:57] + "..."
			}
			line := fmt.Sprintf("  [%d] %s", i+1, qText)
			if q.Marked {
				ansDisplay := q.SolvedAns
				if len(ansDisplay) > 28 {
					ansDisplay = ansDisplay[:25] + "..."
				}
				line += "  " + lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Background(colorCardPurple).Render(fmt.Sprintf("→ %s ✓ (%d%%)", ansDisplay, q.Confidence))
			}
			qLines = append(qLines, lipgloss.NewStyle().Foreground(colorWhite).Background(colorCardPurple).Render(line))
		}
	}

	// 3. Activity Section (Last 5 log lines, clean monospace)
	var logLines []string
	logHeader := lipgloss.NewStyle().Bold(true).Foreground(colorLogoPurple).Background(colorCardPurple).Render("ACTIVITY")
	logLines = append(logLines, logHeader)
	start := 0
	if len(m.logs) > 5 {
		start = len(m.logs) - 5
	}
	for _, l := range m.logs[start:] {
		logLines = append(logLines, lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorCardPurple).Render("  "+l))
	}

	// Build Unified Card Content
	cardContent := lipgloss.JoinVertical(lipgloss.Left,
		titleBar,
		statusLine,
		divider,
		strings.Join(qLines, "\n"),
		divider,
		strings.Join(logLines, "\n"),
	)

	unifiedCard := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorderPurple).
		Background(colorCardPurple).
		Padding(1, 2).
		Width(dashWidth).
		Render(cardContent)

	footer := lipgloss.NewStyle().Foreground(colorDimPurple).Background(colorBgPurple).Render(
		"/ commands  •  s solve  •  e extract  •  m mode  •  q quit",
	)

	return m.placeCentered(lipgloss.JoinVertical(lipgloss.Center,
		unifiedCard,
		"",
		footer,
	))
}

func (m *Model) log(s string) {
	ts := time.Now().Format("15:04:05")
	m.logs = append(m.logs, fmt.Sprintf("[%s] %s", ts, s))
	if len(m.logs) > 200 {
		m.logs = m.logs[len(m.logs)-200:]
	}
	m.viewport.GotoBottom()
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
		res, err := v.Evaluate(t.WebSocketURL, extractor.ExtractAllJS)
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
	Index      int
	Total      int
	SolvedAns  string
	Confidence int
	Snippets   []string
	Query      string
	Err        error
}

type nextQuestionMsg struct {
	Index int
}

func solveSingleQuestionCmd(quizVon, searchVon *von.Client, sc solver.Config, questions []extractor.Question, idx int, solveMode SolveMode) tea.Cmd {
	return func() tea.Msg {
		if idx >= len(questions) {
			return questionStepSolvedMsg{Index: idx, Total: len(questions), SolvedAns: "A", Confidence: 50}
		}

		q := questions[idx]
		var snippets []string
		var searchErr error
		var query string

		if solveMode != ModeLocalAI {
			query = q.Text
			for _, c := range q.Choices {
				query += " " + c.Text
			}

			// Ensure search client uses dedicated headless browser on port 9223 (zero search tabs in quiz browser!)
			clientToSearch := searchVon
			if clientToSearch == nil || !browser.IsCDPAvailable(browser.SearchCDPURL) {
				sClient, err := browser.EnsureHeadlessSearchBrowser(browser.BrowserInfo{})
				if err == nil && sClient != nil {
					clientToSearch = sClient
				} else {
					clientToSearch = quizVon
				}
			}

			if clientToSearch != nil {
				snippets, searchErr = clientToSearch.BackgroundSearch(query, 10*time.Second)
			}
		}

		res, err := solver.Solve(&q, snippets, sc)
		ans := "A"
		conf := 50
		if err == nil && res != nil {
			ans = res.Answer
			conf = res.Confidence
		} else if len(q.Choices) > 0 {
			ans = q.Choices[0].Label
		}

		// Mark in active quiz browser DOM
		if quizVon != nil {
			t, err := quizVon.ActiveTarget()
			if err == nil && t != nil {
				markJS := extractor.MarkAnswerJS(q.Index, ans)
				_, _ = quizVon.Evaluate(t.WebSocketURL, markJS)
			}
		}

		return questionStepSolvedMsg{
			Index:      idx,
			Total:      len(questions),
			SolvedAns:  ans,
			Confidence: conf,
			Snippets:   snippets,
			Query:      query,
			Err:        searchErr,
		}
	}
}
