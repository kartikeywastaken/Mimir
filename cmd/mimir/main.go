package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"mimir/internal/browser"
	"mimir/internal/daemon"
	"mimir/internal/laya"
	"mimir/internal/solver"
	"mimir/internal/tui"
	"mimir/internal/von"
)

func main() {
	// --daemon-mode: skip TUI, run Unix Domain Socket solver server.
	// Used by the Rust mimir-daemon to get solve results via IPC.
	if len(os.Args) > 1 && os.Args[1] == "--daemon-mode" {
		runDaemon()
		return
	}

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

// runDaemon starts the Unix Domain Socket server and blocks until SIGINT/SIGTERM.
func runDaemon() {
	sc := solver.ConfigFromEnv()
	if m := os.Getenv("LAYA_MODEL"); m != "" {
		sc.Model = m
	}

	srv := daemon.NewServer(sc)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Printf("mimir daemon: socket=%s\n", daemon.SocketPath())
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "daemon error: %v\n", err)
		os.Exit(1)
	}
}
