"""Public binding API. Does not use AstrBot or MAA administrator sessions."""
import hmac
import asyncio
from aiohttp import web
from urllib.parse import urlsplit

COOKIE = '__Secure-skland_session'


class BindingWeb:
    def __init__(self, service, origin):
        self.service = service
        self.origin = origin.rstrip('/')
        parsed = urlsplit(self.origin)
        if parsed.scheme != 'https' or not parsed.netloc or parsed.path or parsed.query or parsed.fragment:
            raise ValueError('绑定站点必须是 HTTPS origin，不包含路径')
        self.runner = None

    def app(self):
        @web.middleware
        async def guard(request, handler):
            try:
                if request.method != 'GET':
                    if request.headers.get('Origin') != self.origin or request.headers.get('Sec-Fetch-Site') == 'cross-site':
                        raise web.HTTPForbidden()
                    if request.content_type != 'application/json':
                        raise web.HTTPUnsupportedMediaType()
                    if request.path != '/skland/api/session':
                        session = self.service.session(request.cookies.get(COOKIE, ''))
                        if not hmac.compare_digest(request.headers.get('X-CSRF-Token', ''), session['csrf']):
                            raise web.HTTPForbidden()
                response = await handler(request)
            except ValueError as exc:
                response = web.json_response({'message': str(exc)}, status=400)
            except web.HTTPException as exc:
                response = web.json_response({'message': '请求未授权、格式错误或入口不存在'}, status=exc.status)
            except Exception:
                # Never expose upstream responses or request bodies in exceptions.
                response = web.json_response({'message': '服务暂不可用，请稍后重试'}, status=503)
            response.headers['Cache-Control'] = 'no-store'
            response.headers['Referrer-Policy'] = 'no-referrer'
            response.headers['X-Content-Type-Options'] = 'nosniff'
            return response
        app = web.Application(middlewares=[guard], client_max_size=131072)
        app.router.add_post('/skland/api/session', self.start)
        app.router.add_get('/skland/api/status', self.status)
        app.router.add_post('/skland/api/credential', self.credential)
        app.router.add_post('/skland/api/commit', self.commit)
        app.router.add_post('/skland/api/sign', self.sign)
        app.router.add_post('/skland/api/cancel', self.cancel)
        return app

    async def start(self, request):
        data = await request.json()
        rid = data.get('request_id', '')
        if not isinstance(rid, str) or len(rid) > 100:
            raise ValueError('绑定入口无效')
        cookie, csrf = self.service.browser(rid, request.cookies.get(COOKIE, ''))
        result = web.json_response(self.service.public(self.service.session(cookie)))
        result.set_cookie(COOKIE, cookie, path='/skland', secure=True, httponly=True, samesite='Strict', max_age=600)
        return result

    async def status(self, request):
        return web.json_response(self.service.public(self.service.session(request.cookies.get(COOKIE, ''))))

    async def credential(self, request):
        data = await request.json()
        token = data.get('token', '')
        if not isinstance(token, str):
            raise ValueError('Token 格式无效')
        await asyncio.wait_for(self.service.verify(request.cookies.get(COOKIE, ''), token), timeout=50)
        return await self.status(request)

    async def commit(self, request):
        data = await request.json()
        if data.get('consent') is not True or not isinstance(data.get('auto', True), bool):
            raise ValueError('请确认保存凭证与自动签到设置')
        await self.service.commit(request.cookies.get(COOKIE, ''), data.get('auto', True))
        return await self.status(request)

    async def sign(self, request):
        session = self.service.session(request.cookies.get(COOKIE, ''))
        if not session['confirmed'] or session['status'] != 'complete':
            raise web.HTTPForbidden()
        self.service.submit_sign(session['identity'])
        return await self.status(request)

    async def cancel(self, request):
        session = self.service.session(request.cookies.get(COOKIE, ''))
        self.service.store.db.execute('DELETE FROM sessions WHERE id=?', (session['id'],))
        result = web.json_response({'ok': True})
        result.del_cookie(COOKIE, path='/skland', secure=True, httponly=True, samesite='Strict')
        return result

    async def start_server(self, host='0.0.0.0', port=6190):
        self.runner = web.AppRunner(self.app(), access_log=None)
        await self.runner.setup()
        await web.TCPSite(self.runner, host, port).start()

    async def close(self):
        if self.runner:
            await self.runner.cleanup()
