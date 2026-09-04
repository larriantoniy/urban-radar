# Zakupki real V0 dataset

This frozen dataset contains 50 normalized real procurement records captured by
the bounded EIS HTML probe. Human labels are stored in `items.json`; 49 records
are labelled (3 `CANDIDATE`, 46 `SKIP`) and one remains unlabeled. `capture.json`
records capture metadata. Source procurement fields and system decisions are
unchanged; future edits may add only human labels.

## Baseline

The deterministic source-filter baseline over the 49 labelled records is:

```text
TP=2 TN=26 FP=20 FN=1
precision=0.0909 recall=0.6667 F1=0.1600
normalization_errors=0
```

This is a real-world source-layer baseline, separate from the synthetic
`zakupki-v0` regression dataset. It must not be used to tune the filter in this
commit.
