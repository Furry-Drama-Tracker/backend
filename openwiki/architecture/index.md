# 文件

- [配置与环境变量](configuration.md) - neo-server 如何加载默认值、按 OS 区分的 ini、可选 .env 与真实环境变量，哪些值是密钥，以及 IsDev、启动致命校验与安全告警如何影响运行时行为。
- [架构总览](overview.md) - neo-server 的分层包结构、从 main 开始的显式依赖注入链、各 internal 包的职责，以及文档所述分层比理想情况更松散的地方。
- [持久化与数据模型](persistence-and-data-model.md) - neo-server 如何在不使用 ODM 的情况下使用官方 MongoDB 驱动、集合与 BSON 模型如何组织、ID 与错误约定，以及启动时接线的索引与迁移如何容错执行。
- [请求生命周期与中间件管线](request-lifecycle.md) - 固定的 Gin 中间件顺序、请求体解析与清洗、CSRF 校验、限流挂载语义、单前缀路由、静态上传，以及错误实际如何到达客户端。
