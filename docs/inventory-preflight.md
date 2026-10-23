# 多账号库存预检与理智汇报

MAA 6.20.1 会在开始唤醒、切号和仓库识别前构建整条队列。库存保持以共享的 `data/DepotData.json` 预检，已被旧数据判为充足的计划不会下发；切号后识别出缺货，也只能记录“预检时已跳过的计划无法在运行中添加”。

Control Center 从 0.5.1 起，在每轮 daily 任务正常关闭空闲 MAA、校验原配置并保存备份后，将共享缓存原样移动到同目录 `DepotData.maacc-before-<job-id>.json`。新进程不载入上一账号库存，因此预留所有可执行计划；原生仓库识别在切号后取得本账号库存，每个作战计划开始前再重算缺口。药品、源石、临期药开关、目标和关卡都来自原配置，不添加补刷任务。

启用库存保持的配置必须启用 `UpdateDepot`。缺失或关闭时拒绝执行并提示，避免拿无归属的缓存替代本次识别。缓存备份存在时不覆盖；路径异常或备份失败时不启动。单纯 check 任务不移动缓存。备份保留在 Windows 本机，不上传原始仓库数据。

依据：[MAA 6.20.1 库存保持实现](https://github.com/MaaAssistantArknights/MaaAssistantArknights/blob/v6.20.1/src/MaaWpfGui/ViewModels/UserControl/TaskQueue/DepotMaintainTaskUserControlModel.cs)，以及 [仓库缓存载入](https://github.com/MaaAssistantArknights/MaaAssistantArknights/blob/v6.20.1/src/MaaWpfGui/ViewModels/UI/ToolboxViewModel.cs)。

理智目前来自本轮 MAA `SanityBeforeStage` 回调。没有回调时明确显示未知，不使用上一账号、上一轮数据，也不把自然恢复估算当作当前实测。完整日志里没有结算时报告 0 次结算；捕获旧库存漏刷警告时明确提示补货未完成。修复部署与下一轮真实游戏执行是两项不同验证。
