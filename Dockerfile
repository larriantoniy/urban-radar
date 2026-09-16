# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS go-builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY content ./content
COPY newscheck ./newscheck
COPY publisher ./publisher
COPY runtime ./runtime
COPY source ./source
COPY storage ./storage
COPY tgl ./tgl
COPY zakupki ./zakupki

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/urban-radar ./cmd/urban-radar \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/tgl-mcp ./cmd/tgl-mcp \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/zakupki-mcp ./cmd/zakupki-mcp

FROM python:3.12-slim-bookworm AS hermes-builder
ARG HERMES_REF=05f548f35dd3242bf2ff74743e9112acde251f77

RUN apt-get update \
 && apt-get install -y --no-install-recommends git \
 && rm -rf /var/lib/apt/lists/* \
 && python -m venv /opt/hermes-venv \
 && /opt/hermes-venv/bin/pip install --no-cache-dir --upgrade pip uv \
 && git init -q /opt/hermes-src \
 && git -C /opt/hermes-src remote add origin https://github.com/NousResearch/hermes-agent.git \
 && git -C /opt/hermes-src fetch -q --depth=1 origin "${HERMES_REF}" \
 && git -C /opt/hermes-src checkout -q --detach FETCH_HEAD \
 && test "$(git -C /opt/hermes-src rev-parse HEAD)" = "${HERMES_REF}" \
 && cd /opt/hermes-src \
 && UV_PYTHON=/opt/hermes-venv/bin/python UV_PROJECT_ENVIRONMENT=/opt/hermes-venv /opt/hermes-venv/bin/uv sync --locked \
 && /opt/hermes-venv/bin/hermes --help >/dev/null

FROM python:3.12-slim-bookworm AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tini \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 10001 urban-radar \
 && useradd --uid 10001 --gid urban-radar --create-home --home-dir /home/urban-radar --shell /usr/sbin/nologin urban-radar

COPY --from=hermes-builder /opt/hermes-venv /opt/hermes-venv
COPY --from=hermes-builder /opt/hermes-src /opt/hermes-src
COPY --from=go-builder /out/urban-radar /usr/local/bin/urban-radar
COPY --from=go-builder /out/tgl-mcp /usr/local/bin/tgl-mcp
COPY --from=go-builder /out/zakupki-mcp /usr/local/bin/zakupki-mcp
COPY agents/discovery/prompt-runtime-v0.md /app/agents/discovery/prompt-runtime-v0.md
COPY agents/editor/prompt-v1.md /app/agents/editor/prompt-v1.md
COPY agents/research/prompt-v0.2.md /app/agents/research/prompt-v0.2.md
COPY agents/content/prompt-v1.md /app/agents/content/prompt-v1.md
COPY .hermes/skills/urban-radar-editorial-style-v1/SKILL.md /app/.hermes/skills/urban-radar-editorial-style-v1/SKILL.md

ENV PATH=/opt/hermes-venv/bin:/usr/local/bin:/usr/bin:/bin \
    HOME=/home/urban-radar \
    HERMES_HOME=/var/lib/hermes \
    TMPDIR=/tmp
WORKDIR /app
RUN /opt/hermes-venv/bin/pip install --no-cache-dir "requests[socks]"
USER urban-radar
RUN /opt/hermes-venv/bin/python --version \
 && /opt/hermes-venv/bin/python -c 'import hermes_cli; assert hermes_cli.__file__.startswith("/opt/hermes-src/")' \
 && /opt/hermes-venv/bin/python -c 'import requests, socks' \
 && test -r /app/.hermes/skills/urban-radar-editorial-style-v1/SKILL.md \
 && /opt/hermes-venv/bin/hermes --help >/dev/null
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/urban-radar"]
