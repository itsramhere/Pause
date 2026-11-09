package app

import (
	"context"
	"time"

	"github.com/ramst/pause/internal/domain"
)

// RequestStory contains only canonical PostgreSQL history, without search data.
type RequestStory struct {
	HistoryEnabled bool                 `json:"history_enabled"`
	WindowStart    time.Time            `json:"window_start_utc"`
	WindowEnd      time.Time            `json:"window_end_utc"`
	Rows           []domain.TimelineRow `json:"timeline"`
	Totals         domain.HistoryTotals `json:"totals"`
	Rules          []domain.PatternRule `json:"rules"`
	Attention      bool                 `json:"attention"`
	Summary        string               `json:"summary"`
	Notes          []string             `json:"explanations"`
	Policy         string               `json:"policy_version"`
}

func (s *Service) RequestStory(ctx context.Context, a domain.Actor, id string) (RequestStory, error) {
	id, err := canonicalID(id)
	if err != nil {
		return RequestStory{}, err
	}
	p, h, err := s.historyForAnalysis(ctx, a, id)
	if err != nil {
		return RequestStory{}, err
	}
	return RequestStory{HistoryEnabled: p.history, WindowStart: h.WindowStart, WindowEnd: h.WindowEnd, Rows: h.Rows, Totals: h.Totals, Rules: h.Rules, Attention: patternAttention(h), Summary: historySummary(h), Notes: h.Notes, Policy: h.Policy}, nil
}
