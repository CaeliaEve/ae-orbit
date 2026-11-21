"""Bounded HTTP and durable send receipts. No access to MaiBot internals."""

from __future__ import annotations

from contextlib import contextmanager
from pathlib import Path
from typing import Any, Dict, List
from urllib.parse import urlparse
from urllib.request import Request, urlopen

import json
import os
import re
import sqlite3


def normalize_channels(value: Dict[str, Any]) -> Dict[str, Any]:
    groups = value.get("groups", [])
    if not isinstance(groups, list) or len(groups) > 16 or not all(isinstance(g, str) and re.fullmatch(r"[1-9][0-9]{4,14}",g) for g in groups):
        raise ValueError("最多允许 16 个有效 QQ 群")
    recipient = value.get("alarm_recipient", "")
    if not isinstance(recipient, str) or recipient and not re.fullmatch(r"[1-9][0-9]{4,14}",recipient):
        raise ValueError("告警接收人 QQ 号无效")
    if type(value.get("allow_private", False)) is not bool:
        raise ValueError("私聊开关必须为布尔值")
    return {"groups": sorted(set(groups)), "allow_private": value.get("allow_private", False), "alarm_recipient": recipient}


class ChannelPolicy:
    def __init__(self, path: Path) -> None:
        self.path = path

    def read(self) -> Dict[str, Any]:
        if not self.path.exists():
            return normalize_channels({})
        return normalize_channels(json.loads(self.path.read_text(encoding="utf-8")))

    def save(self, value: Dict[str, Any]) -> Dict[str, Any]:
        policy = normalize_channels(value)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        temporary = self.path.with_suffix(".tmp")
        with temporary.open("w", encoding="utf-8") as output:
            output.write(json.dumps(policy, ensure_ascii=False))
            output.flush()
            os.fsync(output.fileno())
        temporary.chmod(0o600)
        os.replace(temporary, self.path)
        return policy


class BridgeClient:
    def __init__(self, endpoint: str, token: str) -> None:
        parsed = urlparse(endpoint)
        if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError("Invalid AE Orbit endpoint")
        self.endpoint = endpoint.rstrip("/")
        self.token = token

    def request(self, path: str, body: Dict[str, Any], method: str = "POST") -> Any:
        request = Request(self.endpoint + path, data=json.dumps(body, ensure_ascii=False).encode("utf-8"),
                          headers={"Authorization": "Bearer " + self.token, "Content-Type": "application/json"}, method=method)
        with urlopen(request, timeout=12) as response:
            raw = response.read(2 * 1024 * 1024 + 1)
        if len(raw) > 2 * 1024 * 1024:
            raise ValueError("AE Orbit response exceeds budget")
        return json.loads(raw) if raw else None


class ReceiptJournal:
    def __init__(self, path: Path) -> None:
        self.path = path
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with self._open() as db:
            db.execute("CREATE TABLE IF NOT EXISTS receipts(id TEXT PRIMARY KEY,attempt TEXT NOT NULL,phase TEXT NOT NULL,receipt TEXT)")
        self.path.chmod(0o600)

    @contextmanager
    def _open(self):
        db = sqlite3.connect(self.path, timeout=5)
        db.execute("PRAGMA synchronous=FULL")
        try:
            with db:
                yield db
        finally:
            db.close()

    def started(self, delivery: Dict[str, Any]) -> None:
        with self._open() as db:
            db.execute("INSERT INTO receipts VALUES(?,?,'started',NULL)", (delivery["id"], delivery["attempt_id"]))

    def completed(self, delivery_id: str, receipt: Dict[str, Any]) -> None:
        with self._open() as db:
            db.execute("UPDATE receipts SET phase='receipt',receipt=? WHERE id=?", (json.dumps(receipt), delivery_id))

    def pending(self) -> List[Dict[str, Any]]:
        with self._open() as db:
            rows = db.execute("SELECT id,attempt,phase,receipt FROM receipts ORDER BY rowid LIMIT 32").fetchall()
        result = []
        for delivery_id, attempt, phase, raw in rows:
            receipt = json.loads(raw) if raw else {"attempt_id": attempt, "state": "uncertain", "external_id": "",
                                                  "detail": "机器人在发送过程中重启，未自动重发"}
            result.append({"id": delivery_id, "receipt": receipt})
        return result

    def acknowledged(self, delivery_id: str) -> None:
        with self._open() as db:
            db.execute("DELETE FROM receipts WHERE id=?", (delivery_id,))


def notification_text(delivery: Dict[str, Any]) -> str:
    payload = delivery["payload"]
    lines = [str(payload.get("title", "AE Orbit 通知"))]
    for key, label in (("cpu", "CPU"), ("device", "设备"), ("network", "网络"), ("owner", "下单玩家")):
        if payload.get(key):
            lines.append(f"{label}：{payload[key]}")
    if payload.get("item") or payload.get("registry"):
        product = payload.get("item") or payload["registry"]
        quantity = payload.get("quantity", "")
        unit = payload.get("unit", "")
        if unit == "fluid": unit = "mB"
        if unit == "item": unit = "件"
        lines.append(f"产物：{product}" + (f" × {quantity} {unit}" if quantity else ""))
    if payload.get("alert_kind"):
        labels = {"low_energy": "低电量", "collector_offline": "监控 OC 离线", "read_error": "读取异常", "falling": "储能持续下降", "capacity_changed": "容量变化"}
        lines.append("类型：" + labels.get(payload["alert_kind"], payload["alert_kind"]))
    if payload.get("reason"):
        lines.append(str(payload["reason"]))
    if payload.get("at"):
        lines.append("发生时间：" + str(payload["at"]))
    lines.append("AE Orbit · " + delivery["id"][:8])
    return "\n".join(lines)[:1600]


def message_segments(delivery: Dict[str, Any], mention: bool) -> List[Dict[str, Any]]:
    segments = []
    if mention and delivery.get("recipient") and delivery["channel"] == "group":
        segments.append({"type": "at", "data": {"qq": delivery["recipient"]}})
        segments.append({"type": "text", "data": {"text": "\n"}})
    segments.append({"type": "text", "data": {"text": notification_text(delivery)}})
    return segments
