# Incremental News Check Runtime V0

The runtime has two responsibilities and no scheduler:

```text
manual CLI
  → NewsCheckRunner
      → TGL and Zakupki collectors
      → PostgreSQL SourceItem/checkpoint state
      → RuntimeCoordinator, once per processable SourceItem
          → Discovery → Editor V1
          → optional Research V0.2 → Editor V1 re-evaluation
```

`NewsCheckRunner` owns collection windows, source failure isolation, upserts,
checkpoint advancement, deterministic batch ordering and summaries.
`RuntimeCoordinator` owns only one SourceItem's persisted state transitions.
Neither source adapter knows about agents, and the coordinator knows nothing
about CLI or a future Telegram adapter.

## Collection and deduplication

All stored timestamps are absolute instants. Source query boundaries are
converted explicitly to Europe/Samara calendar dates by the adapters.

- Without a checkpoint, a source reads `[now-48h, now]`.
- With a checkpoint, it reads `[checkpoint-24h, now]`.
- Pagination continues to the temporal/end-of-results boundary. The default
  100-page guard is safety-only, never an item-count success boundary.
- A source checkpoint advances to the run start only after its complete
  collection pass. One source may advance while the other fails.
- An overlap may return old rows; `(source, source_item_id)` deduplicates them.
- Every fetched source identity is retained. Pending and retryable rows keep a
  full SourceItem so a later resume never requires another source fetch.
  After a successful non-candidate Discovery result, `DISCOVERY_DROPPED`
  retains identity, URL/title, timestamps, compact Discovery provenance and a
  source fingerprint, but clears the heavy summary, body text and metadata.
  A repeated overlap refreshes `last_seen_at` and the fingerprint without
  re-materializing that payload or invoking Discovery again.
- Re-seen non-dropped rows refresh mutable source fields and `last_seen_at`,
  while keeping first-seen time, pipeline history, decisions, evidence and
  usage.
- A material source-content change does not automatically reopen a terminal
  item in V0. Deliberate reprocessing after a policy/content change remains an
  explicit operator action.

The processable set is all pending records plus persisted retryable `ERROR`
records, not only records returned by the current collection. Items are
processed sequentially by publication time ascending, then source and source
ID. One item failure does not stop later items.

## Per-item states

```text
RECEIVED → DISCOVERY
  non-candidate → DISCOVERY_DROPPED
  candidate → EDITOR

EDITOR
  IGNORE         → IGNORED
  PUBLISH        → READY_TO_PUBLISH
  UPDATE_PROJECT → PROJECT_ACTION
  RESEARCH       → RESEARCH (Zakupki) | RESEARCH_UNSUPPORTED (TGL)

RESEARCH (maximum one successful semantic round)
  valid persisted Evidence Pack → EDITOR_REEVALUATION
  technical/schema/tool failure → ERROR, retry_stage=RESEARCH, rounds unchanged

EDITOR_REEVALUATION
  IGNORE/PUBLISH/UPDATE_PROJECT → corresponding terminal state
  RESEARCH → RESEARCH_EXHAUSTED (Research is not called again)
```

`ERROR` never means a negative editorial decision. The next manual check can
resume its recorded retry stage. Agent calls are fresh Hermes oneshots.
Successful stage records include the policy hash, stable semantic input hash,
output, usage and timestamp. `retrieved_at` is excluded from the semantic hash.
Non-terminal work is reused only while policy and input hashes match.

## Crash behavior

- A validated agent result lost before persistence may be called again.
- A persisted successful stage is reused after restart.
- A persisted Evidence Pack resumes directly at Editor re-evaluation; Research
  is not repeated.
- A completed collection lost before checkpoint persistence may be recollected;
  source-level dedup makes this safe.
- Checkpoints are independent from per-item completion. A processing error
  after collection remains queryable even after that source checkpoint moves.

One PostgreSQL advisory lock prevents overlapping `news check` commands from
burning tokens against the same database. This is a single-runtime guard, not
a distributed workflow system.

## Running

Configure PostgreSQL and apply all migrations documented in
[`database.md`](database.md). Configure the source-backed Hermes MCP entries
from `hermes/tgl-mcp.example.yaml`, `ZAKUPKI_SEARCH_URL`, and when required the
external `ZAKUPKI_CA_FILE`. Then run:

```sh
go run ./cmd/urban-radar news check
```

Before a first live agent batch, inspect the same collection semantics without
committing state:

```sh
go run ./cmd/urban-radar news check --preflight
```

Preflight takes the same advisory lock, derives the same bootstrap/checkpoint
windows, calls the same collectors, and reports received/unique/new/known and
would-be processable counts. It does not create a `news_check_runs` row, write
or update a SourceItem, advance a checkpoint, initialize Hermes, or invoke an
agent.

Optional flags expose the overlap, bootstrap lookback, pagination safety bound,
repository root, model and provider. The command writes one JSON summary with
per-source windows/counts/errors, pipeline terminal counts and aggregate agent
usage.

Runtime V0 does not publish, poll in the background, update a project store,
perform cross-source semantic deduplication, or provide Research for TGL. A
future Telegram command `проверь новости` should be a thin adapter calling the
same `NewsCheckRunner`; it must not duplicate business logic.
