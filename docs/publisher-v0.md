# Publisher V0 — Architecture Design

Publisher V0 is explicit human-triggered work. An authorized Telegram approval
may invoke it only after approval has committed; there is no scheduler or
automatic publication independent of human approval.

## Authority and input

PostgreSQL remains the source of truth. A publication attempt may start only
from a persisted `ContentDraft` for which `content.IsPublishable(draft)` is
true:

```text
ValidateApprovedPayload(draftID) = ELIGIBLE
```

An approved status by itself is insufficient. A changed draft, a missing audit
field, or an invalid state fails closed. Telegram is transport/UI only and
does not authorize publication by itself.

## Proposed flow

```text
approved immutable ContentDraft + media SHA
→ deterministic Go publisher validates current payload and local media bytes
→ persisted VK publication attempt
→ VK wall.post
→ persisted external post identity and terminal result
```

The deterministic Go runtime owns API credentials, request execution,
idempotency, persistence, and every publication-state transition. No LLM stage
exists after human approval.

## Payload and fact integrity

The payload must identify the exact `content_draft_id`, approved content hash,
target platform, and the text submitted to that platform. Validation rejects a
payload that does not preserve the approved draft text and provenance required
by the publication contract.

A future humanizer is permissible only as a bounded style pass before human
approval. It may not add, remove, or alter facts, stage, quantities, dates,
addresses, source attribution, or uncertainty. Humanized text becomes the new
draft text and needs its own review/hash approval; it cannot modify an already
approved payload.

## Reliability model

Publisher V0 needs persisted publication attempts keyed by the target platform
and exact approved draft version. The deterministic runtime records intent,
request identity/idempotency data, retryable technical failures, confirmed VK
post identity, and terminal failure. A retry must reuse the same persisted
idempotency identity; it must never decide to publish a different or newer
draft.

`publications` is unique per draft/platform. A confirmed `PUBLISHED` row is
idempotent. Definitive pre-`wall.post` failures become `FAILED` and may be
retried explicitly. A transport ambiguity after `wall.post` becomes
`RECOVERY_REQUIRED`: it must never be automatically or normally retried because
VK cannot provide a proven exactly-once guarantee; an operator must reconcile.
`MARK_PUBLISHED` records an operator-supplied post ID without calling VK;
`MARK_NOT_PUBLISHED` moves the publication to `FAILED`. No automatic VK wall
search is performed.

## Telegram bridge

The Telegram review plugin invokes the existing `urban-radar content publish
<draft-id>` CLI only after the approve CLI has returned its committed approval
result. It never calls VK directly. Publisher results update the same review
card: `FAILED` offers one explicit retry; `RECOVERY_REQUIRED` offers only
reconciliation controls; `PUBLISHED` is idempotent. Process or malformed JSON
errors are shown as an unknown publication outcome and never affect approval.
The retry callback addresses a persisted publication ID; Go rereads the row and
allows a new attempt only from `FAILED`. A subsequent claimant observing
`PUBLISHING` is blocked without altering the row because it may belong to an
active VK call. Telegram displays reconciliation only for persisted
`RECOVERY_REQUIRED`, never for a merely inferred crash.

For a confirmed process crash, an operator may explicitly run
`urban-radar publication recover <publication-id> --actor <actor>`. This is the
sole V0 transition from `PUBLISHING` to `RECOVERY_REQUIRED`; it records a
dedicated operator audit timestamp/actor and never calls VK.

## Explicit non-goals

- No scheduler or autonomous publication after review approval.
- No notification/review transport changes.
