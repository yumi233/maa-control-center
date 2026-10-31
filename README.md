# MAA Control Center

轻量、独立部署的 MAA Web 管理中心。NAS 负责可视化、配置方案和通知，Windows 继续运行现有 MAA 和模拟器。

**v0.5.5：公招高星特别提醒与 Telegram 专用出口。** MAA 识别到保底五星组合或高级资深干员标签时，独立通知该账号的现有渠道；以标签保底等级判断，普通候选列表里的五星不触发，也不冒充已经获得干员。事件按原执行记录归属账号，跨心跳、轮转与重启去重，保留原公招设置。首次升级从当前时刻开始监听，不重发历史标签。代理指挥无法启用时，完成汇报明确提示库存保持未完成。管理员可用 `MAACC_TELEGRAM_PROXY=http://<可信代理IP>:<端口>` 为官方 Telegram HTTPS 配置专用出口；其他渠道的内网保护、TLS 校验与未知送达不自动重发规则不变。详见 [通知说明](docs/notifications.md)。

**Agent v0.5.4：修复日志轮转导致的汇报缺失。** 执行明细同时读取当前日志和 MAA 保留的 `.bak.log`，按本轮时间取理智、关卡与掉落；长任务不再受旧 8 MiB 日志尾部限制，重复回调不会重复计数。保留读取上限，历史日志确实缺失时仍明确标注不完整。NAS 服务兼容 v0.5.3，无需改变原定时或资源预算。

**v0.5.3：保护执行中的安装探测与等待告警。** 安装信息暂时不可读时，已有任务保留占用并等待核实，恢复后采集原生结束结果；不会提前记失败或重复点击开始。尚未启动的任务只在原有效期内等待，已完成回执不被后续探测覆盖。`waiting` 长时间受阻也会按账号发送 TG/QQ 告警。

**v0.5.2：断连与队列异常提醒。** MAA 原生重连失败后保留防重复执行保护，并通知对应账号；排队超过 10 分钟且前序结果待核实、回执超时或未下发时发送队列受阻提醒。过期清理独立于执行锁，解除占用也不会补跑旧计划。TG / QQ 沿用账号路由和发送去重。见 [异常恢复说明](docs/execution-recovery.md)。

**v0.5.0：账号独立的挂机汇报与群控。** 挂机开始通知一次，结束附理智读数、本次与今日记录；各渠道可限定账号。支持 AstrBot 群指令和网页成员管理，按 UID 区分仅查看、可启停，支持撤销与账号移交保护。成员加入及新号接入见 [群控操作说明](docs/group-control.md)。

**v0.4.3：QQ 官方机器人 / AstrBot 直连通知。** 支持完整 UMO 路由、仅有 im 权限的 API Key、独立渠道测试与送达记录。升级默认保留原通知配置，新增渠道关闭。详见 [通知接入](docs/agent-mode.md)。

**v0.4.1：Windows 无窗口自启与通知去重。** Agent 使用无控制台构建，开机与登录触发；桌面控制需要用户已登录。任务回执和心跳共用完成事件去重，避免重复推送。

**v0.4.0：直接配置编辑与多账号定时执行。** 在「配置编辑」调整任务开关、预算、招募、库存目标、基建干员及原有定时；预览差异后保存。支持每账号每日/每周计划、串行执行队列、请求停止和真实 Windows 回执。无需 AI 接口即可使用这些功能，详见 [配置与定时操作](docs/configuration-and-scheduling.md)。

**v0.3.0：多用户与自助改密。** 管理员创建用户、分配账号与配置档；成员管理自己的账号、模型接入和通知。现有管理员密码保留。详见 [多用户与通知排错](docs/multi-user.md) 和 [Cloudflare 接入](docs/cloudflare.md)。

**v0.2.0：Agent 配置与通知。** 通过自己的 OpenAI 兼容 API，用自然语言生成 MAA 配置方案；版本化规则校验、差异确认、Windows 本地备份、应用校验和受控回滚构成完整流程。支持 Telegram 与 QQ OneBot/NapCat 通知，详情见 [Agent 模式](docs/agent-mode.md)。

继续提供真实 Agent 心跳、多配置档、库存目标、独立基建排班与日志。兼容范围为已核实的 MAA 6.20 业务字段；未支持字段明确拒绝。实际任务控制适配已核实的 Windows MAA 6.20，通过当前用户的交互窗口执行。原有 MAA 定时不会自动关闭；创建网页计划前应核对重叠时段。自动搓玉仍未启用。

## 设计与技术

- Vue 3 / TypeScript / Vite 响应式前端
- Go HTTP API / SQLite WAL
- Go Windows Agent，发现、心跳和经确认的配置同步
- 使用 [Endfield UI skill](https://github.com/hhikr/endfield-ui-skill) 的设计规范
- MIT 开源；自托管 Saira 字体遵循 SIL OFL 1.1

## 隐私

本仓库不包含真实账号、内网地址、生产配置、数据库或令牌。生产环境信息放在被 Git 忽略的本地配置中。

## 快速开始

需要 Go 1.26+、Node.js 24+。

```sh
bash scripts/build.sh
go test -race ./...
go vet ./...
```

本机试用：设置自己的 `MAACC_ADMIN_PASSWORD`（12–72 字节）后运行 `./bin/maa-server`，访问 `http://127.0.0.1:8421`。管理员用户名是 `admin`，没有默认密码。

页面地址加 `?demo=1` 可以进入有明显标记的交互演示；演示不向真实 API 下发任务，也不修改生产数据。真实环境未连接时只显示未知与离线状态，不自动替换为演示数据。

NAS 与 Windows 的 HTTPS 部署、Agent 登录启动任务及卸载方法见[部署说明](docs/deployment.md)。

## 文档

- [环境调查（脱敏）](docs/environment-inventory.md)
- [MAA 能力矩阵](docs/maa-capability-matrix.md)
- [配置保护与备份](docs/existing-config-backup.md)
- [架构与安全边界](docs/architecture.md)
- [API / OpenAPI](docs/openapi.yaml)
- [设计记录](docs/design.md)
- [Agent 配置、规则库与 TG/QQ 通知](docs/agent-mode.md)
- [验证记录与未完成项](docs/verification.md)

## 接入原则

账号、MAA 安装、配置档与模拟器分别管理。一个 MAA 可以包含多个配置档，不假设每个账号独占进程。现有文件默认只读；启用配置同步后也必须逐次核对差异，在 MAA 空闲时正常关闭、应用并重载。没有账号归属的仓库缓存不作为实际库存展示。

支持管理员与成员，账号、配置方案、执行计划和通知按归属隔离。首次启动在新 SQLite 数据库中建立账号，密码使用 bcrypt。Agent 使用独立配对令牌，公网与非回环访问要求 HTTPS。没有任意路径写入或远程 Shell 接口。

## 许可

本项目使用 [MIT](LICENSE)；字体及上游组件许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。本项目与游戏及其开发商无关联，不包含游戏官方素材。

## 森空岛网页认证（无需管理面板账号）

公共入口：[/skland](https://maa.yumi233.cn/skland)。用户在 QQ 群发起并确认身份后，在网页提交凭证并签到。

这是需随网站一起维护的已上线功能。开发、部署与回归约定见 [森空岛集成说明](docs/SKLAND_INTEGRATION.md)，修改登录入口或发布新版本前请核对。
