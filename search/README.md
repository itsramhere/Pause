# Search projection contracts

P03 implements the real public pause-playbooks alias and versioned
pause-playbooks-v1-minilm1110 index with the separately pinned 384-dimensional
cosine encoder. Offline cmd/investigation-setup verifies/creates mappings,
embeds/ingests 18 approved public entries and explicitly approves local releases.
See the [runbook](../docs/investigation.md).

Keyword and vector branches both filter language,active,encoder. Go fuses ranks
with RRF60; this is not native Elastic RRF. Queries check resolved index,
mapping/encoder/corpus metadata and approved source revisions. Receipts retain
actual branch ranks. Empty success is distinct from unavailable.

The 30 sealed relevance queries are generated once by freeze-evaluation.py for
P06; ingestion never imports them. Private pause-request-history indexing is
P04 work. No household text/vectors are indexed here. Private projection jobs
remain pending. History opt-in blocks release until required scoped retrieval
exists; opt-out is NOT_APPLICABLE. P04 must retain filters in both branches and
hydrate against authoritative PG membership,consent,version,expiry.
