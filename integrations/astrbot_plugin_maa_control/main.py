"""Explicit, account-scoped MAA commands. No model or shell execution."""

import json
import os
import re
import ssl
from datetime import datetime, timezone
from pathlib import Path

import aiohttp
from astrbot.api.event import AstrMessageEvent, filter
from astrbot.api.star import Context, Star


ACTIONS = {"帮助": "help", "help": "help", "状态": "status", "开始": "start",
           "启动": "start", "停止": "stop", "汇报": "report", "今日": "today", "计划": "schedules"}


def select_route(config, umo, action, target="", job_id=""):
    if not isinstance(config, dict) or not isinstance(config.get("routes"), list):
        raise ValueError("群控配置无效，请联系管理员。")
    routes = [r for r in config["routes"] if isinstance(r, dict) and r.get("umo") == umo]
    if not routes:
        raise ValueError("当前会话尚未绑定 MAA 群控。")
    matches = [r for r in routes if target in (r.get("account_id"), r.get("label"))]
    if len(matches) == 1:
        return matches[0], matches[0]["account_id"], job_id
    if len(routes) != 1:
        raise ValueError("此群有多个账号，请指定账号备注或联系管理员。")
    if action == "report" and re.fullmatch(r"[a-f0-9]{64}", target):
        return routes[0], "", target
    return routes[0], target, job_id


async def request_command(config, payload, route):
    base = config.get("base_url", "").rstrip("/")
    if not base.startswith("https://"):
        raise ValueError("群控服务必须使用 HTTPS，并配置可信 CA。")
    tls = ssl.create_default_context(cafile=config.get("ca_file") or None)
    timeout = aiohttp.ClientTimeout(total=20)
    async with aiohttp.ClientSession(timeout=timeout) as session:
        async with session.post(base + "/api/v1/bot/commands", json=payload,
                                headers={"Authorization": "Bearer " + route["token"]},
                                ssl=tls, allow_redirects=False) as response:
            raw = await response.content.read(65537)
            if len(raw) > 65536:
                raise ValueError("响应过大，结果未确认；请查看网页状态。")
            data = json.loads(raw)
            if response.status != 200:
                return str(data.get("message", "请求被拒绝，请查看网页状态。"))[:600]
            return str(data.get("message", "请求未确认，请查看网页状态。"))[:4000]


class MAAControl(Star):
    def __init__(self, context: Context):
        super().__init__(context)

    @filter.command("maa")
    async def maa(self, event: AstrMessageEvent, action: str = "帮助",
                  target: str = "", job_id: str = ""):
        """MAA 状态、开始、停止、汇报、今日、计划；成员权限由控制中心核对。"""
        event.stop_event()
        command = ACTIONS.get(action)
        if command is None:
            yield event.plain_result("支持：/maa 帮助、状态、开始、停止、汇报、今日、计划。")
            return
        try:
            path = Path(os.environ.get("MAACC_BOT_CONFIG", "data/plugin_data/maa_control/connection.json"))
            if path.stat().st_size > 65536:
                raise ValueError("群控配置过大，请联系管理员。")
            config = json.loads(path.read_text(encoding="utf-8"))
            route, selected, report_id = select_route(config, event.unified_msg_origin,
                                                       command, target, job_id)
            message_id = str(getattr(event.message_obj, "message_id", "") or "")
            if not message_id:
                raise ValueError("平台未提供消息 ID，不能安全去重，请稍后重新发送指令。")
            payload = {"umo": event.unified_msg_origin, "sender_id": event.get_sender_id(),
                       "message_id": message_id, "sent_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
                       "command": command, "target": selected, "job_id": report_id}
            message = await request_command(config, payload, route)
        except ValueError as error:
            message = str(error) if len(str(error)) < 200 else "群控配置或响应无效，请联系管理员。"
        except (OSError, aiohttp.ClientError, TimeoutError):
            message = "请求结果未确认。请先用 /maa 状态 核实，勿连续重发开始或停止指令。"
        except (KeyError, TypeError, AttributeError):
            message = "群控配置或响应缺少必要字段，请联系管理员。"
        yield event.plain_result(message)
