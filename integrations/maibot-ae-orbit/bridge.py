"""Bounded HTTP and durable send receipts. No access to MaiBot internals."""

from __future__ import annotations

from contextlib import contextmanager
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional
from urllib.parse import urlparse
from urllib.request import Request, urlopen
from zoneinfo import ZoneInfo

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

    def request(self, path: str, body: Optional[Dict[str, Any]], method: str = "POST") -> Any:
        request = Request(self.endpoint + path, data=None if body is None else json.dumps(body, ensure_ascii=False).encode("utf-8"),
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


def observation_time(value: Any) -> str:
    if not value:
        return "尚无有效观测"
    try:
        return datetime.fromisoformat(str(value).replace("Z", "+00:00")).astimezone(ZoneInfo("Asia/Shanghai")).strftime("%m-%d %H:%M:%S")
    except ValueError:
        return str(value)


def energy_lines(energy: Dict[str, Any], heading: str = "") -> List[str]:
    status = energy.get("status", "waiting_sample")
    labels = {"offline":"监控 OC 离线", "stale":"数据过期", "read_error":"读取异常", "paused":"监控已暂停", "revoked":"监控 OC 已撤销", "waiting_collector":"等待监控 OC", "waiting_sample":"等待首次观测"}
    lines = [heading] if heading else []
    valid = all(isinstance(energy.get(k), str) and re.fullmatch(r"[0-9]{1,128}", energy[k]) for k in ["stored_eu", "capacity_eu"])
    if valid:
        stored, capacity = int(energy["stored_eu"]), int(energy["capacity_eu"])
        valid = capacity > 0 and stored <= capacity
    if valid:
        label = "当前储能" if status == "online" else "最后观测储能"
        lines.append(f"{label}：{short_energy(stored)}")
        hundredths = (stored * 10000 + capacity // 2) // capacity
        percent = f"{hundredths // 100}.{hundredths % 100:02d}".rstrip("0").rstrip(".")
        lines.append("电量：" + percent + "%")
    else:
        lines.append("储能：尚无有效观测")
    if status != "online":
        lines.append(labels.get(status, status) + " · " + observation_time(energy.get("observed_at")))
    return lines


def short_energy(value: int) -> str:
    for exponent, unit in [(24,"Y"),(21,"Z"),(18,"E"),(15,"P"),(12,"T"),(9,"G"),(6,"M"),(3,"k")]:
        scale = 10 ** exponent
        if value >= scale:
            hundredths = (value * 100 + scale // 2) // scale
            amount = f"{hundredths // 100:,}.{hundredths % 100:02d}".rstrip("0").rstrip(".")
            return f"{amount} {unit} EU"
    return f"{value:,} EU"


def inventory_keyword(text: Any) -> str:
    if not isinstance(text, str) or len(text) > 256:
        return ""
    text = text.strip()
    explicit = re.fullmatch(r"[!/／]?库存\s+(.+)", text)
    if explicit:
        text = explicit.group(1)
    key = " ".join(text.split()).lower()
    if not 1 <= len(key) <= 32 or not all(c.isalpha() or c.isnumeric() or c in " _-" for c in key):
        return ""
    return key


def matching_inventory_keywords(text: Any, keywords: Iterable[str]) -> List[str]:
    if not isinstance(text, str):
        return []
    normalized = " ".join(text.split()).lower()
    # Stable ordering makes one message one request regardless of set order;
    # the API unions identities so overlapping aliases never duplicate stock.
    return sorted(k for k in set(keywords) if k and k in normalized)


def inventory_text(payload: Dict[str, Any]) -> str:
    lines = ["库存 · " + str(payload.get("keyword", ""))]
    items = payload.get("items") or []
    for item in items:
        label = str(item.get("label") or "物品")
        unit = "mB" if item.get("kind") == "fluid" else "件"
        suffix = " · NBT " + str(item.get("item_id", ""))[:8] if item.get("nbt") else ""
        heading = str(item.get("subnet") or "子网") + " · " + label + suffix
        q = item.get("quantity")
        valid = isinstance(q, str) and re.fullmatch(r"[0-9]{1,19}", q)
        if item.get("known") is True and valid:
            lines.append(heading + "：" + f"{int(q):,}" + " " + unit)
        else:
            last = "最后 " + f"{int(q):,}" + " " + unit + " · " if valid else ""
            lines.append(heading + "：未知（" + last + observation_time(item.get("observed_at")) + "）")
    if not items:
        lines.append("没有匹配的监控项")
    total = int(payload.get("total") or len(items))
    if total > len(items):
        lines.append(f"共 {total} 项，显示前 {len(items)} 项；可设置更具体的关键词。")
    # Whole rows remain readable when an unusually large shared keyword matches.
    while len("\n".join(lines)) > 1600 and len(lines) > 2:
        lines.pop(-2)
        lines[-1] = "其余结果请在子网页面查看。"
    return "\n".join(lines)[:1600]


def notification_text(delivery: Dict[str, Any]) -> str:
    payload = delivery["payload"]
    if delivery.get("kind") == "inventory_query" or "keyword" in payload and "items" in payload:
        return inventory_text(payload)
    if payload.get("alert_kind") == "low_energy" or isinstance(payload.get("energy"), dict):
        device = str(payload.get("device") or "蓝波顿").strip()
        if device == "蓝波顿超级电容库": device = "蓝波顿"
        current = payload.get("current_energy") or payload.get("energy") or {}
        return "\n".join([f"⚠️ {device}电量预警", *energy_lines(current)])[:1600]
    if "devices" in payload:
        devices = payload["devices"]
        lines = ["蓝波顿电量"]
        if not devices: lines.append("尚无有效电量数据")
        for device in devices:
            name = str(device.get("device") or "蓝波顿")
            heading = "\n" + name if len(devices) > 1 or name not in ("蓝波顿", "蓝波顿超级电容库") else ""
            lines.extend(energy_lines(device, heading))
        return "\n".join(lines)[:1600]
    if delivery.get("kind") == "subnet":
        lines = ["⚠️ " + str(payload.get("subnet", "子网")) + "库存预警"]
        for item in (payload.get("items") or [payload])[:10]:
            label = {"low": "低于", "high": "高于", "zero": "归零"}.get(item.get("rule"), "越界")
            rule = label if item.get("rule") == "zero" else label + " " + str(item.get("threshold", ""))
            lines.append(str(item.get("item") or item.get("registry") or "物品") + "：" + str(item.get("quantity", "未知")) + " " + str(item.get("unit", "件")) + "（" + rule + "）")
        return "\n".join(lines)
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
    if payload.get("reason"):
        lines.append(str(payload["reason"]))
    if payload.get("at"):
        lines.append("发生时间：" + observation_time(payload["at"]))
    return "\n".join(lines)[:1600]


def delivery_recipients(delivery: Dict[str, Any]) -> List[str]:
    if delivery["channel"] != "group":
        return []
    candidates = [delivery.get("recipient")]
    if delivery.get("kind") == "subnet":
        extra = delivery["payload"].get("recipients")
        if isinstance(extra, list):
            candidates.extend(extra[:10])
    return list(dict.fromkeys(qq for qq in candidates if isinstance(qq, str) and re.fullmatch(r"[1-9][0-9]{4,14}", qq)))[:10]


def message_segments(delivery: Dict[str, Any], mentions: Iterable[str]) -> List[Dict[str, Any]]:
    segments = []
    if delivery["channel"] == "group":
        for qq in mentions:
            if segments:
                segments.append({"type": "text", "data": {"text": " "}})
            segments.append({"type": "at", "data": {"qq": qq}})
    if segments:
        segments.append({"type": "text", "data": {"text": "\n"}})
    segments.append({"type": "text", "data": {"text": notification_text(delivery)}})
    return segments
