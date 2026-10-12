# 网关可观测性

## 从问题找到调用

用量页与管理员调用页先选时间窗，再按结果、协议、模型、Key、标签、耗时/token/实际扣费范围、流式、尝试次数、上游状态/错误、交付、用量观测或记账筛选。分析与调用列表使用同一筛选；点击分组或时间桶下钻，返回按钮清除下钻。按最终渠道分组的是请求集合，其尝试成功率包含该集合经过的全部上游；要排查具体失败尝试，使用「尝试渠道 + 状态/错误」筛选，多个条件要求命中同一次尝试。管理员可以再按账户拆分。

分析直接从原始调用求分位数，不平均已聚合分位数。空时间桶保留，计数为 0、分位为未知。分析限制 366 天、最多 1000 分组及约 1000 时间桶，超出时缩短时间或增加筛选；不会只取前 N 组冒充全部。实时模式保持原时间窗长度并将结束时间向现在推进；按时间下钻后固定选定区间。前期取紧邻当前窗口的等长区间，保留其余筛选。

## 口径与未知值

| 项目 | 口径 |
| --- | --- |
| 最终成功率 | succeeded 与 succeeded_unbilled / 已结束调用；不含 in_progress |
| 首次成功率、回退发生率 | 以实际有尝试且已结束的调用为分母；拒绝调用不混入 |
| 尝试成功率 | completed 尝试 / 全部尝试；与请求成功率分开 |
| 回退挽回率 | 最终成功的多尝试调用 / 多尝试调用 |
| 完整交付率 | complete / 已开始且有交付观测的调用；旧记录与 not_started 不入分母 |
| 首响应 | 首个响应体字节，可能是元数据；分位只收流式调用 |
| 响应总耗时、网关额外耗时 | 站内 duration_ms 截止响应读写结束，不含后续计价与结算；额外耗时减去各次上游尝试 |
| 处理总耗时（Prometheus） | 直到 handler 退出，包含计价与结算；额外耗时减去各次上游尝试 |
| 速度 | 流式用输出 tokens / 首响应之后的时间，低分位反映慢速尾部；非流式详情为端到端平均速度 |
| 帧间隔 | SSE JSON data 帧（非 SSE 流为读取块）的间隔，不是 token 间隔；每次调用 p50/p95 最多取前 20 万个间隔，最大停顿覆盖全部间隔 |
| 阶段 | 鉴权、请求准备、打开记录、准入、路由、计价、结算、客户端写入；写入含 flush 且与上游交换重叠，不能直接相加 |
| 回退额外耗时 | 最后一次尝试之前的全部尝试耗时；无回退的调用不入耗时分位 |
| 费用 | 实际扣费/手续费只计有账本交易的调用；未计费、自用、免费与拒绝为 0，单次费用分位只收已结束调用 |
| 用量 | 非缓存输入、输出、缓存读/写分开；reported 是读到了 usage，并不保证厂商报告完整；parse_limit 是超出解析上限且没读到 usage |

中断或断连仍保留已观察用量，可能发生结算；HTTP 200、模型停止、完整交付和记账是不同事实。详情可从账本交易跳到自己的分录或管理员交易详情。NULL/— 表示历史未采集或没有样本，不表示 0。上游响应 ID 仅用于关联，平台不保存请求/响应正文。

## 内网监控

可选组件使用官方镜像；服务默认关闭，显式合并文件后启动。Grafana 的口令只在首次初始化其数据卷时生效，已有部署的口令通过 Grafana 管理。

```bash
export GRAFANA_ADMIN_PASSWORD='<独立的强口令>'
docker compose -f compose.yaml -f compose.observability.yaml up -d prometheus grafana
```

Grafana 位于 `http://127.0.0.1:3100`（账号 admin），自动配置数据源与「AIHub · 网关观测」看板。远程机器用 SSH 隧道访问回环端口。Prometheus 在容器网络抓取 `backend:9090`、保留 15 天；不发布抓取端口。停止组件不删除卷：

```bash
docker compose -f compose.yaml -f compose.observability.yaml stop prometheus grafana
```

看板按协议、模型、渠道、流式筛选，含请求结果、首次成功、回退、首响应/总耗时/内部阶段、慢速尾部、停顿、错误、交付、用量、缓存、费用、字节和观测健康。请求级、入口级和全局健康面板只使用其具有的标签，标题/图例说明范围。分位由 Prometheus 直方图估计，与站内原始样本精确分位可能不同。

核心指标在调用结束时同步累加，不查库、不走可丢事件流；运行中并发单独计数。只有目录内模型成为 model 标签，其他为 other；channel 使用已配置渠道 ID。账户/Key/标签/请求/响应 ID/上游原始错误不作为标签，细分问题在权限受控的站内分析解决。渠道 × 模型 × 协议 × 流式的直方图会增加系列数量，可在 Prometheus 用 metric_relabel_configs 丢弃不需要的直方图，避免添加高基数标签。

故障排查顺序：

1. 看 `up{job="aihub"}`，抓取不通与业务没有请求分开判断。
2. 看 `time()-aihub_metrics_last_refresh_timestamp_seconds`（正常约 60 秒内）及 `aihub_metrics_refresh_failures_total`。刷新失败保留旧值，不能将旧值当当前健康；首次成功前时间戳为 0。
3. 看 `aihub_gateway_events_dropped_total`、`aihub_live_messages_dropped_total`、`aihub_live_read_failures_total`。它们解释实时视图不完整，不影响核心完成计数；站内筛选刷新从库读取。
4. 看 `aihub_http_requests_total` 的状态码与 `aihub_attempt_errors_total` 的原因；鉴权失败没有持久化调用，但计入口指标。400/401 的模型标签可能为 other。
5. 看 `aihub_settlement_failures_total` 与 `aihub_reconciliation_failed`，按[事故手册](incident.md)核对。`aihub_accounting_total{result="not_booked"}` 还包含免费、自用和拒绝，不能直接当结算故障率。

进程重启清空计数与在途 gauge，Prometheus rate 会处理计数器重置，但抓取中断可能损失增量；数据库才是调用/账务长期事实。未接 OTel、正文采集或推送告警。

配置格式参考 [Grafana provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/) 与 [Prometheus getting started](https://prometheus.io/docs/prometheus/latest/getting_started/)。
