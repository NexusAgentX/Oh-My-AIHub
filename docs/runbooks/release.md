# 发版 Runbook

适用范围：ai.isok.dev 生产实例的常规发版、重跑与回滚。生产链路结构、密钥与运行时事实见
`deployment.md` 与个人运维仓库 `remote-hosts/hk-vps/sites/oh-my-aihub/`；本文只讲操作步骤。

## 常规发版（vX.Y.Z）

1. 确认目标提交已合入 `main` 且 PR 门禁（`ci.yml`）通过。
2. 把 `CHANGELOG.md`「未发布」章节收口为 `## vX.Y.Z - YYYY-MM-DD`，并保留新的空
   「未发布」章节；随最后一个 PR 合入。版本号用语义化 `vMAJOR.MINOR.PATCH`，
   预发布加 `-rc.1` 等后缀。
3. 在 main 合并提交上打 annotated tag 并推送：

   ```bash
   git fetch origin
   git switch main && git merge --ff-only origin/main
   git tag -a vX.Y.Z -m 'vX.Y.Z'
   git push origin vX.Y.Z
   ```

4. `release` workflow 自动执行，无需干预：`mise run check-release` 门禁 → backend/frontend
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

或在 Actions 页面 *Run workflow* 填入 tag。注意：数据库迁移随部署自动执行且没有 down
迁移；若新版本包含破坏性迁移，回滚旧镜像前必须先按备份恢复 Runbook 恢复该版本的
部署前备份。

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

## v0.8.0 → v0.9.0 数据库前置步骤

先确认生产缺少 `model_sources`、`catalog_sync` 两表，运行
`sudo systemctl start oh-my-aihub-backup.service` 并核对 Result=success、ExecMainStatus=0。
在现有库执行下列事务，只新增同步表，不重建数据库或改动既有模型及账本。

```sql
BEGIN;
SET LOCAL lock_timeout = '5s';
CREATE TABLE model_sources (
 source_key text PRIMARY KEY,
 model_id text NOT NULL UNIQUE,
 ignored boolean NOT NULL DEFAULT false,
 sync_enabled boolean NOT NULL DEFAULT true,
 raw_record jsonb NOT NULL,
 info jsonb NOT NULL,
 seen_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE catalog_sync (
 id boolean PRIMARY KEY DEFAULT true CHECK(id),
 exchange_rate text NOT NULL DEFAULT '',
 started_at timestamptz,
 finished_at timestamptz,
 status text NOT NULL DEFAULT 'idle',
 error text NOT NULL DEFAULT '',
 result jsonb NOT NULL DEFAULT '{}'::jsonb
);
INSERT INTO catalog_sync(id) VALUES(true);
COMMIT;
```

核对两表及唯一初始配置行后再批准部署。汇率保持空值，首次启动自动同步资料，
新模型停用，既有同名手工模型不接管。管理员之后在模型页自行设置汇率。
升级失败可保留新增表并回滚到 v0.8.0 镜像；不得执行基线 Down。

## v0.9.0 → v0.10.0 升级

部署前沿用自动加密备份，迁移服务自动执行 `0002_catalog_provider_whitelist.sql`，无需手工补表。
升级保留已有模型和汇率，白名单初始未配置，不导入或清理目录。管理员保存有序提供商白名单后，下次成功同步才按规则整理。
部署后核对 goose 版本为 2、`providers_configured=false`（管理员尚未配置时），并检查服务健康与账本核对。

本次迁移改变 `model_sources` 主键，旧版同步程序与新表结构不兼容。回滚须先停止新后端，使用新版本迁移工具回退 0002 后再切旧镜像；如已出现含 `:`/`@` 的模型 ID 或重复来源键，Down 会拒绝，此时按备份恢复手册恢复匹配备份。不能只切回 v0.9.0 镜像而保留版本 2 结构。
