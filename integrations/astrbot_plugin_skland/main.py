"""森空岛签到：群聊证明身份，maa.yumi233.cn 网页提交凭证。"""
import asyncio
import json
import secrets
import time
from pathlib import Path
from astrbot.api import logger
from astrbot.api.event import AstrMessageEvent, MessageChain, filter
from astrbot.api.star import Context, Star, register
from .skland_api import SklandAPI
from .store import Store
from .service import Service, identity, now, DONE
from .web import BindingWeb


@register('astrbot_plugin_skland', 'Azincc / yumi', '森空岛群聊网页认证与可靠签到', '2.0.2')
class SklandPlugin(Star):
    def __init__(self, context: Context, config):
        super().__init__(context)
        self.config = config
        self.api = SklandAPI()
        self.store = None
        self.service = None
        self.web = None
        self.task = None
        self.logout_requests = {}

    async def initialize(self):
        from astrbot.core.utils.astrbot_path import get_astrbot_data_path
        root = Path(get_astrbot_data_path()) / 'plugin_data' / 'astrbot_plugin_skland'
        self.store = Store(root, Path(get_astrbot_data_path()) / 'config' / 'skland_credentials.key')
        self.service = Service(self.store, self.api, self.config)
        await self._migrate(root)
        self.web = BindingWeb(self.service, self.config.get('binding_public_url', 'https://maa.yumi233.cn'))
        await self.web.start_server(port=int(self.config.get('binding_port', 6190)))
        self.task = asyncio.create_task(self._loop())
        logger.info('森空岛 v2.0.2：网页认证入口与恢复扫描已启动')

    async def _migrate(self, root):
        if self.store.db.execute("SELECT 1 FROM meta WHERE key='migrated'").fetchone():
            # A previous process may have committed migration before clearing legacy KV.
            await self.put_kv_data('users', {})
            await self.put_kv_data('groups', {})
            return
        users = await self.get_kv_data('users', {})
        groups = await self.get_kv_data('groups', {})
        if users:
            backup = root / 'legacy-migration.encrypted'
            if not backup.exists():
                import os
                fd = os.open(backup, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                with os.fdopen(fd, 'w') as f:
                    f.write(self.store.seal({'users': users, 'groups': groups}))
            for sender, data in users.items():
                origin = data.get('umo', '')
                instance = origin.split(':', 1)[0] if ':' in origin else 'legacy-unresolved'
                uid = json.dumps([data.get('platform_name', 'aiocqhttp'), instance, str(sender)], separators=(',', ':'))
                if not self.store.account(uid):
                    self.store.put(uid, {'token': data.get('token', ''), 'nickname': data.get('nickname', ''),
                        'sender': str(sender), 'origin': origin, 'group_id': '', 'auto': True, 'notify': 'off',
                        'auth_status': 'active' if data.get('token') else 'expired', 'bound_at': data.get('bound_at', ''),
                        'legacy_last_sign': data.get('last_sign', {})})
        self.store.db.execute("INSERT OR REPLACE INTO meta VALUES('migrated','1')")
        await self.put_kv_data('users', {})
        await self.put_kv_data('groups', {})

    async def terminate(self):
        if self.task:
            self.task.cancel()
            try:
                await self.task
            except asyncio.CancelledError:
                pass
        if self.web:
            await self.web.close()
        if self.service:
            await self.service.close()
        await self.api.close()
        if self.store:
            self.store.db.close()

    async def _loop(self):
        while True:
            try:
                self.store.cleanup()
                for uid, data, results in await self.service.due():
                    failed = any(r['state'] not in DONE for r in results)
                    mode = data.get('notify', 'off')
                    if mode == 'off' or (mode == 'failure' and not failed):
                        continue
                    key = 'notice:' + uid + ':' + now().date().isoformat()
                    summary = ('部分签到未完成，请查询 /skd；认证失效时用 /skdlogin 更新。' if failed else '今日签到已完成，可发送 /skd 查询。')
                    # Notifications are independent of attendance. Never retry attendance because send failed.
                    if data.get('group_id') and self.store.rate(key, 1, 86400):
                        try:
                            import astrbot.api.message_components as Comp
                            chain = MessageChain([Comp.At(qq=data['sender']), Comp.Plain(' 森空岛：' + summary)])
                            await self.context.send_message(data['origin'], chain)
                        except Exception:
                            logger.warning('森空岛通知发送失败（签到结果已保存）')
            except asyncio.CancelledError:
                raise
            except Exception:
                logger.warning('森空岛恢复扫描暂未完成，将在下轮继续')
            await asyncio.sleep(60)

    @filter.command('skdhelp')
    async def skdhelp(self, event: AstrMessageEvent):
        yield event.plain_result('森空岛签到\n/skdlogin 网页绑定或更新凭证\n/skdconfirm 确认码：确认网页归属\n/skdsign 本人签到\n/skd 查询本人今日状态（不执行签到）\n/skdlogout 解绑\n/skdnotify off|failure|daily 通知设置\n/skdusers 人数与名额\n/skdgroup 本群授权摘要；/skdshare on|off 个人公开设置\n请勿在群内发送 Token。无需私聊机器人。')

    @filter.command('skdlogin')
    async def skdlogin(self, event: AstrMessageEvent, token: str = ''):
        if token:
            yield event.plain_result('请勿在聊天中发送 Token。请撤回刚才的消息，并按官方支持的方式处理已泄露凭证。重新发送 /skdlogin 获取网页入口。')
            return
        try:
            rid = self.service.request(event)
            url = self.config.get('binding_public_url', 'https://maa.yumi233.cn').rstrip('/')
            yield event.plain_result(f'打开网页完成森空岛认证（10 分钟有效）：\n{url}/skland?r={rid}\n按网页提示，在本会话发送确认命令。Token 只填网页。只确认你自己网页上的命令。')
        except ValueError as exc:
            yield event.plain_result(str(exc))

    @filter.command('skdconfirm')
    async def skdconfirm(self, event: AstrMessageEvent, code: str = ''):
        try:
            self.service.confirm(event, code)
            yield event.plain_result('QQ 身份已确认，请返回你刚才的网页继续认证。')
        except ValueError as exc:
            yield event.plain_result(str(exc))

    @filter.command('skdsign')
    async def skdsign(self, event: AstrMessageEvent):
        try:
            results = await self.service.sign(identity(event))
            good = sum(r['state'] in DONE for r in results)
            yield event.plain_result(f'森空岛今日签到：{good}/{len(results)} 项完成。详细结果可在本人的认证网页查看。')
        except ValueError as exc:
            yield event.plain_result(str(exc))
        except Exception:
            yield event.plain_result('签到暂未完成，请稍后查询 /skd。')

    @filter.command('skd')
    async def skd(self, event: AstrMessageEvent):
        uid = identity(event)
        account = self.store.account(uid)
        if not account:
            yield event.plain_result('尚未绑定，请发送 /skdlogin。')
            return
        results = self.store.results(uid, now().date().isoformat())
        good = sum(r['state'] in DONE for r in results)
        auth = '认证已失效，请用 /skdlogin 更新。' if account[1].get('auth_status') == 'expired' else ''
        yield event.plain_result(f'森空岛 {now().date().isoformat()}：' + (f'{good}/{len(results)} 项完成。' if results else '今日尚无签到结果，可发送 /skdsign。') + auth)

    @filter.command('skdlogout')
    async def skdlogout(self, event: AstrMessageEvent, action: str = '', code: str = ''):
        uid = identity(event)
        account = self.store.account(uid)
        if not account:
            yield event.plain_result('尚未绑定森空岛账号。')
            return
        if not self.store.rate('logout:' + uid, 6, 180):
            yield event.plain_result('操作频繁，请稍后重试。')
            return
        if action == 'confirm':
            entry = self.logout_requests.get(uid)
            if not entry or entry['expires'] < time.time() or entry['origin'] != event.unified_msg_origin or not secrets.compare_digest(entry['code'], code):
                yield event.plain_result('解绑确认无效或已过期，请重新发送 /skdlogout。')
                return
            async with self.service.lock(uid):
                ok = self.store.remove(uid, entry['version'])
                self.logout_requests.pop(uid, None)
            yield event.plain_result('已停止自动签到并删除绑定凭证。' if ok else '绑定已变化，请重新发起解绑。')
            return
        confirm = secrets.token_hex(6)
        self.logout_requests = {k: v for k, v in self.logout_requests.items() if v['expires'] > time.time()}
        self.logout_requests[uid] = {'code': confirm, 'version': account[0], 'origin': event.unified_msg_origin, 'expires': time.time() + 180}
        yield event.plain_result(f'解绑后停止签到并删除凭证。确认请在 3 分钟内发送：\n/skdlogout confirm {confirm}')

    @filter.command('skdnotify')
    async def skdnotify(self, event: AstrMessageEvent, mode: str = ''):
        uid = identity(event)
        async with self.service.lock(uid):
            account = self.store.account(uid)
            if not account:
                yield event.plain_result('请先发送 /skdlogin 绑定。')
                return
            version, data = account
            if mode not in ('off', 'failure', 'daily'):
                yield event.plain_result('通知模式：' + data.get('notify', 'off') + '\n/skdnotify off 关闭\n/skdnotify failure 失败提醒\n/skdnotify daily 每日摘要')
                return
            if mode != 'off' and not event.get_group_id():
                yield event.plain_result('请在希望接收通知的群内设置，或使用 off 关闭通知。')
                return
            data.update(notify=mode, origin=event.unified_msg_origin, group_id=str(event.get_group_id() or ''), sender=str(event.get_sender_id()))
            self.store.update(uid, version, data)
        yield event.plain_result('通知设置已更新；群通知仅显示签到摘要。')

    @filter.command('skdshare')
    async def skdshare(self, event: AstrMessageEvent, mode: str = ''):
        if not event.get_group_id():
            yield event.plain_result('请在需要公开状态的群内操作。')
            return
        uid = identity(event)
        async with self.service.lock(uid):
            account = self.store.account(uid)
            if not account:
                yield event.plain_result('请先绑定账号。')
                return
            version, data = account
            groups = data.get('public_groups', [])
            group = event.unified_msg_origin
            if mode not in ('on', 'off'):
                yield event.plain_result('本群公开状态：' + ('开启' if group in groups else '关闭') + '\n/skdshare on|off 选择是否纳入本群签到摘要。')
                return
            if mode == 'on' and group not in groups:
                groups.append(group)
            if mode == 'off':
                groups = [g for g in groups if g != group]
            data['public_groups'] = groups
            self.store.update(uid, version, data)
        yield event.plain_result('已更新你在本群的签到摘要授权。')

    @filter.command('skdgroup')
    async def skdgroup(self, event: AstrMessageEvent, mode: str = ''):
        if not event.get_group_id():
            yield event.plain_result('请在群内使用。')
            return
        key = 'group-public:' + event.unified_msg_origin
        if mode in ('on', 'off'):
            role = getattr(getattr(event.message_obj, 'sender', None), 'role', '')
            if not event.is_admin() and role not in ('admin', 'owner'):
                yield event.plain_result('仅群管理员或机器人管理员可以设置本群摘要。')
                return
            self.store.db.execute('INSERT OR REPLACE INTO meta VALUES(?,?)', (key, mode))
        enabled = self.store.db.execute('SELECT value FROM meta WHERE key=?', (key,)).fetchone()
        if not enabled or enabled[0] != 'on':
            yield event.plain_result('本群签到摘要未开启。管理员可发送 /skdgroup on；成员还需自行 /skdshare on 授权。')
            return
        if not self.store.rate('group-query:' + event.unified_msg_origin, 6, 60):
            yield event.plain_result('查询频繁，请稍后重试。')
            return
        lines = ['森空岛今日群摘要（只读缓存）']
        for uid, _, data in self.store.accounts():
            if event.unified_msg_origin not in data.get('public_groups', []):
                continue
            rows = self.store.results(uid, now().date().isoformat())
            good = sum(r['state'] in DONE for r in rows)
            sender = str(data.get('sender', ''))
            label = sender[:3] + '****' + sender[-2:]
            lines.append(label + ('：' + str(good) + '/' + str(len(rows)) + ' 项完成' if rows else '：尚无今日记录'))
        yield event.plain_result('\n'.join(lines) if len(lines) > 1 else '本群暂无授权公开状态的用户，可发送 /skdshare on 加入。')

    @filter.command('skdusers')
    async def skdusers(self, event: AstrMessageEvent):
        accounts = self.store.accounts()
        today = now().date().isoformat()
        complete = sum(bool(rows := self.store.results(uid, today)) and all(r['state'] in DONE for r in rows) for uid, _, _ in accounts)
        maximum = int(self.config.get('max_users', 10))
        yield event.plain_result(f'森空岛：已绑定 {len(accounts)} 人，今日全部完成 {complete} 人。' + (f'剩余名额 {max(0, maximum-len(accounts))}。' if maximum else '名额不限。'))
