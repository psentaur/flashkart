"""Verify finalized windows and checkpoint restoration against local Kafka and Flink."""
import argparse
from datetime import datetime, timezone
import json
import shlex
import subprocess
import time
from urllib.request import urlopen
from uuid import uuid4

from jsonschema import Draft202012Validator

parser = argparse.ArgumentParser()
parser.add_argument("--compose", required=True)
args = parser.parse_args()
compose = shlex.split(args.compose)
suffix = uuid4().hex[:12]
events, results = f"analytics-test-events-{suffix}", f"analytics-test-results-{suffix}"
jobs = []


def run(service, *command, data=None, timeout=45):
    return subprocess.run(
        [*compose, "exec", "-T", service, *command],
        input=data, text=True, capture_output=True, check=True, timeout=timeout,
    ).stdout


def kafka(tool, *args, data=None, timeout=45):
    return run("kafka", f"/opt/kafka/bin/kafka-{tool}.sh", "--bootstrap-server", "kafka:29092",
               *args, data=data, timeout=timeout)


def api(path):
    with urlopen(f"http://127.0.0.1:8081/{path}", timeout=4) as response:
        return json.load(response)


def wait_for(check, description):
    deadline = time.monotonic() + 50
    while time.monotonic() < deadline:
        try:
            result = check()
        except OSError:
            result = None
        if result:
            return result
        time.sleep(0.5)
    raise AssertionError(f"timed out waiting for {description}")


def start(checkpoint=None):
    restore = ["-s", checkpoint] if checkpoint else []
    output = run("flink-jobmanager", "/opt/flink/bin/flink", "run", "-d", "-p", "1",
                 *restore, f"/tmp/{events}.jar", "--brokers", "kafka:29092",
                 "--checkpoints", f"file:///opt/flink/checkpoints/{events}",
                 "--checkpoint-ms", "1000", "--group", events,
                 "--events-topic", events, "--results-topic", results)
    job = output.strip().splitlines()[-1].split()[-1]
    jobs.append(job)
    wait_for(lambda: api(f"jobs/{job}")["state"] == "RUNNING", "Flink job startup")
    return job


def produce(*records):
    kafka("console-producer", "--topic", events, "--producer-property", "partitioner.ignore.keys=false",
          data="".join(json.dumps(record) + "\n" for record in records))


def instant(seconds):
    return datetime.fromtimestamp(seconds, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


base = 1_800_000_000
sale = f"sale-{suffix}"


def event(name, event_id, seconds, **payload):
    record = dict(schema_version=1, event_type=name, event_id=event_id,
                  occurred_at=instant(base + seconds), trace_id=event_id, payload=payload)
    if name != "analytics.heartbeat":
        record.update(sale_id=sale, attempt_id=event_id, customer_id="customer")
    if name in ("reservation.held", "order.confirmed"):
        record["reservation_id"] = event_id
    if name == "order.confirmed":
        record["order_id"] = event_id
    return record


def source_progress(job):
    vertex = next(v for v in api(f"jobs/{job}")["vertices"] if v["name"].startswith("Source:"))
    return metric(job, vertex, ".Filter.currentOutputWatermark")


def metric(job, vertex, suffix):
    path = f"jobs/{job}/vertices/{vertex['id']}/metrics"
    name = next((m["id"] for m in api(path) if m["id"].endswith(suffix)), None)
    values = api(f"{path}?get={name}") if name else []
    return int(values[0]["value"]) if values else -1


created = []
try:
    wait_for(lambda: api("overview")["slots-available"] >= 1, "a free Flink task slot")
    for topic in (events, results):
        kafka("topics", "--create", "--topic", topic, "--partitions", "1", "--replication-factor", "1")
        created.append(topic)
    with open("analytics/target/sales-analytics-0.1.0.jar", "rb") as artifact:
        subprocess.run([*compose, "exec", "-T", "flink-jobmanager", "sh", "-c", 'cat > "$1"',
                        "sh", f"/tmp/{events}.jar"], stdin=artifact, check=True, timeout=30)

    first = start()
    accepted = event("attempt.accepted", "accepted", 1)
    held = event("reservation.held", "held", 58, expires_at=instant(base + 178))
    produce(accepted, accepted, event("attempt.rejected", "throttled", 2, reason="THROTTLED"),
            event("attempt.rejected", "sold-out", 3, reason="SOLD_OUT"), held,
            event("order.confirmed", "confirmed", 59, latency_ms=20),
            event("attempt.accepted", "boundary", 60),
            event("analytics.heartbeat", "heartbeat-before-restore", 70))
    wait_for(lambda: source_progress(first) >= (base + 59) * 1000, "initial Kafka watermark")
    initial = api(f"jobs/{first}/checkpoints")["latest"].get("completed")
    last_id = initial["id"] if initial else -1

    def new_checkpoint():
        checkpoint = api(f"jobs/{first}/checkpoints")["latest"].get("completed")
        if checkpoint and checkpoint["id"] > last_id:
            return checkpoint["external_path"]

    wait_for(new_checkpoint, "checkpoint with open windows")
    run("flink-jobmanager", "/opt/flink/bin/flink", "cancel", first)
    wait_for(lambda: api(f"jobs/{first}")["state"] == "CANCELED", "canceled job")
    checkpoint_dir = f"/opt/flink/checkpoints/{events}/{first}"
    metadata = run("flink-jobmanager", "find", checkpoint_dir, "-name", "_metadata").splitlines()
    assert len(metadata) == 1, metadata
    checkpoint = "file://" + metadata[0].rsplit("/", 1)[0]
    restored = start(checkpoint)
    produce(event("analytics.heartbeat", "open-late-period", 80))
    wait_for(lambda: source_progress(restored) >= (base + 69) * 1000, "watermark inside allowed lateness")
    produce(accepted, event("order.confirmed", "allowed-late", 59, latency_ms=40),
            event("analytics.heartbeat", "finish-first-window", 104))
    wait_for(lambda: source_progress(restored) >= (base + 93) * 1000, "watermark past first deadline")
    produce(event("attempt.accepted", "too-late", 7), event("analytics.heartbeat", "finish-second-window", 164))
    output = kafka("console-consumer", "--topic", results, "--from-beginning", "--max-messages", "2",
                   "--timeout-ms", "25000", timeout=35)
    records = [json.loads(line) for line in output.splitlines()]
    with open("contracts/events/analytics-result.v1.schema.json") as contract:
        schema = Draft202012Validator(json.load(contract), format_checker=Draft202012Validator.FORMAT_CHECKER)
    for record in records:
        schema.validate(record)
        assert record["sale_id"] == sale, record
    first_result, second_result = (record["payload"] for record in records)
    assert first_result == dict(starts_at=instant(base), ends_at=instant(base + 60),
                                demand=2, admission_rejections=1, stock_rejections=1, reservations=1,
                                confirmed_purchases=2, average_latency_ms=30), first_result
    assert second_result == dict(starts_at=instant(base + 60), ends_at=instant(base + 120),
                                 demand=1, admission_rejections=0, stock_rejections=0, reservations=0,
                                 confirmed_purchases=0, average_latency_ms=None), second_result
    def counted_late():
        vertex = next(v for v in api(f"jobs/{restored}")["vertices"] if "count dropped late events" in v["name"])
        return metric(restored, vertex, ".lateEventsDropped") == 1
    wait_for(counted_late, "late-event counter")
    print("Flink checkpoint recovery, deduplication, late events and both final windows verified.")
except subprocess.CalledProcessError as error:
    raise RuntimeError(f"{error.cmd}\n{error.stdout}\n{error.stderr}") from error
finally:
    for job in jobs:
        try:
            if api(f"jobs/{job}")["state"] in {"RUNNING", "RESTARTING"}:
                run("flink-jobmanager", "/opt/flink/bin/flink", "cancel", job)
        except (OSError, subprocess.SubprocessError):
            pass
    for topic in created:
        try:
            kafka("topics", "--delete", "--topic", topic)
        except subprocess.SubprocessError:
            pass
    run("flink-jobmanager", "rm", "-rf", f"/tmp/{events}.jar", f"/opt/flink/checkpoints/{events}")
