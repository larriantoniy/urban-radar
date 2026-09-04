#!/usr/bin/env python3
"""Run one live Research V0.1 handoff from the frozen Editor RESEARCH case."""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
UNIVERSE = ROOT / "data/evals/live-pipeline-probe-v1/runs/20260904T145556Z/universe.json"
EDITOR_RESULTS = ROOT / "data/evals/live-pipeline-probe-v1/runs/20260904T145556Z/editor-results.json"
PROMPT = ROOT / "agents/research/prompt-v0.1.md"
RUNS = ROOT / "data/evals/research-zakupki-live-v1/runs"
REGISTRY_ID = "0142200001326017137"


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def parse_json(text: str):
    start = text.find("{")
    if start < 0:
        raise ValueError("no JSON object found")
    return json.JSONDecoder().raw_decode(text[start:])[0]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="deepseek/deepseek-v4-flash-0731")
    ap.add_argument("--provider", default="openrouter")
    args = ap.parse_args()

    universe = json.loads(UNIVERSE.read_text(encoding="utf-8"))
    source = next((item for item in universe if item.get("id") == REGISTRY_ID), None)
    if source is None:
        raise ValueError(f"{REGISTRY_ID} is absent from frozen universe")
    editor = json.loads(EDITOR_RESULTS.read_text(encoding="utf-8"))
    result = next((item for item in editor if item.get("source_item_id") == REGISTRY_ID), None)
    if result is None or result.get("decision") != "RESEARCH":
        raise ValueError("frozen Editor result is not the expected RESEARCH case")

    request = {
        "source": "zakupki",
        "source_item_id": REGISTRY_ID,
        "source_url": source["source_url"],
        "research_goal": "Уточнить факты, необходимые для оценки закупки редактором.",
        "missing_information": result.get("missing_information", []),
    }
    RUNS.mkdir(parents=True, exist_ok=True)
    base = datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ")
    rid, suffix = base, 1
    while (RUNS / rid).exists():
        rid, suffix = f"{base}-{suffix}", suffix + 1
    rd = RUNS / rid
    rd.mkdir()
    write(rd / "research-request.json", request)

    context = (
        PROMPT.read_text(encoding="utf-8")
        + "\n\nResearch Request JSON follows. Treat it as the complete request.\n\n"
        + json.dumps(request, ensure_ascii=False)
        + "\n\nPROVENANCE HANDOFF\n"
        + "For every Zakupki MCP call include both registry_id and the supplied canonical source_url. "
        + "Use source_url only as the validated ProcurementRef provenance; never treat it as an arbitrary URL.\n"
    )
    raw_path = rd / "raw-output.txt"
    usage_path = rd / "usage.json"
    env = os.environ.copy()
    env["ZAKUPKI_SEARCH_URL"] = ""
    ca = env.get("ZAKUPKI_CA_FILE")
    if not ca:
        ca = str(Path.home() / ".certs/Russian_Trusted_CA.pem")
        env["ZAKUPKI_CA_FILE"] = ca
    with raw_path.open("w", encoding="utf-8") as raw:
        cp = subprocess.run(
            ["hermes", "--oneshot", context, "--toolsets", "urban-radar-zakupki", "--usage-file", str(usage_path), "--model", args.model, "--provider", args.provider],
            cwd=ROOT,
            env=env,
            stdout=raw,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )
    errors = []
    pack = None
    raw_text = raw_path.read_text(encoding="utf-8")
    if cp.returncode != 0:
        errors.append({"error_type": "hermes_failure", "error_message": cp.stderr.strip() or f"hermes exit {cp.returncode}", "raw_output": raw_text})
    else:
        try:
            pack = parse_json(raw_text)
            if not isinstance(pack, dict) or pack.get("source_item_id") != REGISTRY_ID:
                raise ValueError("invalid source_item_id")
            if pack.get("status") not in {"COMPLETE", "PARTIAL", "NOT_FOUND"}:
                raise ValueError("invalid status")
            if not isinstance(pack.get("findings"), list) or not isinstance(pack.get("unresolved"), list) or not isinstance(pack.get("sources"), list):
                raise ValueError("invalid Evidence Pack shape")
        except (json.JSONDecodeError, ValueError) as exc:
            errors.append({"error_type": "schema_error", "error_message": str(exc), "raw_output": raw_text})

    if pack is not None:
        write(rd / "evidence-pack.json", pack)
    else:
        write(rd / "evidence-pack.json", None)
    usage = json.loads(usage_path.read_text(encoding="utf-8")) if usage_path.exists() else None
    write(rd / "usage.json", usage)
    trace = {
        "source_url": request["source_url"],
        "fallback_search_resolver": "NOT_USED (ZAKUPKI_SEARCH_URL intentionally empty)",
        "tools": ["get_procurement", "list_procurement_documents", "list_procurement_attachments", "get_procurement_attachment"],
        "trace_basis": "Evidence Pack provenance and successful MCP calls with empty search resolver configuration",
    }
    if pack:
        attachment_ids = []
        attachment_names = []
        for finding in pack.get("findings", []):
            for evidence in finding.get("evidence", []):
                if evidence.get("source_type") == "ATTACHMENT":
                    if evidence.get("attachment_id"):
                        attachment_ids.append(evidence["attachment_id"])
                    if evidence.get("document_name"):
                        attachment_names.append(evidence["document_name"])
        trace["attachment_ids_opened"] = list(dict.fromkeys(attachment_ids))
        trace["attachment_names_opened"] = list(dict.fromkeys(attachment_names))
    write(rd / "tool-trace.json", trace)
    write(rd / "errors.json", {"runtime_errors": errors})
    if pack:
        rows = ["# Research manual review", "", "| question | research_status | answer | evidence_source | human_supported | human_notes |", "|---|---|---|---|---|---|"]
        for finding in pack.get("findings", []):
            source_urls = ", ".join(e.get("source_url", "") for e in finding.get("evidence", []))
            esc = lambda value: str(value or "").replace("|", "\\|").replace("\n", "<br>")
            rows.append(f"| {esc(finding.get('question'))} | {esc(finding.get('status'))} | {esc(finding.get('answer'))} | {esc(source_urls)} | null | null |")
        (rd / "manual-review.md").write_text("\n".join(rows) + "\n", encoding="utf-8")
    run = {
        "run_id": rid,
        "timestamp": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        "status": "success" if not errors else "failed",
        "experiment": "LIVE RESEARCH HANDOFF V0.1",
        "source_item_id": REGISTRY_ID,
        "source_url": request["source_url"],
        "source_universe_path": str(UNIVERSE.relative_to(ROOT)),
        "source_universe_hash": sha(UNIVERSE),
        "editor_result_path": str(EDITOR_RESULTS.relative_to(ROOT)),
        "research_prompt_path": str(PROMPT.relative_to(ROOT)),
        "research_prompt_hash": sha(PROMPT),
        "model": args.model,
        "provider": args.provider,
        "procurement_ref": "validated SourceURL; search fallback disabled for this run",
        "errors": len(errors),
        "usage": usage,
    }
    write(rd / "run.json", run)
    summary = [f"# Live Research Handoff V0.1 — {rid}", "", f"Registry ID: {REGISTRY_ID}", f"Status: {pack.get('status') if pack else 'ERROR'}", "", "Fallback search resolver: NOT USED", "", "Tool trace: see `tool-trace.json`"]
    if pack:
        summary += [f"Unresolved: {len(pack.get('unresolved', []))}", f"Findings: {len(pack.get('findings', []))}"]
    (rd / "summary.md").write_text("\n".join(summary) + "\n", encoding="utf-8")
    raw_path.unlink(missing_ok=True)
    if cp.stderr:
        (rd / "errors.json").write_text(json.dumps({"runtime_errors": errors, "hermes_stderr": cp.stderr}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(rid)
    return 0 if not errors else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"run-zakupki-research-live-v1: {exc}", file=sys.stderr)
        raise SystemExit(1)
