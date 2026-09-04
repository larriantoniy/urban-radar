#!/usr/bin/env python3
"""Run one Editor V1 re-evaluation for the frozen live Research case."""
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
UNIVERSE = ROOT / "data/evals/live-pipeline-probe-v1/runs/20260904T145556Z/universe.json"
EDITOR_RESULTS = ROOT / "data/evals/live-pipeline-probe-v1/runs/20260904T145556Z/editor-results.json"
EVIDENCE = ROOT / "data/evals/research-zakupki-live-v1/runs/20260904T161529Z/evidence-pack.json"
REVIEW = ROOT / "data/evals/research-zakupki-live-v1/runs/20260904T161529Z/manual-review.md"
PROMPT = ROOT / "agents/editor/prompt-v1.md"
RUNS = ROOT / "data/evals/editor-research-live-v1/runs"
REGISTRY_ID = "0142200001326017137"


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def source_item(item: dict) -> dict:
    retrieved = item.get("retrieved_at", "")
    return {
        "Source": "zakupki",
        "SourceItemID": item["id"],
        "URL": item["source_url"],
        "Title": item.get("object", ""),
        "Summary": item.get("object", ""),
        "Text": item.get("object", ""),
        "PublishedAt": item.get("published_at", ""),
        "RetrievedAt": retrieved,
        "Metadata": {
            "registry_id": item["id"],
            "law": item.get("law", ""),
            "customer_name": item.get("customer_name", ""),
            "price": item.get("price", 0),
            "currency": item.get("currency", "RUB"),
            "stage": item.get("stage", ""),
            "delivery_place": item.get("delivery_place", ""),
            "address": item.get("address", ""),
        },
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="deepseek/deepseek-v4-flash-0731")
    ap.add_argument("--provider", default="openrouter")
    args = ap.parse_args()

    universe = json.loads(UNIVERSE.read_text(encoding="utf-8"))
    item = next((x for x in universe if x.get("id") == REGISTRY_ID), None)
    if item is None:
        raise ValueError(f"{REGISTRY_ID} is absent from frozen universe")
    editor_results = json.loads(EDITOR_RESULTS.read_text(encoding="utf-8"))
    previous = next((x for x in editor_results if x.get("source_item_id") == REGISTRY_ID), None)
    if previous is None or previous.get("decision") != "RESEARCH":
        raise ValueError("frozen Editor result is not the expected RESEARCH case")
    evidence = json.loads(EVIDENCE.read_text(encoding="utf-8"))

    review_text = REVIEW.read_text(encoding="utf-8")
    review_rows = [line for line in review_text.splitlines() if line.startswith("| ") and "human_supported" not in line and "factual claim" not in line and "review assertion" not in line and not line.startswith("|---")]
    if len(review_rows) != 18 or any("| null |" in line for line in review_rows) or any("| false |" in line for line in review_rows):
        raise ValueError("human evidence review is not fully approved")

    si = source_item(item)
    request = {
        "source": "zakupki",
        "source_item_id": REGISTRY_ID,
        "source_url": item["source_url"],
        "research_goal": "Уточнить факты, необходимые для оценки закупки редактором.",
        "missing_information": previous.get("missing_information", []),
    }
    input_value = {
        "source_item": si,
        "previous_editor_decision": previous.get("decision"),
        "research_request": request,
        "evidence_pack": evidence,
    }
    RUNS.mkdir(parents=True, exist_ok=True)
    base = datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ")
    rid, n = base, 1
    while (RUNS / rid).exists():
        rid, n = f"{base}-{n}", n + 1
    rd = RUNS / rid
    rd.mkdir()
    write(rd / "input.json", input_value)
    raw_path, usage_path = rd / "raw-output.txt", rd / "usage.json"
    context = (
        PROMPT.read_text(encoding="utf-8")
        + "\n\nSOURCE ITEM\n"
        + json.dumps(si, ensure_ascii=False)
        + "\n\nRESEARCH EVIDENCE\n"
        + json.dumps({"research_goal": request["research_goal"], "missing_information": request["missing_information"], "evidence_pack": evidence}, ensure_ascii=False)
    )
    with raw_path.open("w", encoding="utf-8") as raw:
        cp = subprocess.run(
            ["hermes", "--oneshot", context, "--toolsets", "", "--usage-file", str(usage_path), "--model", args.model, "--provider", args.provider],
            cwd=ROOT, stdout=raw, stderr=subprocess.PIPE, text=True, check=False,
        )
    errors = []
    parsed = None
    raw_text = raw_path.read_text(encoding="utf-8")
    if cp.returncode != 0:
        errors.append({"error_type": "hermes_failure", "error_message": cp.stderr.strip() or f"hermes exit {cp.returncode}", "raw_output": raw_text})
    else:
        try:
            parsed = json.loads(raw_text)
            decisions = parsed.get("decisions") if isinstance(parsed, dict) else None
            if not isinstance(decisions, list) or not decisions:
                raise ValueError("missing decisions array")
            match = next((d for d in decisions if isinstance(d, dict) and d.get("source_url") == si["URL"]), None)
            if match is None:
                raise ValueError("missing decision for source_url")
            if match.get("decision") not in {"PUBLISH", "IGNORE", "RESEARCH", "UPDATE_PROJECT"}:
                raise ValueError("unsupported editor decision")
        except (json.JSONDecodeError, ValueError) as exc:
            errors.append({"error_type": "schema_error", "error_message": str(exc), "raw_output": raw_text})
    write(rd / "editor-output.json", parsed)
    usage = json.loads(usage_path.read_text(encoding="utf-8")) if usage_path.exists() else None
    write(rd / "usage.json", usage)
    write(rd / "errors.json", {"runtime_errors": errors})
    raw_path.unlink(missing_ok=True)
    decision = None
    rationale = ""
    missing = []
    if parsed:
        match = next((d for d in parsed.get("decisions", []) if isinstance(d, dict) and d.get("source_url") == si["URL"]), None)
        if match:
            decision, rationale, missing = match.get("decision"), match.get("reason", ""), match.get("missing_information", [])
    run = {
        "run_id": rid,
        "timestamp": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "status": "success" if not errors else "failed",
        "evaluation_type": "standalone live post-research Editor V1 re-evaluation",
        "editor_prompt_path": str(PROMPT.relative_to(ROOT)),
        "editor_prompt_hash": sha(PROMPT),
        "source_universe_path": str(UNIVERSE.relative_to(ROOT)),
        "source_universe_hash": sha(UNIVERSE),
        "source_research_run_id": "20260904T161529Z",
        "evidence_pack_path": str(EVIDENCE.relative_to(ROOT)),
        "evidence_pack_hash": sha(EVIDENCE),
        "source_item_id": REGISTRY_ID,
        "previous_editor_decision": previous.get("decision"),
        "human_review_gate": {"path": str(REVIEW.relative_to(ROOT)), "claims": 18, "approved": 18},
        "decision": decision,
        "errors": len(errors),
        "usage": usage,
    }
    write(rd / "run.json", run)
    summary = [f"# Live Editor V1 re-evaluation — {rid}", "", f"Registry ID: {REGISTRY_ID}", f"Decision: {decision or 'ERROR'}", f"Importance: {match.get('importance') if parsed and match else ''}", f"Reason: {rationale}", f"Missing information: {json.dumps(missing, ensure_ascii=False)}", "", "Research facts were provided as context; human review was validation-only.", f"Runtime/schema errors: {len(errors)}"]
    (rd / "summary.md").write_text("\n".join(summary) + "\n", encoding="utf-8")
    print(rid)
    return 0 if not errors else 1


if __name__ == "__main__":
    raise SystemExit(main())
