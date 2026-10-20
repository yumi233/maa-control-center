# 设计记录

变体：standard / yellow / neutral / tight / plain / standard / calm。
上游参考版本：hhikr/endfield-ui-skill，提交 `96702a3`。
主视觉：NAS → Windows Agent → MAA → 模拟器的实际连接关系。
页面类型：总览采用控制台，其余采用任务导向的列表与表单。

依据用户指定的 [Endfield UI skill](https://github.com/hhikr/endfield-ui-skill)，复用其基础 CSS 与 token。侧栏组织实际功能，账号按配置档分开；顶部展示真实计数，连接图将未知状态明示为待确认。与模板不同之处包括实际设备关系、双账号策略与独立基建视图。

字体为自托管 Saira，中文使用系统黑体，完整许可证随应用分发。没有使用游戏官方立绘、标志、截图或字体。

演示入口带固定横幅，数据与操作只保存在当前页面，不调用真实写入 API。真实环境不自动降级成演示数据。
