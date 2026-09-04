# Zakupki Source Agent

The adapter turns bounded EIS procurement documents into normalized
`zakupki.Procurement` values and the shared `source.SourceItem` contract. It
does source-level locality and noise filtering; it never makes the final
`PUBLISH` decision. Candidates can be sent to the existing Discovery → Editor
V1 pipeline later.

The deterministic filter marks records `relevant`, `maybe_relevant` or
`irrelevant`, with human-readable `relevance_reasons`. Explicit Togliatti
locations and municipal institutions are strong signals. Samara-region-only
records without a concrete city signal are not forwarded. Category and price
signals reduce routine procurement noise without a permanent category
whitelist.

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
