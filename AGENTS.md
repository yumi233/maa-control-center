# Repository maintenance notes

## 森空岛公共认证入口

本仓库包含 AstrBot 森空岛插件的公共网页绑定功能。修改前端入口、登录流程、API 路由或部署方式前，先读 [集成说明](docs/SKLAND_INTEGRATION.md)。

- `/skland`、`/skland/`、`/#/skland` 无需 MAA 管理账号，不能回退到管理面板登录页。
- 保留 `SklandBinding.vue`、入口分流、`s.sklandRoutes(m)` 和专用接口代理测试。
- 部署保留 `MAACC_SKLAND_UPSTREAM` 与 AstrBot Docker 网络；管理鉴权保持独立。
- 发版必须用未登录浏览器检查公共页面及 JSON API，不能只检查主站或健康接口。
- 不在网页、日志、URL 或 Git 中保存用户 Token。不要用未包含该功能的旧快照覆盖新版本。
