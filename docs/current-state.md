# Urban Radar — Current State

## Current pipeline

Incremental NewsCheck collects public source items with persisted PostgreSQL checkpoints, then runs Discovery, Editor V1, and bounded Research V0.2 where required. Eligible events become `READY_TO_PUBLISH`; Content V1 creates a persisted `ContentDraft`.

The current review path is deterministic:

```text
ContentDraft PENDING
→ targeted `urban-radar content review-notify --draft-id <id>`
→ Hermes Telegram/PTB notification with inline buttons
→ authenticated `ur:approve:<content_draft_id>` or `ur:reject:<content_draft_id>` callback
→ `ReviewService`
→ PostgreSQL
```

The notification command is controlled/manual; it is not automatically wired after NewsCheck.

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

`content_drafts` supports the fail-closed review lifecycle:

```text
PENDING → APPROVED
PENDING → REJECTED
```

Approval persists actor, timestamp, and a SHA-256 hash of the exact `post_text`. A future publisher must require both `APPROVED` and a current hash equal to `approved_content_hash`; an approval never authorizes changed content.

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

- Automatic notification after NewsCheck is not wired.
- VK Publisher, publication state, and autonomous publishing do not exist.
- Production deployment of the review path has not been performed.
- Notification delivery is not exactly-once; there is no outbox or retry worker.

The current callback UX/preflight change has no-network test coverage. Its
success acknowledgement and keyboard-removal behavior have not yet been
separately observed on a new live callback.

The gateway registration preflight requires a gateway-owned absolute
`URBAN_RADAR_REVIEW_COMMAND` and `DATABASE_URL`; it never relies on an
interactive shell. Current Compose has no long-running gateway service.

## Do not revisit without new evidence

- Validated Discovery, Editor, Research, and Content prompts/policies.
- The LLM-free deterministic review boundary.
- PostgreSQL as the authority for review state and publication eligibility.
- Host cron + `flock` scheduling model; do not add a second scheduler.

Publisher V0 is design-only: see [publisher-v0.md](publisher-v0.md). It must
remain deterministic at the VK side-effect boundary and operate only on the
exact approved content hash.

## Current milestone

Review callback UX and gateway-owned deployment preflight are ready for the
next controlled callback observation. Publisher V0 remains architecture-only.

## Next step

Before any Publisher implementation, run one controlled callback against a
fresh notified `PENDING` draft and observe the APPLIED/IDEMPOTENT UX. Do not
introduce automatic publication.
