# Publisher V0 — Architecture Design

Publisher V0 is explicit operator-triggered work and never authorizes automatic
publication.

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

## Explicit non-goals

- No automatic publication after review approval.
- No notification/review transport changes.
