package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"mimir/internal/browser"
	"mimir/internal/laya"
	"mimir/internal/solver"
	"mimir/internal/tui"
	"mimir/internal/von"
)

func main() {
	initialURL := ""
	if len(os.Args) > 1 {
		initialURL = os.Args[1]
	}

	httpBase := os.Getenv("BROWSER_CDP_URL")
	if httpBase == "" {
		httpBase = os.Getenv("VON_CDP_URL")
	}
	if httpBase == "" {
		httpBase = browser.DefaultCDPURL
	}
	if len(httpBase) > 5 && httpBase[:5] == "ws://" {
		httpBase = "http://" + httpBase[5:]
	}
	if len(httpBase) > 5 && httpBase[len(httpBase)-5:] == "/json" {
		httpBase = httpBase[:len(httpBase)-5]
	}

	v := von.New(httpBase)

	// Laya solver config — no OpenAI key
	sc := solver.ConfigFromEnv()
	if m := os.Getenv("LAYA_MODEL"); m != "" {
		sc.Model = m
	}
	lc := laya.New()
	if sc.Model != "" {
		lc.Model = sc.Model
	}

	m := tui.New(v, sc, initialURL)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
