# 应用性能数据采集方案

状态：设计方案，尚未实现性能数据采集代码。

本文按采集的数据类型说明各类数据的含义及对应采集方式。基础指标用于观察变化趋势，Profile 用于定位具体函数或堆栈，Trace 用于拆解一次请求的执行过程。整体可观测性设计见[日志与可观测性体系设计](application-log.md)。

采集能力主要来自 [Go 运行时诊断](https://go.dev/doc/diagnostics)和 [Prometheus Go Collector / Process Collector](https://prometheus.io/docs/guides/go-application/)。

| 数据类型 | 这些数据是什么、能看出什么 | 对应采集方案 |
| --- | --- | --- |
| **进程 CPU 使用情况** | 进程消耗了多少 CPU，是否持续处于高负载 | Process Collector 采集累计 CPU 时间，计算时间窗口内的使用情况；Prometheus 保存历史 |
| **CPU 热点函数** | 哪些函数消耗 CPU 最多，各占多少比例 | 按需使用 **pprof CPU Profile** 采样，例如采集 30 秒，查看函数排行、调用图和火焰图 |
| **进程内存占用** | 整个进程实际占用的物理内存，即 RSS，观察是否持续增长 | Process Collector 采集 RSS；Prometheus 保存趋势。RSS 与 Go 堆内存是不同口径 |
| **Go 堆内存** | Go 对象占用了多少堆内存，哪些代码分配的对象仍占用内存 | Go Collector 采集堆内存指标；**pprof heap** 定位分配来源，对比不同时间的 Profile |
| **内存分配** | 程序分配内存的速度，以及哪些函数频繁创建对象 | Go Collector 观察累计分配量及增长速率；**pprof allocs** 分析分配热点 |
| **垃圾回收 GC** | GC 次数、暂停时间等，观察回收频率和延迟影响 | Go Collector、`runtime/metrics` 持续采集；复杂问题再使用运行时执行追踪 |
| **Goroutine 数量** | 当前有多少个 Goroutine，数量是否随请求结束回落 | Go Collector 持续采集 Goroutine 数量，保存变化曲线 |
| **Goroutine 堆栈** | 各 Goroutine 正在执行或等待什么，用于调查卡住、数量持续增长等问题 | 按需抓取 **pprof goroutine**，查看调用栈及等待状态 |
| **阻塞等待** | Goroutine 在 channel、同步原语等位置阻塞了多久 | 开启 `runtime.SetBlockProfileRate` 采样，通过 **pprof block** 查看阻塞位置 |
| **锁竞争** | 哪些锁存在竞争，竞争带来了多少等待 | 开启 `runtime.SetMutexProfileFraction` 采样，通过 **pprof mutex** 分析竞争来源 |
| **运行时调度** | Goroutine 如何被调度，何时运行、等待，以及 GC、系统调用等事件 | 短时采集 **runtime execution trace**，使用 `go tool trace` 查看时间线 |
| **接口及业务步骤耗时** | 一次请求、一个业务步骤实际经过多久，包含计算和远端等待 | 在 HTTP 入口和业务步骤记录耗时 Histogram；需要查看某一次请求的执行过程时使用 Trace Span |
| **外部调用耗时** | Qwen、图片服务等依赖分别耗时多久，是否失败或超时 | 在 `llm` 公共调用层、company HTTP 公共层记录调用次数、结果、耗时；通过 Trace 关联具体请求 |
| **数据库性能** | SQL 耗时、连接池使用量、连接等待次数及等待时间 | 在 GORM 调用边界记录 SQL 耗时，通过 `sql.DB.Stats()` 采集连接池数据 |

CPU、heap、allocs、goroutine、block、mutex 的具体采集入口由 [Go pprof](https://pkg.go.dev/net/http/pprof) 提供；block 和 mutex 需要额外启用采样。运行时执行追踪用于分析调度和 GC 等事件，与通过 Span 拆解业务调用的请求 Trace 分别使用。

对应当前头像接口：要知道“Qwen 用了多久、图片生成用了多久”，采集调用耗时；要知道“本服务哪些函数消耗 CPU 或内存”，采集 CPU、heap、allocs Profile。两类数据需要分别采集。
