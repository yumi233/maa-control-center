import asyncio
import importlib.util
import json
import pathlib
import sys
import tempfile
import time
import types
import unittest
from types import SimpleNamespace

ROOT = pathlib.Path(__file__).resolve().parents[1]
pkg = types.ModuleType('skland_test')
pkg.__path__ = [str(ROOT)]
sys.modules['skland_test'] = pkg
from skland_test.store import Store, digest
from skland_test.service import Service, identity, now
from skland_test.web import BindingWeb, COOKIE
from aiohttp.test_utils import TestClient, TestServer

class Event:
    def __init__(self, sender='123456789', group='777', instance='qq-main'):
        self.sender, self.group, self.instance = sender, group, instance
        self.unified_msg_origin = f'{instance}:GroupMessage:{group}'
    def get_platform_name(self): return 'aiocqhttp'
    def get_platform_id(self): return self.instance
    def get_sender_id(self): return self.sender
    def get_group_id(self): return self.group

class API:
    def __init__(self): self.calls = 0; self.enter = None; self.release = None; self.fail = False; self.signed = False
    async def get_authorization(self, token):
        self.last_token = token
        if self.enter:
            self.enter.set()
            await self.release.wait()
        if self.fail: raise RuntimeError('secret must never escape')
        return 'grant'
    async def get_credential(self, grant): return 'cred'
    async def get_binding_list(self, cred):
        return [SimpleNamespace(app_code='arknights', game_name='明日方舟',nickname='tester',game_id=1,uid='other' if self.last_token=='OTHER_TOKEN' else 'a',roles=[])]
    async def sign_arknights(self, cred, binding):
        self.calls += 1
        return SimpleNamespace(success=True,error='',game='明日方舟',nickname='tester',awards=[])
    async def get_attendance_status(self, cred, binding, role=None): return self.signed
    def _is_signed_today(self, result): return False

class SecurityTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.store = Store(self.tmp.name)
        self.api = API()
        self.service = Service(self.store,self.api,{'max_users':1,'auto_sign_delay':0,'auto_sign_enabled':True,'auto_sign_hour':0})
        self.event = Event()
    def tearDown(self):
        self.store.db.close()
        self.tmp.cleanup()
    def candidate(self,event=None):
        rid=self.service.request(event or self.event)
        cookie,_=self.service.browser(rid)
        public=self.service.public(self.service.session(cookie))
        return rid,cookie,public['command'].split()[1]
    async def bound(self,event=None):
        event=event or self.event
        rid,cookie,code=self.candidate(event)
        self.service.confirm(event,code)
        await self.service.verify(cookie,'SUPER_PRIVATE_TOKEN')
        await self.service.commit(cookie)
        return cookie
    async def test_group_link_is_not_authority(self):
        rid,cookie,code=self.candidate()
        attacker,_=self.service.browser(rid)
        with self.assertRaises(ValueError): await self.service.verify(attacker,'bad')
        with self.assertRaises(ValueError): self.service.confirm(Event('999'),code)
        with self.assertRaises(ValueError): self.service.confirm(Event(group='888'),code)
        self.service.confirm(self.event,code)
        with self.assertRaises(ValueError): self.service.session(attacker)
        self.assertTrue(self.service.session(cookie)['confirmed'])
        self.service.confirm(self.event,code) # duplicate delivery is harmless
    async def test_expiry_and_instance_isolation(self):
        rid,cookie,code=self.candidate()
        with self.assertRaises(ValueError): self.service.confirm(Event(instance='other'),code)
        self.store.db.execute('UPDATE sessions SET expires=?',(time.time()-1,))
        with self.assertRaises(ValueError): self.service.confirm(self.event,code)
    async def test_credentials_encrypted_and_not_returned(self):
        cookie=await self.bound()
        body=json.dumps(self.service.public(self.service.session(cookie)))
        self.assertNotIn('SUPER_PRIVATE_TOKEN',body)
        self.assertNotIn('SUPER_PRIVATE_TOKEN', ''.join(self.store.db.iterdump()))
        self.assertTrue(self.store.account(identity(self.event)))
    async def test_bound_refresh_and_maximum_conflict(self):
        cookie=await self.bound()
        other=Event('555')
        rid,c2,code=self.candidate(other)
        self.service.confirm(other,code)
        await self.service.verify(c2,'OTHER_TOKEN')
        with self.assertRaises(ValueError): await self.service.commit(c2)
        self.assertEqual(len(self.store.accounts()),1)
        self.assertTrue(self.service.public(self.service.session(cookie))['complete'])
    async def test_stale_callback_cannot_revive_deleted_binding(self):
        await self.bound()
        rid,cookie,code=self.candidate()
        self.service.confirm(self.event,code)
        await self.service.verify(cookie,'new')
        uid=identity(self.event); version=self.store.account(uid)[0]
        self.store.remove(uid,version)
        with self.assertRaises(ValueError): await self.service.commit(cookie)
        self.assertIsNone(self.store.account(uid))
    async def test_network_return_rechecks_cancelled_session(self):
        rid,cookie,code=self.candidate();self.service.confirm(self.event,code)
        self.api.enter=asyncio.Event();self.api.release=asyncio.Event()
        task=asyncio.create_task(self.service.verify(cookie,'token'))
        await self.api.enter.wait()
        self.store.db.execute('DELETE FROM sessions WHERE id=?',(digest(cookie),))
        self.api.release.set()
        with self.assertRaises(ValueError): await task
    async def test_duplicate_sign_and_yesterday_not_today(self):
        await self.bound();uid=identity(self.event)
        self.store.record(uid,'arknights:1:a','2000-01-01','success','old')
        self.assertFalse(self.store.results(uid,now().date().isoformat()))
        first,second=await asyncio.gather(self.service.sign(uid),self.service.sign(uid))
        self.assertEqual(self.api.calls,1)
        self.assertEqual(first[0]['state'],'success')
        self.assertEqual(second[0]['state'],'success')
    async def test_restart_due_recovery_and_failed_auth_redacted(self):
        await self.bound();self.api.fail=True
        result=await self.service.due()
        self.assertEqual(len(result),1)
        self.assertNotIn('secret',json.dumps(result))
        self.assertEqual(result[0][2][0]['state'],'retry')
        self.assertFalse(await self.service.due()) # backoff persisted
    async def test_remove_and_old_update_cannot_resurrect(self):
        await self.bound();uid=identity(self.event);version,data=self.store.account(uid)
        self.assertTrue(self.store.remove(uid,version))
        self.assertFalse(self.store.update(uid,version,data))
        self.assertFalse(self.store.results(uid,now().date().isoformat()))
    async def test_durable_restart_keeps_today_results_and_credentials(self):
        await self.bound();uid=identity(self.event)
        await self.service.sign(uid)
        self.store.db.close()
        self.store=Store(self.tmp.name)
        self.service=Service(self.store,self.api,{'auto_sign_enabled':True,'auto_sign_hour':0,'auto_sign_delay':0})
        self.assertEqual(self.store.account(uid)[1]['token'],'SUPER_PRIVATE_TOKEN')
        self.assertFalse(await self.service.due())
        self.assertEqual(self.api.calls,1)

    async def test_partial_success_and_only_failed_role_retries(self):
        async def bindings(cred):
            return [SimpleNamespace(app_code='arknights',game_name='明日方舟',nickname=key,game_id=1,uid=key,roles=[]) for key in ['a','b']]
        calls=[]
        async def sign(cred,binding):
            calls.append(binding.uid)
            return SimpleNamespace(success=binding.uid=='a' or calls.count('b')>1,error='temporary',game='明日方舟',nickname=binding.nickname,awards=[])
        self.api.get_binding_list=bindings;self.api.sign_arknights=sign
        await self.bound();uid=identity(self.event)
        rows=await self.service.sign(uid)
        self.assertEqual({r['state'] for r in rows},{'success','retry'})
        self.store.db.execute('UPDATE results SET next_at=0 WHERE identity=?',(uid,))
        rows=await self.service.sign(uid)
        self.assertTrue(all(r['state']=='success' for r in rows))
        self.assertEqual(calls.count('a'),1)
        self.assertEqual(calls.count('b'),2)

    async def test_upstream_already_signed_never_submits_attendance(self):
        await self.bound();uid=identity(self.event);self.api.signed=True
        rows=await self.service.sign(uid)
        self.assertEqual(self.api.calls,0)
        self.assertEqual(rows[0]['state'],'already')
        self.assertIn('明日方舟',rows[0]['message'])

    async def test_unknown_record_reconciles_even_when_retries_exhausted(self):
        await self.bound();uid=identity(self.event);self.api.signed=True
        self.store.record(uid,'arknights:1:a',now().date().isoformat(),'unknown','old',4,time.time()+3600)
        rows=await self.service.sign(uid)
        self.assertEqual(self.api.calls,0)
        self.assertEqual(rows[0]['state'],'already')
        self.assertEqual(rows[0]['attempts'],4)

    async def test_unreadable_status_never_submits_attendance(self):
        await self.bound();uid=identity(self.event);self.api.signed=None
        rows=await self.service.sign(uid)
        self.assertEqual(self.api.calls,0)
        self.assertEqual(rows[0]['state'],'unknown')
        self.assertIn('未重复提交',rows[0]['message'])

    async def test_write_exception_reconciles_success_without_second_write(self):
        await self.bound();uid=identity(self.event)
        async def sign(cred,binding):
            self.api.calls+=1;self.api.signed=True
            raise RuntimeError('PRIVATE_UPSTREAM_VALUE')
        self.api.sign_arknights=sign
        with self.assertLogs('skland_test.service',level='WARNING') as logs:
            rows=await self.service.sign(uid)
        self.assertEqual(self.api.calls,1)
        self.assertEqual(rows[0]['state'],'already')
        self.assertNotIn('PRIVATE_UPSTREAM_VALUE',json.dumps(rows)+' '.join(logs.output))

    async def test_unconfirmed_write_stays_unknown_and_does_not_retry(self):
        await self.bound();uid=identity(self.event)
        async def sign(cred,binding):
            self.api.calls+=1
            raise RuntimeError('PRIVATE_UPSTREAM_VALUE')
        self.api.sign_arknights=sign
        with self.assertLogs('skland_test.service',level='WARNING') as logs:
            rows=await self.service.sign(uid)
        self.assertEqual(self.api.calls,1)
        self.assertEqual(rows[0]['state'],'unknown')
        self.assertIn('编号',rows[0]['message'])
        self.assertNotIn('PRIVATE_UPSTREAM_VALUE',json.dumps(rows)+' '.join(logs.output))

    async def test_failed_commit_transaction_preserves_binding_and_session(self):
        cookie=await self.bound();uid=identity(self.event);version,data=self.store.account(uid)
        def fail(): raise RuntimeError('injected commit failure')
        with self.assertRaises(RuntimeError): self.store.put(uid,{'token':'replacement'},expected=version,finalize=fail)
        self.assertEqual(self.store.account(uid)[0],version)
        self.assertEqual(self.store.account(uid)[1]['token'],'SUPER_PRIVATE_TOKEN')

    async def test_full_json_extracts_only_token_and_ignores_large_other_fields(self):
        rid,cookie,code=self.candidate();self.service.confirm(self.event,code)
        received=[]
        original=self.api.get_authorization
        async def capture(token):
            received.append(token)
            return await original(token)
        self.api.get_authorization=capture
        raw=json.dumps({'status':0,'content':'HG_TOKEN','unrelated':'X'*10000})
        await self.service.verify(cookie,raw)
        self.assertEqual(received,['HG_TOKEN'])
        result=self.service.public(self.service.session(cookie))
        self.assertIn('content',result['credential_source'])
        self.assertNotIn('HG_TOKEN',json.dumps(result))
        self.assertNotIn('X'*100,json.dumps(result))

    async def test_auth_failure_reports_stage_but_never_response_or_token(self):
        import httpx
        from skland_test.skland_api import SklandAPI
        rid,cookie,code=self.candidate();self.service.confirm(self.event,code)
        api=SklandAPI();api._did='test-device'
        api._client=httpx.AsyncClient(transport=httpx.MockTransport(lambda request:httpx.Response(400,json={'message':'echo PRIVATE_CREDENTIAL'})))
        self.service.api=api
        try:
            with self.assertLogs('skland_test.service',level='WARNING') as logs:
                with self.assertRaises(ValueError) as ctx:await self.service.verify(cookie,'PRIVATE_CREDENTIAL')
            output=str(ctx.exception)+' '.join(logs.output)
            self.assertIn('HTTP 400',output)
            self.assertIn('鹰角授权',output)
            self.assertNotIn('PRIVATE_CREDENTIAL',output)
            self.assertNotIn('echo',output)
        finally:await api.close()

    async def test_http_origin_csrf_and_cookie_boundary(self):
        app=BindingWeb(self.service,'https://maa.yumi233.cn').app()
        client=TestClient(TestServer(app)); await client.start_server()
        try:
            rid=self.service.request(self.event)
            resp=await client.post('/skland/api/session',json={'request_id':rid},headers={'Origin':'https://evil.invalid'})
            self.assertEqual(resp.status,403)
            resp=await client.post('/skland/api/session',json={'request_id':rid},headers={'Origin':'https://maa.yumi233.cn'})
            self.assertEqual(resp.status,200)
            cookie=resp.cookies[COOKIE]
            self.assertTrue(cookie['secure']);self.assertTrue(cookie['httponly'])
            body=await resp.json();code=body['command'].split()[1]
            self.service.confirm(self.event,code)
            headers={'Origin':'https://maa.yumi233.cn','Cookie':f'{COOKIE}={cookie.value}'}
            resp=await client.post('/skland/api/credential',json={'token':'bad'},headers=headers)
            self.assertEqual(resp.status,403)
            headers['X-CSRF-Token']=body['csrf']
            resp=await client.post('/skland/api/credential',json={'token':json.dumps({'content':'SUPER_PRIVATE_TOKEN','ignored':'X'*10000})},headers=headers)
            self.assertEqual(resp.status,200)
            self.assertNotIn('SUPER_PRIVATE_TOKEN',await resp.text())
            self.assertEqual(self.api.last_token,'SUPER_PRIVATE_TOKEN')
            self.assertEqual(resp.headers['Cache-Control'],'no-store')
            resp=await client.post('/skland/api/commit',json={'consent':True,'auto':True},headers=headers)
            self.assertEqual(resp.status,200)
            self.assertTrue((await resp.json())['complete'])
            resp=await client.post('/skland/api/sign',json={},headers=headers)
            self.assertEqual(resp.status,200)
            self.assertTrue((await resp.json())['signing'])
            await asyncio.gather(*list(self.service.tasks.values()))
            resp=await client.get('/skland/api/status',headers=headers)
            status=await resp.json()
            self.assertFalse(status['signing'])
            self.assertEqual(status['results'][0]['state'],'success')

        finally: await client.close()

class CredentialParsingTests(unittest.TestCase):
    def test_raw_quoted_full_and_nested_official_responses(self):
        from skland_test.credentials import parse_credential
        samples=['HG_TOKEN',json.dumps('HG_TOKEN'),json.dumps({'status':0,'content':'HG_TOKEN','other':'ignored'}),json.dumps({'code':0,'data':{'content':'HG_TOKEN','uid':'ignored'}}),json.dumps({'data':{'token':'HG_TOKEN','name':'ignored'}})]
        for sample in samples:
            self.assertEqual(parse_credential(sample)[0],'HG_TOKEN')
    def test_content_priority_and_duplicate_fields(self):
        from skland_test.credentials import parse_credential
        self.assertEqual(parse_credential(json.dumps({'content':'HG_TOKEN','data':{'token':'SIGN_TOKEN'}}))[0],'HG_TOKEN')
        self.assertEqual(parse_credential(json.dumps({'token':'HG_TOKEN','data':{'token':'HG_TOKEN'}}))[0],'HG_TOKEN')
    def test_cred_signing_tokens_and_ambiguous_json_rejected(self):
        from skland_test.credentials import parse_credential
        for data in [{'data':{'cred':'C','token':'SIGN_TOKEN'}},{'data':{'token':'ONE'},'user':{'token':'TWO'}},{'content':None},{'status':1}]:
            with self.assertRaises(ValueError):parse_credential(json.dumps(data))
    def test_malformed_and_oversized_inputs_not_echoed(self):
        from skland_test.credentials import parse_credential
        for raw in ['{"content":"PRIVATE_VALUE"','X'*16385]:
            with self.assertRaises(ValueError) as ctx:parse_credential(raw)
            self.assertNotIn('PRIVATE_VALUE',str(ctx.exception))

class ProtocolTests(unittest.IsolatedAsyncioTestCase):
    async def test_arknights_status_uses_today_records_and_only_get(self):
        import httpx
        from datetime import datetime,timedelta
        from zoneinfo import ZoneInfo
        from skland_test.skland_api import SklandAPI,Credential
        api=SklandAPI();api._did='test-device'
        ts=int(time.time());midnight=int(datetime.now(ZoneInfo('Asia/Shanghai')).replace(hour=0,minute=0,second=0,microsecond=0).timestamp())
        data={'currentTs':str(ts),'records':[{'ts':str(midnight)}]}
        def handle(request):
            self.assertEqual(request.method,'GET')
            self.assertEqual(request.url.path,'/api/v1/game/attendance')
            self.assertEqual(request.url.params['uid'],'role')
            return httpx.Response(200,json={'code':0,'data':data})
        api._client=httpx.AsyncClient(transport=httpx.MockTransport(handle))
        binding=SimpleNamespace(game_id=1,uid='role')
        try:
            self.assertTrue(await api.get_attendance_status(Credential('sign-key','cred'),binding))
            data['records']=[{'ts':str(midnight-86400)}]
            self.assertIs(await api.get_attendance_status(Credential('sign-key','cred'),binding),False)
            data['currentTs']=str(ts-86400)
            self.assertIsNone(await api.get_attendance_status(Credential('sign-key','cred'),binding))
            data['currentTs']=str(ts);data['records']=[{}]
            self.assertIsNone(await api.get_attendance_status(Credential('sign-key','cred'),binding))
        finally:await api.close()

    async def test_endfield_status_requires_explicit_bool_and_role_header(self):
        import httpx
        from skland_test.skland_api import SklandAPI,Credential
        api=SklandAPI();api._did='test-device';data={'hasToday':True}
        def handle(request):
            self.assertEqual(request.method,'GET')
            self.assertEqual(request.url.path,'/web/v1/game/endfield/attendance')
            self.assertEqual(request.headers['sk-game-role'],'3_role_server')
            return httpx.Response(200,json={'code':0,'data':data})
        api._client=httpx.AsyncClient(transport=httpx.MockTransport(handle))
        try:
            self.assertTrue(await api.get_attendance_status(Credential('sign-key','cred'),SimpleNamespace(),{'roleId':'role','serverId':'server'}))
            data['hasToday']=False
            self.assertIs(await api.get_attendance_status(Credential('sign-key','cred'),SimpleNamespace(),{'roleId':'role','serverId':'server'}),False)
            data['hasToday']='true'
            self.assertIsNone(await api.get_attendance_status(Credential('sign-key','cred'),SimpleNamespace(),{'roleId':'role','serverId':'server'}))
        finally:await api.close()

    async def test_unrelated_already_message_is_not_success(self):
        from skland_test.skland_api import SklandAPI
        api=SklandAPI()
        self.assertFalse(api._is_signed_today(SimpleNamespace(success=False,error='already logged out')))
        self.assertTrue(api._is_signed_today(SimpleNamespace(success=False,error='already signed in today')))

    async def test_signature_body_matches_transmitted_bytes(self):
        import httpx
        from skland_test.skland_api import SklandAPI, Credential
        api=SklandAPI();api._did='test-device'
        def handle(request):
            self.assertEqual(request.content,b'{"gameId":1,"uid":"role"}')
            expected,_=api._generate_signature('sign-key',request.url.path,request.content.decode(),'test-device')
            self.assertEqual(request.headers['sign'],expected)
            return httpx.Response(200,json={'code':0,'data':{'awards':[]}})
        api._client=httpx.AsyncClient(transport=httpx.MockTransport(handle))
        try:
            result=await api.sign_arknights(Credential('sign-key','cred'),SimpleNamespace(game_id=1,uid='role',nickname='name',channel_name='channel'))
            self.assertTrue(result.success)
        finally: await api.close()

    async def test_authorization_and_credential_posts_have_json_content_type(self):
        import httpx
        from skland_test.skland_api import SklandAPI
        api=SklandAPI();api._did='test-device';seen=[]
        def handle(request):
            seen.append(request.url.path)
            self.assertEqual(request.headers.get('content-type'),'application/json')
            body=json.loads(request.content)
            if request.url.path.endswith('/grant'):
                self.assertEqual(body['token'],'HG_TOKEN')
                return httpx.Response(200,json={'status':0,'data':{'code':'grant'}})
            self.assertEqual(body,{'code':'grant','kind':1})
            return httpx.Response(200,json={'code':0,'data':{'token':'SIGN_TOKEN','cred':'CRED','userId':'stable-account'}})
        api._client=httpx.AsyncClient(transport=httpx.MockTransport(handle))
        try:
            grant=await api.get_authorization('HG_TOKEN')
            credential=await api.get_credential(grant)
            self.assertEqual(credential.cred,'CRED')
            self.assertEqual(credential.user_id,'stable-account')
            self.assertEqual(len(seen),2)
        finally:await api.close()

    async def test_rate_limit_cools_down_later_requests(self):
        import httpx
        from skland_test.skland_api import SklandAPI, UpstreamError
        api=SklandAPI();calls=[]
        def handle(request):
            calls.append(1)
            return httpx.Response(429,headers={'Retry-After':'120'})
        api._client=httpx.AsyncClient(transport=httpx.MockTransport(handle))
        try:
            for _ in range(2):
                with self.assertRaises(UpstreamError) as ctx: await api._request('GET','https://example.invalid/status')
                self.assertEqual(ctx.exception.kind,'rate')
            self.assertEqual(len(calls),1)
        finally:await api.close()

if __name__=='__main__': unittest.main()
