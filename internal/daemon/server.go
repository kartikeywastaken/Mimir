// Package daemon exposes Mimir's solver pipeline over a Unix Domain Socket so
// that the Rust mimir-daemon can request answers without running a TUI.
//
// Protocol: newline-delimited JSON over a Unix stream socket.
//
//	Request:
//	  {"id":"<uuid>","text":"<question>","choices":["A. ...", "B. ..."],
//	   "field_type":"mcq|text","context":"<optional extra context>"}
//
//	Response:
//	  {"id":"<uuid>","answer":"B","confidence":87,"reason":"...","fill_strategy":"clipboard|ax","error":""}
//
// The socket path is /tmp/mimir.sock by default; override with MIMIR_SOCK env.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"mimir/internal/extractor"
	"mimir/internal/solver"
)

// DefaultSocketPath is the Unix Domain Socket path used for IPC.
const DefaultSocketPath = "/tmp/mimir.sock"

// SocketPath returns the socket path, preferring MIMIR_SOCK env.
func SocketPath() string {
	if p := os.Getenv("MIMIR_SOCK"); p != "" {
		return p
	}
	return DefaultSocketPath
}

// SolveRequest is the JSON payload sent from the Rust daemon to Go.
type SolveRequest struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`              // question text
	Choices   []string `json:"choices,omitempty"` // ["A. ...", "B. ..."]
	FieldType string   `json:"field_type"`        // "mcq" | "text"
	Context   string   `json:"context,omitempty"` // optional extra context from AX tree
}

// SolveResponse is the JSON payload returned from Go to the Rust daemon.
type SolveResponse struct {
	ID           string `json:"id"`
	Answer       string `json:"answer"`
	Confidence   int    `json:"confidence"`
	Reason       string `json:"reason"`
	FillStrategy string `json:"fill_strategy"` // "clipboard" | "ax"
	Backend      string `json:"backend,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Server is the Unix Domain Socket IPC server.
type Server struct {
	cfg        solver.Config
	socketPath string
}

// NewServer creates a daemon server with the given solver config.
func NewServer(cfg solver.Config) *Server {
	return &Server{cfg: cfg, socketPath: SocketPath()}
}

// ListenAndServe starts the UDS server. It blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	// Remove stale socket file
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon: remove stale socket: %w", err)
	}

	l, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("daemon: listen on %s: %w", s.socketPath, err)
	}
	defer l.Close()
	defer os.Remove(s.socketPath)

	log.Printf("daemon: listening on %s", s.socketPath)

	// Close listener when ctx done
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			log.Printf("daemon: accept error: %v", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req SolveRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(SolveResponse{Error: fmt.Sprintf("bad request JSON: %v", err)})
			continue
		}

		resp := s.solve(ctx, req)
		if err := enc.Encode(resp); err != nil {
			log.Printf("daemon: encode response: %v", err)
			return
		}
	}
}

// solve routes a SolveRequest through the Mimir solver pipeline.
func (s *Server) solve(ctx context.Context, req SolveRequest) SolveResponse {
	resp := SolveResponse{ID: req.ID, FillStrategy: "clipboard"}

	if req.Text == "" {
		resp.Error = "empty question text"
		return resp
	}

	// Build extractor.Question from the daemon request
	q := &extractor.Question{
		Index:   0,
		Text:    req.Text,
		Context: req.Context,
	}

	switch req.FieldType {
	case "text", "":
		q.Type = extractor.TypeText
	default: // "mcq"
		q.Type = extractor.TypeChoice
	}

	// Parse "A. Foo", "B. Bar" style choices
	for i, raw := range req.Choices {
		raw = strings.TrimSpace(raw)
		label := string(rune('A' + i))
		text := raw
		// Strip leading "A. ", "A) " etc.
		if len(raw) >= 3 && (raw[1] == '.' || raw[1] == ')') {
			label = strings.ToUpper(string(raw[0]))
			text = strings.TrimSpace(raw[2:])
		}
		q.Choices = append(q.Choices, extractor.Choice{Label: label, Text: text})
	}

	if len(q.Choices) > 0 {
		q.Type = extractor.TypeChoice
	}

	// Use a short timeout so the Rust side doesn't hang indefinitely.
	_, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()

	// Daemon solve strategy:
	//   - MCQ with choices → SolveLocal (Laya typed decision, fast, private)
	//   - Text fields or no choices → SolveWeb (web research evidence)
	//   - "web" backend override → always SolveWeb
	//   - "local" backend override → always SolveLocal
	backend := s.cfg.Backend
	if backend == "" {
		if q.Type == extractor.TypeChoice && len(q.Choices) > 0 {
			backend = "local"
		} else {
			backend = "web"
		}
	}

	var result *solver.Result
	var solveErr error

	switch backend {
	case "local":
		result, solveErr = solver.SolveLocal(q, nil, s.cfg)
	default: // "web" or unrecognised — use top-level Solve which does research
		// SolveWeb requires pre-fetched evidence; for daemon mode without a
		// running Von browser we use SolveLocal even for text (returns error
		// if no choices) so the Rust side knows the question is unresolved.
		if len(q.Choices) > 0 {
			result, solveErr = solver.SolveLocal(q, nil, s.cfg)
		} else {
			// No choices and no browser: return an unresolved hint
			result = &solver.Result{
				Answer:        "",
				Confidence:    0,
				Reason:        "text question without browser — start Von/laya-browser and retry",
				Backend:       "daemon-noop",
				LowConfidence: true,
			}
		}
	}

	if solveErr != nil {
		resp.Error = solveErr.Error()
		return resp
	}
	if result == nil {
		resp.Error = "solver returned nil"
		return resp
	}

	resp.Answer = result.Answer
	resp.Confidence = result.Confidence
	resp.Reason = result.Reason
	resp.Backend = result.Backend

	// Decide fill strategy hint for the Rust filler.
	// AX fill is possible for native MCQ when confidence is high.
	if q.Type == extractor.TypeChoice && result.Confidence >= 70 {
		resp.FillStrategy = "ax"
	}

	return resp
}
