# 部署 Runbook

适用范围：受邀小圈子实例的单机部署。

## 前置条件

- Docker 与 Docker Compose。
- `mise`（工具版本见 `mise.toml`）。
- 已生成并安全保存的三组密钥材料：
  - 上游凭据密钥环：32 字节密钥（base64）+ 激活 key id（`UPSTREAM_CREDENTIAL_KEYRING` / `UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID`）。
  - C2C 私密数据密钥环：另一组独立密钥（`C2C_PRIVATE_DATA_KEYRING` / `C2C_PRIVATE_DATA_ACTIVE_KEY_ID`），禁止与上游凭据密钥环复用。
  - 备份口令（`BACKUP_PASSPHRASE`），只用于备份加密。

## 首次部署

1. `git clone` 并检出目标发布提交。
2. 准备 `.env` 或密钥管理方式，至少包含：`POSTGRES_PASSWORD`、`TRUSTED_PROXY_CIDR`、`BACKEND_TRUSTED_PROXY_CIDRS`、两组密钥环与激活 key id。
   - `TRUSTED_PROXY_CIDR` 必须填写前端容器实际观察到的 TLS 代理源地址，而不是想当然地使用 `127.0.0.1/32`；Docker NAT 后该地址因运行环境而异。可以先在受限环境以 `mise run up-dev` 启动，让同机代理发起一次请求，再从 `docker compose logs frontend` 的访问日志首列取得源 IP，并以最窄的 CIDR（通常为单地址 `/32` 或 `/128`）配置安全入口。
   - `BACKEND_TRUSTED_PROXY_CIDRS` 应填写当前 Compose 项目网络的实际子网。可先运行 `docker compose create` 只创建容器与网络，再通过 `docker inspect` 取得前端容器所连接的 Network ID，并用 `docker network inspect` 读取其 IPAM 子网；不要硬编码假定的 `172.16.0.0/12`。
3. `mise install && mise run install`。按[观测手册](observability.md#请求链路地图)配置网关部署位置和在线定位开关；默认启用在线查询。
4. `mise run up` 启动安全栈：一次性 migration 容器先执行迁移，后端启动时校验两组密钥环格式与出站配置，并逐条解密数据库里全部存量密文；密钥环缺少仍被引用的 key id 或存在损坏的密文时后端拒绝启动，日志（`docker compose logs backend`）列出记录类型与 ID。新库没有密文，直接通过。
5. 启动后立即用浏览器访问部署地址的 `/initialize`，在网页上创建唯一管理员（ADR-0037）。密码由你自设，创建成功即登录，无需首次改密。若页面显示实例已初始化而管理员不是你创建的，视为部署失败：停止服务，按故障处理 Runbook 排查。
6. 按需创建受邀账户。

## 发布检查

- 门禁：PR 与推送到 `main` 时由 CI 按改动范围检查（`.github/workflows/ci.yml`）；发版工作流复用该提交在 `main` 上的 CI 结果，缺失时才执行 `mise run check-release`。不必在本地重复。
- 部署后烟测：`GET /api/health` 返回 ok；管理员打开 `/admin/points` 确认五项核对全部通过。

## 升级流程

1. 确认目标提交在 `main` 上的 CI 已通过。
2. 执行一次数据库备份（见备份恢复 Runbook）。
3. 拉取新提交并 `mise run up` 重建变更容器；迁移随启动自动执行。升级前确认两组密钥环仍含全部历史 key id：后端启动时会解密全部存量密文，缺失则无法启动（升级到首个带此检查的版本时尤其如此，此前缺失的密钥不会被发现）。
4. 升级后烟测同上；核对未通过时按故障处理 Runbook 处置并保留现场。

## 生产部署（ai.isok.dev）

生产实例部署在 HK VPS，由 GitHub Actions 自动部署（`.github/workflows/release.yml`）。
常规发版、审批上线、重跑/回滚与紧急手动部署的逐步操作见 `release.md`：

1. 推送 `v*` tag（如 `v0.1.0`）后自动执行：门禁（复用 `main` 上该提交的 CI 结果，缺失时执行 `mise run check-release`）→ backend/frontend 多架构镜像构建推送 GHCR（tag + digest 固定）→ 创建 GitHub Release。
2. `production-hub` Environment 需人工审批。批准后 workflow 通过 SSH forced-command 调用 VPS 上的受限发布脚本：先做部署前加密备份，再以目标 digest 切换 Compose 镜像、`up -d`、等待健康并烟测本机与公网端点；任一步失败自动回滚到备份 Compose。
3. 重跑或回滚：对 `release` workflow 使用 `workflow_dispatch` 并填入既有 tag，直接复用已发布镜像 digest 部署，不重新构建。
4. 运行时事实（生产 Compose、Nginx、备份与发布脚本副本）以个人运维仓库 `remote-hosts/hk-vps/sites/oh-my-aihub/` 为准；首次部署与新环境自举使用该仓库的 `bin/deploy-oh-my-aihub`。

