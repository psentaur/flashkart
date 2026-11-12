"""Verify selected AWS API calls against an isolated local emulator."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
from uuid import uuid4

port = int(os.environ.get("FLASHKART_EMULATOR_PORT", "9297"))
if not 1 <= port <= 65535:
    raise SystemExit("Invalid emulator port")
files = Path.home() / ".aws" / "emulator" / str(port)
if not all((files / name).is_file() for name in ("config", "emulator_credentials", "ca.pem")):
    raise SystemExit(f"Start a signed AWS Emulator on loopback port {port} before running this check.")

env = {
    "PATH": os.environ["PATH"],
    "AWS_CONFIG_FILE": str(files / "config"),
    "AWS_SHARED_CREDENTIALS_FILE": str(files / "emulator_credentials"),
    "AWS_CA_BUNDLE": str(files / "ca.pem"),
    "AWS_EC2_METADATA_DISABLED": "true",
    "AWS_MAX_ATTEMPTS": "1",
    "AWS_PAGER": "",
}

def aws(*args):
    result = subprocess.run(
        ["aws", "--endpoint-url", f"https://127.0.0.1:{port}", "--region", "us-east-1",
         "--output", "json", "--no-cli-pager", "--cli-connect-timeout", "4",
         "--cli-read-timeout", "8", *args],
        env=env, text=True, capture_output=True, timeout=20,
    )
    if result.returncode:
        raise RuntimeError(result.stderr.strip())
    return json.loads(result.stdout) if result.stdout.strip() else None

if aws("sts", "get-caller-identity")["Account"] != "000000000000":
    raise SystemExit("Unexpected account from the emulator endpoint")

name = "flashkart-smoke-" + uuid4().hex[:12]

def sqs():
    url = aws("sqs", "create-queue", "--queue-name", name)["QueueUrl"]
    try:
        aws("sqs", "send-message", "--queue-url", url, "--message-body", name)
        result = aws("sqs", "receive-message", "--queue-url", url, "--wait-time-seconds", "2")
        messages = (result or {}).get("Messages", [])
        if len(messages) != 1 or messages[0]["Body"] != name:
            raise RuntimeError("Expected the submitted queue message")
        aws("sqs", "delete-message", "--queue-url", url, "--receipt-handle", messages[0]["ReceiptHandle"])
    finally:
        aws("sqs", "delete-queue", "--queue-url", url)

def dynamodb():
    aws("dynamodb", "create-table", "--table-name", name, "--attribute-definitions", "AttributeName=id,AttributeType=S",
        "--key-schema", "AttributeName=id,KeyType=HASH", "--billing-mode", "PAY_PER_REQUEST")
    try:
        aws("dynamodb", "wait", "table-exists", "--table-name", name)
        item = json.dumps({"id": {"S": name}})
        put = ("dynamodb", "put-item", "--table-name", name, "--item", item,
               "--condition-expression", "attribute_not_exists(id)")
        aws(*put)
        try:
            aws(*put)
        except RuntimeError as error:
            if "ConditionalCheckFailedException" not in str(error):
                raise
        else:
            raise RuntimeError("Conditional replay was accepted")
        if aws("dynamodb", "get-item", "--table-name", name, "--key", item, "--consistent-read")["Item"] != json.loads(item):
            raise RuntimeError("Stored item did not match")
    finally:
        aws("dynamodb", "delete-table", "--table-name", name)

def s3():
    aws("s3api", "create-bucket", "--bucket", name)
    try:
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "source"
            target = Path(tmp) / "target"
            source.write_text(name)
            aws("s3api", "put-object", "--bucket", name, "--key", "probe", "--body", str(source))
            try:
                aws("s3api", "get-object", "--bucket", name, "--key", "probe", str(target))
                if target.read_text() != name:
                    raise RuntimeError("Stored object did not match")
            finally:
                aws("s3api", "delete-object", "--bucket", name, "--key", "probe")
    finally:
        aws("s3api", "delete-bucket", "--bucket", name)

failed = []
for label, check in (("SQS round trip", sqs), ("DynamoDB conditional write", dynamodb),
                     ("S3 round trip", s3), ("Lambda ListFunctions", lambda: aws("lambda", "list-functions"))):
    try:
        check()
        print(f"PASS {label}", flush=True)
    except (RuntimeError, subprocess.TimeoutExpired) as error:
        print(f"UNVERIFIED {label} ({error})", flush=True)
        failed.append(label)
print("Lambda deployment, invocation and SQS event source mappings require separate verification.")
raise SystemExit(bool(failed))
