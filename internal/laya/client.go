package laya

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Client talks to local Laya model via scripts/laya_solve.py
// No API key — runs on-device via Core ML (laya-coreml, ~5ms) or MLX (laya-mlx, ~15ms).
// This is the same family as layaForWeb (ONNX WASM) but native, not browser.
// See https://vishalmysore.github.io/layaForWeb/ (ONNX) vs https://github.com/mizorewww/laya-coreml (CoreML) vs laya-mlx (used by Benny93/laya-browser)

type Client struct {
	PythonBin string // python3
	Script    string // path to scripts/laya_solve.py
	Model     string // LAYA_MODEL env
	Timeout   time.Duration
}

func New() *Client {
	py := os.Getenv("LAYA_PYTHON")
	if py == "" {
		home, _ := os.UserHomeDir()
		candidates := []string{
			".venv/bin/python",
			"./.venv/bin/python",
			filepath.Join(home, "Desktop", "Mimir", ".venv", "bin", "python"),
		}
		for _, cand := range candidates {
			if _, err := os.Stat(cand); err == nil {
				py = cand
				break
			}
		}
		if py == "" {
			py = "python3"
		}
	}
	script := os.Getenv("LAYA_SCRIPT")
	if script == "" {
		// default relative to executable or cwd
		// try ~/Desktop/Mimir/scripts/laya_solve.py first
		home, _ := os.UserHomeDir()
		try := []string{
			filepath.Join(home, "Desktop", "Mimir", "scripts", "laya_solve.py"),
			"scripts/laya_solve.py",
			"./scripts/laya_solve.py",
		}
		for _, t := range try {
			if _, err := os.Stat(t); err == nil {
				script = t
				break
			}
		}
		if script == "" {
			script = try[0]
		}
	}
	model := os.Getenv("LAYA_MODEL")
	if model == "" {
		model = "aac6fef/laya-multilingual-coreml-ane" // default ANE 96 tokens, fastest
	}
	return &Client{PythonBin: py, Script: script, Model: model, Timeout: 120 * time.Second}
}

type Request struct {
	State        string   `json:"state"`
	Question     string   `json:"question"`
	Choices      []Choice `json:"choices"`
	Instructions string   `json:"instructions"`
}

type Choice struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type Response struct {
	Choice        string  `json:"choice"`
	Answer        string  `json:"answer"`
	Confidence    float64 `json:"confidence"` // 0-1
	Probabilities any     `json:"probabilities,omitempty"`
	Backend       string  `json:"backend,omitempty"`
	Model         string  `json:"model,omitempty"`
	Mock          bool    `json:"mock,omitempty"`
	Error         string  `json:"error,omitempty"`
}

// Predict runs local Laya: state + choice question -> answer label + confidence
func (c *Client) Predict(state string, question string, choices []Choice, instructions string) (*Response, error) {
	if instructions == "" {
		instructions = "Which answer correctly answers the question?"
	}
	req := Request{State: state, Question: question, Choices: choices, Instructions: instructions}
	b, _ := json.Marshal(req)

	cmd := exec.Command(c.PythonBin, c.Script)
	cmd.Env = append(os.Environ(),
		"LAYA_MODEL="+c.Model,
		"TMPDIR=/tmp/ramdisk",
		"UV_CACHE_DIR=/tmp/ramdisk/uv-cache",
		"HF_HOME=/tmp/ramdisk/hf-home",
	)
	cmd.Stdin = bytes.NewReader(b)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		if err != nil {
			// include stderr for debugging
			msg := strings.TrimSpace(errBuf.String())
			if msg != "" {
				return nil, fmt.Errorf("laya %s: %w | stderr: %s | out: %s", c.Script, err, msg, out.String())
			}
			return nil, fmt.Errorf("laya failed: %w", err)
		}
	case <-time.After(c.Timeout):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("laya timeout after %s (model loading? first run downloads ~800MB)", c.Timeout)
	}

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse laya response: %w | raw: %s | stderr: %s", err, out.String(), errBuf.String())
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("laya error: %s", resp.Error)
	}
	// normalize answer to label
	resp.Answer = strings.ToUpper(strings.TrimSpace(resp.Answer))
	if len(resp.Answer) > 1 {
		resp.Answer = string(resp.Answer[0])
	}
	return &resp, nil
}

// IsAvailable checks if laya_coreml or laya_mlx is installed
func (c *Client) IsAvailable() bool {
	cmd := exec.Command(c.PythonBin, "-c", "import sys; sys.exit(0 if __import__('importlib').util.find_spec('laya_coreml') or __import__('importlib').util.find_spec('laya_mlx') else 1)")
	return cmd.Run() == nil
}

func (c *Client) Backend() string {
	if c.IsAvailable() {
		return "laya-coreml/mlx (local)"
	}
	return "unavailable (no laya installed)"
}
