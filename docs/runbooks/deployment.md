# 部署 Runbook

适用范围：首版受邀小圈子实例的单机部署。内容由 Feature #22 交付并随实现演进。

## 前置条件

- Docker 与 Docker Compose。
- `mise`（工具版本见 `mise.toml`）。
- 已生成并安全保存的三组密钥材料：
  - 上游凭据密钥环：32 字节密钥（base64）+ 激活 key id（`UPSTREAM_CREDENTIAL_KEYRING` / `UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID`）。
  - C2C 私密数据密钥环：另一组独立密钥（`C2C_PRIVATE_DATA_KEYRING` / `C2C_PRIVATE_DATA_ACTIVE_KEY_ID`），禁止与上游凭据密钥环复用。
  - 备份口令（`BACKUP_PASSPHRASE`），只用于备份加密。

## 首次部署

1. `git clone` 并检出目标发布提交。
2. 准备 `.env` 或密钥管理方式，至少包含：`POSTGRES_PASSWORD`、`TRUSTED_PROXY_CIDR`、`BACKEND_TRUSTED_PROXY_CIDRS`、两组密钥环与激活 key id（具体要求见 `compose.yaml` 顶部注释与 `ARCHITECTURE.md`）。
3. `mise install && mise run install`。
4. `mise run up` 启动安全栈；首次启动会自动执行迁移、凭据可解密自检和一次跨模块巡检（结果进入运营总览的巡检历史）。
5. `printf '<密码>\n<密码>\n' | script -q /dev/null mise exec -- go -C backend run ./cmd/bootstrap-admin -username <用户名> -display-name <名称>` 创建唯一管理员（需 pty 交互输入两次密码）。
6. 管理员登录后完成首次改密，再按需创建受邀账户。

## 发布检查

- 本地门禁：`mise run check-release`（设计资产、前端测试与构建、后端 vet 与 race、Compose 配置）。
- CI：PR 与 main 推送自动执行同一组门禁（`.github/workflows/ci.yml`）。
- 部署后烟测：`GET /api/health` 返回 ok；管理员打开 `/admin/ops` 确认“账本已核对”且巡检历史最新记录全部正常。

## 升级流程

1. 在新版本上完成 `mise run check-release`。
2. 执行一次数据库备份（见备份恢复 Runbook）。
3. 拉取新提交并 `mise run up` 重建变更容器；迁移随启动自动执行。
4. 升级后烟测同上；若巡检出现硬异常，按故障处理 Runbook 处置并保留现场。

## 生产部署（hub.isok.dev）

生产实例部署在 HK VPS，由 GitHub Actions 自动部署（`.github/workflows/release.yml`）。
常规发版、审批上线、重跑/回滚与紧急手动部署的逐步操作见 `release.md`：

1. 推送 `v*` tag（如 `v0.1.0`）后自动执行：`mise run check-release` 门禁 → backend/frontend 多架构镜像构建推送 GHCR（tag + digest 固定）→ 创建 GitHub Release。
2. `production-hub` Environment 需人工审批。批准后 workflow 通过 SSH forced-command 调用 VPS 上的受限发布脚本：先做部署前加密备份，再以目标 digest 切换 Compose 镜像、`up -d`、等待健康并烟测本机与公网端点；任一步失败自动回滚到备份 Compose。
3. 重跑或回滚：对 `release` workflow 使用 `workflow_dispatch` 并填入既有 tag，直接复用已发布镜像 digest 部署，不重新构建。
4. 运行时事实（生产 Compose、Nginx、备份与发布脚本副本）以个人运维仓库 `remote-hosts/hk-vps/sites/oh-my-aihub/` 为准；首次部署与新环境自举使用该仓库的 `bin/deploy-oh-my-aihub`。

