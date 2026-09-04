# Zakupki Source Agent V0 evaluation

`items.json` is a 30-item candidate set covering positive, negative, hard
negative and borderline procurement shapes. `human_label` is intentionally
`null` until a person verifies each source record; no model-generated labels
are ground truth. The records are normalized evaluation candidates and retain
their EIS registry identifiers/URLs where available.

Allowed human labels are `true` (should become a source candidate) and `false`
(should be filtered as noise). After manual labelling, run:

```sh
go run ./cmd/zakupki-eval
```

This eval measures only source normalization, Togliatti relevance and noise
filtering. It does not invoke Hermes, Discovery, Editor or PostgreSQL.
