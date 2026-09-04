#!/usr/bin/env python3
"""Bounded fresh Zakupki -> Discovery -> Editor V1 probe."""
from __future__ import annotations

import argparse, hashlib, json, subprocess
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DISCOVERY_PROMPT = ROOT / "agents/discovery/prompt.md"
EDITOR_PROMPT = ROOT / "agents/editor/prompt-v1.md"
RUNS = ROOT / "data/evals/live-pipeline-probe-v0/runs"

def sha(p: Path) -> str: return hashlib.sha256(p.read_bytes()).hexdigest()
def write(p: Path, v) -> None: p.write_text(json.dumps(v, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
def usage(paths):
    out = {"api_calls": 0, "input_tokens": 0, "output_tokens": 0, "estimated_cost_usd": 0.0}
    for p in paths:
        if not p.exists(): continue
        try: d=json.loads(p.read_text(encoding="utf-8"))
        except json.JSONDecodeError: continue
        for k in out:
            if isinstance(d.get(k),(int,float)): out[k] += d[k]
        for k in ("model","provider"):
            if d.get(k): out.setdefault(k,set()).add(d[k])
    for k in ("model","provider"):
        if isinstance(out.get(k),set): out[k]=sorted(out[k])
    return out
def source_item(x):
    return {"source":"zakupki","source_item_id":x.get("id",""),"url":x.get("source_url",""),"title":x.get("object", ""),"summary":x.get("object", ""),"text":x.get("object", ""),"published_at":x.get("published_at",""),"retrieved_at":datetime.now(UTC).isoformat().replace("+00:00","Z"),"metadata":{"registry_id":x.get("id",""),"law":x.get("law",""),"customer_name":x.get("customer_name",""),"price":x.get("price",0),"currency":x.get("currency","RUB"),"stage":x.get("stage",""),"delivery_place":x.get("delivery_place",""),"address":x.get("address","")}}
def run_one(prompt, payload, out, usage_path, toolset):
    text=prompt+"\n\nInput follows. Use only this factual data, do not call external tools, and return the required JSON.\n\n"+json.dumps(payload,ensure_ascii=False)
    with out.open("w",encoding="utf-8") as stream:
        cp=subprocess.run(["hermes","--oneshot",text,"--toolsets",toolset,"--usage-file",str(usage_path)],cwd=ROOT,stdout=stream,stderr=subprocess.PIPE,text=True,check=False)
    return cp
def main():
    ap=argparse.ArgumentParser(); ap.add_argument("--universe",required=True); args=ap.parse_args()
    universe_path=Path(args.universe); universe=json.loads(universe_path.read_text(encoding="utf-8"));
    if not isinstance(universe,list) or len(universe)>50: raise ValueError("universe must be a list of at most 50 items")
    RUNS.mkdir(parents=True,exist_ok=True); base=datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ"); rid=base; n=1
    while (RUNS/rid).exists(): rid=f"{base}-{n}"; n+=1
    rd=RUNS/rid; rd.mkdir(); write(rd/"universe.json",universe)
    dp=[]; ep=[]; errors=[]; usages=[]; dtext=DISCOVERY_PROMPT.read_text(encoding="utf-8"); etext=EDITOR_PROMPT.read_text(encoding="utf-8")
    for i,item in enumerate(universe,1):
        sid=item.get("id",""); idir=rd/f"work/{i:03d}-{sid}"; idir.mkdir(parents=True); si=source_item(item); dpath=idir/"discovery-output.json"; dup=idir/"discovery-usage.json"; cp=run_one(dtext,si,dpath,dup,"urban-radar-tgl"); usages.append(dup)
        dstatus="SUCCESS"; dparsed=None
        if cp.returncode!=0: dstatus="ERROR"; errors.append({"stage":"discovery","source_item_id":sid,"error_type":"hermes_failure","error_message":cp.stderr.strip() or f"hermes exit {cp.returncode}","raw_output":dpath.read_text(encoding="utf-8")})
        else:
            try:
                dparsed=json.loads(dpath.read_text(encoding="utf-8"));
                if not isinstance(dparsed.get("candidates"),list): raise ValueError("missing candidates")
            except (json.JSONDecodeError,ValueError) as e: dstatus="ERROR"; errors.append({"stage":"discovery","source_item_id":sid,"error_type":"schema_error","error_message":str(e),"raw_output":dpath.read_text(encoding="utf-8")})
        positive=bool(dparsed and any(c.get("source_url")==item.get("source_url") for c in dparsed.get("candidates",[]) if isinstance(c,dict)))
        dp.append({"source_item_id":sid,"source_url":item.get("source_url",""),"title":item.get("object",""),"status":dstatus,"discovery_result":"CANDIDATE" if positive else ("DROP" if dstatus=="SUCCESS" else "ERROR"),"positive":positive,"raw_output":str(dpath.relative_to(rd))})
        if not positive or dstatus!="SUCCESS": continue
        epath=idir/"editor-output.json"; eup=idir/"editor-usage.json"; cp=run_one(etext,dparsed,epath,eup,""); usages.append(eup); estatus="SUCCESS"; decision=None; match=None
        if cp.returncode!=0: estatus="ERROR"; errors.append({"stage":"editor","source_item_id":sid,"error_type":"hermes_failure","error_message":cp.stderr.strip() or f"hermes exit {cp.returncode}","raw_output":epath.read_text(encoding="utf-8")})
        else:
            try:
                parsed=json.loads(epath.read_text(encoding="utf-8")); ds=parsed.get("decisions")
                if not isinstance(ds,list): raise ValueError("missing decisions")
                match=next((d for d in ds if isinstance(d,dict) and d.get("source_url")==item.get("source_url")),None)
                if match is None: raise ValueError("missing decision for source_url")
                decision=match.get("decision")
                if decision not in {"PUBLISH","IGNORE","RESEARCH","UPDATE_PROJECT"}: raise ValueError("unsupported decision")
            except (json.JSONDecodeError,ValueError) as e: estatus="ERROR"; errors.append({"stage":"editor","source_item_id":sid,"error_type":"schema_error","error_message":str(e),"raw_output":epath.read_text(encoding="utf-8")})
        ep.append({"source_item_id":sid,"source_url":item.get("source_url",""),"title":item.get("object",""),"status":estatus,"decision":decision,"importance":match.get("importance") if match else None,"reason":match.get("reason","") if match else "","missing_information":match.get("missing_information",[]) if match else [],"raw_output":str(epath.relative_to(rd))})
    us=usage(usages); dist={d:sum(x.get("decision")==d and x.get("status")=="SUCCESS" for x in ep) for d in ("PUBLISH","IGNORE","RESEARCH","UPDATE_PROJECT")}; dsuccess=[x for x in dp if x["status"]=="SUCCESS"]; esuccess=[x for x in ep if x["status"]=="SUCCESS"]; summary={"universe_raw":len(universe),"universe_normalized":len(universe),"discovery_candidates":sum(x["positive"] for x in dsuccess),"discovery_ignored":sum(not x["positive"] for x in dsuccess),"discovery_errors":sum(x["status"]=="ERROR" for x in dp),"candidate_rate":sum(x["positive"] for x in dsuccess)/len(universe) if universe else 0,"editor_distribution":dist,"editor_errors":sum(x["status"]=="ERROR" for x in ep),"usage":us}
    write(rd/"discovery-results.json",dp); write(rd/"editor-results.json",ep); write(rd/"errors.json",{"errors":errors}); write(rd/"usage.json",us); write(rd/"run.json",{"run_id":rid,"timestamp":datetime.now(UTC).isoformat().replace("+00:00","Z"),"status":"success" if not errors else "failed","universe_path":"universe.json","discovery_prompt_path":str(DISCOVERY_PROMPT.relative_to(ROOT)),"discovery_prompt_hash":sha(DISCOVERY_PROMPT),"editor_prompt_path":str(EDITOR_PROMPT.relative_to(ROOT)),"editor_prompt_hash":sha(EDITOR_PROMPT),"raw":len(universe),"normalized":len(universe),"errors":len(errors),"usage":us});
    lines=[f"# Live pipeline probe {rid}","",f"Universe: {len(universe)} raw → {len(universe)} normalized",f"Discovery: {summary['discovery_candidates']} candidates, {summary['discovery_ignored']} ignored, {summary['discovery_errors']} errors",f"Editor: {dist}","", "RESEARCH cases:"]
    for x in ep:
        if x.get("decision")=="RESEARCH": lines.append(f"- {x['source_item_id']}: {x['title']} — {x['reason']} — missing: {x['missing_information']}")
    (rd/"summary.md").write_text("\n".join(lines)+"\n",encoding="utf-8"); print(rid); return 0 if not errors else 1
if __name__=="__main__": raise SystemExit(main())
