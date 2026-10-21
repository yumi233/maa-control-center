# MAA 能力矩阵

v0.2 新增经过差异审批的 MAA 6.20 配置同步、备份校验与受控回滚，以及 OpenAI 兼容配置助手和 TG/QQ 通知。兼容字段与限制见 [Agent 模式](agent-mode.md)。v0.4 已接入直接编辑、多账号日/周计划及 Windows 实际执行适配。下表为历史 v0.1 状态；当前操作能力以[配置与定时](configuration-and-scheduling.md)及[验证记录](verification.md)为准。自动搓玉仍未启用。

| 能力 | v0.1 实现与证据 | 真实控制状态 |
|---|---|---|
| 安装与版本识别 | 明确安装目录，读取 MAA.exe 版本；不匹配 MaaEnd | 只读 |
| 多配置档 | 解析已观察到的 gui.new.json / Configurations / TaskQueue | 只读 |
| 药剂 / 源石 | 仅汇总启用任务中有效预算；全局显示开关不等同于消耗预算 | 只读，不覆盖主号 |
| 临期药 | 读取现有任务的 UseExpiringMedicine；启用时显示 48h | 只读 |
| 基建排班 | 仅读取明确允许目录中的已存在 JSON；按配置档隔离；未知字段保留 | 只读 |
| 库存保持 | 读取现有材料、关卡、优先次序、目标；材料名来自本机 item_index | 当前数量未验证归属，保持 null |
| 日志 | 读取最多 64KiB 的文件尾，上传最多 60 行；凭据、URL 和本地路径过滤 | 只读 |
| 运行状态 | 心跳独立于 MAA 进程；超过 65 秒判定 Agent 离线 | 不用进程存在推断任务 running |
| 官方协议 | LinkStart、Base、Recruiting、StopTask、HeartBeat、CaptureImageNow 类型与 Mock 测试 | 生产端点返回 423 |
| SUCCESS | 协议接受不升级为游戏任务已成功 | Mock 已测试 |
| StopTask | 停止请求保持 stopping，需要心跳确认 | Mock 已测试，生产未启用 |
| 配置修改 | 原值 / 新值预览；副号硬约束校验 | 保存端点拒绝写入 |
| 自动搓玉 | 展示条件与未验证状态 | API / 页面均不支持自动切换 |

参考：[官方远程控制协议](https://docs.maa.plus/zh-cn/protocol/remote-control-schema.html)、[库存保持](https://docs.maa.plus/zh-cn/manual/introduction/depot-maintain.html)、[基建排班](https://docs.maa.plus/zh-cn/protocol/base-scheduling-schema.html)。文档只说明协议，不能替代对已安装 GUI 的验证。

库存预检可能使用旧缓存，不能用尚未验证来源的数据来证明计划达标。MAA 配方切换与安全重载尚未验证，不将 product 字段视为执行能力。
