# Urban Radar — Current State

## Current pipeline

```text
Source → Discovery → Editor → Content → Telegram Human Review
→ APPROVED payload → Publisher V0
```

An authorized Telegram Approve invokes Publisher only after the independent
approval transaction has committed. The bridge invokes the existing
`urban-radar content publish <draft-id>` boundary; it does not call VK itself.

The current READY-to-review path is deterministic:

```text
READY_TO_PUBLISH source item
→ `urban-radar content process-ready`
→ content generation/reuse → ContentDraft PENDING → deterministic review-notify
→ Hermes Telegram/PTB notification with inline buttons
→ authenticated `ur:approve:<content_draft_id>` or `ur:reject:<content_draft_id>` callback
→ `ReviewService`
→ PostgreSQL
```

The cron wrapper runs `content process-ready` after a successful `news check`.
It selects persisted `READY_TO_PUBLISH` records in stable source order,
reuses an exact draft when possible, and sends a review card only for an
undelivered `PENDING` draft. It never publishes to VK.

## Validated milestones

- Editor V1
- Research V0.2
- Content V1
- Persisted Review Model
- Hermes Review Callback Contract
- Hermes Plugin Loading
- Hermes → Persisted Review Integration
- Telegram Review Notification V0
- Controlled Live Telegram Review transport and persisted callback transition

See [database.md](database.md) for persisted review and notification fields, and [runtime.md](runtime.md) for scheduled runtime/deployment operations.

## Current Review & Publish state

Manual Media Attach V0 uses `content_drafts 1 → 0..1 content_media`:
`content_drafts` is content plus human review, `content_media` is its attached
local media artifact, and `publications` owns persisted external side effects.
DB metadata is durable; the local file is disposable; cleanup is
driven by DB state.

The completed V0 contract is:

```text
Telegram review → manual JPEG/PNG attach → persistent local media
→ content_media → human Approve/Reject
```

Publisher V0 runs only from explicit human action: `Telegram Approve → persisted
APPROVED → approved_payload_hash → ValidateApprovedPayload → local media SHA
verification → deterministic Publisher → external side effect`. A failed or
unknown Publisher invocation never rolls back approval. No LLM stage exists
after human approval.

Approve/Reject work through Telegram. Manual JPEG/PNG attachment is available.
Approved text and media are immutable; a change requires new human review.

`APPROVED` is the human decision and is independent from publication outcome.
`FAILED` means the system knows no final VK post was created; it may be retried
explicitly. `RECOVERY_REQUIRED` means the system cannot prove whether final VK
side effect happened and must never be automatically retried.
It requires explicit human reconciliation; operator confirmation is authoritative
for V0 and no automatic VK wall search occurs.

After human approval, `post_text` and attached media are immutable for the
publication pipeline. No LLM, Content, or Publisher stage may rewrite approved
text or replace approved media; any approved-payload change requires new human
review. The text-level audit hash remains available alongside the payload hash
used by the future Publisher eligibility gate.

`content_drafts` supports the fail-closed review lifecycle:

```text
PENDING → APPROVED
PENDING → REJECTED
```

Approval persists actor, timestamp, and two SHA-256 audit values.
`approved_content_hash` is the existing text-level audit hash.
`approved_payload_hash` is the publication eligibility boundary over canonical
`post_text` plus active media SHA (or `null`). No external publication side
effect may occur unless `current_payload_hash == approved_payload_hash`.
Legacy APPROVED rows without `approved_payload_hash` fail closed; migration 010
does not infer a retrospective approval.

Publisher state is:

```text
PENDING → PUBLISHING → PUBLISHED
PUBLISHING → FAILED
PUBLISHING → RECOVERY_REQUIRED
```

`PUBLISHED` is idempotent; `FAILED` permits explicit retry; `PUBLISHING` fails
closed; `RECOVERY_REQUIRED` prohibits normal retry and requires reconciliation.
Before every VK side effect Publisher validates the approved payload, rereads
current payload, and verifies local media bytes SHA. Validation failure makes
zero VK calls. No LLM exists after approval. Definitive/pre-final failures are
`FAILED`; ambiguous transport/read failure after final `wall.post` starts is
`RECOVERY_REQUIRED`. Publication failure never rolls back approval.

Reconciliation V0 is `RECOVERY_REQUIRED → MARK_PUBLISHED → PUBLISHED` or
`RECOVERY_REQUIRED → MARK_NOT_PUBLISHED → FAILED`. Neither action calls VK.
Operator confirmation is authoritative; normalized external ID plus
`reconciled_at`/`reconciled_by` are persisted. Telegram reconciliation input is
transient, bounded, and context-bound; PostgreSQL remains authority.

The same Telegram review card renders Publisher outcomes after approval:
`PUBLISHED`, `IDEMPOTENT`, `FAILED`, `RECOVERY_REQUIRED`, or `BLOCKED`.
Only `FAILED` has an explicit `🔁 Повторить публикацию` control. Each retry is
addressed as `ur:pub-retry:<publication_id>` and first rereads that persisted
publication. Only persisted `FAILED` may invoke one fresh Publisher CLI attempt,
which repeats payload and local-media verification. A retry encountering
`PUBLISHING` is blocked as an active/in-progress attempt and does not alter the
persisted row. Telegram shows reconciliation controls only for persisted
`RECOVERY_REQUIRED`. `PUBLISHED` and `RECOVERY_REQUIRED` cannot be retried;
reconciliation to `FAILED` is required first.

If an operator has established that a `PUBLISHING` process crashed, the only
recovery entry point is `urban-radar publication recover <publication-id>
--actor <actor>`. It explicitly transitions `PUBLISHING → RECOVERY_REQUIRED`,
performs no VK call, and records `recovery_required_at` / `recovery_required_by`.
The normal reconciliation flow then determines `PUBLISHED` or `FAILED`.

Media cleanup is DB-timestamp driven, never filesystem mtime: active media is
deleted after seven days for REJECTED drafts or PUBLISHED VK publications, while
its metadata remains as audit history.

Review notification delivery is separate from review status. Successful delivery persists notification time, channel, and Telegram external message ID. Ordinary replay skips a delivered draft; the current semantics are at-least-once because a crash after Telegram accepts a message but before the database mark can duplicate delivery.

`ContentDraft.post_text` owns rendered source attribution and is shown unchanged
under the review-card heading. The structured `source_url` remains persisted
provenance for validation and future Publisher V0 payloads; the notifier must
not append a second source footer.

Hermes is Telegram transport/UI only. The pinned Hermes plugin routes the exact `ur:` callback through Hermes authorization and a strict parser to the Go review CLI. The actor is represented as `telegram:<numeric-user-id>`. No LLM participates in notification, approve, or reject execution paths.

The callback UX acknowledges every handled callback. After a durable decision,
it edits the original review message in place, preserving its text and adding
one final status line (`✅ Одобрено`, `❌ Отклонено`, or the corresponding
idempotent wording), then removes the inline keyboard. It never sends a second
chat message. Bridge/action failures are logged and returned as a Telegram
alert; authorization and the `ur:approve|reject:<content_draft_id>` contract
remain fail-closed.

## Current database/runtime state

Migrations `007_content_draft_persisted_review.sql` and `008_content_draft_review_notifications.sql` exist and have been applied to the local `urban_radar_dev` database. Historical Content Experiment review notes were reset to fail-safe `PENDING` when the audited review model was introduced; they are not publication approvals.

The local database contains two controlled live review outcomes: draft `2`
(`tgl/25864`) is `APPROVED`, with notification external ID `4`; draft `6`
(`tgl/25871`) is `REJECTED`, with notification external ID `16`. Other current
drafts remain `PENDING` and unsent. These are review outcomes only: no source
item is `PUBLISHED` and no VK call has occurred.

## Known gaps

- Notification delivery is not exactly-once; there is no outbox or retry worker.

The production deployment contract provides a long-running `hermes-gateway`
and a configured Telegram review plugin through the operator-owned Hermes
home, plus the `/opt/urban-radar/bin/run-news-check` cron wrapper. Both the
gateway and one-shot CLI depend on the host Xray/SOCKS5 endpoint at
`host.docker.internal:10808`. Actual VPS activation remains an operator
verification step: validate Compose interpolation, proxy reachability, gateway
and plugin status, then run the wrapper once before enabling the documented
`20:30 Europe/Samara` user cron entry. Repository state alone cannot confirm
that this host cron has been installed or executed.

The current callback UX/preflight change has no-network test coverage. Its
success acknowledgement and keyboard-removal behavior have not yet been
separately observed on a new live callback.

The gateway registration preflight requires a gateway-owned absolute
`URBAN_RADAR_REVIEW_COMMAND` and `DATABASE_URL`; it never relies on an
interactive shell. Compose runs the long-lived Hermes gateway with the same
PostgreSQL, VK configuration, and media bind mount as the CLI.

## Do not revisit without new evidence

- Validated Discovery, Editor, Research, and Content prompts/policies.
- The LLM-free deterministic review boundary.
- PostgreSQL as the authority for review state and publication eligibility.
- Host cron + `flock` scheduling model; do not add a second scheduler.

Publisher V0 is deterministic and explicit-CLI only: see [publisher-v0.md](publisher-v0.md).

## Current milestone

Telegram Approve → Publisher Bridge V0 + Retry is implemented.

## Next step

Perform a separate controlled real VK end-to-end test.
