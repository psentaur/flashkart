#!/usr/bin/env python3
"""Start, stop, or inspect the local Flashkart development stack (Linux)."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
STATE = ROOT / "build/local"
RECORD = STATE / "processes.json"


def load_env():
    if (ROOT / ".env").exists():
        for line in (ROOT / ".env").read_text().splitlines():
            parts = shlex.split(line, comments=True)
            if not parts: continue
            if parts[0] == "export": parts = parts[1:]
            if len(parts) != 1 or "=" not in parts[0]:
                raise RuntimeError("Use KEY=value entries in the root .env file.")
            key, value = parts[0].split("=", 1)
            os.environ.setdefault(key, value)

def compose():
    if os.environ.get("COMPOSE"):
        return os.environ["COMPOSE"]
    for engine in ("docker", "podman"):
        if shutil.which(engine) and subprocess.run(
            [engine, "compose", "version"], stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        ).returncode == 0:
            return f"{engine} compose"
    raise RuntimeError("Install Docker Compose or Podman Compose, or set COMPOSE.")


def records():
    return json.loads(RECORD.read_text()) if RECORD.exists() else {}


def identity(pid):
    return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()


def alive(process):
    # Kernel start time protects against signalling a reused PID.
    try:
        fields = identity(process["pid"])
        return fields[0] != "Z" and fields[19] == process["started"]
    except FileNotFoundError:
        return False


def stop_apps(state):
    for process in state.get("apps", {}).values():
        if alive(process):
            os.killpg(process["pid"], signal.SIGTERM)
    deadline = time.monotonic() + 10
    while any(alive(p) for p in state.get("apps", {}).values()):
        if time.monotonic() >= deadline:
            for process in state["apps"].values():
                if alive(process):
                    os.killpg(process["pid"], signal.SIGKILL)
            break
        time.sleep(0.1)


def start():
    if any(alive(p) for p in records().get("apps", {}).values()):
        raise RuntimeError("Local apps are already running. Run stop first.")
    for port in (*range(7001, 7005), *range(7101, 7104), *range(8101, 8105), *range(8201, 8210), 5173):
        with socket.socket() as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                sock.bind(("127.0.0.1", port))
            except OSError as error:
                raise RuntimeError(f"Port {port} is occupied; stop the existing app first.") from error
    env = os.environ.copy()
    mode = env.setdefault("FLASHKART_AUTH_MODE", "clerk")
    env["VITE_AUTH_MODE"] = mode
    env.setdefault("FLASHKART_ALLOWED_ORIGINS", "http://127.0.0.1:5173,http://localhost:5173")
    if mode == "demo":
        env.setdefault("FLASHKART_ADMIN_TOKEN", "local-admin")
        env.setdefault("FLASHKART_CUSTOMER_TOKEN", "local-customer")
        env.setdefault("FLASHKART_CUSTOMER_ID", "local-customer")
    elif mode == "clerk":
        for key in ("VITE_CLERK_PUBLISHABLE_KEY", "CLERK_SECRET_KEY", "CLERK_ISSUER_URL"):
            if not env.get(key):
                raise RuntimeError(f"Set {key} in the root .env or environment. See docs/authentication.md.")
    else:
        raise RuntimeError("FLASHKART_AUTH_MODE must be clerk or demo")
    provider = compose()
    subprocess.run(["make", "dev-up", "migrate", "build-go", f"COMPOSE={provider}"], cwd=ROOT, check=True)
    lockfile = ROOT / "web/package-lock.json"
    installed = ROOT / "web/node_modules/.package-lock.json"
    if not installed.exists() or installed.stat().st_mtime < lockfile.stat().st_mtime:
        subprocess.run(["npm", "--prefix", "web", "ci", "--no-audit", "--no-fund"], cwd=ROOT, check=True)
    STATE.mkdir(parents=True, exist_ok=True)
    state = {"compose": provider, "apps": {}}
    for line in (ROOT / "deploy/local/postgres.env").read_text().splitlines():
        key, value = line.removeprefix("export ").split("=", 1)
        env[key] = shlex.split(value)[0]
    try:
        commands = {
            "market": [sys.executable, "deploy/local/run-replicas.py", "build/bin/catalog-replica", "--market"],
            "web": ["npm", "--prefix", "web", "run", "dev", "--", "--strictPort"],
        }
        for name, command in commands.items():
            with (STATE / f"{name}.log").open("w") as log:
                child = subprocess.Popen(command, cwd=ROOT, env={k: v for k, v in env.items() if name != "web" or k != "CLERK_SECRET_KEY"}, stdin=subprocess.DEVNULL,
                                         stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            state["apps"][name] = {"pid": child.pid, "started": identity(child.pid)[19]}
            RECORD.write_text(json.dumps(state))
        urls = ["http://127.0.0.1:5173/", "http://127.0.0.1:5173/api/products"]
        urls += [f"http://127.0.0.1:{port}/readyz" for port in (*range(8101, 8105), 8201, 8202, 8203, 8209)]
        deadline = time.monotonic() + 30
        while True:
            if not all(alive(p) for p in state["apps"].values()):
                raise RuntimeError(f"An app exited; inspect logs in {STATE}.")
            try:
                for url in urls:
                    with urllib.request.urlopen(url, timeout=2) as response:
                        response.read()
                break
            except (OSError, TimeoutError):
                if time.monotonic() >= deadline:
                    raise RuntimeError(f"Startup timed out; inspect logs in {STATE}.")
                time.sleep(0.3)
    except BaseException:
        stop_apps(state)
        RECORD.unlink(missing_ok=True)
        raise
    print(f"Flashkart running: http://127.0.0.1:5173/\nLogs: {STATE}")
    print(f"Authentication: {mode}. " + ("Demo admin token defaults to local-admin." if mode == "demo" else "Sign in with your Clerk account."))
    print("Checkout, notifications, and analytics require additional environment configuration.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("start", "stop", "status"))
    action = parser.parse_args().action
    os.chdir(ROOT)
    load_env()
    if action == "start":
        start()
    elif action == "stop":
        state = records()
        stop_apps(state)
        provider = state.get("compose") or compose()
        subprocess.run(shlex.split(provider) + ["-p", "flashkart-dev", "-f", "deploy/local/compose.yml",
                       "stop", "postgres-a", "postgres-b", "postgres-c", "postgres-d", "postgres-transactional"], check=True)
        RECORD.unlink(missing_ok=True)
        print("Local apps and databases stopped. Database volumes preserved.")
    else:
        state = records()
        for name in ("market", "web"):
            running = name in state.get("apps", {}) and alive(state["apps"][name])
            print(f"{name}: {'running' if running else 'stopped (or started outside this script)'}")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
