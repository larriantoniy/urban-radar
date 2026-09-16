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

## Per-item Discovery execution

The historical Discovery evaluation prompt remains a batch contract. Runtime
uses `agents/discovery/prompt-runtime-v0.md` instead: it receives one already
normalized SourceItem and returns exactly one of:

```json
{"outcome":"DROP"}
```

or a single `CANDIDATE` object. The runtime decoder rejects unknown fields,
multiple-candidate batch shapes, a candidate on `DROP`, and a missing candidate
on `CANDIDATE`. This changes execution shape only; the high-recall relevance
policy remains the same.

Each Discovery invocation has an end-to-end 90-second default timeout,
configurable with `--discovery-timeout`. Timeout cancels the Hermes process and
becomes retryable `ERROR` with `retry_stage=DISCOVERY`; it is never retried in
the same news check.

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

## Run lifecycle

`news_check_runs` begins as `RUNNING` and is finalized even when the CLI
context receives SIGINT or SIGTERM:

- `COMPLETED`: normal completed batch, including item-level retryable errors;
- `INTERRUPTED`: SIGINT, SIGTERM, or cancelled runtime context;
- `FAILED`: a whole-run failure such as no successful source collection or a
  fatal runtime-store error.

Every terminal run stores `finished_at` and its current structured summary.
Source checkpoints already advanced by a completed collection are not rolled
back on later interruption.

## Validated V0 runtime baseline

On 2026-09-05, the first live run
`20260905T084906.673212000Z` was interrupted. It exposed an execution-contract
bug: a one-item runtime invocation used the historical batch `candidates[]`
shape, producing 35 Discovery errors (32 were `expected at most one candidate`)
and one hung Hermes invocation. The historical run remains preserved as
`RUNNING` evidence of that failure.

Regression run `20260905T102253.966491000Z` then completed in 449.12 seconds:

- 71 processable SourceItems completed, with none remaining;
- Discovery: 71 attempts, 66 `DROP`, 5 `CANDIDATE`, and zero technical,
  schema, JSON, or timeout errors;
- Editor: four `PUBLISH` decisions (`READY_TO_PUBLISH`) and one `IGNORE`;
- 32 former batch-shape errors became 29 `DROP` and three
  `CANDIDATE` → `READY_TO_PUBLISH`, with zero errors.

This validates V0 for observed daily Discovery/Editor runtime operation. It
does **not** authorize autonomous publishing: `READY_TO_PUBLISH` remains a
human-in-the-loop editorial candidate. The next evaluation stage is daily
human-reviewed editorial evaluation, not automated publication.

## Content Experiment V1 baseline

On 2026-09-05, Content Agent V1 generated four drafts from existing
`READY_TO_PUBLISH` items only: `tgl/25864`, `tgl/25865`, `tgl/25868`, and
`zakupki/0142200001326017126`. Human review found no factual, stage-truth, or
source-provenance failures: one draft was `APPROVED` and three were
`APPROVED_WITH_NOTES` for editorial style. The notes identify two future style
questions: avoid ceremonial attendee lists that do not explain the city change,
and do not ask readers to discover an unconfirmed material fact.

The experiment produced four valid persisted drafts in nine API calls; five
responses were rejected by the structured-output contract. Reported aggregate
usage was 141,212 tokens and $0.005712, while the four accepted drafts used
64,901 tokens and $0.003547. This is a small, human-reviewed experiment, not
authorization for autonomous publication. The pipeline boundary remains:

```text
READY_TO_PUBLISH → ContentDraft → HUMAN REVIEW → future Publisher
```

No VK API or publisher is implemented or invoked.

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

## Ubuntu Docker Compose daily operation

Urban Radar remains a one-shot CLI. The operating system, not Go and not a
container scheduler, owns scheduling and overlap prevention:

```text
Ubuntu cron → host wrapper → flock -n → docker compose run --rm urban-radar news check
                                      → Hermes / configured MCP tools → PostgreSQL
```

Cron runs on the Ubuntu host. `postgres` and `hermes-gateway` are the
long-running Compose services; `urban-radar` is created for one CLI invocation
and removed after it exits. PostgreSQL is private to the Compose network and
persists in the named `postgres_data` volume.

After a successful (including the existing exit-0 `PARTIAL`) `news check`, the
same wrapper runs `urban-radar content process-ready`. That command selects the
stable `READY_TO_PUBLISH` queue, generates or reuses content drafts, and sends
only undelivered pending review cards. A non-zero NewsCheck prevents this
downstream stage; a downstream error makes the wrapper non-zero. Neither stage
publishes to VK: publication remains the committed Telegram human-approval
flow.

`content process-ready` runs in the one-shot `urban-radar` container and uses
the existing plugin-owned sender at
`/var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py`.
That container therefore receives the same `TELEGRAM_BOT_TOKEN` and
`TELEGRAM_HOME_CHANNEL` as the gateway, plus the same absolute
`URBAN_RADAR_REVIEW_COMMAND=/usr/local/bin/urban-radar` and bounded
`URBAN_RADAR_REVIEW_COMMAND_TIMEOUT_SECONDS` plugin-loader contract. Callback
authorization remains gateway-only and continues to use `TELEGRAM_ALLOWED_USERS`.
The sender file in the operator-owned Hermes home must be executable.

The image installs the pinned Hermes `messaging` extra through its locked
dependency graph. This provides the pinned `python-telegram-bot` dependency for
both the long-running gateway and the one-shot notification sender; it is not
installed at container startup.

Project-local `.hermes/skills` under `/app` is not a production Hermes runtime
source: the image is not a Git checkout and Hermes does not discover that path
as a project repository. Git remains the source of truth, but the persistent
Hermes profile is the runtime location:

```text
/srv/urban-radar/
  .hermes/skills/urban-radar-editorial-style-v1/
  hermes/telegram_review_plugin_experiment/
      ↓ deploy sync
/opt/urban-radar/hermes/
  skills/urban-radar-editorial-style-v1/
  plugins/urban-radar-telegram-review-experiment/
      ↓ Docker bind mount
/var/lib/hermes/
```

Run the root-only asset operation after every Git update, never from cron:

```sh
sudo /srv/urban-radar/scripts/deploy-hermes-assets.sh
```

It atomically replaces only the managed Urban Radar skill/plugin directories,
removes stale files within those directories, sets ownership to `10001:10001`,
and preserves all other Hermes configuration, skills and plugins.

For ordinary deployment use the repository Makefile as the normal `radar`
user. It keeps Git and Docker under that user and uses `sudo` only for the
numeric container-ownership asset sync:

```sh
cd /srv/urban-radar
make deploy            # preflight, git pull --ff-only, assets, build, gateway reload, verify
make deploy-no-pull    # deploy the current checkout without changing Git state
make deploy-assets     # managed Hermes assets plus host assertion only
make verify-production # safe runtime verification only
make production-status # Compose and Hermes gateway status
```

`make verify-production` is fail-closed. `make production-status` is also
read-only, but prints managed-asset, Compose, PostgreSQL and gateway diagnostics
before returning non-zero when any observed component is unhealthy.

### Install Docker and deploy

The following is a clean personal Ubuntu VPS procedure. The host operator is
the existing Linux user `radar`; do not create a separate host user for Urban
Radar. Dockerfile references to `urban-radar` are intentionally a distinct,
non-root *container* identity.

```sh
sudo apt update
sudo apt install -y ca-certificates curl git make
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo \"${UBUNTU_CODENAME:-$VERSION_CODENAME}\") stable" | sudo tee /etc/apt/sources.list.d/docker.list >/dev/null
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo docker version
sudo docker compose version

sudo usermod -aG docker radar
sudo install -d -o radar -g radar /srv/urban-radar /opt/urban-radar/{bin,logs,run,secrets}
sudo install -d -m 0750 /opt/urban-radar/{hermes,media}
sudo chown 10001:10001 /opt/urban-radar/{hermes,media}
git clone <REPOSITORY_URL> /srv/urban-radar
git -C /srv/urban-radar checkout <VALIDATED_COMMIT>
sudo install -m 700 -o radar -g radar /srv/urban-radar/scripts/run-news-check.sh /opt/urban-radar/bin/run-news-check
cp /srv/urban-radar/.env.example /opt/urban-radar/.env
chmod 600 /opt/urban-radar/.env
```

Run the commands after the initial host-administration commands as `radar`.
Log out and back in before running Docker commands so the new Docker group
membership applies. Docker-group membership effectively grants very high host
privileges; use it only for this trusted personal-server operator. The
deployment layout is owned by `radar`:

```text
/srv/urban-radar/                 # Git checkout: Dockerfile and compose.yaml
/opt/urban-radar/.env             # owner-readable Compose values
/opt/urban-radar/bin/run-news-check
/opt/urban-radar/hermes/          # Hermes config, auth and state; UID/GID 10001; not Git
/opt/urban-radar/media/           # shared media; UID/GID 10001; not Git
/opt/urban-radar/logs/news-check.log
/opt/urban-radar/run/news-check.lock
/opt/urban-radar/secrets/         # optional externally obtained EIS CA
```

There are no host Urban Radar, TGL MCP, or Zakupki MCP binaries in this layout:
the image contains `/usr/local/bin/urban-radar`, `/usr/local/bin/tgl-mcp`, and
`/usr/local/bin/zakupki-mcp`. The image contains the exact pinned Hermes Agent
revision declared by `Dockerfile`, Python, CA certificates, the three compiled
binaries, and the four runtime policy prompt files. It does not contain Go,
Git, the checkout, provider credentials, `.env`, certificates, or API keys.

### Environment, Hermes and optional EIS CA

Edit `/opt/urban-radar/.env` from `.env.example`. Required values are
`POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, a URL-escaped
`DATABASE_URL` whose host is `postgres` and port is `5432`,
`ZAKUPKI_SEARCH_URL`, `HERMES_HOME_HOST_DIR`, `URBAN_RADAR_MEDIA_HOST_DIR`,
`VK_ACCESS_TOKEN`, `VK_GROUP_ID`, `TELEGRAM_BOT_TOKEN`,
`TELEGRAM_HOME_CHANNEL`, and `TELEGRAM_ALLOWED_USERS`.
`TELEGRAM_HOME_CHANNEL_NAME` is optional deployment metadata; current Compose
does not inject it into a container. `URBAN_RADAR_REVIEW_COMMAND_TIMEOUT_SECONDS`
is the bounded gateway-to-CLI timeout and defaults to `45` seconds. Do not put
the database URL or any provider credential in cron or Compose source files.

`docker compose run --rm urban-radar news check` still interpolates the entire
`compose.yaml` before selecting the one-shot service. Therefore all required
variables, including the Telegram variables used by `hermes-gateway`, must be
present in `/opt/urban-radar/.env` even when only `urban-radar` is run. Keep
the `${VAR:?required}` Compose guards intact: they fail closed on an incomplete
production configuration.

### Host SOCKS/Xray dependency

The current Compose topology sets
`ALL_PROXY=socks5://host.docker.internal:10808` for both Urban Radar and the
Hermes gateway. The production host must provide a SOCKS5 listener there; the
current deployment uses Xray. On Linux Docker, Compose maps
`host.docker.internal` to the bridge gateway with
`extra_hosts: host.docker.internal:host-gateway`, so the Xray listener must be
reachable from that bridge. Do not store VLESS credentials in this repository
or `.env.example`.

Before activation, verify the proxy path without calling Urban Radar, Telegram,
or VK:

```sh
docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  curlimages/curl:latest \
  -sS \
  --socks5-hostname host.docker.internal:10808 \
  https://api.ipify.org
```

Hermes owns model/provider selection, provider authentication and MCP settings;
Go does not read an OpenRouter key itself. Set
`HERMES_HOME_HOST_DIR=/opt/urban-radar/hermes` and configure its `config.yaml` with the
container-path MCP commands in
[`hermes/mcp-container.example.yaml`](../hermes/mcp-container.example.yaml).
Hermes credentials remain inside that operator-owned directory and are mounted
into the application and gateway containers as `HERMES_HOME=/var/lib/hermes`.
Because containers run as UID/GID `10001`, both bind-mounted Hermes home and
media directory must be owned by `10001:10001` and mode `0750`; do not use
world-writable permissions. Do not copy the review plugin or Content skill by
hand: `deploy-hermes-assets.sh` installs their managed runtime copies from the
validated checkout with the required ownership and modes.

### Telegram review gateway

Compose runs `hermes-gateway` as the long-running Telegram transport alongside
PostgreSQL. The one-shot `urban-radar` service remains the manual/cron runtime.
Both mount the same host media directory at `/var/lib/urban-radar/media` and
receive the same `DATABASE_URL`, `VK_ACCESS_TOKEN`, and `VK_GROUP_ID`.

The gateway keeps its root filesystem read-only. Hermes' token-scoped gateway
lock is explicitly placed at `/var/lib/hermes/gateway-locks` through
`HERMES_GATEWAY_LOCK_DIR`; it is part of the existing UID/GID `10001`
`/var/lib/hermes` bind mount. It replaces the Hermes default
`$HOME/.local/state` only for this token-scoped lock. Persistent
profile/configuration state remains under that same mount.

When a gateway is deliberately deployed, its own process environment must
contain both of these values before plugin discovery:

```text
URBAN_RADAR_REVIEW_COMMAND=<absolute executable path>
DATABASE_URL=<PostgreSQL URL reachable from that gateway process>
```

The review plugin validates both at registration. A missing value, a relative
path, or a non-executable command prevents the plugin from registering; it
never falls back to an interactive shell or a guessed `urban-radar` command.
Values are provided by the Compose `environment` block sourced from the
operator-owned `/opt/urban-radar/.env`. They must not be added to Git, a plugin
manifest, or the image. `URBAN_RADAR_REVIEW_COMMAND_TIMEOUT_SECONDS` defaults
to 45 seconds; expiry is fail-closed and never retries a command.

The local macOS gateway currently demonstrates the launchd variant with an
absolute operator-owned Urban Radar binary path. Hermes loads its managed home
before plugin discovery; the plugin registration preflight then verifies the
effective process environment without logging any value. This is a
configuration check only and does not contact Telegram, PostgreSQL, or VK.

For this container topology, the stable command is
`/usr/local/bin/urban-radar`, because that is the path baked into the existing
Urban Radar image. The gateway receives `DATABASE_URL` from the same
operator-owned Compose environment and mounts the configured Hermes home
containing the review plugin. No systemd unit or second scheduler is added.

If EIS requires an external CA, place it at
`/opt/urban-radar/secrets/Russian_Trusted_CA.pem`, mode 600, and set:

```sh
ZAKUPKI_CA_HOST_PATH='/opt/urban-radar/secrets/Russian_Trusted_CA.pem'
ZAKUPKI_CA_FILE='/run/secrets/Russian_Trusted_CA.pem'
```

Otherwise leave both variables empty. The CA is a read-only mount and is never
copied into the image or Git.

### VPS validation, database, migrations and first controlled run

For a normal update, use the single deployment command:

```sh
cd /srv/urban-radar
make deploy
```

`make deploy` builds the shared image, starts PostgreSQL only if absent with
`--no-recreate`, and recreates only `hermes-gateway` so it reloads the new
image and synchronized plugin. `urban-radar` remains a one-shot Compose
service and uses the rebuilt image on its next `docker compose run --rm`.

Before activating cron, run these safe configuration checks as `radar`. They
do not call Urban Radar collection, Telegram, or VK:

```sh
cd /srv/urban-radar

# The managed directories are intentionally UID/GID 10001 and mode 0750.
sudo test -f /opt/urban-radar/hermes/skills/urban-radar-editorial-style-v1/SKILL.md
sudo test -x /opt/urban-radar/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py

docker compose \
  --env-file /opt/urban-radar/.env \
  run --rm \
  --entrypoint sh \
  urban-radar \
  -lc '
    test -f /var/lib/hermes/skills/urban-radar-editorial-style-v1/SKILL.md &&
    echo "SKILL OK"

    head -n 1 /var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py

    test -x /var/lib/hermes/plugins/urban-radar-telegram-review-experiment/review_notify.py &&
    echo "SENDER EXECUTABLE"

    test -n "${URBAN_RADAR_REVIEW_COMMAND:-}" &&
    echo "REVIEW COMMAND SET"

    /opt/hermes-venv/bin/python -c \
      "from telegram import Bot, InlineKeyboardButton, InlineKeyboardMarkup; print('telegram runtime: OK')"
  '

docker compose \
  --env-file /opt/urban-radar/.env \
  config

docker compose \
  --env-file /opt/urban-radar/.env \
  ps

# Print only set/missing markers, never variable values or secrets.
docker compose \
  --env-file /opt/urban-radar/.env \
  run --rm --no-deps --entrypoint /bin/sh urban-radar -c '
    for name in DATABASE_URL ZAKUPKI_SEARCH_URL HERMES_HOME URBAN_RADAR_MEDIA_DIR VK_ACCESS_TOKEN VK_GROUP_ID; do
      eval "value=\${$name:-}"
      if [ -n "$value" ]; then printf "%s=set\\n" "$name"; else printf "%s=missing\\n" "$name"; fi
    done
  '

docker compose \
  --env-file /opt/urban-radar/.env \
  exec hermes-gateway \
  /opt/hermes-venv/bin/hermes gateway status

docker compose \
  --env-file /opt/urban-radar/.env \
  exec hermes-gateway \
  /opt/hermes-venv/bin/hermes plugins list --plain --no-bundled

crontab -l
sudo crontab -l
```

The production schedule belongs to the non-root `radar` user; `sudo crontab -l`
is only a read-only check that no separate root schedule was installed.

Apply migrations explicitly in filename order. They are not applied during an
application start, and migration rollback is not automatic:

```sh
set -eu
set -a; . /opt/urban-radar/.env; set +a
for migration in /srv/urban-radar/migrations/*.sql; do
  docker compose --project-directory /srv/urban-radar --env-file /opt/urban-radar/.env -f /srv/urban-radar/compose.yaml exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$migration"
done
```

Manually run the same production wrapper that cron will use, then inspect its
log, the latest persisted run and source checkpoints. Do this once before
installing cron; do not substitute a direct `docker compose run`. The wrapper
is the production executable at `/opt/urban-radar/bin/run-news-check` and
validates env sourcing, project path, `flock`, log path, Docker invocation, and
child exit propagation:

```sh
/opt/urban-radar/bin/run-news-check
tail -n 200 /opt/urban-radar/logs/news-check.log
set -a; . /opt/urban-radar/.env; set +a
docker compose --project-directory /srv/urban-radar --env-file /opt/urban-radar/.env -f /srv/urban-radar/compose.yaml exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT run_id,status,started_at,finished_at FROM news_check_runs ORDER BY started_at DESC LIMIT 1;"
docker compose --project-directory /srv/urban-radar --env-file /opt/urban-radar/.env -f /srv/urban-radar/compose.yaml exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT source,last_successful_collection_at FROM source_checkpoints ORDER BY source;"
```

The wrapper takes the host-side non-blocking lock
`/opt/urban-radar/run/news-check.lock`. A competing manual or cron invocation
exits 75 before it starts a container or touches PostgreSQL. It appends both
container stdout and stderr to `/opt/urban-radar/logs/news-check.log`; inspect
it with `tail -n 200 /opt/urban-radar/logs/news-check.log`. Successful CLI
execution exits 0. Runtime/bootstrap failures propagate non-zero status;
item-level retryable errors may still result in a normal CLI exit when the
persisted NewsCheck summary is `PARTIAL`.

Log rotation is not implemented in this deployment contract. Monitor
`/opt/urban-radar/logs/news-check.log` and add a host logrotate policy only as
a separate operational change.

### Cron, backup, upgrade and rollback

Confirm the server timezone with `timedatectl`, then, as `radar`, run
`crontab -e` and add this host crontab entry. It contains no secrets, no
`sudo`, and never runs inside a container:

```cron
CRON_TZ=Europe/Samara
30 20 * * * /opt/urban-radar/bin/run-news-check
```

Use the same wrapper for a safe manual run. To pause scheduling, comment out
that line with `crontab -e`; do not delete checkpoints or runtime state.

Take a manual PostgreSQL backup before an upgrade:

```sh
set -a; . /opt/urban-radar/.env; set +a
docker compose --project-directory /srv/urban-radar --env-file /opt/urban-radar/.env -f /srv/urban-radar/compose.yaml exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc > urban-radar-$(date +%F).dump
```

To upgrade, check out the intended commit in `/srv/urban-radar`, build it,
apply any new forward-only migrations explicitly, then validate one manual
wrapper run before restoring cron operation. To roll back application code,
check out the previous known-good commit and rebuild the image. Database
migrations are not rolled back automatically; restore a compatible database
backup only through a deliberate recovery procedure.

The wrapper does not bypass Hermes/provider permission guards. Unattended cron
is viable only after the deployment's Hermes configuration has an operator-
approved non-interactive authorization policy for the exact NewsCheck calls.
