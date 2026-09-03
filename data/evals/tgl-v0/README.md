# tgl-v0 evaluation dataset

`items.json` contains the latest 100 city-news metadata records from tgl.ru.
The dataset is versioned in Git. Labels are intentionally assigned only by a
human; leave a label as `null` until it has been reviewed.

## Discovery label

Set `human_discovery` to `true` when a material contains, or could contain, a
real city change and should be passed to the Editor Agent. Set it to `false`
for congratulations, standalone events, commemorative dates, ordinary meetings,
personnel news, sports results, and other obvious noise.

## Editor labels

- `PUBLISH`: the change is interesting enough for a standalone post.
- `IGNORE`: a real change exists but is too small, local, or insignificant.
- `RESEARCH`: it may be important, but the available information is insufficient.
- `UPDATE_PROJECT`: it is a new stage of an already known project.

Set `human_importance` from `0.0` (almost no public interest) to `1.0` (a
city-scale change with major impact on residents). Record the concise rationale
in `human_reason`.

## Commands

From the project root:

```sh
go run ./cmd/build-tgl-eval
go run ./cmd/validate-tgl-eval
```

Compact historical evidence for the V0 baseline and accepted Editor V1 run is
kept under `evidence/`. Full per-item Hermes work directories remain ignored
runtime artifacts.
