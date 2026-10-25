#!/usr/bin/env python3
"""Validate draft wire shapes, examples and generated bindings; never imply peer approval."""
from __future__ import annotations
import argparse,copy,hashlib,json,subprocess,sys
from pathlib import Path
from jsonschema import Draft202012Validator,FormatChecker
from openapi_spec_validator import validate
from contract_sources import NAMES,expected_content

ROOT=Path(__file__).resolve().parents[1]
CONTRACTS=ROOT/"contracts"
SOURCE=ROOT/"specs/001-flash-sale-platform/contracts"

def main():
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--require-adopted",action="store_true");args=parser.parse_args()
    for name in NAMES:
        if (CONTRACTS/name).read_bytes()!=expected_content(name):raise SystemExit("Feature/canonical contract drift: "+name)
    documents={};operations={}
    for path in CONTRACTS.glob("*.openapi.json"):
        doc=json.loads(path.read_text());validate(doc);documents[path.name]=doc
        for item in doc["paths"].values():
            for method,operation in item.items():
                if method in ("get","post","put","delete","patch") and "operationId" in operation:operations[operation["operationId"]]=(doc,operation)
    events=json.loads((CONTRACTS/"events.schema.json").read_text());Draft202012Validator.check_schema(events)
    validator=Draft202012Validator(events,format_checker=FormatChecker())
    examples=json.loads((CONTRACTS/"examples.json").read_text());negative=0
    for event in examples["events"]:
        validator.validate(event)
        broken=copy.deepcopy(event);del broken["event_id"]
        assert not validator.is_valid(broken);negative+=1
        if event["type"] in ("SaleConfigured","SaleActivated"):
            broken=copy.deepcopy(event);del broken["data"]["per_shopper_unit_cap"]
            assert not validator.is_valid(broken);negative+=1
        if event["type"]=="AttemptRecorded" and event["data"]["response_status"]>=400:
            broken=copy.deepcopy(event);del broken["data"]["response_error_code"]
            assert not validator.is_valid(broken);negative+=1
    for example in examples["http"]:
        doc,operation=operations[example["operation_id"]]
        def check(schema,value):Draft202012Validator({**schema,"components":doc["components"]},format_checker=FormatChecker()).validate(value)
        check(operation["requestBody"]["content"]["application/json"]["schema"],example["request"])
        status=str(example.get("response_status",next(s for s in operation["responses"] if s.startswith("2"))))
        check(operation["responses"][status]["content"]["application/json"]["schema"],example["response"])
    public=documents["public.openapi.json"]
    def valid(name,value):return Draft202012Validator({"$ref":"#/components/schemas/"+name,"components":public["components"]},format_checker=FormatChecker()).is_valid(value)
    sale=next(e["data"] for e in examples["events"] if e["type"]=="SaleConfigured")
    for invalid in (0,-1,1.5,"1",9007199254740992):
        broken=copy.deepcopy(sale);broken["per_shopper_unit_cap"]=invalid;assert not valid("Sale",broken);negative+=1
    create=next(e["request"] for e in examples["http"] if e["operation_id"]=="createSale")
    omitted=copy.deepcopy(create);del omitted["per_shopper_unit_cap"];assert valid("SaleWrite",omitted)
    keys=["sale_id","product_id","version","unit_price_minor","currency","allocated_units","per_shopper_unit_cap","starts_at","ends_at","admission_limits"]
    digest=hashlib.sha256(json.dumps({k:sale[k] for k in keys},sort_keys=True,separators=(',',':')).encode()).hexdigest()
    assert all(e["data"]["allocation_digest"]==digest for e in examples["events"] if e["type"]=="SaleReady")
    changed=copy.deepcopy(sale);changed["per_shopper_unit_cap"]+=1
    assert hashlib.sha256(json.dumps({k:changed[k] for k in keys},sort_keys=True,separators=(',',':')).encode()).hexdigest()!=digest
    subprocess.run([sys.executable,ROOT/"scripts/generate_contracts.py","--check"],check=True)
    subprocess.run([sys.executable,ROOT/"scripts/generate_events.py","--check"],check=True)
    adoption=json.loads((CONTRACTS/"adoption.json").read_text())
    adopted=adoption["status"]=="approved" and set(adoption["required_reviewers"])<=set(adoption["approved_reviewers"])
    if args.require_adopted and not adopted:raise SystemExit("Contract adoption blocked: producer/consumer reviews are pending (E4)")
    print(f"PASS: {len(documents)} OpenAPI documents, {len(examples['events'])} events, {len(examples['http'])} HTTP examples, {negative} rejection controls, cap-aware digest, source/generated drift")
    print("Draft adoption: "+("approved" if adopted else "pending E4; independent scaffold checks only"))
    return 0

if __name__=="__main__":raise SystemExit(main())
