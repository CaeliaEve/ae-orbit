"""Read-only post-restart check for the AE Orbit backend and OC bridges.

The check never mutates the service, inventory, orders, or device state. It
only reads health, readiness, the selected network, and its registered devices.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


def normalize_origin(value: str) -> str:
    parsed = urllib.parse.urlsplit(value.strip())
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("origin must be an http(s) URL")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("origin must not contain credentials, query, or fragment")
    if parsed.path not in {"", "/"}:
        raise ValueError("origin must not contain a path")
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, "", "", ""))


def fetch_json(origin: str, path: str, token: str) -> tuple[int, object]:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(origin + path, headers=headers)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request, timeout=5) as response:
            return response.status, json.loads(response.read(4 * 1024 * 1024 + 1))
    except urllib.error.HTTPError as error:
        try:
            body = json.loads(error.read(1024 * 1024))
        except (ValueError, json.JSONDecodeError):
            body = None
        return error.code, body


def summarize_snapshot(network: dict, devices: list[dict], expected_devices: int = 0) -> dict:
    rows = devices if isinstance(devices, list) else []
    online = sum(bool(row.get("online")) for row in rows)
    offline = len(rows) - online
    has_heartbeat = bool(network.get("last_seen")) or any(row.get("last_seen") for row in rows)
    if not network.get("online"):
        reason = "heartbeat_waiting" if not has_heartbeat else "oc_offline"
    elif expected_devices and len(rows) < expected_devices:
        reason = "device_count"
    elif offline:
        reason = "oc_offline"
    else:
        reason = "ready"
    return {
        "network": "online" if network.get("online") else "offline",
        "devices": {"total": len(rows), "online": online, "offline": offline},
        "expected_devices": expected_devices,
        "ready": reason == "ready",
        "reason": reason,
        "last_seen": network.get("last_seen") or next((row.get("last_seen") for row in rows if row.get("last_seen")), None),
    }


def check_once(origin: str, token: str, network_id: str, expected_devices: int) -> dict:
    health_status, health = fetch_json(origin, "/healthz", "")
    ready_status, ready = fetch_json(origin, "/readyz", "")
    if health_status != 200 or ready_status != 200:
        return {
            "ready": False,
            "reason": "backend_unavailable",
            "health_status": health_status,
            "ready_status": ready_status,
            "health": health,
            "ready_response": ready,
        }
    status, network = fetch_json(origin, "/api/v1/networks/" + urllib.parse.quote(network_id, safe=""), token)
    if status != 200 or not isinstance(network, dict):
        return {"ready": False, "reason": "network_unavailable", "network_status": status, "network": network}
    status, devices = fetch_json(origin, "/api/v1/networks/" + urllib.parse.quote(network_id, safe="") + "/devices", token)
    if status != 200 or not isinstance(devices, list):
        return {"ready": False, "reason": "devices_unavailable", "devices_status": status, "devices": devices}
    snapshot = summarize_snapshot(network, devices, expected_devices)
    snapshot.update({"health_status": health_status, "ready_status": ready_status})
    return snapshot


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--origin", default=os.environ.get("AE_PUBLIC_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--token", default=os.environ.get("AE_ADMIN_TOKEN", ""))
    parser.add_argument("--network", default=os.environ.get("AE_NETWORK_ID", ""))
    parser.add_argument("--expected-devices", type=int, default=2)
    parser.add_argument("--wait", type=float, default=0, help="seconds to wait for OC recovery")
    parser.add_argument("--interval", type=float, default=5, help="seconds between read-only checks")
    args = parser.parse_args(argv)
    if not args.token or not args.network:
        parser.error("set --token/--network or AE_ADMIN_TOKEN/AE_NETWORK_ID")
    try:
        origin = normalize_origin(args.origin)
    except ValueError as error:
        parser.error(str(error))

    deadline = time.monotonic() + max(0, args.wait)
    while True:
        try:
            result = check_once(origin, args.token, args.network, max(0, args.expected_devices))
        except (OSError, ValueError, json.JSONDecodeError) as error:
            result = {"ready": False, "reason": "backend_unavailable", "error": str(error)[:240]}
        print(json.dumps(result, ensure_ascii=False, sort_keys=True), flush=True)
        if result.get("ready") or time.monotonic() >= deadline:
            return 0 if result.get("ready") else 1
        time.sleep(max(0.2, args.interval))


if __name__ == "__main__":
    sys.exit(main())
