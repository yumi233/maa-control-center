# MAA Control Center

轻量、独立部署的 MAA Web 管理中心。NAS 负责可视化、配置方案和通知，Windows 继续运行现有 MAA 和模拟器。

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
