import json
from pathlib import Path

from jsonschema import Draft202012Validator
from openapi_spec_validator import validate

root = Path(__file__).resolve().parents[1] / "contracts"
api = json.loads((root / "openapi/flashkart.v1.json").read_text())
validate(api)
schemas = {}
for path in sorted((root / "events").glob("*.schema.json")):
    schema = json.loads(path.read_text())
    Draft202012Validator.check_schema(schema)
    schemas[path.name] = Draft202012Validator(schema, format_checker=Draft202012Validator.FORMAT_CHECKER)

identity = {
    "schema_version": 1,
    "occurred_at": "2026-11-12T12:00:00Z",
    "trace_id": "trace-1",
    "sale_id": "sale-1",
    "attempt_id": "attempt-1",
    "customer_id": "customer-1",
}
schemas["purchase-work.v1.schema.json"].validate(identity)
event = dict(identity, event_id="event-1", event_type="attempt.accepted", payload={})
purchase = schemas["purchase-event.v1.schema.json"]
purchase.validate(event)
assert not purchase.is_valid(dict(event, schema_version=2))
assert not purchase.is_valid(dict(event, occurred_at="tomorrow"))
terminal = schemas["terminal-outcome.v1.schema.json"]
outcome = dict(event, event_type="attempt.terminal", payload={"state": "CONFIRMED", "order_id": "order-1"})
terminal.validate(outcome)
assert not terminal.is_valid(dict(outcome, payload={"state": "QUEUED"}))
print(f"Validated OpenAPI and {len(schemas)} event schemas.")
