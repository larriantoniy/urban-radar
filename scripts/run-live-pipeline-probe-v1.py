#!/usr/bin/env python3
"""Freeze one live Zakupki snapshot, then run Discovery and Editor V1 on it."""
from __future__ import annotations

import argparse, hashlib, json, os, re, subprocess
from datetime import UTC, datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DP = ROOT / "agents/discovery/prompt.md"
EP = ROOT / "agents/editor/prompt-v1.md"
RUNS = ROOT / "data/evals/live-pipeline-probe-v1/runs"

def sha(p): return hashlib.sha256(Path(p).read_bytes()).hexdigest()
def write(p,v): Path(p).write_text(json.dumps(v,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
def source_item(x, retrieved):
    return {"source":"zakupki","source_item_id":x.get("id",""),"url":x.get("source_url",""),"title":x.get("object",""),"summary":x.get("object",""),"text":x.get("object",""),"published_at":x.get("published_at",""),"retrieved_at":retrieved,"metadata":{"registry_id":x.get("id",""),"law":x.get("law",""),"customer_name":x.get("customer_name",""),"price":x.get("price",0),"currency":x.get("currency","RUB"),"stage":x.get("stage",""),"delivery_place":x.get("delivery_place",""),"address":x.get("address","")}}
def run_agent(prompt, payload, out, usage, toolset, model, provider):
    text=prompt+"\n\nInput follows. Use only this data and return the required JSON.\n\n"+json.dumps(payload,ensure_ascii=False)
    cmd=["hermes","--oneshot",text,"--toolsets",toolset,"--usage-file",str(usage)]
    if model: cmd += ["--model",model]
    if provider: cmd += ["--provider",provider]
    with open(out,"w",encoding="utf-8") as stream:
        return subprocess.run(cmd,cwd=ROOT,stdout=stream,stderr=subprocess.PIPE,text=True,check=False)
def aggregate(paths):
    out={"api_calls":0,"input_tokens":0,"output_tokens":0,"estimated_cost_usd":0.0}
    models=set(); providers=set()
    for p in paths:
        try: d=json.loads(Path(p).read_text(encoding="utf-8"))
        except (OSError,json.JSONDecodeError): continue
        for k in out:
            if isinstance(d.get(k),(int,float)): out[k]+=d[k]
        if d.get("model"): models.add(d["model"])
        if d.get("provider"): providers.add(d["provider"])
    out["model"]=sorted(models); out["provider"]=sorted(providers); return out
def main():
    ap=argparse.ArgumentParser(); ap.add_argument("--limit",type=int,default=50); ap.add_argument("--days",type=int,default=7); ap.add_argument("--model",default="deepseek/deepseek-v4-flash-0731"); ap.add_argument("--provider",default="openrouter"); args=ap.parse_args()
    if not 1<=args.limit<=50: raise ValueError("limit must be 1..50")
    if args.days<0: raise ValueError("days must be non-negative")
    RUNS.mkdir(parents=True,exist_ok=True); base=datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ"); rid=base; n=1
    while (RUNS/rid).exists(): rid=f"{base}-{n}"; n+=1
    rd=RUNS/rid; rawdir=rd/"raw"; (rawdir/"discovery").mkdir(parents=True); (rawdir/"editor").mkdir();
    retrieved=datetime.now(UTC); start=retrieved.date().fromordinal(retrieved.date().toordinal()-args.days).isoformat(); end=retrieved.date().isoformat()
    universe_path=rd/"universe.json"; source_log=rd/"source-probe.log"
    env=os.environ.copy(); env.setdefault("ZAKUPKI_CA_FILE",str(Path.home()/".certs/Russian_Trusted_CA.pem"))
    cmd=["go","run","./cmd/zakupki-probe","-source","html","-limit",str(args.limit),"-days",str(args.days),"-export",str(universe_path)]
    with source_log.open("w",encoding="utf-8") as log:
        cp=subprocess.run(cmd,cwd=ROOT,env=env,stdout=log,stderr=subprocess.STDOUT,text=True,check=False)
    if cp.returncode!=0: raise RuntimeError(f"source probe failed; see {source_log}")
    universe=json.loads(universe_path.read_text(encoding="utf-8")); capture=json.loads((universe_path.parent/"capture.json").read_text(encoding="utf-8"))
    source_text=source_log.read_text(encoding="utf-8")
    def metric(pattern):
        m=re.search(pattern,source_text); return int(m.group(1)) if m else None
    pages=metric(r"pages read: (\d+)"); retrieved_match=re.search(r"retrieved_at: ([^\n]+)",source_text)
    if retrieved_match:
        retrieved_text=retrieved_match.group(1).strip()
    else:
        retrieved_text=retrieved.isoformat().replace("+00:00","Z")
    dates=[x.get("published_at") for x in universe if x.get("published_at")]; metadata={"retrieved_at":retrieved_text,"window_start":start,"window_end":end,"source_limit":args.limit,"pages_read":pages,"raw_count":metric(r"raw documents: (\d+)"),"unique_count":len(universe),"normalized_count":metric(r"normalized: (\d+)"),"enrichment_success":metric(r"enrichment success: (\d+)"),"enrichment_failures":metric(r"enrichment failure: (\d+)"),"normalization_errors":metric(r"normalization errors: (\d+)"),"selected_count":len(universe),"oldest_published_at":min(dates) if dates else None,"newest_published_at":max(dates) if dates else None,"universe_semantics":"bounded deterministic snapshot, max 50 records"}
    write(rd/"universe.json",universe); write(rd/"universe-metadata.json",metadata)
    discoveries=[]; editors=[]; errors=[]; discovery_usage_paths=[]; editor_usage_paths=[]; dprompt=DP.read_text(encoding="utf-8"); eprompt=EP.read_text(encoding="utf-8")
    for i,item in enumerate(universe,1):
        sid=item.get("id",""); si=source_item(item,retrieved_text); out=rawdir/"discovery"/f"{i:03d}-{sid}.json"; up=out.with_suffix(".usage.json"); discovery_usage_paths.append(up); cp=run_agent(dprompt,si,out,up,"urban-radar-tgl",args.model,args.provider); status="SUCCESS"; parsed=None
        if cp.returncode!=0: status="ERROR"; errors.append({"stage":"discovery","source_item_id":sid,"error_type":"hermes_failure","error_message":cp.stderr.strip() or f"hermes exit {cp.returncode}","raw_output":out.read_text(encoding="utf-8")})
        else:
            try: parsed=json.loads(out.read_text(encoding="utf-8")); assert isinstance(parsed.get("candidates"),list)
            except (json.JSONDecodeError,AssertionError): status="ERROR"; errors.append({"stage":"discovery","source_item_id":sid,"error_type":"schema_error","error_message":"invalid candidates output","raw_output":out.read_text(encoding="utf-8")})
        positive=bool(parsed and any(c.get("source_url")==item.get("source_url") for c in parsed.get("candidates",[]) if isinstance(c,dict))); discoveries.append({"source_item_id":sid,"source_url":item.get("source_url",""),"title":item.get("object",""),"published_at":item.get("published_at",""),"status":status,"decision":"CANDIDATE" if positive else ("DROP" if status=="SUCCESS" else "ERROR"),"positive":positive,"raw_output":str(out.relative_to(rd))})
        if status!="SUCCESS" or not positive: continue
        eout=rawdir/"editor"/f"{i:03d}-{sid}.json"; eup=eout.with_suffix(".usage.json"); editor_usage_paths.append(eup); cp=run_agent(eprompt,parsed,eout,eup,"",args.model,args.provider); est="SUCCESS"; match=None
        if cp.returncode!=0: est="ERROR"; errors.append({"stage":"editor","source_item_id":sid,"error_type":"hermes_failure","error_message":cp.stderr.strip() or f"hermes exit {cp.returncode}","raw_output":eout.read_text(encoding="utf-8")})
        else:
            try:
                ed=json.loads(eout.read_text(encoding="utf-8")); ds=ed.get("decisions"); assert isinstance(ds,list); match=next(d for d in ds if d.get("source_url")==item.get("source_url")); assert match.get("decision") in {"PUBLISH","IGNORE","RESEARCH","UPDATE_PROJECT"}
            except (json.JSONDecodeError,AssertionError,StopIteration): est="ERROR"; errors.append({"stage":"editor","source_item_id":sid,"error_type":"schema_error","error_message":"invalid editor output","raw_output":eout.read_text(encoding="utf-8")})
        editors.append({"source_item_id":sid,"source_url":item.get("source_url",""),"title":item.get("object",""),"status":est,"decision":match.get("decision") if match else None,"importance":match.get("importance") if match else None,"reason":match.get("reason","") if match else "","missing_information":match.get("missing_information",[]) if match else [],"raw_output":str(eout.relative_to(rd))})
    du=aggregate(discovery_usage_paths); eu=aggregate(editor_usage_paths); us={"discovery":du,"editor":eu,"total":aggregate(discovery_usage_paths+editor_usage_paths)}; dist={d:sum(x.get("decision")==d and x.get("status")=="SUCCESS" for x in editors) for d in ("PUBLISH","IGNORE","RESEARCH","UPDATE_PROJECT")}; dsuccess=[x for x in discoveries if x["status"]=="SUCCESS"]; summary={"source":{"pages_read":pages,"raw":metadata["raw_count"],"unique":len(universe),"normalized":metadata["normalized_count"],"selected":len(universe),"oldest_published_at":metadata["oldest_published_at"],"newest_published_at":metadata["newest_published_at"]},"discovery":{"processed":len(discoveries),"candidates":sum(x["positive"] for x in dsuccess),"ignored":sum(not x["positive"] for x in dsuccess),"errors":sum(x["status"]=="ERROR" for x in discoveries),"candidate_rate":sum(x["positive"] for x in dsuccess)/len(universe) if universe else 0},"editor":{"PUBLISH":dist["PUBLISH"],"IGNORE":dist["IGNORE"],"RESEARCH":dist["RESEARCH"],"UPDATE_PROJECT":dist["UPDATE_PROJECT"],"errors":sum(x["status"]=="ERROR" for x in editors)},"usage":us}
    write(rd/"discovery-results.json",discoveries); write(rd/"editor-results.json",editors); write(rd/"errors.json",{"errors":errors}); write(rd/"usage.json",us); write(rd/"run.json",{"run_id":rid,"timestamp":datetime.now(UTC).isoformat().replace("+00:00","Z"),"status":"success" if not errors else "failed","universe_semantics":"bounded deterministic snapshot, max 50 records","universe_metadata":"universe-metadata.json","discovery_prompt_path":str(DP.relative_to(ROOT)),"discovery_prompt_hash":sha(DP),"editor_prompt_path":str(EP.relative_to(ROOT)),"editor_prompt_hash":sha(EP),"model":args.model,"provider":args.provider,"raw":len(universe),"normalized":len(universe),"errors":len(errors),"usage":us}); write(rd/"summary.json",summary); lines=[f"# Live pipeline probe V1 {rid}","",f"Universe: {len(universe)} raw → {len(universe)} normalized; pages={pages}; window={start}..{end}",f"Discovery: {summary['discovery']['candidates']} candidates, {summary['discovery']['ignored']} ignored, {summary['discovery']['errors']} errors",f"Editor: {dist}","","RESEARCH cases:"]
    for x in editors:
        if x.get("decision")=="RESEARCH": lines.append(f"- {x['source_item_id']}: {x['title']} — {x['reason']} — missing: {x['missing_information']}")
    (rd/"summary.md").write_text("\n".join(lines)+"\n",encoding="utf-8"); print(rid); return 0 if not errors else 1
if __name__=="__main__": raise SystemExit(main())
