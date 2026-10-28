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
P06; ingestion never imports them. P04 implements the separate private
pause-request-history alias, durable permission-aware projection jobs, scoped
lexical/vector retrieval and canonical PG hydration. Only currently permitted
redacted snippets and their pinned vectors enter that private index. Public
playbooks never contain household text. See the [private-history runbook](../docs/patterns.md)
for mapping/reindex, tombstones, retention, complete financial totals and release
guards. Missing private dependencies still block opted-in release; opt-out is
NOT_APPLICABLE. The three P04 development probes are separate from sealed P06
relevance evaluation and do not establish general retrieval quality.
