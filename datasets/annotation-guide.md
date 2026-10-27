# Pause signal annotation v1

These English synthetic examples are development research data, released under
CC0-1.0. They contain no household messages. Each scenario template is a family;
its variants stay together. One implementation author performed a deterministic
label/context audit; no independent human agreement study has been completed.
Synthetic coverage and validation metrics cannot establish real-world accuracy.

Annotate what the message asks, not whether its author is a criminal:

* urgent_payment_pressure: a demand to pay quickly, including a legitimate urgent
  family purchase. An urgent weather report with no payment request is negative.
* secrecy_pressure: asks the recipient to hide the payment or avoid independent
  discussion. Ordinary privacy of a receipt is negative.
* impersonation_style_request: claims a family/authority role while introducing
  an unusual identity or unverified channel and asks for payment. A familiar
  enrolled account requesting an ordinary purchase is negative. This label does
  not establish that impersonation occurred.
* advance_fee_or_release_fee: requires an upfront payment to unlock promised
  funds, a prize, credit or release. An ordinary disclosed delivery fee is negative.

Quoted warnings are negative in their warning context. Ambiguous messages retain
their contextual labels and an ambiguity flag; nonpayment text is all-negative.
Unsupported languages are not negative training examples: inference returns
UNSUPPORTED. Borderline vocabulary/unknown wording may ABSTAIN; it still needs
exact subject confirmation and reviewer attention. No label selects a contact,
changes an amount, verifies an event, or grants release.

Generation uses reviewed template clauses plus neutral item/context variants.
Every reuse of a template remains in one family. Exact normalized duplicates are
rejected. The split is shuffled by connected template group with seed 42,
60/20/20; learned TF-IDF and linear weights use train only. Thresholds are selected
on validation; the initial .35-.65 grid was expanded to .05-.95 after validation
inspection, as recorded in validation-report.json. The current .08 abstention band
is a development choice, not a calibrated confidence guarantee. Test rows are
written separately and never read by training.
The final P06 evaluator must use the frozen test digest and report this synthetic,
single-author limitation. Product fixtures and relevance queries are separate.
