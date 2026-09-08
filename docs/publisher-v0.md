# Publisher V0 — Architecture Design

Publisher V0 is not implemented and does not authorize automatic publication.
This document defines the boundary for a later, separately evaluated milestone.

## Authority and input

PostgreSQL remains the source of truth. A publication attempt may start only
from a persisted `ContentDraft` for which `content.IsPublishable(draft)` is
true:

```text
human_review_status = APPROVED
approved_content_hash = SHA-256(current post_text)
```

An approved status by itself is insufficient. A changed draft, a missing audit
field, or an invalid state fails closed. Telegram is transport/UI only and
does not authorize publication by itself.

## Proposed flow

```text
approved immutable ContentDraft
→ Publisher Agent builds a structured publication payload
→ deterministic Go publisher validates payload against approved content
→ persisted idempotency/publication attempt
→ VK wall.post
→ persisted external post identity and terminal result
```

The Publisher Agent is limited to forming the platform payload. The
deterministic Go runtime owns API credentials, request execution, retry policy,
idempotency, persistence, and every publication-state transition. It must not
call VK from an LLM tool path.

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

The exact PostgreSQL schema, VK request contract, retry budget, and recovery
semantics are intentionally deferred to the Publisher V0 milestone. They must
be evaluated against VK's documented idempotency and response behavior before
implementation. No queue, worker, scheduler, or autonomous trigger is implied
by this design.

## Explicit non-goals

- No VK credentials or API calls.
- No `wall.post` implementation.
- No automatic publication after review approval.
- No publication state or migration in the current runtime.
- No notification/review transport changes.
