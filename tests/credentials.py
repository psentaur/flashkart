"""Read current test credentials from the local Clerk browser-session helper."""
import json
import os
import sys
from urllib.parse import urlparse
from urllib.request import urlopen


def credential(customer=False):
    source = os.environ.get("FLASHKART_TEST_TOKEN_URL")
    if source:
        url = urlparse(source)
        if url.scheme != "http" or url.hostname != "127.0.0.1":
            raise ValueError("The test token helper must run on HTTP loopback.")
        with urlopen(source, timeout=15) as response:
            return json.load(response)["customer" if customer else "admin"]
    return os.environ["FLASHKART_CUSTOMER_TOKEN" if customer else "FLASHKART_ADMIN_TOKEN"]


if __name__ == "__main__":
    print(credential(customer=sys.argv[1] == "customer"))
