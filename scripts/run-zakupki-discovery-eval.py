#!/usr/bin/env python3
"""Evaluate the existing Discovery prompt on real Zakupki SourceItems."""
from __future__ import annotations

import argparse, hashlib, json, subprocess, sys
from datetime import UTC, datetime
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
DATASET=ROOT/"data/evals/zakupki-real-v0/items.json"
PROMPT=ROOT/"agents/discovery/prompt.md"
RUNS=ROOT/"data/evals/zakupki-discovery-v0/runs"

def sha(path): return hashlib.sha256(path.read_bytes()).hexdigest()
def write(path,value): path.write_text(json.dumps(value,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
def usage(paths):
    reports=[]
    for p in paths:
        if p.exists():
            try: reports.append(json.loads(p.read_text(encoding="utf-8")))
            except json.JSONDecodeError: pass
    out={}
    for key in ("api_calls","input_tokens","output_tokens","estimated_cost_usd"):
        vals=[r.get(key) for r in reports if isinstance(r.get(key),(int,float))]
        out[key]=sum(vals) if vals else None
    return out
def run_dir():
    base=datetime.now(UTC).strftime("%Y%m%dT%H%M%SZ"); rid=base; n=1
    while (RUNS/rid).exists(): rid=f"{base}-{n}"; n+=1
    d=RUNS/rid; d.mkdir(parents=True); return rid,d
def source_item(item):
    # Construct only factual fields; historical filter and human fields are excluded.
    published=item.get("published_at") or ""
    metadata={"registry_id":item.get("id",""),"law":item.get("law","") ,"customer_name":item.get("customer_name","") ,"customer_region":item.get("customer_region","") ,"price":item.get("price",0),"currency":item.get("currency","RUB"),"stage":item.get("stage","") ,"delivery_place":item.get("delivery_place","") ,"address":item.get("address","")}
    facts=[item.get("object","")]
    if item.get("customer_name"): facts.append("Заказчик: "+item["customer_name"])
    if item.get("stage"): facts.append("Этап: "+item["stage"])
    if item.get("price"): facts.append(f"Цена: {item['price']} {item.get('currency','RUB')}")
    return {"source":"zakupki","source_item_id":item.get("id",""),"url":item.get("source_url",""),"title":item.get("object",""),"summary":item.get("object","")+(("; "+item["customer_name"]) if item.get("customer_name") else ""),"text":". ".join(facts),"published_at":published,"retrieved_at":datetime.now(UTC).isoformat().replace("+00:00","Z"),"metadata":metadata}
def prompt_input(prompt,item,si):
    return prompt+"\n\nEvaluation input follows. It is the complete factual SourceItem for one Zakupki procurement. Evaluate only this item; do not call tgl_list_news or tgl_get_news. Return the required JSON and stop.\n\n"+json.dumps(si,ensure_ascii=False)
def main():
    ap=argparse.ArgumentParser(); ap.add_argument("--input",default=str(DATASET)); ap.add_argument("--discovery-prompt",default=str(PROMPT)); args=ap.parse_args()
    dataset_path=Path(args.input); prompt_path=Path(args.discovery_prompt); data=json.loads(dataset_path.read_text(encoding="utf-8")); prompt=prompt_path.read_text(encoding="utf-8")
    eligible=[x for x in data if x.get("human_label") in {"CANDIDATE","SKIP"}]; excluded=[x for x in data if x.get("human_label") is None]
    rid,rd=run_dir(); work=rd/"work"; work.mkdir(); predictions=[]; runtime=[]; usage_paths=[]
    for i,item in enumerate(eligible,1):
        idir=work/f"{i:03d}-{item['id']}"; idir.mkdir(); out=idir/"discovery-output.json"; up=idir/"usage.json"; usage_paths.append(up); si=source_item(item)
        ok=True; err=""
        with out.open("w",encoding="utf-8") as stream:
            cp=subprocess.run(["hermes","--oneshot",prompt_input(prompt,item,si),"--toolsets","urban-radar-tgl","--usage-file",str(up)],cwd=ROOT,stdout=stream,stderr=subprocess.PIPE,text=True,check=False)
        if cp.returncode!=0: ok=False; err=cp.stderr.strip() or f"hermes exit {cp.returncode}"; runtime.append({"registry_id":item["id"],"source_url":item.get("source_url",""),"error_type":"hermes_failure","error_message":err,"raw_output":out.read_text(encoding="utf-8") if out.exists() else None})
        parsed={"candidates":[]}; result="ERROR"
        if ok:
            try:
                parsed=json.loads(out.read_text(encoding="utf-8")); cs=parsed.get("candidates") if isinstance(parsed,dict) else None
                if not isinstance(cs,list): raise ValueError("missing candidates array")
                result="CANDIDATE" if any(c.get("source_url")==item.get("source_url") for c in cs if isinstance(c,dict)) else "DROP"
            except (json.JSONDecodeError,ValueError) as exc: runtime.append({"registry_id":item["id"],"source_url":item.get("source_url",""),"error_type":"invalid_discovery_output","error_message":str(exc),"raw_output":out.read_text(encoding="utf-8") if out.exists() else None}); result="ERROR"
        predictions.append({"id":item["id"],"title":item.get("object",""),"url":item.get("source_url",""),"human_label":item["human_label"],"status":"SUCCESS" if result!="ERROR" else "ERROR","discovery_result":result,"positive":result=="CANDIDATE","raw_output":str(out.relative_to(rd))})
    successful=[p for p in predictions if p["status"]=="SUCCESS"]
    tp=sum(p["human_label"]=="CANDIDATE" and p["positive"] for p in successful); tn=sum(p["human_label"]=="SKIP" and not p["positive"] for p in successful); fp=sum(p["human_label"]=="SKIP" and p["positive"] for p in successful); fn=sum(p["human_label"]=="CANDIDATE" and not p["positive"] for p in successful)
    precision=tp/(tp+fp) if tp+fp else 0; recall=tp/(tp+fn) if tp+fn else 0; f1=2*precision*recall/(precision+recall) if precision+recall else 0
    metrics={"attempted":len(predictions),"successful":len(successful),"errors":len(predictions)-len(successful),"error_rate":(len(predictions)-len(successful))/len(predictions) if predictions else 0,"evaluated":len(successful),"excluded_unlabeled":len(excluded),"tp":tp,"tn":tn,"fp":fp,"fn":fn,"precision":precision,"recall":recall,"f1":f1,"accuracy":(tp+tn)/len(successful) if successful else 0,"discovery_candidates":sum(p["positive"] for p in successful),"editor_reduction_rate":1-sum(p["positive"] for p in successful)/len(predictions) if predictions else 0}
    us=usage(usage_paths); run={"run_id":rid,"timestamp":datetime.now(UTC).isoformat().replace("+00:00","Z"),"status":"success" if not runtime else "failed","evaluation_type":"transfer eval of the existing Discovery prompt","prompt_contract":"The prompt/output contract is unchanged.","source_representation":"Zakupki uses the common factual SourceItem representation, which contains richer source-specific factual fields than the historical TGL adapter.","dataset_path":str(dataset_path.relative_to(ROOT)) if dataset_path.is_relative_to(ROOT) else str(dataset_path),"dataset_hash":sha(dataset_path),"discovery_prompt_path":str(prompt_path.relative_to(ROOT)) if prompt_path.is_relative_to(ROOT) else str(prompt_path),"discovery_prompt_hash":sha(prompt_path),"labelled_count":len(eligible),"excluded_count":len(excluded),"model":None,"provider":None,"runtime_errors":len(runtime),"usage":us}
    write(rd/"predictions.json",predictions); write(rd/"metrics.json",metrics); write(rd/"errors.json",{"runtime_errors":runtime,"false_positives":[p for p in successful if p["human_label"]=="SKIP" and p["positive"]],"false_negatives":[p for p in successful if p["human_label"]=="CANDIDATE" and not p["positive"]]}); write(rd/"usage.json",us); write(rd/"run.json",run); print(rid); return 0 if not runtime else 1
if __name__=="__main__":
    try: raise SystemExit(main())
    except Exception as e: print(f"run-zakupki-discovery-eval: {e}",file=sys.stderr); raise SystemExit(1)
