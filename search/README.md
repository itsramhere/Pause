# Search projection contracts

`pause-playbooks` and `pause-request-history` are fixed logical aliases held only by the Go server. P01 creates no Elastic index and runs no search. Mapping templates describe the future 384-dimensional cosine space; P03/P04 must verify the released encoder dimension, create concrete versioned indexes, and record mapping/encoder/fusion versions before attaching aliases. A changed encoder requires a new index.

Curated original playbook summaries with primary source URLs, publishers, dates, review dates, revisions, and usage notes belong in `playbooks/`. Ingestion is offline administrative work. Do not add household messages or held-out evaluation answers. Private indexing jobs remain queued until P04 implements actual dispatch and monotonic revision/tombstone handling. Hydrate every private source from PostgreSQL at its exact current intent/consent/projection version before showing it.
