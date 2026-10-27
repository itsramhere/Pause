# Pause

**Time to check the story before a supported payment goes through.**

Pause lets an older adult check whether an enrolled relative requested an exact
merchant purchase before a supported PayPal authorization is captured. A trained
English message-signal classifier and sourced public Elasticsearch matches help
an independently signed-in reviewer assess the request. They do not establish
identity, prove an emergency, or authorize payment.

## Current implementation

**Phase 3 is complete in its documented prototype scope.** All P03-T01 through
P03-T17 acceptance cases passed independent implementation/debugging checks on
October 27, 2026. Predictive effectiveness remains limited, as detailed below;
private history and final held-out evaluation remain P04 and P06 work.

P01–P03 implement scoped sessions, explicit versioned intake, encrypted retained
messages, durable sandbox payment commands/reconciliation, a trained TF-IDF linear
classifier, a separately pinned MiniLM encoder, real public Elastic retrieval,
exact enrolled-account confirmation and guarded reviewer approval or decline.
There is no generative model, LLM API key, or generative fallback.

The final PostgreSQL-backed Go suite passed **286 tests/subtests** in both normal
and race-detector runs, with zero failures or skips. **11 Python tests**, Go vet
and command builds, frontend typecheck/build, and the diff check passed. Real
Elastic checks also rejected deleted, inactive, revised and changed public
sources, then restored the original documents and recovered a successful lookup.

Two real $1.00 USD merchant purchases completed through three independent
application sessions: confirmed/approved capture and denied/declined void. The
[sanitized P03 receipt](evidence/p03-sandbox-2026-10-27.json) includes authoritative
provider GET results and exact IDs. Buyer approval was performed manually through
the PayPal SDK; no provider state was simulated. The merchant receives a capture,
not the relative named in the story.

See the [investigation runbook](docs/investigation.md),
[implementation evidence](docs/investigation-implementation-2026-10-27.md),
[independent debugging report](docs/investigation-debugging-2026-10-27.md),
[model card](models/classifier-card.md), [payment setup](docs/payments.md), and
[foundation setup](docs/foundation.md).

## How the current flow works

1. The senior selects an existing enrolled contact and enters amount, currency,
   actual merchant, purpose and the received message. Text cannot replace those
   transaction fields or create a trusted contact.
2. A small trained multilabel classifier reports urgency, secrecy,
   impersonation-style or advance-fee signals. The fixed search encoder supports
   public keyword/vector retrieval. Go combines ranks with RRF and renders
   original curated summaries linked to their FTC sources.
3. The addressed subject's own authenticated session answers the exact purchase,
   including receiving merchant and creation time. Its fifteen-minute task TTL
   is bounded by the thirty-minute application deadline. Confirmation is evidence
   only; denial blocks release and requests guarded void independently of search.
4. PayPal SDK buyer approval creates an authorization without immediate capture.
   The assigned reviewer reads current evidence and records a rationale. Release
   rechecks the bound version, independent answer, approved artifacts, mandatory
   lookup, current policy, evidence hash and deadline before reserving capture.
5. Durable workers dispatch and reconcile provider operations. The UI reports
   CAPTURED or VOIDED only after provider confirmation. Unknown outcomes remain
   under reconciliation; cancellation cannot undo an in-flight capture.

Successful empty retrieval does not mean a request is safe. Valid abstention
requires reviewer attention. Unsupported/unavailable analysis blocks release;
subject answers and cancellation remain available. Contact confirmation records
that enrolled account's statement; it does not prove an event objectively occurred.

## Data and measured limitations

The pilot uses 400 CC0 synthetic labeled examples, grouped into ten shared-clause
clusters before a 240/80/80 train/validation/test split. Only one author audited
labels. TF-IDF preprocessing and weights use train only; thresholds use validation
only. The final classifier test and 30 frozen retrieval queries remain sealed for
P06. Runtime development tests are separate from those final evaluations.

The linear model's validation micro F1 is about .764 versus .772 for fixed rules,
with eight benign validation false triggers. No advantage over rules or real-world
effectiveness is claimed. See [the complete validation report](models/validation-report.json).
The independent development audit found 72 of 80 validation messages abstained;
an ordinary quoted scam warning also triggered false signals. These results
limit present usefulness and remain visible in the debugging report. No model or
threshold was tuned after those probes.
The [runtime report](models/runtime-budget.json) records actual startup, memory and
CPU latency from development probes. SetFit experimentation is deferred.

The public corpus has 18 original summaries with source/revision/review metadata.
No private messages are trained on or indexed here. Related-request private
retrieval, consent-aware projection/deletion, timeline and user-facing aggregate
patterns belong to P04. Until then, history opt-in remains an unavailable mandatory
lookup and blocks release; opt-out is NOT_APPLICABLE. History permission never
means training permission.

## Scope

English text and USD purchases through one controlled sandbox merchant are
supported. Pause controls capture only in integrated checkout flows. It cannot
intercept arbitrary PayPal transfers, bank wires or purchases elsewhere. Guardian
conflict protection, generative models, institutional verification, personalized
anomaly learning and native apps remain outside this iteration.

## Run and verify

Follow the foundation and payment setup for PostgreSQL and paired sandbox
credentials. Apply migrations through version 6. Preserve populated environment
files; select an ignored local configuration with ENV_FILE. Start API and worker
separately. The investigation runbook describes exact Python dependency locks,
reproducible artifact training/download, private service authentication and public
index ingestion through cmd/investigation-setup. Public setup does not depend on
ignored planning files.

The repository includes the frozen public synthetic splits, reviewed manifests,
source code and sanitized acceptance evidence. Model weights, downloaded encoder
files, populated environment files, local databases and raw verification logs are
ignored. Recreate the weights using the runbook; do not regenerate the frozen
splits with `--curate` or use final test/relevance cases for development tuning.

Run the actual PostgreSQL suite with TEST_DATABASE_URL, then go vet ./... and the
frontend build. Python unittest loads the actual approved classifier and encoder
for runtime/contract checks. The 40-case product fixture catalog references
executable Go authority, payment and retrieval tests. Test-only provider/search
adapters do not count as live sandbox evidence. Windows race checks require CGO
and a compatible C compiler as documented in the P02 acceptance report.
Ten additional concrete adversarial fixtures exercise intake, addressed tasks,
evidence and corrected versions. The independent report records actual-artifact,
real Elastic source withdrawal/restoration, PostgreSQL and race results separately.

GitHub Actions runs the PostgreSQL Go checks (including the race detector), web
checks, and Python runtime tests. Its Python job installs the locked environment,
trains from the existing train/validation splits and downloads the pinned public
encoder in its disposable checkout. This CI build does not approve a deployment
release or replace the recorded sandbox evidence.

The historical PayPal spike and P00 toy ML/search probes remain separate
feasibility evidence. The product ignores the spike's legacy AI_* settings.

Pause is being developed for the [PayPal AI Hackathon](https://paypalaihackathon.devpost.com/).
