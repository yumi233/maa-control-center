# MAA Control Center

轻量、独立部署的 MAA Web 管理中心。NAS 负责可视化与调度，Windows 继续运行现有 MAA 和模拟器。

**v0.1.0：只读接入 MVP。** 支持真实 Windows Agent 心跳、多配置档、原有任务顺序、库存目标、独立基建排班与日志。实际任务下发、配置写入、自动调度和自动搓玉保持关闭，详细范围见[验证与限制](docs/verification.md)。

## 设计与技术

- Vue 3 / TypeScript / Vite 响应式前端
- Go HTTP API / SQLite WAL
- Go Windows Agent，只读发现与心跳
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
- [验证记录与未完成项](docs/verification.md)

## 接入原则

账号、MAA 安装、配置档与模拟器分别管理。一个 MAA 可以包含多个配置档，不假设每个账号独占进程。现有基建文件分别只读，不覆盖；没有账号归属的仓库缓存不作为实际库存展示。

首版只有单管理员。首次启动在新 SQLite 数据库中建立账号，密码使用 bcrypt。Agent 使用独立配对令牌，公网与非回环访问要求 HTTPS。没有任意路径写入或远程 Shell 接口。

## 许可

本项目使用 [MIT](LICENSE)；字体及上游组件许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。本项目与游戏及其开发商无关联，不包含游戏官方素材。
