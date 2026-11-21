package solver

import (
	"context"

	"mimir/internal/extractor"
)

// Backend answers a question locally, optionally grounded in web evidence.
// Implementations must never fabricate an answer: return an error instead.
type Backend interface {
	Name() string
	// CanAnswer reports whether this backend supports the question's shape
	// (e.g. Laya can only choose among options; it cannot write free text).
	CanAnswer(q *extractor.Question) bool
	Answer(ctx context.Context, q *extractor.Question, evidence []Evidence) (*Result, error)
}

// LocalBackend picks the backend for cfg.Engine. Auto prefers the LLM when an
// API key (or a local OpenAI-compatible endpoint) is configured.
func LocalBackend(cfg Config) Backend {
	switch cfg.Engine {
	case EngineLLM:
		return newLLMBackend(cfg)
	case EngineLaya:
		return newLayaBackend(cfg)
	default:
		if cfg.LLM.Configured() {
			return newLLMBackend(cfg)
		}
		return newLayaBackend(cfg)
	}
}
