"""HTTP fixtures and HPA evidence for the deployed-service tests."""
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import sys
import time
from urllib.request import Request, urlopen
from uuid import uuid4


def api(path, method="GET", body=None, customer=False):
    token = os.environ["FLASHKART_CUSTOMER_TOKEN" if customer else "FLASHKART_ADMIN_TOKEN"]
    headers = {"Authorization": "Bearer " + token, "Content-Type": "application/json"}
    if body and "attempt_id" in body:
        headers["Idempotency-Key"] = body["attempt_id"]
    request = Request(os.environ["BASE_URL"] + path, headers=headers, method=method,
                      data=json.dumps(body).encode() if body is not None else None)
    with urlopen(request, timeout=8) as response:
        return json.load(response)


def wait_for(fetch, expected):
    for _ in range(60):
        result = fetch()
        if result["state"] == expected:
            return result
        time.sleep(1)
    raise AssertionError(result)


def product(identifier):
    api("/api/admin/products/" + identifier, "PUT", {
        "operation_id": uuid4().hex, "product": {"product_id": identifier, "name": identifier}, "context": {}})


action, value = sys.argv[1:]
if action == "hpa":
    peaks = {}
    data = Path(value).read_text().lstrip()
    while data:
        document, end = json.JSONDecoder().raw_decode(data)
        for item in document["items"]:
            name = item["metadata"]["name"]
            peaks[name] = max(peaks.get(name, 0), item.get("status", {}).get("currentReplicas", 0))
        data = data[end:].lstrip()
    print(json.dumps(peaks))
    assert all(peaks.get(name, 0) > 2 for name in ("product-catalog", "admission-controller")), peaks
elif action == "product":
    product(value)
elif action == "read":
    assert api("/api/products/" + value)["product"]["product_id"] == value
elif action in ("read-ready", "read-unready"):
    expected = action == "read-ready"
    for _ in range(60):
        statuses = api("/api/admin/replicas")
        replica = next(item for item in statuses if item["replica_id"] == value)
        if replica["backend_ready"] == expected:
            print(json.dumps(statuses))
            break
        time.sleep(2)
    else:
        raise AssertionError(statuses)
elif action == "sale":
    identifier = value + "-sale"
    product(identifier)
    now = datetime.now(timezone.utc)
    draft = api("/api/admin/sale-drafts", "POST", {
        "product_id": identifier, "stock": 30, "price_minor": 100, "currency": "USD",
        "starts_at": (now - timedelta(minutes=1)).isoformat(),
        "ends_at": (now + timedelta(minutes=20)).isoformat(),
    })
    operation = uuid4().hex
    api("/api/admin/sale-drafts/" + draft["sale_id"] + "/publish", "POST", {"operation_id": operation})
    wait_for(lambda: api("/api/admin/publications/" + operation), "PUBLISHED")
    print(draft["sale_id"])
elif action == "purchase":
    attempt = uuid4().hex
    body = {"attempt_id": attempt, "sale_id": value}
    for _ in range(2):
        assert api("/api/attempts", "POST", body, customer=True)["attempt_id"] == attempt
    outcome = wait_for(lambda: api("/api/attempts/" + attempt, customer=True), "CONFIRMED")
    assert api("/api/orders/" + outcome["order_id"], customer=True)["state"] == "CONFIRMED"
    print(json.dumps(outcome))
else:
    raise ValueError(action)
