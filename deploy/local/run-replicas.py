"""Start the four local replica agents as one foreground process group."""
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
        for index, (identity, address) in enumerate(config["peers"].items(), 1):
            variable = config["database_env"][identity]
            if not os.environ.get(variable):
                raise SystemExit(f"Missing {variable}")
            env = {key: value for key, value in os.environ.items()
                   if key not in config["database_env"].values() or key == variable}
            path = Path(temp) / f"{identity}.json"
            path.write_text(json.dumps(dict(config, replica_id=identity, grpc_address=address,
                                            http_address=f"127.0.0.1:{8100 + index}")))
            agents.append((identity, subprocess.Popen([binary, "-config", path], env=env)))
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
