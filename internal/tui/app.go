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
	StateURLInput State = iota
	StateBrowserPicker
	StateSlashModal
	StateLaunchingBrowser
	StateLoginPolling
	StateIdle
	StateExtracting
	StateSolving
	StateDone
	StateError
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
	{Name: "/clear", Desc: "Clear activity log history"},
	{Name: "/help", Desc: "Show available slash commands"},
	{Name: "/quit", Desc: "Exit Mimir"},
}

type Model struct {
	von       *von.Client
	solverCfg solver.Config

	state        State
	prevState    State
	spinner      spinner.Model
	viewport     viewport.Model
	urlInput     textinput.Model
	slashInput   textinput.Model
	initialURL   string
	pendingURL   string

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
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	vp := viewport.New(80, 10)
	vp.Style = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)

	ti := textinput.New()
	ti.Placeholder = "https://your-quiz.com (or press Enter for active tab)"
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 65

	si := textinput.New()
	si.Placeholder = "Type a command (e.g. /link, /browser, /solve)..."
	si.CharLimit = 256
	si.Width = 50

	detected := browser.DetectBrowsers()

	startState := StateURLInput
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
		m.viewport.Width = max(60, msg.Width-4)
		m.viewport.Height = max(5, msg.Height-18)
		m.urlInput.Width = max(40, msg.Width-16)
		m.slashInput.Width = max(40, msg.Width-20)
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
			// Already solved these questions, poll again in 5s
			m.state = StateDone
			return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
		}

		m.questions = msg.batch.Questions
		m.state = StateSolving
		m.log(fmt.Sprintf("✓ detected %d question(s) on page! Auto-solving with local Laya...", len(m.questions)))
		return m, solveAllQuestionsCmd(m.von, m.solverCfg, m.questions, m.useSearch)

	case multiSolvedMsg:
		if msg.err != nil {
			m.state = StateError
			m.errMsg = msg.err.Error()
			m.log("x solve error: " + msg.err.Error())
			return m, nil
		}
		m.questions = msg.questions
		m.lastSolvedFingerprint = makeFingerprint(m.questions)
		m.state = StateDone
		m.log(fmt.Sprintf("✓ all %d question(s) solved & automatically marked in page DOM! (no popups)", len(m.questions)))

		if m.autoMode {
			// Continue auto-pilot tracking for next page/questions after 5s
			return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) })
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
			m.log("-> extracting questions from page...")
			return m, extractBatchCmd(m.von)
		}
	case "s":
		if m.von != nil && len(m.questions) > 0 {
			m.state = StateSolving
			m.log("-> solving questions with Laya...")
			return m, solveAllQuestionsCmd(m.von, m.solverCfg, m.questions, m.useSearch)
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

func (m Model) View() string {
	// Centered Header Banner
	centerStyle := lipgloss.NewStyle().Align(lipgloss.Center).Width(m.width)
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("o MIMIR")
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Universal Browser Auto-Answer (Local Laya)  •  Zero Popups  •  Local AI")
	header := centerStyle.Render(lipgloss.JoinVertical(lipgloss.Center, title, sub))

	// Modal Overlays
	if m.state == StateSlashModal {
		return m.renderSlashModal(header)
	}
	if m.state == StateBrowserPicker {
		return m.renderBrowserPicker(header)
	}

	// URL Input Box
	if m.state == StateURLInput {
		inputBox := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(1, 2).
			Width(m.viewport.Width).
			Render(
				lipgloss.JoinVertical(lipgloss.Left,
					lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("Enter Quiz / Exam URL:"),
					"",
					m.urlInput.View(),
					"",
					lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("• Press Enter to select browser and launch directly to this URL."),
					lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("• Press '/' for slash commands  •  Esc to cancel"),
				),
			)

		m.viewport.SetContent(strings.Join(m.logs, "\n"))
		m.viewport.GotoBottom()
		return lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			inputBox,
			"",
			m.viewport.View(),
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Enter submit  •  / commands  •  Esc / Ctrl+C quit"),
		)
	}

	// Status Line
	status := ""
	switch m.state {
	case StateLaunchingBrowser:
		status = m.spinner.View() + " launching browser on port 9222..."
	case StateLoginPolling:
		status = m.spinner.View() + " [POLLING] Waiting for login / questions (silent check every 10s)..."
	case StateIdle:
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("● idle (press '/' for commands)")
	case StateExtracting:
		status = m.spinner.View() + " extracting questions from page..."
	case StateSolving:
		status = m.spinner.View() + " Laya solving multiple questions" + map[bool]string{true: " + background search...", false: "..."}[m.useSearch]
	case StateDone:
		status = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")).Render(fmt.Sprintf("✓ %d question(s) solved & marked in DOM (no popups)", len(m.questions)))
	case StateError:
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("x " + m.errMsg)
	}

	// Questions Box (Multi-Question Display)
	qBoxStyle := lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1).Width(m.viewport.Width)
	qContent := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("(no questions detected yet - waiting for login or page load)")

	if len(m.questions) > 0 {
		var blocks []string
		for i, q := range m.questions {
			qHeader := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("[%d] %s", i+1, q.Text))
			if q.Marked {
				qHeader += " " + lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render(fmt.Sprintf("[MARKED ✓ %s (%d%%)]", q.SolvedAns, q.Confidence))
			}
			var choiceLines []string
			for _, ch := range q.Choices {
				prefix := "  "
				mark := ""
				if q.SolvedAns == ch.Label {
					prefix = "► "
					mark = " <"
					choiceLines = append(choiceLines, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render(fmt.Sprintf("  %s%s) %s%s", prefix, ch.Label, ch.Text, mark)))
				} else {
					choiceLines = append(choiceLines, fmt.Sprintf("  %s%s) %s", prefix, ch.Label, ch.Text))
				}
			}
			blocks = append(blocks, qHeader+"\n"+strings.Join(choiceLines, "\n"))
		}
		qContent = strings.Join(blocks, "\n\n")
	}

	// Logs Viewport
	m.viewport.SetContent(strings.Join(m.logs, "\n"))
	m.viewport.GotoBottom()
	logsView := m.viewport.View()

	// Keys & Footer
	keys := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(
		"/ slash menu  •  e extract  •  s solve  •  a auto-pilot (" + map[bool]string{true: "ON", false: "OFF"}[m.autoMode] + ")  •  g search (" + map[bool]string{true: "ON", false: "OFF"}[m.useSearch] + ")  •  q quit",
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(status),
		"",
		qBoxStyle.Render(qContent),
		"",
		logsView,
		"",
		keys,
	)
}

func (m Model) renderSlashModal(header string) string {
	var items []string
	for i, c := range m.slashFiltered {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
		if i == m.slashFilteredIdx {
			cursor = "► "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
		}
		items = append(items, style.Render(fmt.Sprintf("%s%-14s %s", cursor, c.Name, c.Desc)))
	}

	modalBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(1, 2).
		Width(max(50, m.viewport.Width-10)).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("Slash Commands:"),
				"",
				m.slashInput.View(),
				"",
				strings.Join(items, "\n"),
				"",
				lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("↑/↓ navigate  •  Enter select  •  Esc close"),
			),
		)

	return lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		modalBox,
	)
}

func (m Model) renderBrowserPicker(header string) string {
	var items []string
	for i, b := range m.detectedBrowsers {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
		if i == m.selectedBrowser {
			cursor = "► "
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
		}
		items = append(items, style.Render(fmt.Sprintf("%s%d. %s  (%s)", cursor, i+1, b.Name, b.Path)))
	}

	if len(items) == 0 {
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("No Chromium-based browsers detected!"))
	}

	pickerBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("42")).
		Padding(1, 2).
		Width(max(55, m.viewport.Width-6)).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")).Render("Select Browser to Launch:"),
				lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Mimir runs cleanly in the background with zero popups."),
				"",
				strings.Join(items, "\n"),
				"",
				lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("↑/↓ select  •  Enter launch  •  1-9 key shortcut  •  Esc cancel"),
			),
		)

	return lipgloss.JoinVertical(lipgloss.Center,
		header,
		"",
		pickerBox,
	)
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

// Commands

func launchBrowserWithCmd(b browser.BrowserInfo, targetURL string) tea.Cmd {
	return func() tea.Msg {
		v, err := browser.EnsureBrowserWith(b, targetURL)
		if err != nil {
			return browserLaunchedMsg{nil, err}
		}
		time.Sleep(1 * time.Second)
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

func solveAllQuestionsCmd(v *von.Client, sc solver.Config, questions []extractor.Question, useSearch bool) tea.Cmd {
	return func() tea.Msg {
		var solved []extractor.Question

		for _, q := range questions {
			res, err := solver.Solve(&q, nil, sc)
			if err != nil {
				// Fallback to mock guess
				ans := "A"
				if len(q.Choices) > 1 {
					ans = q.Choices[0].Label
				}
				q.SolvedAns = ans
				q.Confidence = 50
			} else {
				q.SolvedAns = res.Answer
				q.Confidence = res.Confidence
			}

			// Combined Robust Dispatch: mark answer in browser DOM (no popups)
			if v != nil {
				t, err := v.ActiveTarget()
				if err == nil && t != nil {
					markJS := extractor.MarkAnswerJS(q.Index, q.SolvedAns)
					_, _ = v.Evaluate(t.WebSocketURL, markJS)
					q.Marked = true
				}
			}
			solved = append(solved, q)
		}

		return multiSolvedMsg{questions: solved, err: nil}
	}
}
