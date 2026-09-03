# Urban Radar

## tgl.ru

```sh
go run ./cmd/urban-radar tgl list
go run ./cmd/urban-radar tgl get https://tgl.ru/news/item/25860-razgovor-o-vazhnom/
```

Both commands write JSON to standard output.

## Hermes MCP

The `tgl-mcp` command is a local stdio MCP server. Its standard output is
reserved for MCP JSON-RPC; diagnostics go to standard error. An example Hermes
configuration is in `hermes/tgl-mcp.example.yaml`.

## V0 pipeline

With `urban-radar-tgl` configured in Hermes MCP, run one isolated Discovery →
Editor pass from any directory:

```sh
./scripts/run-v0.sh
```

Artifacts are created under `data/runs/<run_id>/` and are ignored by Git.
