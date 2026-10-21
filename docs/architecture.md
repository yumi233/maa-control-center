# 架构与边界

NAS 的 Go 服务内嵌 Vue 静态文件，SQLite 使用 WAL；Windows Agent 主动通过 HTTPS 上报观察结果，并拉取经管理员逐次确认的配置方案。NAS 不需要 MAA Core，也没有远程 Shell API。

v0.2 的配置链路为：自然语言需求 → 用户指定的 OpenAI 兼容 API → 版本化规则 → 确定性差异 → 管理员确认 → Windows 复核差异/进程/指纹 → 备份、替换、读回校验。模型不取得文件路径、登录身份或工具执行权限。回执和通知发送队列在同一事务提交；队列结束日志采用稳定事件 ID 去重。凭据通过 AES-GCM 加密保存，密钥与数据库分别存放并配套私有备份。

下图是基础读取链路；新增的配置与通知边界详见 [Agent 模式](agent-mode.md)。

```text
Browser ── HTTPS / session + CSRF ── Go API ── SQLite WAL
                                       ↑
                            HTTPS / paired agent token
                                       │
                              Windows bounded Agent
                                       │
                       explicit installation + file allowlist
                                       │
                             existing MAA and schedules
```

账号、Agent、MAA 安装、配置档和模拟器是不同实体。多个配置档可来自同一个安装，共享实例不能证明共享登录环境中已经安全切号。

认证使用 bcrypt 密码哈希、随机会话令牌、数据库仅保存令牌 SHA-256、HttpOnly / SameSite=Strict Cookie。HTTPS 部署的 Cookie 设置 Secure。修改 API 要求会话、来源校验与 CSRF。支持 Admin 与成员，资源按 owner_id 隔离；不把 MAA 的 user/device 字段当作认证。

Agent 使用独立随机令牌，Windows 本地配置不经页面显示。只读快照有大小限制；扫描只针对显式指定的 MAA 安装。引用文件必须通过真实路径允许目录检查，不能借助 JSON 路径访问任意文件。

v0.4 通过成对认证的 Agent 命令端点执行，浏览器不直接操作 Windows。旧的官方远程协议端点仍关闭；实际控制使用核实的原生窗口和明确安装路径，无通用 Shell / 脚本入口。

服务定时循环产生持久任务。同一 Agent 只允许一个活动任务；Windows 本地实例锁进一步互斥配置修改与任务启动。启动意图先落盘，再执行原生命令；结果不明时保持 unknown，拒绝重复点击。服务重启后的活动任务先转 unknown，再凭 Agent 本地记录监视，不自动重发启动。

配置字段投影、完整文件指纹、版本规则和本地角色共同复核。Windows 只在空闲时正常关闭 GUI 后保存并重载。现有 MAA 自己的定时独立存在，页面提示冲突并提供本配置档的受控编辑，不自动关闭。
