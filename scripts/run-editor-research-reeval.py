#!/usr/bin/env python3
"""One isolated Editor V1 re-evaluation with a frozen Research Evidence Pack."""
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PROMPT = ROOT / "agents/editor/prompt-v1.md"
REQUEST = ROOT / "data/evals/research-zakupki-v0/requests/0142200001326017185.json"
EVIDENCE = ROOT / "data/evals/research-zakupki-v0/runs/20260904T134820Z/evidence-pack.json"
RUNS = ROOT / "data/evals/editor-research-reeval-v0/runs"
REGISTRY_ID = "0142200001326017185"


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def source_item() -> dict:
    return {
        "Source": "zakupki",
        "SourceItemID": REGISTRY_ID,
        "URL": "https://zakupki.gov.ru/epz/order/notice/ea20/view/common-info.html?regNumber=" + REGISTRY_ID,
        "Title": "Выполнение работ по техническому обслуживанию и ремонту медицинского оборудования ГБУЗ СО «Тольяттинская городская клиническая больница №5»",
        "Summary": "ГБУЗ СО «ТГКБ №5»: техническое обслуживание и ремонт медицинского оборудования",
        "Text": "Объект: техническое обслуживание и ремонт медицинского оборудования. Место: Тольятти, бульвар Здоровья, 25.",
        "PublishedAt": "2026-09-04T00:00:00Z",
        "RetrievedAt": "2026-09-04T00:00:00Z",
        "Metadata": {
            "registry_id": REGISTRY_ID,
            "law": "44-ФЗ",
            "customer_name": "ГБУЗ СО «Тольяттинская городская клиническая больница №5»",
            "price": 9640000,
            "currency": "RUB",
            "stage": "Электронный аукцион",
            "delivery_place": "Самарская область, г.о. Тольятти, г. Тольятти, б-р Здоровья, д. 25",
        },
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--prompt", default=str(PROMPT))
    ap.add_argument("--request", default=str(REQUEST))
    ap.add_argument("--evidence", default=str(EVIDENCE))
    args = ap.parse_args()
    prompt_path, request_path, evidence_path = map(Path, (args.prompt, args.request, args.evidence))
    request = json.loads(request_path.read_text(encoding="utf-8"))
    evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
    input_value = {"source_item": source_item(), "previous_editor_decision": None,
                   "research_request": request, "evidence_pack": evidence}
    RUNS.mkdir(parents=True, exist_ok=True)
    base = datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ")
    rid, n = base, 1
    while (RUNS / rid).exists():
        rid, n = f"{base}-{n}", n + 1
    rd = RUNS / rid
    rd.mkdir()
    write(rd / "input.json", input_value)
    raw_path, usage_path = rd / "raw-output.txt", rd / "usage.json"
    context = (prompt_path.read_text(encoding="utf-8") +
               "\n\nSOURCE ITEM\n" + json.dumps(input_value["source_item"], ensure_ascii=False) +
               "\n\nRESEARCH EVIDENCE\n" + json.dumps({"research_goal": request["research_goal"],
               "missing_information": request["missing_information"], "evidence_pack": evidence}, ensure_ascii=False))
    with raw_path.open("w", encoding="utf-8") as raw:
        cp = subprocess.run(["hermes", "--oneshot", context, "--toolsets", "", "--usage-file", str(usage_path)],
                            cwd=ROOT, stdout=raw, stderr=subprocess.PIPE, text=True, check=False)
    errors = []
    parsed = None
    if cp.returncode != 0:
        errors.append({"error_type": "hermes_failure", "error_message": cp.stderr.strip() or f"hermes exit {cp.returncode}",
                       "raw_output": raw_path.read_text(encoding="utf-8")})
    else:
        try:
            parsed = json.loads(raw_path.read_text(encoding="utf-8"))
            decisions = parsed.get("decisions") if isinstance(parsed, dict) else None
            if not isinstance(decisions, list) or not decisions:
                raise ValueError("missing decisions array")
            match = next((d for d in decisions if isinstance(d, dict) and d.get("source_url") == input_value["source_item"]["URL"]), None)
            if match is None:
                raise ValueError("missing decision for source_url")
            if match.get("decision") not in {"PUBLISH", "IGNORE", "RESEARCH", "UPDATE_PROJECT"}:
                raise ValueError("unsupported editor decision")
        except (json.JSONDecodeError, ValueError) as exc:
            errors.append({"error_type": "invalid_editor_output", "error_message": str(exc),
                           "raw_output": raw_path.read_text(encoding="utf-8")})
    write(rd / "editor-output.json", parsed)
    usage = json.loads(usage_path.read_text(encoding="utf-8")) if usage_path.exists() else None
    write(rd / "usage.json", usage)
    write(rd / "errors.json", {"runtime_errors": errors})
    run = {"run_id": rid, "timestamp": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
           "status": "success" if not errors else "failed", "evaluation_type": "standalone post-research editorial evaluation",
           "editor_prompt_path": str(prompt_path.relative_to(ROOT)) if prompt_path.is_relative_to(ROOT) else str(prompt_path),
           "editor_prompt_hash": sha(prompt_path), "request_path": str(request_path.relative_to(ROOT)),
           "request_hash": sha(request_path), "evidence_pack_path": str(evidence_path.relative_to(ROOT)),
           "evidence_pack_hash": sha(evidence_path), "source_item_id": REGISTRY_ID,
           "previous_editor_decision": None, "tools": [], "errors": len(errors), "usage": usage}
    write(rd / "run.json", run)
    print(rid)
    return 0 if not errors else 1


if __name__ == "__main__":
    raise SystemExit(main())
