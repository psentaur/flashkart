"""Start the replica agents and optionally the Phase 5 services."""
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time

binary = Path(sys.argv[1]).resolve()
config = json.loads(Path("deploy/config/local.example.json").read_text())
agents = []
stopping = False

def request_shutdown(_signal, _frame):
    global stopping
    stopping = True

signal.signal(signal.SIGTERM, request_shutdown)
signal.signal(signal.SIGINT, request_shutdown)

with tempfile.TemporaryDirectory(prefix="flashkart-replicas-") as temp:
    try:
        excluded = set(config["database_env"].values()) | {
            "FLASHKART_FLASH_SALE_DSN", "FLASHKART_INVENTORY_DSN", "FLASHKART_ADMIN_TOKEN",
            "FLASHKART_S3_CREDENTIALS_FILE", "FLASHKART_PRODUCT_IMAGES_BUCKET",
        }
        common = {key: value for key, value in os.environ.items() if key not in excluded}

        def start(name, executable, settings, variables):
            path = Path(temp) / f"{name}.json"
            path.write_text(json.dumps(settings))
            agents.append((name, subprocess.Popen(
                [executable, "-config", path], env=common | variables
            )))

        for index, (identity, address) in enumerate(config["peers"].items(), 1):
            variable = config["database_env"][identity]
            if not os.environ.get(variable):
                raise SystemExit(f"Missing {variable}")
            start(identity, binary, dict(config, replica_id=identity, grpc_address=address,
                                         http_address=f"127.0.0.1:{8100 + index}"),
                  {variable: os.environ[variable]})

        if "--market" in sys.argv[2:]:
            token = os.environ.get("FLASHKART_ADMIN_TOKEN")
            if not token:
                raise SystemExit("Missing FLASHKART_ADMIN_TOKEN")
            services = (
                ("inventory-reservation", 8203, "127.0.0.1:7101", "FLASHKART_INVENTORY_DSN"),
                ("flash-sale-manager", 8202, "", "FLASHKART_FLASH_SALE_DSN"),
                ("product-catalog", 8201, "", None),
            )
            for name, port, grpc, dsn in services:
                settings = dict(config, http_address=f"127.0.0.1:{port}", grpc_address=grpc)
                variables = {} if name == "inventory-reservation" else {"FLASHKART_ADMIN_TOKEN": token}
                if dsn:
                    variables[config["database_env"]["transactional"]] = os.environ[dsn]
                if name == "product-catalog" and os.environ.get("FLASHKART_PRODUCT_IMAGES_BUCKET"):
                    variables["FLASHKART_PRODUCT_IMAGES_BUCKET"] = os.environ["FLASHKART_PRODUCT_IMAGES_BUCKET"]
                    variables["FLASHKART_S3_CREDENTIALS_FILE"] = os.environ["FLASHKART_S3_CREDENTIALS_FILE"]
                    settings["aws"] = {"region": "us-east-1", "endpoints": {"s3": os.environ["FLASHKART_S3_ENDPOINT"]}}
                start(name, binary.with_name(name), settings, variables)

        while not stopping:
            for identity, agent in agents:
                if agent.poll() is not None:
                    raise SystemExit(f"Replica {identity} exited with status {agent.returncode}")
            time.sleep(0.2)
    finally:
        for _, agent in agents:
            if agent.poll() is None:
                agent.terminate()
        for _, agent in agents:
            try:
                agent.wait(timeout=6)
            except subprocess.TimeoutExpired:
                agent.kill()
                agent.wait()
