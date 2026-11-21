"""AE Orbit notification connector for MaiBot SDK 2.21.

Only explicit notification records are sent. The plugin never generates replies
through an LLM, opens another QQ connection, or imports src.* host internals.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any, Dict, List, Optional
from urllib.error import HTTPError
from urllib.parse import urlencode

import asyncio
import json
import re
import hashlib

from maibot_sdk import API, Command, ConfigWidget, ConfigWidgetState, Field, HomeCard, MaiBotPlugin, PluginConfigBase

from .bridge import BridgeClient, ChannelPolicy, ReceiptJournal, message_segments, normalize_channels, notification_text


class ConnectorSettings(PluginConfigBase):
    enabled: bool = Field(default=True, description="启用连接器")
    endpoint: str = Field(default="http://125.77.172.25:8080", description="AE Orbit 地址")
    credential_file: str = Field(default="/opt/qqbot/ae-orbit-connector.json", description="连接凭据文件（服务器 chmod 600）")
    config_version: str = Field(default="1.2.1", description="配置版本")


class ConnectorConfig(PluginConfigBase):
    plugin: ConnectorSettings = Field(default_factory=ConnectorSettings)


class AEOrbitPlugin(MaiBotPlugin):
    config_model = ConnectorConfig
    config_widgets = [ConfigWidget(id="connection", kind="status", label="AE Orbit 连接"),
                      ConfigWidget(id="channels", kind="status", label="通知渠道")]

    def __init__(self) -> None:
        super().__init__()
        self._task: Optional[asyncio.Task[None]] = None
        self._client: Optional[BridgeClient] = None
        self._journal: Optional[ReceiptJournal] = None
        self._bot_id = ""
        self._group_refresh: Dict[str, float] = {}
        self._policy_store: Optional[ChannelPolicy] = None
        self._policy = normalize_channels({})
        self._policy_lock = asyncio.Lock()
        self._available_groups: List[Dict[str, str]] = []
        self._groups_at = -100.0
        self._catalog_at = -100.0
        self._status = "等待连接"
        self._sending_enabled = False
        self._wake = asyncio.Event()

    async def on_load(self) -> None:
        credentials = await asyncio.to_thread(lambda: json.loads(Path(self.config.plugin.credential_file).read_text()))
        self._bot_id = str(credentials["bot_id"])
        if not re.fullmatch(r"[1-9][0-9]{4,14}", self._bot_id):
            raise ValueError("Invalid configured bot QQ account")
        self._client = BridgeClient(self.config.plugin.endpoint, str(credentials["token"]))
        self._policy_store = ChannelPolicy(self.ctx.paths.data_dir / "channels.json")
        self._policy = await asyncio.to_thread(self._policy_store.read)
        self._journal = await asyncio.to_thread(ReceiptJournal, self.ctx.paths.data_dir / "receipts.sqlite")
        self._task = asyncio.create_task(self._run(), name="ae-orbit-qq")
        self.ctx.logger.info("AE Orbit connector started; MaiBot owns allowed notification channels")

    async def on_unload(self) -> None:
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None

    async def on_config_update(self, scope: str, config_data: Dict[str, object], version: str) -> None:
        del config_data, version
        if scope == "self":
            await self.on_unload()
            await self.on_load()

    async def _http(self, path: str, body: Optional[Dict[str, Any]], method: str = "POST") -> Any:
        if self._client is None:
            raise RuntimeError("Connector not initialized")
        return await asyncio.to_thread(self._client.request, path, body, method)

    async def _qq(self, action: str, params: Dict[str, Any]) -> Any:
        return await self.ctx.api.call("adapter.snowluma.action.call_data", version="1", action_name=action, params=params)

    async def get_config_widget_state(self) -> Dict[str, ConfigWidgetState]:
        return {"connection": ConfigWidgetState(text=self._status, tone="success" if self._status.startswith("已连接") else "warning"),
                "channels": ConfigWidgetState(text=f"{len(self._policy['groups'])} 个允许通知的群；请在插件扩展 → AE Orbit 通知渠道中选择", tone="neutral")}

    @HomeCard("connector", title="AE Orbit QQ 联动", description="选择通知群、私聊范围；查看连接状态。",
              content=[{"type": "markdown", "content": "在插件扩展的「AE Orbit 通知渠道」勾选群，AE Orbit 自动同步可用通知目标。"}], order=100)
    async def connector_card(self) -> None:
        return None

    async def _refresh_groups(self, force: bool = False) -> None:
        loop = asyncio.get_running_loop()
        if not force and loop.time() - self._groups_at < 60:
            return
        groups = await self.ctx.api.call("adapter.snowluma.group.get_group_list", version="1", no_cache=True)
        if not isinstance(groups, list):
            raise RuntimeError("机器人群列表读取失败，请稍后刷新")
        self._available_groups = [{"group_id": str(g["group_id"]), "name": re.sub(r"[\x00-\x1f\x7f]","",str(g.get("group_name", "")))[:32]}
                                  for g in groups if isinstance(g, dict) and "group_id" in g]
        self._groups_at = loop.time()

    async def _sync_channels(self) -> None:
        known = {g["group_id"]: g for g in self._available_groups}
        channels = [{"group_id": gid, "name": known[gid]["name"], "alarm_recipient": self._policy["alarm_recipient"]}
                    for gid in self._policy["groups"] if gid in known]
        await self._http("/bridge/qq/v1/channels", {"groups": channels, "allow_private": self._policy["allow_private"]}, "PUT")
        self._catalog_at = asyncio.get_running_loop().time()

    @API("channels_status", description="读取通知渠道和机器人已加入群", version="1")
    async def channels_status(self) -> Dict[str, Any]:
        return {"groups": self._available_groups, "policy": dict(self._policy), "status": self._status,
                "sending_enabled": self._sending_enabled, "endpoint": self.config.plugin.endpoint}

    @API("channels_refresh", description="刷新机器人群列表，不发送消息", version="1")
    async def channels_refresh(self) -> Dict[str, Any]:
        async with self._policy_lock:
            await self._refresh_groups(force=True)
            await self._sync_channels()
        return await self.channels_status()

    @API("channels_save", description="保存允许通知的 QQ 群和私聊范围", version="1")
    async def channels_save(self, groups: List[str], allow_private: bool = False, alarm_recipient: str = "") -> Dict[str, Any]:
        if self._policy_store is None:
            raise RuntimeError("通知渠道尚未初始化")
        policy = normalize_channels({"groups": groups, "allow_private": allow_private, "alarm_recipient": alarm_recipient})
        async with self._policy_lock:
            await self._refresh_groups(force=True)
            known = {g["group_id"] for g in self._available_groups}
            if not set(policy["groups"]) <= known:
                raise ValueError("只能选择机器人已加入的群；请刷新后重新选择")
            self._policy = await asyncio.to_thread(self._policy_store.save, policy)
            # Local policy takes effect before remote sync; a temporary HTTP
            # failure cannot keep a removed group authorized to send.
            await self._sync_channels()
        return await self.channels_status()

    @API("power_preview", description="预览授权群的蓝波顿电量，不发送消息", version="1")
    async def power_preview(self, group_id: str) -> Dict[str, Any]:
        if group_id not in self._policy["groups"]:
            raise ValueError("请先保存并授权该群")
        payload = await self._http("/bridge/qq/v1/power?" + urlencode({"group_id":group_id}), None, "GET")
        return {"text": notification_text({"id":"preview", "payload":payload}), "sending_enabled":payload["sending_enabled"]}

    @Command(name="lsc_power", description="查询当前蓝波顿电量（授权群内 /电量 或 /电量 设备名）",
             pattern=r"^\s*[!/／](?:电量|蓝波顿|蓝博顿|ae电量)(?:\s+(?P<device>[^\r\n]{1,128}))?\s*$",
             aliases=["/电量", "/蓝波顿", "/蓝博顿", "/ae电量"])
    async def query_power(self, group_id: str = "", platform: str = "", matched_groups: Optional[Dict[str, str]] = None,
                          message: Optional[Dict[str, Any]] = None, **kwargs: Any) -> tuple[bool, Optional[str], bool]:
        del kwargs
        if platform != "qq" or group_id not in self._policy["groups"]:
            return True, None, True
        incoming = message or {}
        if incoming.get("account_id") and str(incoming["account_id"]) != self._bot_id:
            return True, None, True
        message_id = str(incoming.get("message_id", ""))
        if not message_id:
            self.ctx.logger.warning("Power query has no stable incoming message ID; request skipped")
            return False, None, True
        key = hashlib.sha256((group_id+":"+message_id).encode()).hexdigest()
        device = str((matched_groups or {}).get("device") or "").strip()
        try:
            await self._http("/bridge/qq/v1/power", {"group_id":group_id,"request_key":"qq-power:"+key,"device":device})
            self._wake.set()
        except Exception as exc:
            self.ctx.logger.warning("Power request could not be queued: %s",type(exc).__name__)
            return False, None, True
        return True, None, True

    async def _flush_receipts(self) -> None:
        if self._journal is None:
            raise RuntimeError("Receipt journal not initialized")
        for saved in await asyncio.to_thread(self._journal.pending):
            try:
                await self._http(f"/bridge/qq/v1/deliveries/{saved['id']}/receipt", saved["receipt"])
            except HTTPError as exc:
                if exc.code not in (404, 409):
                    raise
                self.ctx.logger.warning("AE Orbit receipt no longer owns its lease: %s", saved["id"])
            await asyncio.to_thread(self._journal.acknowledged, saved["id"])

    async def _sync_group(self, group: Dict[str, Any]) -> None:
        gid = group["group_id"]
        loop = asyncio.get_running_loop()
        if loop.time() - self._group_refresh.get(gid, -100) < 30:
            return
        self._group_refresh[gid] = loop.time()
        try:
            members = await self.ctx.api.call("adapter.snowluma.group.get_group_member_list", version="1", group_id=gid, no_cache=True)
            if not isinstance(members, list):
                raise RuntimeError("Group membership response unavailable")
            safe = [{"user_id": str(m["user_id"]), "nickname": str(m.get("nickname", ""))[:32], "card": str(m.get("card", ""))[:32]}
                    for m in members if isinstance(m, dict) and "user_id" in m]
            body = {"version": group["refresh_version"], "name": group["name"], "members": safe, "error": ""}
        except Exception as exc:
            body = {"version": group["refresh_version"], "name": group["name"], "members": [], "error": "群名单读取失败：" + type(exc).__name__}
        await self._http(f"/bridge/qq/v1/groups/{gid}", body, "PUT")

    async def _deliver(self, delivery: Dict[str, Any]) -> None:
        if self._journal is None:
            raise RuntimeError("Receipt journal not initialized")
        if delivery["group_id"] not in self._policy["groups"] or delivery["group_id"] not in {g['group_id'] for g in self._available_groups} or delivery["channel"] == "private" and not self._policy["allow_private"]:
            return
        # Persist before the network side effect. A lost begin response is never
        # authorization to send; the durable started record becomes uncertain.
        await asyncio.to_thread(self._journal.started, delivery)
        await self._http(f"/bridge/qq/v1/deliveries/{delivery['id']}/begin", {"attempt_id": delivery["attempt_id"]})
        receipt = {"attempt_id": delivery["attempt_id"], "state": "uncertain", "external_id": "", "detail": ""}
        try:
            mention = False
            note = ""
            if delivery["recipient"] and delivery["channel"] == "group":
                try:
                    info = await self.ctx.api.call("adapter.snowluma.group.get_group_member_info", version="1", group_id=delivery["group_id"], user_id=delivery["recipient"], no_cache=True)
                    mention = isinstance(info, dict) and str(info.get("user_id")) == delivery["recipient"] and str(info.get("group_id")) == delivery["group_id"]
                    if not mention: note = "接收人群身份未确认，改为普通播报"
                except Exception:
                    note = "接收人群身份查询失败，改为普通播报"
            params = {"message_type": delivery["channel"], "message": message_segments(delivery, mention)}
            params["group_id" if delivery["channel"] == "group" else "user_id"] = delivery["group_id"] if delivery["channel"] == "group" else delivery["recipient"]
            # SnowLuma's adapter validates the real group message sequence.
            data = await self._qq("send_msg", params)
            external_id = str(data.get("message_id", "")) if isinstance(data, dict) else ""
            if not external_id or external_id == "0":
                raise RuntimeError("No reliable message receipt")
            receipt.update(state="confirmed", external_id=external_id, detail=note)
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            receipt["detail"] = "投递未确认：" + type(exc).__name__ + "；没有自动重发"
        await asyncio.to_thread(self._journal.completed, delivery["id"], receipt)
        await self._flush_receipts()

    async def _run(self) -> None:
        delay = 3
        while True:
            self._wake.clear()
            has_deliveries = False
            try:
                # Explicitly bind to the SnowLuma account, even if another QQ
                # adapter is also installed in MaiBot.
                login = await self._qq("get_login_info", {})
                if not isinstance(login, dict) or str(login.get("user_id")) != self._bot_id:
                    raise RuntimeError("SnowLuma bot account does not match configured account")
                await self._flush_receipts()
                async with self._policy_lock:
                    await self._refresh_groups()
                    if asyncio.get_running_loop().time() - self._catalog_at >= 30:
                        await self._sync_channels()
                response = await self._http("/bridge/qq/v1/poll", {"bot_id": self._bot_id, "adapter": "snowluma"})
                self._sending_enabled = response["sending_enabled"]
                self._status = "已连接 · " + ("允许发送" if self._sending_enabled else "仅预览，不发送消息")
                if response["enabled"]:
                    for group in response["groups"]:
                        if group["refresh_version"] != group["refreshed_version"]:
                            await self._sync_group(group)
                    for delivery in response["deliveries"]:
                        async with self._policy_lock:
                            await self._deliver(delivery)
                    has_deliveries = bool(response["deliveries"])
                delay = 3
            except asyncio.CancelledError:
                raise
            except Exception as exc:
                # Error bodies can contain service credentials; log categories
                # and never dump complete requests or adapter exceptions.
                self.ctx.logger.warning("AE Orbit connector cycle failed: %s", type(exc).__name__)
                self._status = "连接待恢复：" + type(exc).__name__
                delay = min(delay * 2, 60)
            # Drain a backlog immediately; distinct queries never wait for an
            # application-level cooldown or a fixed gap between sends.
            if has_deliveries:
                continue
            try:
                await asyncio.wait_for(self._wake.wait(), timeout=delay)
            except asyncio.TimeoutError:
                pass


def create_plugin() -> AEOrbitPlugin:
    return AEOrbitPlugin()
