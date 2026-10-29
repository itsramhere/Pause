# Pause TF-IDF linear signal baseline v1

Purpose: English message-style decision support in controlled merchant checkout.
The four fixed labels describe urgency, secrecy, identity-style claims, and fees
to unlock funds. Scores are decision scores, not calibrated fraud probabilities.
They cannot identify a person, confirm an emergency or authorize payment.

Data: 400 CC0 authored synthetic rows, 100 template families, conservatively
connected into ten disjoint clause clusters. Train 240 / validation 80 / test 80.
One author audited template/context labels; no independent annotator study or
real-world representative dataset exists. This is a small, repetitive synthetic
pilot. Selection finished before the final test was opened in P06. See
[the frozen final report](../reports/classifier-final.json); no model or
threshold was changed after opening it.

The word unigram/bigram plus character 3–5 TF-IDF vectorizers and balanced
one-versus-rest logistic regressions fit training rows only. Seed 42, C 4.
Per-label thresholds maximize validation F1 with higher threshold tie-breaking.
The initial .35–.65 candidate grid missed every validation urgency positive.
After that validation-only observation, development revision 2 expanded the grid
to .05–.95. This is disclosed tuning on validation, with no final-test access.
See validation-report.json for actual precision/recall/support, false triggers,
rule comparator, timing, memory and source hashes. The classifier has not shown
an advantage over the fixed-rule comparator. No effectiveness claim is made.

SetFit is deferred. Runtime uses only the chosen baseline and a separate pinned
MiniLM search encoder. No generative model or external LLM API is used.
Unsupported declared languages and obvious non-Latin scripts return UNSUPPORTED
without scores. English declarations of other Latin languages cannot be detected
reliably; this is a known limitation. Short text, unknown word vocabulary or
scores within .08 of a decision threshold ABSTAIN and require reviewer attention.

Weights are reproducible with the offline CLI and pinned requirements; artifact
SHA-256, label order, preprocessing, thresholds, data/split hashes, dependency
lock hash and base code revision are in classifier.manifest.json. Original
training-run Python source hashes and serialized reload equality are in the
validation report; current sources are frozen in source-freeze.manifest.json.
Only trusted offline operators may install releases. Never unpickle third-party
artifacts. Runtime checks the approved weight digest before loading.
