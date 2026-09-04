#!/usr/bin/env python3
"""Run one isolated Research Agent experiment for a fixed Zakupki request."""
from __future__ import annotations

import argparse, hashlib, json, subprocess, sys
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_REQUEST = ROOT / "data/evals/research-zakupki-v0/requests/0142200001326017185.json"
PROMPT = ROOT / "agents/research/prompt-v0.md"
RUNS = ROOT / "data/evals/research-zakupki-v0/runs"

def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()

def write(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

def parse_json_output(text: str):
    """Extract the first JSON object while preserving the raw model output."""
    start = text.find("{")
    if start < 0:
        raise ValueError("no JSON object found")
    value, _ = json.JSONDecoder().raw_decode(text[start:])
    return value

def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--request", default=str(DEFAULT_REQUEST))
    ap.add_argument("--prompt", default=str(PROMPT))
    args = ap.parse_args()
    request_path, prompt_path = Path(args.request), Path(args.prompt)
    request = json.loads(request_path.read_text(encoding="utf-8"))
    prompt = prompt_path.read_text(encoding="utf-8")
    RUNS.mkdir(parents=True, exist_ok=True)
    base = datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ")
    rid, n = base, 1
    while (RUNS / rid).exists(): rid, n = f"{base}-{n}", n + 1
    rd = RUNS / rid; rd.mkdir()
    raw_path, usage_path, trace_path = rd / "raw-output.txt", rd / "usage.json", rd / "hermes-stderr.log"
    text = prompt + "\n\nResearch Request JSON follows. Treat it as the complete request.\n\n" + json.dumps(request, ensure_ascii=False)
    with raw_path.open("w", encoding="utf-8") as raw:
        cp = subprocess.run(["hermes", "--oneshot", text, "--toolsets", "urban-radar-zakupki", "--usage-file", str(usage_path)], cwd=ROOT, stdout=raw, stderr=subprocess.PIPE, text=True, check=False)
    trace_path.write_text(cp.stderr or "", encoding="utf-8")
    errors = []
    pack = None
    if cp.returncode != 0:
        errors.append({"error_type": "hermes_failure", "error_message": cp.stderr.strip() or f"hermes exit {cp.returncode}", "raw_output": raw_path.read_text(encoding="utf-8")})
    else:
        try:
            pack = parse_json_output(raw_path.read_text(encoding="utf-8"))
            if not isinstance(pack, dict) or pack.get("source_item_id") != request.get("source_item_id"):
                raise ValueError("invalid source_item_id")
            if pack.get("status") not in {"COMPLETE", "PARTIAL", "NOT_FOUND"}: raise ValueError("invalid status")
            if not isinstance(pack.get("findings"), list) or not isinstance(pack.get("unresolved"), list) or not isinstance(pack.get("sources"), list): raise ValueError("invalid Evidence Pack shape")
        except (json.JSONDecodeError, ValueError) as exc:
            errors.append({"error_type": "invalid_evidence_pack", "error_message": str(exc), "raw_output": raw_path.read_text(encoding="utf-8")})
    if pack is not None: write(rd / "evidence-pack.json", pack)
    else: write(rd / "evidence-pack.json", None)
    usage = json.loads(usage_path.read_text(encoding="utf-8")) if usage_path.exists() else None
    write(rd / "usage.json", usage)
    review = ["# Research manual review\n", "| question | research_status | answer | evidence_source | human_supported | human_notes |", "|---|---|---|---|---|---|"]
    if pack:
        for finding in pack.get("findings", []):
            src = ", ".join(e.get("source_url", "") for e in finding.get("evidence", []))
            review.append(f"| {finding.get('question','').replace('|','\\|')} | {finding.get('status','')} | {finding.get('answer','').replace('|','\\|')} | {src} | null | null |")
    (rd / "manual-review.md").write_text("\n".join(review) + "\n", encoding="utf-8")
    run = {"run_id": rid, "timestamp": datetime.now(UTC).isoformat().replace("+00:00", "Z"), "status": "success" if not errors else "failed", "request_path": str(request_path.relative_to(ROOT)) if request_path.is_relative_to(ROOT) else str(request_path), "request_hash": sha(request_path), "research_prompt_path": str(prompt_path.relative_to(ROOT)) if prompt_path.is_relative_to(ROOT) else str(prompt_path), "research_prompt_hash": sha(prompt_path), "source_item_id": request.get("source_item_id"), "errors": len(errors), "tool_policy": "Zakupki MCP only; no arbitrary URLs or other MCP servers", "trace_path": "hermes-stderr.log", "usage": usage}
    write(rd / "run.json", run); write(rd / "errors.json", {"runtime_errors": errors})
    print(rid)
    return 0 if not errors else 1

if __name__ == "__main__":
    try: raise SystemExit(main())
    except Exception as exc:
        print(f"run-zakupki-research-eval: {exc}", file=sys.stderr); raise SystemExit(1)
