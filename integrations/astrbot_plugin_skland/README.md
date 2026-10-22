# AstrBot 森空岛群聊网页认证插件 v2.0.1

基于 [Azincc/astrbot_plugin_skland](https://github.com/Azincc/astrbot_plugin_skland) v1.4.0 改造，保留上游森空岛 API 与游戏签到实现及作者署名。与网站公共 `/skland` 页面配套，部署说明见 [网站集成文档](../../docs/SKLAND_INTEGRATION.md)。

## 使用

群内 `/skdlogin` → 打开网页 → 在原群发送网页上的确认命令 → 网页粘贴官方凭证页面的全部 JSON → 验证角色并同意保存。也兼容单独 Token，不要求私聊机器人或管理面板账号。

`/skdsign` 签到，`/skd` 查询本人今日缓存，`/skdlogout` 发起解绑，`/skdnotify` 设置群通知。群摘要需管理员 `/skdgroup on` 与用户本人 `/skdshare on` 双重开启。

## 部署

将本目录的插件文件安装到 AstrBot `data/plugins/astrbot_plugin_skland/`，按 `requirements.txt` 安装依赖后重载插件。绑定 API 使用容器内部端口 6190，不直接发布公网；网站通过同一 Docker 网络代理专用接口。

不要复制或提交数据库、主密钥、旧数据备份。现有数据目录和配置必须保留；自动签到总开关由原插件配置决定，默认不擅自开启。

## v2.0.1 修复

- 使用 `content=` 发送已签名 JSON 时显式补 `Content-Type: application/json`，修复正确 Token 被当作空参数的问题。
- 完整 JSON 自动提取 `content` / `token`，忽略无关字段，拒绝把 cred 配套签名密钥当作通行证 Token。
- 请求体上限与网站一致为 128 KiB；认证错误只输出阶段、状态码和排查编号，不泄露凭证或上游完整响应。

## 验证

```sh
python -m pip install -r integrations/astrbot_plugin_skland/requirements.txt
python -m unittest discover -s integrations/astrbot_plugin_skland/tests
```

测试使用模拟凭证和 HTTP 响应，不读取生产凭证，不访问真实账号，不发送 QQ 消息。
