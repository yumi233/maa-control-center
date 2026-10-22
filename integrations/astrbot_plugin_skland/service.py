"""QQ-bound browser sessions and recoverable role-level attendance."""
import asyncio
import copy
import json
import logging
import secrets
import time
from datetime import datetime
from zoneinfo import ZoneInfo
from .store import digest
from .credentials import parse_credential

TZ = ZoneInfo('Asia/Shanghai')
DONE = {'success', 'already'}
logger = logging.getLogger(__name__)


def now():
    return datetime.now(TZ)


def identity(event):
    return json.dumps([event.get_platform_name(), event.get_platform_id(), str(event.get_sender_id())], separators=(',', ':'))


def role_specs(bindings):
    out = []
    for binding in bindings:
        if binding.app_code == 'arknights':
            if binding.uid and binding.game_id is not None:
                out.append((f'arknights:{binding.game_id}:{binding.uid}', binding, None))
        elif binding.app_code == 'endfield':
            for role in binding.roles:
                if role.get('roleId') and role.get('serverId') is not None:
                    out.append((f'endfield:{role["serverId"]}:{role["roleId"]}', binding, role))
    return out


class Service:
    def __init__(self, store, api, config):
        self.store, self.api, self.config = store, api, config
        self.locks = {}
        self.tasks = {}
        self.api_lock = asyncio.Lock()  # global upstream pacing, including bind verification

    def lock(self, uid):
        return self.locks.setdefault(uid, asyncio.Lock())

    def request(self, event):
        self.store.cleanup()
        uid = identity(event)
        if not self.store.rate('request:' + uid, 3, 60):
            raise ValueError('操作频繁，请稍后重试')
        # Reissuing a request deliberately invalidates older candidate sessions.
        self.store.db.execute('DELETE FROM sessions WHERE request_id IN (SELECT id FROM requests WHERE identity=?)', (uid,))
        self.store.db.execute('DELETE FROM requests WHERE identity=?', (uid,))
        rid = secrets.token_urlsafe(24)
        account = self.store.account(uid)
        self.store.db.execute('INSERT INTO requests VALUES(?,?,?,?,?,?,?,?)',
            (rid, uid, event.unified_msg_origin, str(event.get_group_id() or ''), str(event.get_sender_id()),
             account[0] if account else '', time.time() + 600, 'open'))
        return rid

    def browser(self, rid, existing=''):
        self.store.cleanup()
        req = self.store.db.execute('SELECT * FROM requests WHERE id=? AND status=? AND expires>?', (rid, 'open', time.time())).fetchone()
        if not req:
            raise ValueError('绑定请求已失效，请回到群内重新发送 /skdlogin')
        old = self.store.db.execute('SELECT * FROM sessions WHERE id=? AND request_id=? AND expires>?', (digest(existing), rid, time.time())).fetchone() if existing else None
        if old:
            return existing, old['csrf']
        if self.store.db.execute('SELECT COUNT(*) FROM sessions WHERE request_id=?', (rid,)).fetchone()[0] >= 3:
            raise ValueError('此请求的浏览器会话已满，请重新发送 /skdlogin')
        session, csrf = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
        challenge = ''.join(secrets.choice('23456789ABCDEFGHJKLMNPQRSTUVWXYZ') for _ in range(12))
        self.store.db.execute('INSERT INTO sessions VALUES(?,?,?,?,?,?,?)',
            (digest(session), rid, digest(challenge), csrf, min(req['expires'], time.time() + 180), 0, self.store.seal({'challenge': challenge})))
        return session, csrf

    def session(self, cookie):
        row = self.store.db.execute('''SELECT s.*,r.identity,r.origin,r.sender,r.version,r.status,r.group_id
          FROM sessions s JOIN requests r ON r.id=s.request_id
          WHERE s.id=? AND s.expires>? AND r.expires>? AND r.status IN ('open','complete')''',
          (digest(cookie), time.time(), time.time())).fetchone()
        if not row:
            raise ValueError('会话已失效，请重新从群内绑定入口进入')
        return row

    def confirm(self, event, code):
        uid = identity(event)
        if not self.store.rate('confirm:' + uid, 5, 180):
            raise ValueError('确认尝试过多，请稍后重新发起绑定')
        row = self.store.db.execute('''SELECT s.id,s.confirmed,s.expires,r.* FROM sessions s JOIN requests r ON r.id=s.request_id
          WHERE s.challenge=? AND s.expires>? AND r.expires>? AND r.status='open' ''', (digest(code.upper().strip()), time.time(), time.time())).fetchone()
        if not row or row['identity'] != uid or row['origin'] != event.unified_msg_origin:
            raise ValueError('确认码无效、已过期或不是你在本群发起的请求')
        # Request id is selected explicitly to avoid duplicate column-name ambiguity.
        sid = row[0]
        rid = self.store.db.execute('SELECT request_id FROM sessions WHERE id=?', (sid,)).fetchone()[0]
        winner = self.store.db.execute('SELECT id FROM sessions WHERE request_id=? AND confirmed=1', (rid,)).fetchone()
        if winner and winner['id'] != sid:
            raise ValueError('该请求已在另一浏览器确认，请重新发起绑定')
        self.store.db.execute('UPDATE sessions SET confirmed=1,expires=(SELECT expires FROM requests WHERE id=?) WHERE id=?', (rid, sid))
        self.store.db.execute('DELETE FROM sessions WHERE request_id=? AND id<>?', (rid, sid))

    def public(self, session):
        pending = self.store.unseal(session['pending']) if session['pending'] else {}
        data = {'confirmed': bool(session['confirmed']), 'csrf': session['csrf'], 'expires_at': session['expires'],
                'complete': session['status'] == 'complete', 'request_id': session['request_id'],
                'signing': session['identity'] in self.tasks,
                'schedule': {'enabled': bool(self.config.get('auto_sign_enabled', True)),
                             'time': f"{int(self.config.get('auto_sign_hour', 9)):02d}:{int(self.config.get('auto_sign_minute', 0)):02d}"}}
        if session['confirmed']:
            account = self.store.account(session['identity'])
            data['auto_sign'] = account[1].get('auto', True) if account else True
            data['binding_conflict'] = bool(account and (account[1].get('binding_conflict') or self.store.conflicted(session['identity'])))
            data['binding_validation_pending'] = bool(account and account[1].get('dedupe_pending'))
            sender = session['sender']
            data['qq'] = sender[:3] + '****' + sender[-2:]
            if 'token' in pending:
                data['roles'] = pending['roles']
                data['credential_source'] = pending.get('credential_source', 'Token 文本')
                data['nickname'] = pending['nickname']
                if account:
                    data['binding_mode'] = 'refresh' if set(self.store.claims(account[1])) & set(pending.get('claim_keys', [])) else 'replace'
            if session['status'] == 'complete':
                data['results'] = self.store.results(session['identity'], now().date().isoformat())
        else:
            data['command'] = '/skdconfirm ' + pending.get('challenge', '')
        return data

    async def verify(self, cookie, token):
        session = self.session(cookie)
        if not session['confirmed'] or session['status'] != 'open':
            raise ValueError('请先在原群发送网页上的确认命令')
        if not self.store.rate('verify:' + session['identity'], 5, 600):
            raise ValueError('凭证验证过于频繁，请稍后重试')
        token, credential_source = parse_credential(token)
        stage = 'authorization'
        try:
            async with self.api_lock:
                grant = await self.api.get_authorization(token)
                stage = 'credential'
                cred = await self.api.get_credential(grant)
                stage = 'binding'
                bindings = await self.api.get_binding_list(cred)
                specs = role_specs(bindings)
        except Exception as exc:
            stage = getattr(exc, 'stage', '') or stage
            kind = getattr(exc, 'kind', 'unexpected')
            code = getattr(exc, 'code', None)
            status = getattr(exc, 'http_status', None)
            reference = secrets.token_hex(4)
            # Only known stage labels and numeric status are logged; never response bodies.
            logger.warning('Skland authentication failed ref=%s stage=%s kind=%s http=%s code=%s type=%s',
                           reference, stage, kind, status, code, type(exc).__name__)
            label = {'device': '设备指纹', 'authorization': '鹰角授权', 'credential': '森空岛凭证交换', 'binding': '游戏角色查询'}.get(stage, '上游接口')
            reason = {'network': '连接失败', 'server': '暂不可用', 'rate': '请求被限流，请稍后重试',
                      'auth': '登录状态失效，请在官方重新登录后获取凭证', 'device': '生成失败'}.get(kind, '请求未通过')
            details = (f'，HTTP {status}' if status else '') + (f'，业务码 {code}' if code is not None else '')
            raise ValueError(f'{label}{reason}（编号 {reference}{details}）。请勿在聊天中发送 Token。') from None
        if not specs:
            raise ValueError('认证通过，但没有可签到角色；请先在森空岛绑定游戏角色')
        keys = sorted({key for key, _, _ in specs})
        claims = self.store.make_claims(token, keys, getattr(cred, 'user_id', ''))
        self.store.assert_available(session['identity'], claims)
        pending = {'token': token, 'claim_keys': claims, 'role_keys': keys, 'credential_source': credential_source, 'nickname': bindings[0].nickname, 'roles': [f'{b.game_name} · {r.get("nickname", b.nickname) if r else b.nickname}' for _, b, r in specs]}
        # Network calls yielded: recheck session authorization and account generation.
        fresh = self.session(cookie)
        if not fresh['confirmed'] or fresh['status'] != 'open':
            raise ValueError('请求状态已变化，请重新认证')
        self.store.db.execute('UPDATE sessions SET pending=? WHERE id=?', (self.store.seal(pending), fresh['id']))
        return pending['roles']

    async def commit(self, cookie, auto=None):
        session = self.session(cookie)
        uid = session['identity']
        async with self.lock(uid):
            session = self.session(cookie)
            if session['status'] == 'complete':
                return
            pending = self.store.unseal(session['pending']) if session['pending'] else {}
            if not session['confirmed'] or 'token' not in pending:
                raise ValueError('请先验证凭证并确认角色')
            if not pending.get('claim_keys') or not pending.get('role_keys'):
                raise ValueError('绑定页面已更新，请重新验证凭证后再确认，现有绑定保持不变')
            previous = self.store.account(uid)
            if auto is None:
                auto = previous[1].get('auto', True) if previous else True
            if not isinstance(auto, bool):
                raise ValueError('自动签到选项必须为开启或关闭')
            data = {'token': pending['token'], 'nickname': pending['nickname'], 'origin': session['origin'],
                    'group_id': session['group_id'], 'sender': session['sender'], 'auto': bool(auto),
                    'notify': 'off', 'auth_status': 'active', 'roles': pending['roles'], 'role_keys': pending['role_keys'],
                    'claim_keys': pending['claim_keys'], 'bound_at': now().isoformat()}
            if previous:
                data['notify'] = previous[1].get('notify', 'off')
                data['public_groups'] = previous[1].get('public_groups', [])
            def finalize():
                if previous and set(self.store.claims(previous[1])) & set(pending['claim_keys']):
                    placeholders = ','.join('?' for _ in pending['role_keys'])
                    self.store.db.execute(f'DELETE FROM results WHERE identity=? AND role NOT IN ({placeholders})', (uid, *pending['role_keys']))
                else:
                    self.store.db.execute('DELETE FROM results WHERE identity=?', (uid,))
                self.store.db.execute('UPDATE requests SET status=? WHERE id=?', ('complete', session['request_id']))
                self.store.db.execute('UPDATE sessions SET pending=NULL WHERE id=?', (session['id'],))
            self.store.put(uid, data, expected=session['version'], max_users=int(self.config.get('max_users', 10)), finalize=finalize)

    def submit_sign(self, uid):
        if uid in self.tasks:
            return
        async def work():
            try:
                await self.sign(uid)
            except (Exception, asyncio.CancelledError):
                pass  # Role state is persisted; no raw upstream exception is exposed.
            finally:
                self.tasks.pop(uid, None)
        self.tasks[uid] = asyncio.create_task(work())

    async def close(self):
        pending = list(self.tasks.values())
        for task in pending:
            task.cancel()
        if pending:
            await asyncio.gather(*pending, return_exceptions=True)

    async def sign(self, uid):
        async with self.lock(uid):
            current = self.store.account(uid)
            if not current:
                raise ValueError('尚未绑定，请发送 /skdlogin')
            version, data = current
            if data.get('dedupe_pending'):
                raise ValueError('旧绑定尚未完成账号查重，请先发送 /skdlogin 重新验证')
            if data.get('binding_conflict') or self.store.conflicted(uid):
                raise ValueError('此游戏账号存在重复绑定，已暂停执行；请先保留一个绑定并解绑重复项')
            day = now().date().isoformat()
            if data.get('auth_status') == 'expired':
                raise ValueError('认证已失效，请发送 /skdlogin 更新认证')
            if not self.store.rate('sign:' + uid, 3, 60):
                raise ValueError('签到过于频繁，请稍后查询 /skd')
            # Skip all upstream traffic when today's discovered roles are complete.
            previous = self.store.results(uid, day)
            if data.get('role_keys') and all(any(r['role'] == k and r['state'] in DONE for r in previous) for k in data['role_keys']):
                return previous
            async with self.api_lock:
                try:
                    grant = await self.api.get_authorization(data['token'])
                    cred = await self.api.get_credential(grant)
                    specs = role_specs(await self.api.get_binding_list(cred))
                except Exception as exc:
                    expired = getattr(exc, 'kind', '') == 'auth'
                    if expired:
                        data['auth_status'] = 'expired'
                        self.store.update(uid, version, data)
                    self._failure(uid, '__account__', day, 'needs_auth' if expired else 'retry', '认证失效，请重新绑定' if expired else '上游连接或认证流程暂不可用', delay=getattr(exc, 'retry_after', 0))
                    return self.store.results(uid, day)
                self.store.db.execute('DELETE FROM results WHERE identity=? AND day=? AND role=?', (uid, day, '__account__'))
                role_keys = sorted({key for key, _, _ in specs})
                claim_keys = self.store.make_claims(data.get('token', ''), role_keys, getattr(cred, 'user_id', ''))
                try:
                    self.store.assert_available(uid, claim_keys)
                except ValueError:
                    data['binding_conflict'] = True
                    self.store.update(uid, version, data)
                    self._failure(uid, '__account__', day, 'duplicate', '游戏账号重复绑定，已暂停；请先解绑重复项，再重新验证', delay=3600)
                    return self.store.results(uid, day)
                if data.get('role_keys') != role_keys or data.get('claim_keys') != claim_keys:
                    data['role_keys'] = role_keys
                    data['claim_keys'] = claim_keys
                    self.store.update(uid, version, data)
                if not specs:
                    self.store.record(uid, '__account__', day, 'no_roles', '没有可签到角色')
                for key, binding, role in specs:
                    old = next((r for r in self.store.results(uid, day) if r['role'] == key), None)
                    if old and old['state'] in DONE:
                        continue
                    label = f'{binding.game_name} · {role.get("nickname", binding.nickname) if role else binding.nickname}'
                    attempts = old['attempts'] if old else 0
                    try:
                        signed = await self.api.get_attendance_status(cred, binding, role)
                    except Exception as exc:
                        signed = None
                        reference = self._attendance_error(exc)
                    else:
                        reference = None
                    if signed is True:
                        self.store.record(uid, key, day, 'already', label + '：已核实今日已签到', attempts)
                        continue
                    if signed is not False:
                        message = label + '：签到状态暂无法核实，未重复提交；请稍后查询'
                        if reference:
                            message += f'（编号 {reference}）'
                        self._failure(uid, key, day, 'unknown', message, max(1, attempts))
                        continue
                    if old and (old['attempts'] >= 4 or old['next_at'] > time.time()):
                        continue
                    attempt = (old['attempts'] if old else 0) + 1
                    self.store.record(uid, key, day, 'unknown', '请求处理中，异常中断后将核对或重试', attempt, time.time() + 60)
                    try:
                        if role is None:
                            result = await self.api.sign_arknights(cred, binding)
                        else:
                            single = copy.copy(binding)
                            single.roles = [role]
                            result = (await self.api.sign_endfield(cred, single))[0]
                        already = not result.success and self.api._is_signed_today(result)
                        if result.success or already:
                            state = 'success' if result.success else 'already'
                            text = f'{result.game} · {result.nickname}：' + ('签到成功' if result.success else '今日已签到')
                            if result.awards:
                                text += '，' + '、'.join(result.awards)
                            self.store.record(uid, key, day, state, text, attempt)
                        else:
                            if '用户未登录' in result.error or '登录已过期' in result.error:
                                data['auth_status'] = 'expired'
                                self.store.update(uid, version, data)
                                self.store.record(uid, key, day, 'needs_auth', '认证失效，请重新绑定', attempt)
                                break
                            self._failure(uid, key, day, 'retry', f'{result.game}：签到未完成，请稍后重试', attempt)
                    except Exception as exc:
                        reference = self._attendance_error(exc)
                        wait = getattr(exc, 'retry_after', 0)
                        # The write may have succeeded before a timeout or parsing error.
                        # Reconcile with a read; never repeat the write here.
                        try:
                            confirmed = await self.api.get_attendance_status(cred, binding, role)
                        except Exception:
                            confirmed = None
                        if confirmed is True:
                            self.store.record(uid, key, day, 'already', label + '：已核实今日已签到', attempt)
                        else:
                            self._failure(uid, key, day, 'unknown', label + f'：结果待核实，请稍后查询（编号 {reference}）', attempt, wait)
                    # Keep spacing across roles as well as accounts.
                    await asyncio.sleep(max(0, float(self.config.get('auto_sign_delay', 10))) * secrets.randbelow(1001) / 1000)
                return self.store.results(uid, day)

    def _attendance_error(self, exc):
        reference = secrets.token_hex(4)
        status, code = getattr(exc, 'http_status', None), getattr(exc, 'code', None)
        logger.warning('Skland attendance unconfirmed ref=%s type=%s http=%s code=%s',
                       reference, type(exc).__name__, status if isinstance(status, int) else None,
                       code if isinstance(code, int) else None)
        return reference

    def _failure(self, uid, role, day, state, message, attempt=None, delay=0):
        previous = next((r for r in self.store.results(uid, day) if r['role'] == role), None)
        attempt = attempt or ((previous['attempts'] if previous else 0) + 1)
        self.store.record(uid, role, day, state, message, attempt, time.time() + max(delay, min(3600, 60 * 2 ** (attempt - 1))))

    async def due(self):
        today = now().date().isoformat()
        clock = now().hour * 60 + now().minute
        target = int(self.config.get('auto_sign_hour', 9)) * 60 + int(self.config.get('auto_sign_minute', 0))
        if not self.config.get('auto_sign_enabled', True) or clock < target:
            return []
        out = []
        for uid, version, data in self.store.accounts():
            if not data.get('auto', True) or data.get('auth_status') == 'expired' or data.get('dedupe_pending') or data.get('binding_conflict') or self.store.conflicted(uid):
                continue
            rows = self.store.results(uid, today)
            account_failure = next((r for r in rows if r['role'] == '__account__'), None)
            if account_failure and (account_failure['state'] in {'needs_auth', 'no_roles', 'duplicate'} or account_failure['attempts'] >= 4 or account_failure['next_at'] > time.time()):
                continue
            if rows and (not data.get('role_keys') or all(any(r['role'] == key for r in rows) for key in data['role_keys'])) and all(r['state'] in DONE or r['state'] in {'needs_auth', 'no_roles'} or r['attempts'] >= 4 or r['next_at'] > time.time() for r in rows):
                continue
            results = await self.sign(uid)
            latest = self.store.account(uid)
            if latest and latest[0] == version:
                out.append((uid, data, results))
        return out
