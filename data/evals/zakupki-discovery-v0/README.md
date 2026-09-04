# Zakupki → Discovery V0 transfer baseline

Frozen factual summary for run `20260904T093618Z`. Runtime artifacts under
`runs/` are intentionally ignored; this file records the reproducible result.

## Configuration

- dataset: `data/evals/zakupki-real-v0/items.json`
- dataset SHA-256: `5bd73e8c7ff805f6595549a524062e09ef82a27bcadbaf92b27d41506f8dd08d`
- prompt: `agents/discovery/prompt.md`
- prompt SHA-256: `494f2d28df84bb095d052863f1e722469f8f7b8dde5e44aa76d112cefa9f80d7`
- labelled items: 49
- excluded unlabeled items: 1

## Metrics

```text
attempted=49 successful=49 errors=0
TP=3 TN=40 FP=6 FN=0
precision=0.3333 recall=1.0000 F1=0.5000 accuracy=0.8776
discovery_candidates=9
editor_load_reduction=81.63%
```

All human positives were found:

```text
32616305082
0142200001326016814
0142200001326017137
```

False positives:

```text
32616328759
32616328717
32616329341
0342300036026000222
0142200001326016812
0342300183126000003
```

Usage: 49 API calls, 187698 input tokens, 7746 output tokens,
estimated cost `0.013598746 USD`.

Conclusion: the existing Discovery prompt generalized from TGL to Zakupki
without prompt changes. Recall is the primary metric; recall 1.0 means no
human-positive procurement was lost. Discovery reduced downstream Editor load
from 49 to 9 items (81.63%). Do not tune the prompt based on this run.
