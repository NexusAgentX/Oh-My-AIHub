# 备份与恢复 Runbook

Feature #22 交付的加密备份与隔离恢复演练流程。备份三要素必须分开保存：密文、备份口令、密钥环；任何两者合在一起都会让加密失去意义。

## 执行备份

```bash
BACKUP_PASSPHRASE=<备份口令> \
DATABASE_URL='postgres://...@127.0.0.1:55432/oh_my_aihub?sslmode=disable' \
PGPASSWORD=<数据库口令> \
scripts/backup-database.sh backups
```

产出 `backups/backup-<UTC 时间>.dump.aes`（AES-256-CBC + PBKDF2）与同名 `.manifest.json`（密文 SHA-256、schema 版本、密钥环环境变量引用）。`backups/` 已被 `.gitignore` 排除，脚本也会拒绝写入未被忽略的目录。建议节奏：每日一次，重大变更（迁移、升级、大批账户操作）前后各一次。

## 恢复演练（必须定期执行）

```bash
BACKUP_PASSPHRASE=<备份口令> \
UPSTREAM_CREDENTIAL_KEYRING=<...> UPSTREAM_CREDENTIAL_ACTIVE_KEY_ID=<...> \
C2C_PRIVATE_DATA_KEYRING=<...> C2C_PRIVATE_DATA_ACTIVE_KEY_ID=<...> \
scripts/restore-drill.sh backups/backup-<时间>.dump.aes
```

演练在一次性隔离 PostgreSQL 容器中完成，不影响生产数据，步骤全部自动：

1. 校验清单 SHA-256 与密文一致。
2. 解密并恢复到隔离库。
3. 清除恢复出的全部旧会话（备份中的会话不得复活）。
4. 运行迁移到最新版本。
5. 以生产同款后端对隔离库启动：上游凭据与 C2C 私密数据可解密自检、跨模块巡检（零和/投影/调用结算/C2C 一致性）必须全部通过。
6. 输出结论并清理容器。

任何一步失败都视为备份不可用，应立即重新备份并排查（最常见原因：密钥环轮换后旧备份缺 key、口令记错、密文传输损坏）。

## 真实恢复（灾难时）

1. 按“部署 Runbook”准备新环境与两组密钥环（必须与备份时一致或包含全部历史 key id）。
2. 用备份口令解密备份并 `pg_restore` 到新数据库，然后执行迁移。
3. `DELETE FROM sessions;` 清除旧会话，强制全部用户重新登录。
4. 启动后端完成自检；管理员确认 `/admin/ops` 巡检全绿、关键计数与预期一致。
5. 恢复后第一份新备份立即执行，作为新恢复点。
