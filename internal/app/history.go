package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
)

type HistoryMatch struct {
	IntentID string `json:"intent_id"`
	Version  int    `json:"version"`
	Revision int    `json:"projection_revision"`
	Excerpt  string `json:"excerpt"`
	Source   string `json:"source"`
}
type HistoryPacket struct {
	Status             domain.LookupStatus   `json:"status"`
	ConsentVersion     int                   `json:"consent_version"`
	Revision           int64                 `json:"history_revision"`
	WindowStart        time.Time             `json:"window_start_utc"`
	WindowEnd          time.Time             `json:"window_end_utc"`
	Rows               []domain.TimelineRow  `json:"timeline"`
	Totals             domain.HistoryTotals  `json:"totals"`
	Rules              []domain.PatternRule  `json:"rules"`
	SnapshotHash       string                `json:"snapshot_hash"`
	Matches            []HistoryMatch        `json:"context_matches"`
	Notes              []string              `json:"explanations"`
	Receipt            investigation.Receipt `json:"retrieval_receipt"`
	Lookup             domain.SearchResult   `json:"lookup"`
	Policy             string                `json:"policy_version"`
	Template           string                `json:"template_version"`
	PendingProjections int                   `json:"pending_projections"`
	PendingDeletes     int                   `json:"pending_deletions"`
	CleanupOverdue     bool                  `json:"cleanup_overdue"`
}

// Complete canonical rows are independent of Elastic's top-k and indexing lag.
// Current intent appears once; edits replace its effective immutable version.
func (s *Service) canonicalHistory(ctx context.Context, tx pgx.Tx, p paymentRow) (HistoryPacket, error) {
	now := s.Clock.Now().UTC()
	h := HistoryPacket{Status: domain.LookupNotApplicable, ConsentVersion: p.consent, Revision: p.revision, WindowStart: now.Add(-7 * 24 * time.Hour), WindowEnd: now, Rows: []domain.TimelineRow{}, Matches: []HistoryMatch{}, Rules: []domain.PatternRule{}, Notes: []string{}, Policy: domain.PatternPolicy, Template: domain.HistoryTemplate}
	if err := s.historyCleanupState(ctx, tx, p.household, &h); err != nil {
		return h, err
	}
	if !p.history {
		return h, nil
	}
	h.Status = domain.LookupUnavailable
	rows, err := tx.Query(ctx, `SELECT i.id::text,i.current_version,i.consent_version,sp.revision,i.created_at,v.amount_minor,v.purpose,v.payee_id,COALESCE(seed.status,i.payment_status),CASE WHEN answer.status IN ('PENDING','CONFIRMED') AND answer.expires_at<=$6 THEN 'EXPIRED' ELSE COALESCE(answer.status,i.verification_status) END,CASE WHEN answer.responded_at IS NOT NULL THEN 'AUTHENTICATED_RESPONSE' ELSE 'NO_RESPONSE' END,answer.responded_at,CASE WHEN seed.intent_id IS NULL THEN 'USER_SUBMITTED' ELSE 'SYNTHETIC_SEEDED_HISTORY' END,CASE WHEN seed.intent_id IS NOT NULL THEN 'SYNTHETIC_SEEDED_HISTORY' WHEN i.authorization_id IS NOT NULL THEN 'PROVIDER_EVENT' ELSE 'NO_PROVIDER_OUTCOME' END
 FROM payment_intents i JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version JOIN search_projections sp ON sp.intent_id=i.id LEFT JOIN synthetic_history seed ON seed.intent_id=i.id LEFT JOIN verifications answer ON answer.intent_id=i.id AND answer.intent_version=i.current_version
 WHERE i.household_id=$1 AND v.subject_contact_id=$2 AND i.creator_id=$3 AND i.reviewer_id=$4 AND i.created_at BETWEEN $5 AND $6 AND (i.id=$7 OR (c.history_allowed AND sp.action='UPSERT' AND m.deleted_at IS NULL AND m.expires_at>$6)) ORDER BY i.created_at,i.id`, p.household, p.cmd.Binding.SubjectContactID, p.creator, p.reviewer, h.WindowStart, now, p.cmd.IntentID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var r domain.TimelineRow
		if err = rows.Scan(&r.IntentID, &r.Version, &r.ConsentVersion, &r.Revision, &r.Time, &r.Amount, &r.Purpose, &r.Merchant, &r.Status, &r.Response, &r.ResponseSource, &r.ResponseAt, &r.Source, &r.OutcomeSource); err != nil {
			rows.Close()
			return h, err
		}
		r.Current = r.IntentID == p.cmd.IntentID
		h.Rows = append(h.Rows, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return h, err
	}
	h.Totals, h.Rules = domain.CalculateHistory(h.Rows, s.patternThresholds())
	// Reserving this request's capture changes its UI bucket but must not
	// invalidate its own decision. Another request's reservation changes the hash.
	normalized := append([]domain.TimelineRow{}, h.Rows...)
	for n := range normalized {
		if normalized[n].Current && normalized[n].Status == "CAPTURE_PENDING" {
			normalized[n].Status = "HELD"
		}
	}
	h.SnapshotHash = evidenceHash(mustJSON(map[string]any{"rows": normalized, "consent": p.consent, "revision": p.revision, "policy": h.Policy, "thresholds": s.patternThresholds()}))
	for n, r := range h.Rows {
		if n > 0 && r.Purpose != h.Rows[n-1].Purpose {
			h.Notes = append(h.Notes, fmt.Sprintf("Entered purpose on %s: %q; earlier purpose on %s: %q. Different purposes do not establish inconsistency.", r.Time.UTC().Format(time.RFC3339), r.Purpose, h.Rows[n-1].Time.UTC().Format(time.RFC3339), h.Rows[n-1].Purpose))
		}
	}
	h.Notes = append(h.Notes, "Requested amounts are requests, not losses. Similarity supplies context; the selected enrolled contact ID determines the relationship.")
	return h, err
}
func (s *Service) historyCleanupState(ctx context.Context, tx pgx.Tx, household string, h *HistoryPacket) error {
	return tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE sp.indexed_revision<sp.revision)::int,count(*) FILTER(WHERE sp.action='DELETE' AND sp.indexed_revision<sp.revision)::int,COALESCE(bool_or(sp.action='DELETE' AND sp.indexed_revision<sp.revision AND o.created_at<$2),false) FROM search_projections sp JOIN payment_intents i ON i.id=sp.intent_id LEFT JOIN search_outbox o ON o.intent_id=sp.intent_id AND o.projection_revision=sp.revision WHERE i.household_id=$1`, household, s.Clock.Now().Add(-5*time.Minute)).Scan(&h.PendingProjections, &h.PendingDeletes, &h.CleanupOverdue)
}
func (s *Service) patternThresholds() domain.PatternThresholds {
	if s.Config.PatternRequested > 0 && s.Config.PatternMinimum > 0 && s.Config.PatternMultiplier > 0 {
		return domain.PatternThresholds{Requested: s.Config.PatternRequested, Minimum: s.Config.PatternMinimum, Multiplier: s.Config.PatternMultiplier}
	}
	return domain.DefaultPatternThresholds
}
func historyQuery(p paymentRow, h HistoryPacket, encoder, text string) domain.SearchQuery {
	return domain.SearchQuery{Scope: domain.History, HouseholdID: p.household, SubjectContactID: p.cmd.Binding.SubjectContactID, ExcludeIntentID: p.cmd.IntentID, ConsentVersion: p.consent, Cutoff: h.WindowStart, Now: h.WindowEnd, EncoderVersion: encoder, Text: text, Limit: 3}
}
func historyPermissions(h HistoryPacket) []string {
	out := []string{}
	var current domain.TimelineRow
	for _, r := range h.Rows {
		if r.Current {
			current = r
		}
	}
	for _, r := range h.Rows {
		if !r.Current && (r.Time.Before(current.Time) || (r.Time.Equal(current.Time) && r.IntentID < current.IntentID)) {
			out = append(out, investigation.PermissionKey(r.IntentID, r.Version, r.ConsentVersion, r.Revision))
		}
	}
	return out
}

// All candidates are checked under the same short household lock as the packet.
// Elastic snippets never enter this method or a stored snapshot.
func (s *Service) hydrateHistoryPacket(ctx context.Context, tx pgx.Tx, h *HistoryPacket, result domain.SearchResult) error {
	permitted := map[string]domain.TimelineRow{}
	keys := map[string]bool{}
	for _, key := range historyPermissions(*h) {
		keys[key] = true
	}
	for _, r := range h.Rows {
		if keys[investigation.PermissionKey(r.IntentID, r.Version, r.ConsentVersion, r.Revision)] {
			permitted[r.IntentID] = r
		}
	}
	valid := []domain.SearchHit{}
	for _, hit := range result.Hits {
		r, ok := permitted[hit.SourceID]
		if !ok || hit.SourceVersion != r.Version || hit.ConsentVersion != r.ConsentVersion || hit.ProjectionRevision != r.Revision {
			continue
		}
		var ciphertext []byte
		if err := tx.QueryRow(ctx, `SELECT ciphertext FROM request_messages WHERE intent_id=$1 AND version=$2 AND deleted_at IS NULL AND expires_at>$3`, r.IntentID, r.Version, s.Clock.Now()).Scan(&ciphertext); err != nil {
			if err == pgx.ErrNoRows {
				continue
			}
			return err
		}
		text, err := s.Box.Decrypt(ciphertext, fmt.Sprintf("%s:%d", r.IntentID, r.Version))
		if err != nil {
			return err
		}
		h.Matches = append(h.Matches, HistoryMatch{IntentID: r.IntentID, Version: r.Version, Revision: r.Revision, Excerpt: redactHistory(text), Source: r.Source})
		valid = append(valid, hit)
	}
	result.Hits = valid
	h.Lookup = result
	h.Status = result.Status
	return nil
}
func (s *Service) historyForAnalysis(ctx context.Context, a domain.Actor, id string) (paymentRow, HistoryPacket, error) {
	var p paymentRow
	var h HistoryPacket
	err := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		p, err = s.paymentLock(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = s.paymentActor(ctx, tx, a, p, false); err != nil {
			return err
		}
		h, err = s.canonicalHistory(ctx, tx, p)
		return err
	})
	return p, h, err
}
func privateEvidenceMatches(h HistoryPacket) []HistoryMatch {
	out := append([]HistoryMatch{}, h.Matches...)
	for n := range out {
		out[n].Excerpt = ""
	}
	return out
}
func patternAttention(h HistoryPacket) bool {
	for _, r := range h.Rules {
		if r.Matched {
			return true
		}
	}
	return false
}

func historySummary(h HistoryPacket) string {
	if h.Status == domain.LookupNotApplicable {
		return "Private history is off. Current message checks and independent confirmation still apply."
	}
	if patternAttention(h) {
		return "These related requests need a closer look. Changing amounts or purposes do not establish fraud."
	}
	return strings.TrimSpace("Related requests are shown for context. Independent confirmation is still required.")
}
