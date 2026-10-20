# MAA Control Center

轻量、独立部署的 MAA Web 管理中心。NAS 负责可视化与调度，Windows 继续运行现有 MAA 和模拟器。

**开发中：首先交付只读 MVP。** 未验证的账号绑定、任务控制和配置写入默认关闭。演示数据与真实数据明确隔离。

## 设计与技术

- Vue 3 / TypeScript / Vite 响应式前端
- Go HTTP API / SQLite WAL
- Go Windows Agent，只读发现与心跳
- 使用 [Endfield UI skill](https://github.com/hhikr/endfield-ui-skill) 的设计规范
- MIT 开源；自托管 Saira 字体遵循 SIL OFL 1.1

## 隐私

本仓库不包含真实账号、内网地址、生产配置、数据库或令牌。生产环境信息放在被 Git 忽略的本地配置中。

## 开发状态

初始化仓库后开始实现。实际已验证范围、启动方式、限制与部署说明将随首版代码更新。
