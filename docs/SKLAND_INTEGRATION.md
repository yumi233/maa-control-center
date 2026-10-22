# 森空岛群聊网页认证：仓库集成与维护约定

更新日期：2026-10-22。

## 这是什么功能

MAA 网站除管理面板外，还承担 AstrBot 森空岛插件的公共账号绑定入口。

正式入口为 **https://maa.yumi233.cn/skland**。该页面**不需要 MAA 管理面板账号，不得要求管理面板登录，也不得跳转到管理面板登录页**。

用户在 QQ 群发送 `/skdlogin`，打开机器人返回的链接，在网页复制 `/skdconfirm <code>` 回原群确认身份，然后只在网页粘贴官方凭证页面的完整 JSON（也兼容单独 Token），系统自动提取可用字段。网页验证角色、征得保存同意后绑定；可立即签到、查看结果。解绑在 QQ 群通过 `/skdlogout` 确认完成。

裸 `/skland` 不含有效请求时，也应展示独立绑定页面，并提示先到 QQ 群发送 `/skdlogin`；它不能直接接收任意自报 QQ 的账号绑定。

页面与 API 的用户认证来自插件自己的短期 Cookie、QQ 本人确认和 CSRF 校验，**不是 MAA 管理员会话**。

## 为什么必须写进主仓库

本功能曾在 `0.4.4-skland` 上线，但后续网站更新到 `0.5.0` 时，从未包含它的旧源码重新构建，导致 `/skland` 回退为管理面板、专用 API 回退成 HTML。

不能只保留旧 Docker 镜像或一份独立服务器补丁。以下文件必须提交到网站主 Git 仓库，合并到正常开发分支，今后的版本在此基础上构建。本集成提交必须合并进正常发布分支，再从该分支构建；仅部署服务器上的临时版本不足以保证后续发布保留功能。

## 网站仓库必须保留的文件

| 文件 | 作用 | 不可破坏的行为 |
|---|---|---|
| `web/src/components/SklandBinding.vue` | 公共绑定页面 | 不调用 MAA 管理登录，不要求管理账号；Token 不写 localStorage，不进入 URL |
| `web/src/main.ts` | 页面分流 | `/skland`、`/skland/` 与 `/#/skland` 挂载绑定组件，其他页面挂载原 App |
| `web/src/App.vue` | 管理面板登录页的公共入口链接 | “森空岛签到绑定 · 无需登录”链接到 `/skland` |
| `internal/api/skland.go` | 专用接口反向代理 | 仅代理明确白名单；剥离 MAA 管理 Cookie 和 Authorization；拒绝跨站写入 |
| `internal/api/skland_test.go` | 代理边界回归测试 | 验证管理凭据不泄露、路径白名单及 Origin 校验 |
| `internal/api/server.go` | 注册专用路由、健康信息 | 必须调用 `s.sklandRoutes(m)`；健康信息包含 `skland_binding` |
| `cmd/server/main.go` | 可选私有配置解析 | 允许 `MAACC_SKLAND_UPSTREAM` 配置键，与环境变量遵循已有优先级 |
| `docs/SKLAND_INTEGRATION.md` | 本说明 | 发版与登录入口调整前核对 |

本次修复以**线上 0.5.0 完整源码**为基线，重新合并这些文件，没有回退到 0.4.4，也没有替换 0.5.0 的其他功能。

## 代理接口白名单

```
POST /skland/api/session
GET  /skland/api/status
POST /skland/api/credential
POST /skland/api/commit
POST /skland/api/sign
POST /skland/api/cancel
```

其他 `/skland/api/*` 必须返回 JSON 404，不能落回单页应用的 HTML。不得代理 AstrBot 管理 `/api`，不得把 AstrBot 或 MAA 管理员令牌提供给页面。

## 生产部署条件

网站服务设置：

```
MAACC_SKLAND_UPSTREAM=http://astrbot:6190
```

网站容器必须加入 `astrbot_astrbot_network`，与名为 `astrbot` 的容器互通。既有主网络、端口、数据卷、UID、安全选项和 TLS 配置保留。不要将 6190 作为公网宿主端口暴露。

代理目标仅来自受控服务器配置，不能从用户请求指定，不能成为任意 URL 代理。本修复同时支持把上述键写入 `server.private.json` 的现有字符串配置映射；部署环境变量优先。不要把私有配置文件提交到 Git。

只更新前端不够：后端路由、代理配置、Docker 网络缺一不可。发布后 `/health` 的 `skland_binding` 应为 true；这表示代理已配置，仍须另测插件后端可达性。

## AstrBot 端配套

网站本身不保存森空岛凭证、不执行 QQ 身份确认。配套源码在 [`integrations/astrbot_plugin_skland`](../integrations/astrbot_plugin_skland/README.md)，插件为改造后的 `astrbot_plugin_skland` v2.0.1；原作者 Azincc，保留原鉴权和游戏签到协议，新增服务层。

- 插件代码：`/AstrBot/data/plugins/astrbot_plugin_skland/`。
- 专用 API：容器内部 `0.0.0.0:6190`。
- 插件配置 `binding_public_url`：`https://maa.yumi233.cn`。
- 加密数据库：`/AstrBot/data/plugin_data/astrbot_plugin_skland/skland.sqlite3`。
- 主密钥：`/AstrBot/data/config/skland_credentials.key`，0600，单独受控备份。
- 公共确认链接不是授权凭据：必须由同一个 QQ、同一平台实例、原会话确认具体浏览器。
- 主进程单实例运行；不能把同一 SQLite 数据库挂给多个并行插件进程调度。

插件现有 2 个绑定已迁移。自动签到总开关原来为关闭、时间为北京时间 09:00，本次保留。网站勾选“参与自动签到”不会越过总开关；页面必须提示总开关关闭。普通用户无需知道数据库或容器配置。

## 每次发版必须检查

1. 使用全新、未登录的浏览器打开 `/skland`、`/skland/`、`/#/skland`，显示“从 QQ 群开始绑定”，页面没有管理面板账号/密码输入框。
2. `/` 仍显示原 MAA 管理入口；匿名访问 `/api/v1/auth/session` 仍为 401，不能因公共绑定放开管理接口。
3. `/health` 为 JSON 200，`skland_binding` 为 true。
4. 匿名 `/skland/api/status` 返回插件 JSON 错误，而不是 HTML；目前无会话时为 400。
5. 无效 request_id、缺 Cookie、缺 CSRF、跨域写入被拒绝；未列举的专用接口为 JSON 404。
6. 有效请求流程：原群本人确认 → 验证 Token → 明确同意 → 保存 → 刷新恢复 → 首签结果。不使用生产用户 Token 做自动化测试。
7. 手机无横向溢出，失败提交保留输入，Token 不回显，不写 URL/localStorage/日志；支持 reduced-motion。
8. 网站容器重建后仍保留专用上游配置和 AstrBot 网络。不能只验证“容器 running”或主页面 200 就宣布完成。

建议持续集成至少运行：

```
go test ./internal/api
cd web
npm ci
npm run build
```

部署后运行真实站点匿名浏览器检查，不能用仅访问 `/health` 替代页面检查。CI 中的 `/skland` 流程必须同时断言“不是管理面板登录页”和“专用 API 返回 JSON”，防止再次静默回退。

## 运维边界与回滚

更新网站不应卸载插件、清理绑定、重置总开关或读取用户凭证。网站维护不需要向真实 QQ 群发送测试消息。

回滚也必须保留公共绑定入口。如果回到未集成本功能的旧版网站，必须明确将其视为功能下线，而不是普通兼容回滚。插件迁移后的旧 KV 已清理；不能只把插件代码降回 v1.4.0，需另行评估加密备份及上线后的增量变更。

## 仓库维护方式

网站端完整实现随本集成提交纳入 Git。日常维护以当前仓库中的代码、README 和 AGENTS.md 为准，不依赖服务器本地源码目录或额外补丁文件。

配套 AstrBot 插件源码随网站保存在 integrations 目录，但需单独部署；本仓库不包含用户数据库、主密钥、Token 或服务器私有配置。重新部署网站前，应确认插件 v2.0.1 的专用 API 仍可达。

本集成尚未进入发布分支时，先合并集成提交，再发布后续版本。不要只复制本说明而遗漏上表列出的前后端代码及测试。


## 完整 JSON 输入与鉴权请求约定

用户无需挑选字段：支持纯 Token、JSON 字符串以及完整官方响应中的 `content` / `data.content` / `token` 字段。优先使用官方 content；`cred` 同对象的 token 是签名密钥，不能当作通行证 Token。重复相同凭证可去重，不同账号凭证不能混在一个输入中。

输入最多 16384 字符，网站代理与插件 HTTP 请求体上限均为 128 KiB；多余字段仅在解析时读取，不保存、不回传，也不发送给模型。

插件使用 `content=` 保证签名与实际请求字节一致时，必须显式设置 `Content-Type: application/json`。不能依赖 HTTP 客户端像 `json=` 那样自动补请求头；缺少该头会让鹰角接口将有效 JSON 当作缺少 token/appCode 的空参数。

认证失败应只报告安全的阶段、HTTP/业务码与排查编号，不记录请求体、完整响应或凭证值。配套插件 v2.0.1 已加入对应协议和日志脱敏回归测试。
