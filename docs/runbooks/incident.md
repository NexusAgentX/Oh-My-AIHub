# 故障处理 Runbook

面向管理员的异常处置。核对口径与不变量见 `ARCHITECTURE.md` 的“数据与不变量”和“可观测性”。

## 入口

1. 管理后台概览 `/admin` 的“需要处理”：申诉、超出信用额度或长期负余额的账户、信用集中、渠道失败率、读不到用量的成功调用（全站一条，另按账户列出未计费比例过高的消费者）、账本不平与核对失败。点击条目跳到对应页面。
2. 积分页 `/admin/points#checks`：五项实时核对逐项展示；`#risks` 为信用风险。
3. 调用页 `/admin/calls`：按请求 ID、渠道、结果筛选，查看尝试时间线与价格快照。
4. Prometheus（内网 `:9090`）：`aihub_ledger_imbalance` 应恒为 0，`aihub_reconciliation_failed{check}` 应为 0。

## 常见情形

### 账本不平（余额合计不为 0，或余额与分录不一致）

- 数据库约束应阻止此类状态，出现即说明约束被绕过或数据被手工修改。
- 先备份保留现场，记录发现时间，检查该时段内的部署、迁移与管理员调账审计。
- 需要更正时由维护者决定，通过管理员调账（必须填写原因并写审计）完成，不直接改库。

### 成功调用未记账（核对 ④）

- 在 `/admin/points#checks` 逐条“补记”（按用量与价格快照记账，与正常记账同一幂等键，重复补记无效）或“作废”（标为不收费的中断）；两者都写审计 `ledger.repair_call`。
- 同一渠道持续出现时，先在 `/admin/channels/{id}` 强制下架再排查。

### 成功调用读不到用量（未计费）

- 网关读不到用量时调用记为 `succeeded_unbilled`，不收费，共享者照付上游费用。概览会出现全站一条“用量未读到”；某账户近 24 小时成功调用不少于 20 次且其中未计费不少于 20% 时，另出现“账户用量未读到”，标题写明未计费最多的渠道。
- 点击条目进入该账户的未计费调用，打开调用详情查看尝试时间线。调用页默认看 7 天，条目统计的是近 24 小时。
- 判断方向：未计费集中在一个渠道，且其他账户经该渠道的调用也读不到用量，多半是渠道上游的响应形态（非标准流、压缩等），查看渠道详情，必要时联系共享者或强制下架。同一渠道上只有这个账户出现，多半与该账户的请求方式（格式、流式或非流式、客户端）有关，向账户确认用法。
- 只提示，不自动停用账户或渠道；处置由管理员决定。

### 渠道持续失败

- 渠道事件「4xx 异常」（`anomalous_client_errors`，原因写模型与 4xx 次数）表示该渠道在这个模型上几乎每次都返回 4xx、而同模型其他渠道没有：网关已把它在这个模型上排到最后，但没有排除。先看渠道详情的失败分布与最近失败的上游原始错误，常见原因是上游模型名填错或上游不认请求里的某个参数；也可能是客户端的请求本身有问题，这时其他渠道也会出现同样的 4xx，降级会自动解除。渠道修好后，让店主保存一次渠道（改设置或渠道模型，或在测试渠道时应用格式探测结果）即可：渠道被编辑的那一刻，它此前的 4xx 统计清零，降级立即解除，重启后端也不会把编辑之前的 4xx 找回来（重启时只统计编辑之后开始的调用）；管理员下架后恢复同样会清零。没有人编辑时，旧 4xx 要等它们最长 24 小时滑出窗口，重启后端不会清掉它们（重启从调用记录重建同一个窗口）。保存之后若上游仍然不对，攒够 20 次新的尝试会再次降级并再记一条事件。
- 网关会自动冷却连续失败的渠道，以及上游 429 用 `Retry-After` 要求等待的渠道（渠道事件原因为 `rate_limited`）。渠道详情可查看按状态码的失败分布与最近失败的上游原始错误。
- 需要时强制下架（填写原因），排查完成后恢复。

### 上游曾在错误里回显渠道 Key（ADR-0041 之前的排查）

ADR-0041 之前，网关把上游的错误应答原样交给消费者；上游若在错误里回显了收到的 Key，消费者就读到了渠道主的上游 Key。修复只管此后，此前是否发生过只能查调用记录。

- 何时执行：尽快执行一次，修复版本部署前后都可。证据只留 30 天：生产自 v0.7.0（2026-10-08）重建，最早的原始错误文本约从 2026-11-07 起被逐日清空。
- 证据位置：`calls.attempts`（JSONB 数组，一次尝试一个元素）里的 `error_message`，渠道在 `channel.id`、`channel.name`。写入前网关把本次尝试所用的 Key 换成 `***`，Key 没出现时不写占位符；最多存 4 KB；后端每天（和每次启动）把 30 天前的文本置空。
- 执行方式：沿用备份脚本进入数据库的做法（数据库容器内的 `psql`），不新增访问方式。SQL 开头两条 `SET` 让会话只读并限时；输出只有渠道、所有者用户名、次数与时间，不含 Key、密文或错误正文。

```bash
# 把下面的 SQL 存成 leaked-key-check.sql 后执行；容器名用 docker ps 查看
ssh hk-vps 'docker exec -i <数据库容器名> psql -U oh_my_aihub -d oh_my_aihub -v ON_ERROR_STOP=1' < leaked-key-check.sql
```

```sql
SET default_transaction_read_only = on;
SET statement_timeout = '2min';

WITH attempts AS (
    SELECT c.id AS call_id, c.created_at, c.outcome, t.ord,
           t.a ->> 'end_reason' IN ('upstream_error', 'client_error') AS answered,
           position('***' IN coalesce(t.a ->> 'error_message', '')) > 0 AS echoed,
           t.a -> 'channel' ->> 'id' AS channel_id,
           t.a -> 'channel' ->> 'name' AS channel_name
    FROM calls c
    CROSS JOIN LATERAL jsonb_array_elements(c.attempts) WITH ORDINALITY AS t(a, ord)
    WHERE c.created_at >= now() - interval '30 days'
      AND c.attempts::text LIKE '%***%'
), marked AS (
    SELECT *,
           -- 以上游错误结束的调用，消费者收到的是最后一个有上游应答的尝试
           (outcome = 'upstream_failed' AND answered
            AND ord = max(ord) FILTER (WHERE answered) OVER (PARTITION BY call_id)) AS returned
    FROM attempts
)
SELECT m.channel_id,
       coalesce(ch.name, max(m.channel_name)) AS channel_name,
       ch.deleted_at IS NOT NULL AS channel_deleted,
       o.username AS owner_username,
       count(DISTINCT m.call_id) AS echoed_calls,
       count(*) FILTER (WHERE m.returned) AS returned_calls,
       min(m.created_at) AS first_seen,
       max(m.created_at) AS last_seen,
       (array_agg(m.call_id ORDER BY m.created_at DESC))[1] AS latest_request_id
FROM marked m
LEFT JOIN channels ch ON ch.id::text = m.channel_id
LEFT JOIN accounts o ON o.id = ch.owner_id
WHERE m.echoed
GROUP BY m.channel_id, ch.name, ch.deleted_at, o.username
ORDER BY returned_calls DESC, echoed_calls DESC, last_seen DESC;
```

`echoed_calls` 是近 30 天里错误文本带占位符的调用数；`returned_calls` 是其中带占位符的尝试正是交给消费者的那条应答（换渠道后成功的调用，早先的错误应答没有交给消费者）；`first_seen`、`last_seen` 是调用创建时间。

结果处理：

- 0 行：在 Issue 或发版 PR 的描述里记一行（执行日期、窗口 30 天、结论“0 行”）。局限仍然成立，不写成“确认没有泄露”。
- 有行：用 `latest_request_id` 在 `/admin/calls` 打开调用详情复核。占位符独自代替一整个 Key（如“provided: ***”）才是回显；紧贴字母数字（如 `sk-***abcd`）多半是上游自己的掩码，排除。修复版本部署后，应答里的 Key 已被抹掉，只看修复前的调用：在 CTE `attempts` 的 `WHERE` 里追加 `AND c.created_at < '<修复版本部署时间>'`。
- 确认回显且 `returned_calls` 大于 0：通知该渠道主（`owner_username`；已删除的渠道也一样）在上游作废旧 Key、生成新 Key，再到渠道设置里更新 Key；更新写库后网关立即弃用旧 Key 的缓存，下一次尝试起使用新 Key。`returned_calls` 为 0 时 Key 只留在调用记录里，是否通知由维护者决定。
- 在同一处记录脱敏结论：受影响渠道数、通知日期、渠道主是否已更新 Key；不写 Key，也不贴错误正文。

局限：

- 只覆盖错误路径，且只有存下来的部分：请求自身的 4xx 透传（400、422 等）不存错误文本；超过 4 KB 的部分没存；成功响应不在范围（ADR-0041 决定 7）；30 天前的文本已清空。
- ADR-0041 之前的抹除只识别 Key 原样和 URL 查询编码。写成 JSON 转义（`\/`、`\uXXXX`）或路径编码的回显不留占位符，这个查询看不到；Key 加密保存，数据库里无法比对。这类文本里仍是转义后的 Key，管理员能在调用详情里看到。
- `***` 可能是误报：上游自己的掩码、Markdown 强调等。
- `returned_calls` 由调用结局推断，不是消费者实际收到的字节。

### 改了数据库，网关还按旧数据处理

- 网关从进程内快照读取 Key、目录模型、渠道与路由偏好，从进程内缓存读取账号的余额与信用额度（ADR-0040）。管理后台和用户界面的操作（含调账、坏账核销、C2C、信用额度修改）返回时快照与缓存已更新；绕过后端直接改库（手工 SQL、迁移）的数据，要等定期重建与重新加载，最长 60 秒。
- 需要立即生效时重启后端，会中断在途调用，不收费（ADR-0039）；能用管理后台完成的操作（停用 Key 或账户、强制下架渠道）优先用后台，返回即生效。

### C2C 托管不一致（核对 ③）或放行未记账（核对 ⑤）

- 数据库 CHECK 与状态机应阻止此类状态。出现即保留现场，暂停相关卖单操作，把卖单与交易详情交维护者核对。

### 申诉

- 在 `/admin/disputes` 判给买家或退回卖家，必须填写原因，写审计 `c2c.resolve`。

### 卡住的调用

- 后台每分钟把超时仍为 `in_progress` 的调用标为 `interrupted`，通常无需处理；持续出现时保留调用 ID 与日志交维护者。

## 升级与回滚

- 30 分钟内无法解释的账本或核对异常：先备份保留现场，按发布 Runbook 回滚到上一个已知良好版本，再用恢复演练确认备份可用。
- 处置完成后在 Issue 中记录脱敏时间线。
