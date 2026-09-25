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
	"mimir/internal/overlay"
	"mimir/internal/solver"
	"mimir/internal/von"
)

type State int

const (
	StateURLInput State = iota
	StateLaunchingBrowser
	StateIdle
	StateExtracting
	StateSolving
	StateDone
	StateError
)

type Model struct {
	von       *von.Client
	solverCfg solver.Config

	state      State
	spinner    spinner.Model
	viewport   viewport.Model
	urlInput   textinput.Model
	initialURL string

	question *extractor.Question
	result   *solver.Result
	snippets []string

	lastSolvedQuestion string
	useSearch          bool
	autoMode           bool
	overlayShown       bool
	errMsg             string
	logs               []string
	width, height      int
}

func New(v *von.Client, sc solver.Config, initialURL string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	vp := viewport.New(80, 12)
	vp.Style = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)

	ti := textinput.New()
	ti.Placeholder = "https://your-quiz.com (or press Enter for active tab)"
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 65

	startState := StateURLInput
	logs := []string{"Mimir ready — Local Laya + Brave Browser"}
	if initialURL != "" {
		startState = StateLaunchingBrowser
		logs = append(logs, "Launching Brave Browser to: "+initialURL)
	} else {
		logs = append(logs, "Enter your quiz link below to launch Brave automatically.")
	}

	return Model{
		von: v, solverCfg: sc, state: startState, spinner: s, viewport: vp,
		urlInput: ti, initialURL: initialURL,
		useSearch: true, logs: logs,
	}
}

func (m Model) Init() tea.Cmd {
	if m.state == StateLaunchingBrowser {
		return tea.Batch(m.spinner.Tick, launchBrowserCmd(m.initialURL))
	}
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

type tickMsg time.Time
type extractedMsg struct {
	q   *extractor.Question
	err error
}
type solvedMsg struct {
	r   *solver.Result
	err error
}
type searchMsg struct {
	snippets []string
	err      error
}
type overlayMsg struct {
	err      error
	injected bool
}
type browserLaunchedMsg struct {
	v   *von.Client
	err error
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = max(60, msg.Width-4)
		m.viewport.Height = max(6, msg.Height-16)
		m.urlInput.Width = max(40, msg.Width-16)
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
		m.log("✓ Brave Browser connected on port 9222! Entering Auto Mode...")
		m.log("-> extracting initial question from page...")
		return m, tea.Batch(m.spinner.Tick, extractCmd(m.von))

	case tea.KeyMsg:
		if m.state == StateURLInput {
			switch msg.String() {
			case "ctrl+c", "esc":
				return m, tea.Quit
			case "enter":
				raw := strings.TrimSpace(m.urlInput.Value())
				m.state = StateLaunchingBrowser
				if raw != "" {
					m.log("-> launching Brave Browser with port 9222 to: " + raw)
				} else {
					m.log("-> connecting to active tab on port 9222...")
				}
				return m, tea.Batch(m.spinner.Tick, launchBrowserCmd(raw))
			}
			var cmd tea.Cmd
			m.urlInput, cmd = m.urlInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "e":
			if m.state == StateExtracting || m.state == StateSolving {
				return m, nil
			}
			m.state = StateExtracting
			m.errMsg = ""
			m.log("-> extracting from active tab...")
			return m, tea.Batch(m.spinner.Tick, extractCmd(m.von))
		case "s":
			if m.state == StateSolving || m.state == StateExtracting {
				return m, nil
			}
			if m.question == nil {
				m.log("! no question yet - press 'e' first")
				return m, nil
			}
			m.state = StateSolving
			m.log("-> solving via Laya (local, no API key)" + map[bool]string{true: " + background search", false: ""}[m.useSearch] + "...")
			return m, tea.Batch(m.spinner.Tick, solveCmd(m.question, m.snippets, m.solverCfg, m.von, m.useSearch))
		case "o":
			if m.question == nil || m.result == nil {
				m.log("! nothing to overlay - solve first")
				return m, nil
			}
			if m.overlayShown {
				return m, removeOverlayCmd(m.von)
			}
			m.log("-> injecting overlay (no tab switch)...")
			return m, injectOverlayCmd(m.von, m.question, m.result)
		case "a":
			m.autoMode = !m.autoMode
			if m.autoMode {
				m.log("! AUTO mode ON - monitoring for question changes")
				m.state = StateExtracting
				return m, tea.Batch(m.spinner.Tick, extractCmd(m.von))
			}
			m.log("AUTO mode OFF")
			return m, nil
		case "g":
			m.useSearch = !m.useSearch
			m.log(fmt.Sprintf("background Google search: %v", map[bool]string{true: "ON (hidden tab)", false: "OFF"}[m.useSearch]))
			return m, nil
		case "c":
			m.logs = []string{"cleared"}
			m.viewport.SetContent("")
			return m, nil
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case extractedMsg:
		if msg.err != nil {
			m.state = StateError
			m.errMsg = msg.err.Error()
			m.log("x extract failed: " + msg.err.Error())
			if m.autoMode {
				// retry extraction after 2s in auto mode
				return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
			}
			return m, nil
		}
		m.question = msg.q

		if m.autoMode {
			// Smart change detection: If question text matches last solved, wait and re-check
			if m.result != nil && msg.q.Text == m.lastSolvedQuestion {
				m.state = StateDone
				return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
			}
			m.lastSolvedQuestion = msg.q.Text
			m.state = StateSolving
			m.log(fmt.Sprintf("✓ question detected: %q (%d choices)", truncate(msg.q.Text, 80), len(msg.q.Choices)))
			for _, ch := range msg.q.Choices {
				m.log(fmt.Sprintf("  %s) %s", ch.Label, truncate(ch.Text, 70)))
			}
			m.log("-> auto-solving with local Laya...")
			return m, solveCmd(m.question, nil, m.solverCfg, m.von, m.useSearch)
		}

		m.state = StateIdle
		m.log(fmt.Sprintf("✓ extracted: %q (%d choices)", truncate(msg.q.Text, 80), len(msg.q.Choices)))
		for _, ch := range msg.q.Choices {
			m.log(fmt.Sprintf("  %s) %s", ch.Label, truncate(ch.Text, 70)))
		}
		return m, nil

	case searchMsg:
		if msg.err != nil {
			m.log("! background search failed: " + msg.err.Error() + " - solving without it")
		} else {
			m.snippets = msg.snippets
			m.log(fmt.Sprintf("✓ background search got %d snippets (tab closed, no switch)", len(msg.snippets)))
			for i, s := range msg.snippets {
				if i < 2 {
					m.log("  [search] " + truncate(s, 100))
				}
			}
		}
		return m, nil

	case solvedMsg:
		if msg.err != nil {
			m.state = StateError
			m.errMsg = msg.err.Error()
			m.log("x solve failed: " + msg.err.Error())
			if m.autoMode {
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
			}
			return m, nil
		}
		m.result = msg.r
		m.state = StateDone
		m.log(fmt.Sprintf("✓ solved: %s (confidence %d%%) %s", msg.r.Answer, msg.r.Confidence, map[bool]string{true: "[search]", false: ""}[msg.r.SearchUsed]))
		m.log("  reason: " + msg.r.Reason)
		if m.autoMode {
			m.log("-> auto: injecting overlay onto page...")
			return m, injectOverlayCmd(m.von, m.question, m.result)
		}
		return m, nil

	case overlayMsg:
		if msg.err != nil {
			m.log("x overlay failed: " + msg.err.Error())
		} else {
			m.overlayShown = msg.injected
			if msg.injected {
				m.log("✓ overlay injected into Brave Browser (no tab switched)")
			} else {
				m.log("✓ overlay removed")
			}
		}
		if m.autoMode && m.overlayShown {
			// Poll for next question after 2s
			return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
		}
		return m, nil

	case tickMsg:
		if m.autoMode {
			m.state = StateExtracting
			return m, extractCmd(m.von)
		}
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).Render("o MIMIR")
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Brave Browser Auto-Answer (Local Laya)  |  no tab switch  |  you choose")
	header := lipgloss.JoinVertical(lipgloss.Left, title, sub)

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
					lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("• Press Enter to launch Brave Browser directly to this URL."),
					lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("• Or press Enter without a URL to attach to an active browser tab."),
				),
			)

		m.viewport.SetContent(strings.Join(m.logs, "\n"))
		return lipgloss.JoinVertical(lipgloss.Left,
			header,
			"",
			inputBox,
			"",
			m.viewport.View(),
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Enter submit  •  Esc / Ctrl+C quit"),
		)
	}

	status := ""
	switch m.state {
	case StateLaunchingBrowser:
		status = m.spinner.View() + " launching Brave Browser on port 9222..."
	case StateIdle:
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("* idle")
	case StateExtracting:
		status = m.spinner.View() + " extracting from page..."
	case StateSolving:
		status = m.spinner.View() + " Laya solving" + map[bool]string{true: " + background search...", false: "..."}[m.useSearch]
	case StateDone:
		if m.result != nil {
			c := "205"
			if m.result.Confidence >= 80 {
				c = "42"
			} else if m.result.Confidence < 60 {
				c = "214"
			}
			status = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(c)).Render(fmt.Sprintf("✓ %s  %d%%  %s", m.result.Answer, m.result.Confidence, m.result.Reason))
		}
	case StateError:
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("x " + m.errMsg)
	}

	// question box
	qBox := lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1).Width(m.viewport.Width).Render
	qContent := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("(no question yet - press 'e')")
	if m.question != nil {
		lines := []string{lipgloss.NewStyle().Bold(true).Render(m.question.Text)}
		for _, ch := range m.question.Choices {
			mark := " "
			if m.result != nil && ch.Label == m.result.Answer {
				mark = "<"
				lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render(fmt.Sprintf("  %s) %s %s", ch.Label, ch.Text, mark)))
			} else {
				lines = append(lines, fmt.Sprintf("  %s) %s", ch.Label, ch.Text))
			}
		}
		qContent = strings.Join(lines, "\n")
	}

	// logs viewport
	m.viewport.SetContent(strings.Join(m.logs, "\n"))
	m.viewport.GotoBottom()
	logsView := m.viewport.View()

	keys := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(
		"e extract  •  s solve  •  o overlay  •  a auto-loop  •  g toggle search (" + map[bool]string{true: "ON", false: "OFF"}[m.useSearch] + ")  •  c clear  •  q quit",
	)
	if m.autoMode {
		keys += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("[AUTO PILOT ON]")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(status),
		"",
		qBox(qContent),
		"",
		logsView,
		"",
		keys,
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

func launchBrowserCmd(targetURL string) tea.Cmd {
	return func() tea.Msg {
		if err := browser.LaunchBrowser(targetURL); err != nil {
			return browserLaunchedMsg{nil, err}
		}
		v, err := browser.WaitForCDP(browser.DefaultCDPURL, 12*time.Second)
		if err != nil {
			return browserLaunchedMsg{nil, err}
		}
		time.Sleep(1 * time.Second)
		return browserLaunchedMsg{v, nil}
	}
}

func extractCmd(v *von.Client) tea.Cmd {
	return func() tea.Msg {
		if v == nil {
			return extractedMsg{nil, fmt.Errorf("browser client not initialized")}
		}
		t, err := v.ActiveTarget()
		if err != nil {
			return extractedMsg{nil, err}
		}
		raw, err := v.Evaluate(t.WebSocketURL, extractor.ExtractJS)
		if err != nil {
			return extractedMsg{nil, err}
		}
		q, err := extractor.ParseResult(raw)
		return extractedMsg{q, err}
	}
}

func solveCmd(q *extractor.Question, prevSnippets []string, cfg solver.Config, v *von.Client, useSearch bool) tea.Cmd {
	return func() tea.Msg {
		var snippets []string
		if useSearch && len(prevSnippets) == 0 && v != nil {
			sn, err := v.BackgroundSearch(q.Text, 8*time.Second)
			if err == nil {
				snippets = sn
			}
		} else {
			snippets = prevSnippets
		}
		r, err := solver.Solve(q, snippets, cfg)
		return solvedMsg{r, err}
	}
}

func injectOverlayCmd(v *von.Client, q *extractor.Question, r *solver.Result) tea.Cmd {
	return func() tea.Msg {
		if v == nil {
			return overlayMsg{err: fmt.Errorf("browser not connected"), injected: true}
		}
		t, err := v.ActiveTarget()
		if err != nil {
			return overlayMsg{err: err, injected: true}
		}
		html := overlay.HTML(q, r)
		err = v.InjectOverlay(t.WebSocketURL, html)
		return overlayMsg{err: err, injected: true}
	}
}

func removeOverlayCmd(v *von.Client) tea.Cmd {
	return func() tea.Msg {
		if v == nil {
			return overlayMsg{err: nil, injected: false}
		}
		t, err := v.ActiveTarget()
		if err != nil {
			return overlayMsg{err: err, injected: false}
		}
		return overlayMsg{err: v.RemoveOverlay(t.WebSocketURL), injected: false}
	}
}
