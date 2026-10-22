"""Parse official account responses locally without logging credential values."""
import json

MAX_INPUT = 16384
MAX_TOKEN = 4096


def parse_credential(raw):
    if not isinstance(raw, str) or not raw.strip() or len(raw) > MAX_INPUT:
        raise ValueError('请粘贴官方凭证页面完整 JSON 或 Token（最多 16384 字符）')
    raw = raw.strip()
    if raw[0] not in '{["':
        if len(raw) > MAX_TOKEN or any(c.isspace() for c in raw):
            raise ValueError('未识别到有效凭证，请完整复制官方凭证页面的 JSON')
        return raw, 'Token 文本'
    try:
        data = json.loads(raw)
    except (ValueError, RecursionError):
        raise ValueError('JSON 不完整或格式无效，请从官方凭证页面重新全选复制，不用挑选字段') from None
    if isinstance(data, str):
        if not data.strip() or len(data.strip()) > MAX_TOKEN or any(c.isspace() for c in data.strip()):
            raise ValueError('JSON 字符串中没有有效凭证')
        return data.strip(), 'JSON 字符串'

    candidates = []
    nodes = [0]
    def walk(value, path=(), depth=0):
        nodes[0] += 1
        if nodes[0] > 200 or depth > 6:
            return
        if isinstance(value, dict):
            for name in ('content', 'token'):
                token = value.get(name)
                # generate_cred_by_code returns a signing token alongside cred.
                if name == 'token' and 'cred' in value:
                    continue
                if isinstance(token, str) and token.strip() and len(token.strip()) <= MAX_TOKEN and not any(c.isspace() for c in token.strip()):
                    candidates.append((path + (name,), token.strip(), name))
            for key, item in value.items():
                if isinstance(item, (dict, list)):
                    walk(item, path + (key,), depth + 1)
        elif isinstance(value, list):
            for index, item in enumerate(value):
                if isinstance(item, (dict, list)):
                    walk(item, path + (index,), depth + 1)
    walk(data)
    # Prefer the documented official response; unrelated sibling fields are ignored.
    for preferred in (('content',), ('data', 'content')):
        for path, token, field in candidates:
            if path == preferred:
                return token, '完整 JSON 的 content 字段'
    unique = {}
    for path, token, field in candidates:
        unique.setdefault(token, field)
    if len(unique) == 1:
        token, field = next(iter(unique.items()))
        return token, f'完整 JSON 的 {field} 字段'
    if len(unique) > 1:
        raise ValueError('内容包含多个不同账号凭证，请仅复制一次官方凭证页面的完整响应，不要合并多个页面')
    if isinstance(data, dict) and ('cred' in data or isinstance(data.get('data'), dict) and 'cred' in data['data']):
        raise ValueError('识别到的是森空岛 cred/签名凭证，请改为全选复制官方通行证凭证页面的完整响应')
    raise ValueError('完整内容中没有可用的 content 或 token 字段，请确认复制的是官方凭证页面的响应')
