#!/usr/bin/env python3
"""Assign the first local owner after they register and verify their email."""
import argparse
import json
import os
import re
import shlex
import urllib.request

from dev import ROOT, load_env

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("user_id", help="The intended owner's Clerk user_... ID")
args = parser.parse_args()
if not re.fullmatch(r"user_[A-Za-z0-9]+", args.user_id):
    parser.error("Supply a Clerk user ID, not an email address.")
load_env()
existing = os.environ.get("FLASHKART_OWNER_USER_ID")
if existing and existing != args.user_id:
    parser.error("An owner is already configured. Ownership changes require an operator to edit .env explicitly.")
secret = os.environ.get("CLERK_SECRET_KEY")
if not secret:
    parser.error("Configure CLERK_SECRET_KEY in the root .env first.")
request = urllib.request.Request("https://api.clerk.com/v1/users/" + args.user_id,
                                 headers={"Authorization": "Bearer " + secret, "User-Agent": "flashkart-dev/1.0"})
with urllib.request.urlopen(request, timeout=10) as response:
    user = json.load(response)
verified = any(email["id"] == user.get("primary_email_address_id") and
               email.get("verification", {}).get("status") == "verified"
               for email in user.get("email_addresses", []))
if not verified or user.get("banned") or user.get("locked"):
    parser.error("The owner must have a verified primary email and an enabled account.")
path = ROOT / ".env"
lines = path.read_text().splitlines() if path.exists() else []
lines = [line for line in lines if not line.startswith(("FLASHKART_OWNER_USER_ID=", "export FLASHKART_OWNER_USER_ID="))]
lines.append("FLASHKART_OWNER_USER_ID=" + shlex.quote(args.user_id))
temporary = ROOT / ".env.owner.tmp"
fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as stream:
    stream.write("\n".join(lines) + "\n")
temporary.replace(path)
print("Owner configured. Restart with dev.py stop, then dev.py start.")
