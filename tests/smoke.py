import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import tempfile
import threading
from urllib.error import HTTPError
from urllib.request import urlopen

bin_dir = Path(sys.argv[1]).resolve()
config = json.loads(Path("deploy/config/local.example.json").read_text())
config.update(http_address="127.0.0.1:0", grpc_address="127.0.0.1:0")
env = dict(os.environ)
env.pop("AWS_LAMBDA_RUNTIME_API", None)
env["FLASHKART_REPLICA_A_DSN"] = (
    "postgres://replica_app@127.0.0.1:1/flashkart?sslmode=disable&connect_timeout=1"
)
env["FLASHKART_ADMIN_TOKEN"] = "smoke-only-token"
env["FLASHKART_TRANSACTIONAL_DSN"] = env["FLASHKART_REPLICA_A_DSN"]
env.pop("FLASHKART_PRODUCT_IMAGES_BUCKET", None)
env.pop("FLASHKART_PURCHASE_QUEUE_URL", None)
env["FLASHKART_CUSTOMER_TOKEN"] = "smoke-customer-token"
env["FLASHKART_CUSTOMER_ID"] = "smoke-customer"
services = (
    "admission-controller", "analytics-api", "catalog-replica",
    "flash-sale-manager", "inventory-reservation", "notification",
    "order-service", "payment-service", "product-catalog",
)

def check_service(name, path):
    with subprocess.Popen(
        [bin_dir / name, "-config", path],
        stderr=subprocess.PIPE, text=True, env=env,
    ) as process:
        logs = queue.Queue()

        def read_logs():
            for line in process.stderr:
                logs.put(line)
            logs.put(None)

        reader = threading.Thread(target=read_logs, daemon=True)
        reader.start()
        try:
            while True:
                line = logs.get(timeout=10)
                assert line is not None, f"{name} stopped before startup"
                record = json.loads(line)
                assert record["level"] != "ERROR", record
                if record["msg"] == "HTTP listening":
                    base = "http://" + record["address"]
                    break
            for endpoint in ("healthz", "readyz", "metrics"):
                if name in ("catalog-replica", "flash-sale-manager", "inventory-reservation", "admission-controller", "order-service", "payment-service") and endpoint == "readyz":
                    try:
                        urlopen(f"{base}/{endpoint}", timeout=3).close()
                    except HTTPError as error:
                        with error:
                            assert error.code == 503
                    else:
                        raise AssertionError("replica reported ready without a database")
                    continue
                with urlopen(f"{base}/{endpoint}", timeout=3) as response:
                    assert response.status == 200
                    if endpoint == "metrics":
                        assert b"flashkart_http_requests_total" in response.read()
            try:
                urlopen(f"{base}/api/attempts", timeout=3).close()
            except HTTPError as error:
                with error:
                    assert error.code == 501
                    assert json.load(error)["code"] == "NOT_IMPLEMENTED"
            else:
                raise AssertionError(f"{name} returned success for a pending API")
            process.send_signal(signal.SIGTERM)
            assert process.wait(timeout=8) == 0
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            reader.join(timeout=3)

with tempfile.TemporaryDirectory() as directory:
    path = Path(directory) / "service.json"
    path.write_text(json.dumps(config))
    for name in services:
        check_service(name, path)
    path.write_text(json.dumps(dict(config, http_address="invalid")))
    rejected = subprocess.run(
        [bin_dir / "product-catalog", "-config", path],
        capture_output=True, text=True, env=env, timeout=5,
    )
    assert rejected.returncode != 0
    assert "invalid HTTP or gRPC listen address" in rejected.stderr
print("Nine service shells passed startup, HTTP health, API status and SIGTERM checks. Invalid configuration was rejected.")
