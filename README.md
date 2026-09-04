# Urban Radar

Urban Radar («Тольятти меняется») detects physical and socially significant
changes in the city. Procurement is one source signal, not the product itself.

## Current architecture

```text
Source → Normalize → SourceItem → Discovery → Editor V1
                                              ├─ PUBLISH
                                              ├─ IGNORE
                                              ├─ UPDATE_PROJECT
                                              └─ RESEARCH
                                                  ↓
Research Request → Research V0.2 → primary-source tools → Evidence Pack
    → human evidence validation → unchanged Editor V1 re-evaluation
```

Go implements deterministic source, parsing, validation and MCP tool
boundaries. Hermes provides isolated agent/tool execution; LLMs provide
reasoning. Eval harnesses under `scripts/` are experiment infrastructure, not
production orchestration. Agent memory is never a source of truth.

The common `source.SourceItem` identity is `(source, source_item_id)`. For EIS
procurements its canonical URL is handed through `Research Request.source_url`
to the validated `zakupki.ProcurementRef`; the MCP falls back to registry-ID
search only when this provenance URL is absent.

## Validated now

- TGL and Zakupki source parsing/normalization, including bounded paginated EIS
  HTML retrieval with explicit Europe/Samara calendar dates.
- High-recall Discovery transfer from TGL to Zakupki (real baseline recall
  `1.0`; precision was deliberately not prompt-tuned).
- Editor V1 as the single current editorial policy.
- Zakupki MCP primary-source access with trusted procurement provenance and
  bounded DOCX/XLSX attachment extraction.
- Research V0.2 primary-evidence workflow.
- A harness-driven live Editor → Research → human validation → unchanged
  Editor loop. Procurement `0142200001326017137` went from `RESEARCH` to
  `PUBLISH` in run `20260904T172913Z` after 18/18 evidence claims were human
  validated from Research run `20260904T161529Z`.

This validates the architecture experimentally; it is not autonomous or
production orchestration.

## Historical and prepared components

The deterministic Zakupki relevance filter is a rejected legacy baseline
(`F1=0.1600`) retained only for reproducibility. It is not in the runtime
pipeline. Historical prompt and run versions remain immutable evidence.

`storage/` and `migrations/` prepare a PostgreSQL repository, but no database
is active or wired into the runtime. Scheduling, persistent temporal memory,
publishers, Telegram/MAX delivery, autonomous operation and a Content Agent
remain future work.

Future user-facing rendering should expand specialist abbreviations on first
use (for example, «МАФ» → «малые архитектурные формы») and prefer concrete
objects such as benches, bins and playground equipment when primary evidence
provides them. This is a content/rendering rule, not Discovery, Editor or
Research policy.

## Local commands

```sh
go run ./cmd/urban-radar tgl list
go run ./cmd/urban-radar tgl get https://tgl.ru/news/item/25860-razgovor-o-vazhnom/
go run ./cmd/tgl-mcp
go run ./cmd/zakupki-mcp
```

The MCP commands use stdio for JSON-RPC and stderr for diagnostics. The
source-backed Hermes development configuration is documented in
`hermes/tgl-mcp.example.yaml`; it must not point to a temporary compiled
binary. EIS TLS verification remains enabled, with an external CA bundle set
through `ZAKUPKI_CA_FILE` where required. Certificates and credentials do not
belong in Git.

See `docs/sources/zakupki.md` for source/MCP details and `docs/database.md` for
the prepared-but-inactive persistence layer.
