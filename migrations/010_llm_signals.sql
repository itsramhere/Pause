-- C1: advisory Mistral signal reading. The llm column records the validated
-- extractor result (status, model, prompt version, grounded flags) for the run
-- that produced the evidence. It is display data: no release or eligibility
-- path reads it.
ALTER TABLE investigation_runs ADD COLUMN llm jsonb;
-- Template bump for the new evidence packet block. The config trigger invalidates
-- existing snapshots and re-marks approved reviews stale, so every released
-- packet is re-created under the current template after deployment.
UPDATE payment_evidence_config SET template_version='public-evidence-template-v2',revision=revision+1;
