# 发版 Runbook

适用范围：ai.isok.dev 生产实例的常规发版、重跑与回滚。生产链路结构、密钥与运行时事实见
`deployment.md` 与个人运维仓库 `remote-hosts/hk-vps/sites/oh-my-aihub/`；本文只讲操作步骤。

## 本轮市场切换的发版前置

包含 Epic #487 的版本，先在最终集成候选上完成[集中性能 A/B](gateway-baseline.md#供应商分组重构的执行时点epic-487)，证据写发版 PR。开发 PR、Feature 合并和 F12 开发收尾不运行该 A/B；当前仍待发版前执行，不能记为已通过。

首次清理旧市场配置的真实环境与切换时点由维护者另行决定。执行前应按[静止窗口保全核验](backup-restore.md#市场切换的只读保全核验)排空并保留证据，安装并核验个人运维仓库的结构兼容与回滚保护；具体镜像声明、失败边界和自举约束见该手册。不得仅因本仓库代码或核验工具就绪而直接切换。

## 常规发版（vX.Y.Z）

1. 确认目标提交已合入 `main` 且 PR 门禁（`ci.yml`）通过。
2. 开一个发版 PR，在 `CHANGELOG.md` 写入 `## vX.Y.Z - YYYY-MM-DD` 章节：合并「未发布」中已有的条目，
   并按上个正式 tag 以来合并的 PR 补齐（`git log --first-parent --merges --format='%s%n  %b' <上个 tag>..origin/main`
   列出 PR 编号与标题），保留新的空「未发布」章节。平时的 PR 不改 CHANGELOG。版本号用语义化 `vMAJOR.MINOR.PATCH`，
   预发布加 `-rc.1` 等后缀。小改动升 PATCH，大改动升 MINOR，明确确认的大版本升级升 MAJOR；具体判定、清零及 0.x 规则见 [AGENTS.md](../../AGENTS.md#发版)。
3. 在 main 合并提交上打 annotated tag 并推送：

   ```bash
   git fetch origin
   git switch main && git merge --ff-only origin/main
   git tag -a vX.Y.Z -m 'vX.Y.Z'
   git push origin vX.Y.Z
   ```

4. `release` workflow 自动执行，无需干预：门禁（复用 `main` 上该提交的 CI 结果，缺失时执行 `mise run check-release`）→ backend/frontend
   多架构镜像构建推送 GHCR（tag + digest）→ 从 CHANGELOG 章节生成 GitHub Release →
   Deploy 作业进入 `production-hub` Environment **等待审批**。
5. 审批上线（二选一）：
   - 网页：Actions → 对应 `release` run → *Deploy production* → *Review deployments* →
     Approve。
   - 命令行（本仓库 Environment 允许触发者本人审批）：

     ```bash
     gh api repos/NexusAgentX/Oh-My-AIHub/actions/runs/<run_id>/pending_deployments \
       --jq '.[0].environment.id'
     gh api -X POST repos/NexusAgentX/Oh-My-AIHub/actions/runs/<run_id>/pending_deployments \
       --input - <<'JSON'
     {"environment_ids": [<上一步的 id>], "state": "approved", "comment": "vX.Y.Z"}
     JSON
     ```

6. 部署脚本（VPS `/usr/local/sbin/oh-my-aihub-github-deploy`）自动完成：部署前加密备份 →
   Compose 镜像按 `tag@digest` 切换 → `up -d` → 等待 database/backend healthy →
   本机与公网 `/api/health` 烟测；任一步失败自动回滚到上一版 Compose。
7. 人工验收：Actions 作业全绿；打开 `https://ai.isok.dev` 抽查本次变更；
   管理员确认 `/admin/points` 的实时账本核对无异常。

## 重跑 / 回滚

对既有 tag 重新部署，不重新构建，直接复用已发布镜像 digest（同样需要审批）：

```bash
gh workflow run release.yml --repo NexusAgentX/Oh-My-AIHub -f tag=vX.Y.Z
```

或在 Actions 页面 *Run workflow* 填入 tag。数据库迁移随部署自动执行；回退前先核对旧镜像与当前结构是否兼容，不能在不兼容结构上启动旧镜像，也不能因健康检查失败自动恢复旧整库。已经产生新账目或其他需保留写入时采用前向修复，不得以部署前备份覆盖新交易。只有维护者明确决定适用环境，并核对没有后续需保留写入时，才按备份恢复 Runbook 单独执行恢复。

### v0.22.0 撤回后的迁移基线

2026-10-16，维护者确认生产仅本人使用，明确授权作废并复用 v0.21.0 之后的迁移编号。
本次回退移除 0015、0016 以及未发布的 0017；唯一生产实例恢复至升级 v0.22.0 前的
`20261016T092727Z` 备份（迁移版本 14），运行 v0.21.0 镜像。恢复前保留当前完整备份，
恢复后清除旧会话并核对账本。

此例外仅适用于上述已恢复的生产实例及可丢弃的开发、测试数据库。仍需保留 0015 及以后
数据的其他数据库不适用，不能通过修改版本记录适配。其余已发布迁移继续只允许追加。

维护者同时明确授权撤销原 v0.22.0 Release 与 tag，下次仍从 v0.22.0 发布。
撤销后最新正式 tag 及迁移守卫、升级验证的基线均回到 v0.21.0；无需对检查脚本添加
版本例外。原提交继续保留在 Git 历史中，本次授权不扩展到其他正式版本。

### 0015 定价清理升级

从当前生产的迁移 14 升级时，先让旧版本完成所有未结算 Responses 后台任务，再部署。
0015 在事务中检查 `gateway_background_jobs.settled_at IS NULL`；存在未结算任务便中止，
不修改配置或迁移版本。继续由旧版本跟踪、结算这些任务后重试，避免跨版本改变开始时价格。

0015 删除模型及条件档地域倍率、输入/输出的 file 声明，以及含百炼思考条件的整档
（包括同时带 token 或时间条件的档位，避免移除条件后扩大匹配范围）。仅声明 file 的
模态列表回到默认 text；其余模态顺序保留。已有 openai:priority 条件改为 openai:fast，
保留其他条件及档位相对顺序；若原本也有 fast，仍按首个命中档结算。
历史调用、价格快照、交易、余额与账本不改写。

该迁移不可逆。部署前确认加密备份有效；需要回滚到迁移 14 的旧版本时，必须先恢复
部署前备份，不能只切换镜像或修改迁移版本记录。

## 紧急手动部署（GitHub 不可用时）

受限发布脚本支持直接带参数调用，行为与 CD 完全一致（备份、切换、验证、失败回滚）：

```bash
ssh hk-vps '/usr/local/sbin/oh-my-aihub-github-deploy check v0.1.0 <backend-digest> <frontend-digest>'
ssh hk-vps '/usr/local/sbin/oh-my-aihub-github-deploy deploy v0.1.0 <backend-digest> <frontend-digest>'
```

digest 从 GitHub Release 页或 GHCR（`docker buildx imagetools inspect
ghcr.io/nexusagentx/oh-my-aihub-backend:vX.Y.Z`）获取。

## 约束

- 生产 Compose 永远 `tag@digest` 固定，不追 `latest`；镜像只能经发布脚本切换。
- 发版不触碰 VPS 上的 `.env`、`backup.env` 与密钥环。
- 部署前备份自动生成在 `/data/oh-my-aihub/backups`，保留 7 天；密钥环与备份的配套
  关系见备份恢复 Runbook。

## v0.7.0 → v0.8.0 数据库前置步骤

本次定价字段已直接加入数据库基线，既有数据库不会重新执行基线。生产先运行
`sudo systemctl start oh-my-aihub-backup.service`，确认服务 Result=success、
ExecMainStatus=0（备份脚本同时验证解密及 pg_restore 清单），再执行以下事务。
只适用于已确认缺少这些列的 v0.7.0 数据库；保留全部现有数据，旧镜像可以继续运行。

```sql
BEGIN;
SET LOCAL lock_timeout = '5s';
ALTER TABLE models ADD COLUMN token_prices jsonb NOT NULL DEFAULT '{}'::jsonb
  CHECK (jsonb_typeof(token_prices) = 'object');
ALTER TABLE model_price_tiers
  ADD COLUMN token_prices jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(token_prices) = 'object'),
  ADD COLUMN service_tier text NOT NULL DEFAULT '',
  ADD COLUMN thinking_mode text NOT NULL DEFAULT '';
COMMIT;
```

核对列存在后再批准部署。若事务失败则停止发布并检查原因；镜像回滚时可以保留这些附加列。

## v0.9.1 → v0.10.0 数据库升级

论坛四张表由 `0002_forum.sql` 追加，`0001_baseline.sql` 保持 v0.9.1 的结构。新空库依次执行两份脚本，已应用版本 1 的生产库只执行版本 2；无需手工建表或修改 `goose_db_version`。

发布脚本先执行并验证加密备份，随后 Compose 的一次性 `migration` 服务运行 Goose。论坛迁移在事务中执行，锁等待最多 5 秒、语句最多 60 秒；失败时不记录完成版本，后端不会通过 Compose 依赖门禁启动。检查原因后可重试发布，不跳过迁移错误。

部署后确认迁移版本为 2，`forum_boards`、`forum_topics`、`forum_replies`、`forum_attachments` 及其约束和索引存在，再核对服务健康、论坛会话门禁、静态编辑器资源与实时账本。板块初始为空，由管理员按需创建。

本次仅添加独立论坛表，不改变既有账户、渠道与账本。回滚旧镜像时保留新增表及论坛数据，不执行 `Down`；再次部署 v0.10.0 时 Goose 不重复执行已完成的迁移。未发布中间态曾直接创建论坛表的开发库不属于正式 v0.9.1 升级路径，不增加自动覆盖或删除逻辑。
