# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS go-builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY content ./content
COPY newscheck ./newscheck
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
 && /opt/hermes-venv/bin/pip install --no-cache-dir --upgrade pip \
 && /opt/hermes-venv/bin/pip install --no-cache-dir "git+https://github.com/NousResearch/hermes-agent.git@${HERMES_REF}"

FROM python:3.12-slim-bookworm AS runtime
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tini \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --gid 10001 urban-radar \
 && useradd --uid 10001 --gid urban-radar --create-home --home-dir /home/urban-radar --shell /usr/sbin/nologin urban-radar

COPY --from=hermes-builder /opt/hermes-venv /opt/hermes-venv
COPY --from=go-builder /out/urban-radar /usr/local/bin/urban-radar
COPY --from=go-builder /out/tgl-mcp /usr/local/bin/tgl-mcp
COPY --from=go-builder /out/zakupki-mcp /usr/local/bin/zakupki-mcp
COPY agents/discovery/prompt-runtime-v0.md /app/agents/discovery/prompt-runtime-v0.md
COPY agents/editor/prompt-v1.md /app/agents/editor/prompt-v1.md
COPY agents/research/prompt-v0.2.md /app/agents/research/prompt-v0.2.md
COPY agents/content/prompt-v1.md /app/agents/content/prompt-v1.md

ENV PATH=/opt/hermes-venv/bin:/usr/local/bin:/usr/bin:/bin \
    HOME=/home/urban-radar \
    HERMES_HOME=/var/lib/hermes \
    TMPDIR=/tmp
WORKDIR /app
USER urban-radar
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/urban-radar"]
