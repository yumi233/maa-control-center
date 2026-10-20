# 架构与边界

NAS 的 Go 服务内嵌 Vue 静态文件，SQLite 使用 WAL；Windows Agent 主动通过 HTTPS 上报只读观察结果。NAS 不需要 MAA Core，也没有远程 Shell API。

```text
Browser ── HTTPS / session + CSRF ── Go API ── SQLite WAL
                                       ↑
                            HTTPS / paired agent token
                                       │
                              Windows read-only Agent
                                       │
                       explicit installation + file allowlist
                                       │
                             existing MAA and schedules
```

账号、Agent、MAA 安装、配置档和模拟器是不同实体。多个配置档可来自同一个安装，共享实例不能证明共享登录环境中已经安全切号。

认证使用 bcrypt 密码哈希、随机会话令牌、数据库仅保存令牌 SHA-256、HttpOnly / SameSite=Strict Cookie。HTTPS 部署的 Cookie 设置 Secure。修改 API 要求会话、来源校验与 CSRF。首版只有 Admin；不把 MAA 的 user/device 字段当作认证。

Agent 使用独立随机令牌，Windows 本地配置不经页面显示。只读快照有大小限制；扫描只针对显式指定的 MAA 安装。引用文件必须通过真实路径允许目录检查，不能借助 JSON 路径访问任意文件。

生产任务下发、配置保存、官方远程控制端点保持 423。Mock 协议模块可以独立验证返回行为，但并未接入真实执行。数据库预留任务、配置修订和审计等结构，不表示后续阶段已经实现。

服务重启将 dispatched/running 任务降为 unknown，避免不明任务被重复执行。当前没有生产调度循环，也不会与现有 MAA 定时任务争夺调度权。
