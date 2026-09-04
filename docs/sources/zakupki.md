# Zakupki Source Agent

The adapter turns bounded EIS procurement documents into normalized
`zakupki.Procurement` values and the shared `source.SourceItem` contract. It
does not make editorial relevance or candidate decisions. Every technically
valid normalized item is passed to the existing Discovery → Editor V1 pipeline.

EIS access is behind `zakupki.ProcurementSource`. `EISClient` uses the
configured `ZAKUPKI_EIS_URL` and optional `ZAKUPKI_EIS_TOKEN` environment
variables, a bounded limit and an HTTP timeout. The old zakupki.gov.ru FTP
interface is deliberately not used; EIS SOAP/XSD/SOI details can change and
remain behind this adapter boundary.

Fixture tests require no network, token, Hermes or database:

```sh
go test ./zakupki
```

The optional bounded live probe reads environment configuration and prints
normalized JSON without writing a database or invoking agents:

```sh
ZAKUPKI_EIS_URL='https://your-eis-endpoint.example/api' \
ZAKUPKI_EIS_TOKEN="$ZAKUPKI_EIS_TOKEN" \
go run ./cmd/zakupki-probe -limit 5
```

Without `ZAKUPKI_EIS_URL` it exits with an explanatory disabled-adapter error.
The V0 evaluation candidates are in `data/evals/zakupki-v0/items.json`; fill
`human_label` manually and run `go run ./cmd/zakupki-eval`.

Current limitations: EIS response schemas and credentials are deployment
specific; this milestone provides the adapter boundary and safe bounded probe,
not mass crawling or agent orchestration. The HTML source supports explicit
date-bounded, paginated retrieval; the live probe records a bounded
deterministic snapshot (maximum 50 items) and does not claim a complete
seven-day universe when that bound is reached.

The former deterministic locality/noise filter is retained only in
`zakupki/filter.go` and `cmd/zakupki-eval` to reproduce the rejected historical
real baseline (F1=0.1600). It is not part of runtime ingestion.

## Runtime pipeline decision

Old: `Zakupki → deterministic relevance filter → candidate`.

Current: `Zakupki → parse/normalize → SourceItem → Discovery Agent → Editor V1`.

The old filter was removed from runtime after the real-world baseline produced
TP=2, TN=26, FP=20, FN=1 and F1=0.1600. This is an architectural decision
derived from that evaluation; the historical evaluator and dataset remain
available for reproducibility.

## RSS-first live source

For the V0 live source, create an extended search on `zakupki.gov.ru`, apply
the desired region/customer/keyword filters, and copy its RSS link (the
`extendedsearch/rss.html` link). Set it only in the environment:

```sh
export ZAKUPKI_RSS_URL='https://zakupki.gov.ru/epz/order/extendedsearch/rss.html?...'
export ZAKUPKI_CA_FILE=/secure/path/russian-root-ca.pem
go run ./cmd/zakupki-probe -source rss -limit 20
go run ./cmd/zakupki-probe -source rss -limit 100 -export data/evals/zakupki-real-v0/items.json
```

The RSS reader makes one bounded request (maximum 100), supports RSS 2.0 and
Atom, deduplicates registry numbers/guid values, and never follows pagination.
For each entry it may issue one ordinary HTTP request to the public card URL;
card failures are recorded as enrichment/normalization errors and do not abort
the complete sample. No browser, CAPTCHA bypass, FTP, LLM or database is used.

The RSS URL is not committed; `capture.json` stores only its SHA-256 hash.
The real dataset must be frozen from a successful live capture, then reviewed
manually. Synthetic `zakupki-v0` metrics remain regression evidence and must
not be mixed with the real baseline.

If the host system does not trust the certificate chain used by
`zakupki.gov.ru`, set `ZAKUPKI_CA_FILE` to a PEM bundle obtained independently
from the relevant official certificate authority. The client loads the normal
system certificate pool first and appends this bundle; TLS certificate
verification remains enabled. The certificate file and its contents must not
be stored in the repository.

## HTML-first fallback

If RSS is unavailable, configure the public extended-search results URL as
`ZAKUPKI_SEARCH_URL` and run:

```sh
export ZAKUPKI_SEARCH_URL='https://zakupki.gov.ru/epz/order/extendedsearch/results.html?...'
go run ./cmd/zakupki-probe -source html -limit 20
go run ./cmd/zakupki-probe -source html -limit 50 -export data/evals/zakupki-real-v0/items.json
```

The HTML adapter supports bounded pagination. `-days 7` (the probe default)
passes `publishDateFrom` and `publishDateTo` in the EIS form's
`DD.MM.YYYY` format; `-limit` applies to the final unique raw universe and is
capped at 100 for this diagnostic probe. Runtime collection uses the separate
complete-window path: it has no item-count limit, and treats the 100-page
safety guard as an incomplete collection rather than advancing a checkpoint.
Pages retain all configured query parameters and change only `pageNumber`.
Results are deduplicated by registry ID and ordered by `published_at`
descending, then registry ID ascending. The adapter then performs one ordinary
HTTP card request per item for enrichment. It does not use a browser, bypass
CAPTCHA, or alter editorial decisions. HTML selectors are isolated in
`zakupki/html_search.go`.

### Canonical URL resolution

Historical captures may contain a known provenance bug: the first `regNumber`
anchor in a 44-FZ result could be the electronic-signature modal
(`printForm/listModal.html`). New parsing selects the structured
`.registry-entry__header-mid__number a` link instead and preserves the
notice-specific `.../view/common-info.html` URL supplied by EIS. The MCP uses
the same resolver through `ZAKUPKI_SEARCH_URL`; when a downstream caller has a
canonical `SourceItem.URL`, it may pass that URL together with the registry ID
as a validated provenance reference and avoid a second search. The URL must be
HTTPS on `zakupki.gov.ru`, use an allowed procurement-card path, contain the
matching `regNumber`, and must not be a print/signature modal. Without a valid
reference the existing search fallback remains available. Historical evaluation
artifacts are not rewritten.

## On-demand evidence access (Zakupki MCP)

The source ingestion path remains separate from evidence lookup:

```text
EIS → Zakupki Source → SourceItem → Discovery → Editor
EIS → Zakupki MCP → Research Agent
```

Run `go run ./cmd/zakupki-mcp` to expose the stdio MCP tools. Research Agent
V0.2 may use `get_procurement`, `list_procurement_documents`, and
`get_procurement_document`, plus trusted attachment tools
`list_procurement_attachments` and `get_procurement_attachment`. Inputs always
include a registry ID; an optional canonical `source_url` may be passed from a
trusted SourceItem. Document and attachment IDs must still come from the
corresponding listing tools; arbitrary URLs are rejected.
The server reuses the verified HTTP client, card fetcher and card parser. It
returns primary-source provenance and stable structured errors. Downloads are
bounded; V0 extracts text from HTML, XML and plain text, and trusted DOCX/XLSX
attachments are extracted by bounded deterministic parsers. PDF currently
returns `UNSUPPORTED_DOCUMENT_TYPE`. Configure `ZAKUPKI_CA_FILE`
when the host requires the independently obtained official CA bundle.

## Research result semantics

Research `NOT_FOUND` means that confirmation was not found in the investigated
available primary sources; it does not establish that a fact does not exist.
The `unresolved` list is reserved for questions that were not adequately
investigated or remain operationally open. Therefore an overall `PARTIAL`
result containing FOUND and NOT_FOUND findings with `unresolved=[]` is valid:
every question was processed, but not every answer was evidenced.

Human review validates claims against the cited primary evidence. Review text
is a gate for Editor re-evaluation and is not itself an evidence source.
