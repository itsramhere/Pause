package app

import (
	"context"

	"github.com/ramst/pause/internal/llm"
)

// extractSignals runs the advisory Mistral reading for one analysis. It is
// allowed to fail in every possible way: the result is recorded with a
// terminal status and never gates release, never touches packet.Missing and
// never changes a money path. Text is redacted inside the client before any
// outbound request, so no caller can send raw contact channels.
func (s *Service) extractSignals(ctx context.Context, text string) llm.Signals {
	if s.LLM == nil {
		return llm.Disabled()
	}
	// The client always populates a terminal status on its returned Signals;
	// the error is a mirror for callers that want one.
	signals, _ := s.LLM.ExtractSignals(ctx, text)
	if signals.Status == "" {
		signals.Status = llm.StatusUnavailable
	}
	if signals.Labels == nil {
		signals.Labels = []llm.LabelFlag{}
	}
	return signals
}
