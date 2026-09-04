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
not mass crawling or agent orchestration.

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

Only the first result page is requested. The adapter extracts registry numbers
and detail links from result-card DOM, then may perform one ordinary HTTP card
request per item for enrichment. It does not paginate, use a browser, bypass
CAPTCHA, or alter the deterministic source filter. HTML selectors are isolated
in `zakupki/html_search.go`; the existing business filtering remains in
`zakupki/filter.go`.

### Canonical URL resolution

Historical captures may contain a known provenance bug: the first `regNumber`
anchor in a 44-FZ result could be the electronic-signature modal
(`printForm/listModal.html`). New parsing selects the structured
`.registry-entry__header-mid__number a` link instead and preserves the
notice-specific `.../view/common-info.html` URL supplied by EIS. The MCP uses
the same resolver through `ZAKUPKI_SEARCH_URL`; it never guesses `ea20/eap20`
or falls back to a print-form modal. Historical evaluation artifacts are not
rewritten.

## On-demand evidence access (Zakupki MCP)

The source ingestion path remains separate from evidence lookup:

```text
EIS → Zakupki Source → SourceItem → Discovery → Editor
EIS → Zakupki MCP → Research Agent
```

Run `go run ./cmd/zakupki-mcp` to expose three stdio MCP tools for a future
Research Agent: `get_procurement`, `list_procurement_documents`, and
`get_procurement_document`. All inputs are registry IDs (and, for the last
tool, a document ID returned by the listing tool); arbitrary URLs are rejected.
The server reuses the verified HTTP client, card fetcher and card parser. It
returns primary-source provenance and stable structured errors. Downloads are
bounded; V0 extracts text from HTML, XML and plain text only. PDF, DOCX and
XLSX currently return `UNSUPPORTED_DOCUMENT_TYPE`. Configure `ZAKUPKI_CA_FILE`
when the host requires the independently obtained official CA bundle.
