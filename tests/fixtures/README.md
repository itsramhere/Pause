# Workflow fixtures

Synthetic contract fixtures only. `binding-v1.json` freezes the binding/digest across language implementations. It establishes neither model quality nor a payment outcome. Do not include workflow fixtures in held-out model evaluations or claim them as a reviewed training dataset.

`p04-private-queries.json` contains three development probes for permitted prior
context: paraphrase, unrelated query and literal wording. The real private Elastic
suite runs them against causal synthetic PG sources. They are not the sealed P06
evaluation queries, a training set or proof of real-world retrieval quality.
