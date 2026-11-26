# AE Orbit 运维手册

本手册适用于仓库提供的单实例 Docker Compose 部署。命令从仓库根目录运行，使用 Bash。已有实例以实际 Compose 项目名、配置、卷和挂载为准。首次部署见 [README](../README.md#运行本地实例)。

## 记录部署配置

记录当前 Git 提交、API 镜像、Compose 项目名、启用的覆盖文件、NESQL 目录版本、各 OC 角色与安装来源，以及 MaiBot 插件版本。把路径和版本写进内部运维记录，把凭据存放在受控位置。

基础部署的命令前缀如下。已启用 HTTPS、NESQL 目录或纹理包时，把实际使用的覆盖文件按原顺序加入数组。后续命令复用同一份数组，现有实例如指定了 `-p` 也带上。

```bash
compose=(docker compose --env-file .env -f deploy/compose.yaml)
# 只加入当前实例实际使用的覆盖文件
# compose+=(-f deploy/compose.https.yaml)
# compose+=(-f deploy/compose.catalog.yaml)
# compose+=(-f deploy/compose.texture-pack.yaml)
"${compose[@]}" config --quiet
"${compose[@]}" ps
"${compose[@]}" images api
git rev-parse HEAD
```

`AE_PUBLIC_URL` 应匹配实际浏览器访问的源地址（协议、域名和端口），省略路径及末尾斜杠。公开部署可使用 HTTPS 覆盖文件，也可用现有反向代理转发到本机端口。代理须允许 SSE 持续传输。NESQL 与纹理包分别使用 `AE_CATALOG_HOST_PATH` 和 `AE_TEXTURE_PACK_HOST_PATH` 指定宿主机路径。原生 Go 服务从进程环境变量取配置。 [配置示例](../.env.example)

## 备份与恢复演练

| 内容 | 备份范围 |
| --- | --- |
| PostgreSQL | 整库备份，包括库存、记录、订单、身份、插件配置、预警与 `qq_outbox`。单个库存 CSV 只适合查阅明细。 |
| 部署 | `.env`、实际使用的 Compose 文件、反向代理配置、旧镜像标识和对应提交。NESQL 原始目录与发布纹理包单独保存。 |
| 游戏 OC | 各台 `/etc/ae-orbit.cfg` 和其中指定的 `state_dir`，通常为 `/var/lib/ae-orbit`。复制状态目录前停止对应桥接，保留执行日志。 |
| MaiBot | 插件配置、连接凭据、插件数据目录中的 `channels.json` 和 `receipts.sqlite`。先暂停插件再复制状态文件，或使用 SQLite 的在线备份工具。 |

PostgreSQL 的 `pg_dump` 会获得一致快照。升级前若要对齐数据库与 OC、QQ 的执行状态，先暂停新的任务提交，处理正在派发的合成和通知，再短暂停止 API 写入并备份。定期把备份复制到其他存储，检查大小及可恢复性。以下为仓库默认数据库名与用户。

```bash
umask 077
mkdir -p .local/backups
backup=".local/backups/aeorbit-$(date -u +%Y%m%dT%H%M%SZ).dump"
"${compose[@]}" exec -T db pg_dump -Fc -U aeorbit -d aeorbit > "$backup" &&
  test -s "$backup" &&
  "${compose[@]}" exec -T db pg_restore --list < "$backup" > "$backup.list"
```

备份命令失败时，应视该文件为不完整并重新备份。`pg_restore --list` 只验证备份目录可读。演练时将备份导入**独立 PostgreSQL 18 实例和空数据库**，配置适当的数据库角色和 `pg_trgm` 权限。用下面的命令检查恢复过程，使用仅对隔离实例有效的连接串。

```bash
pg_restore --exit-on-error --single-transaction --no-owner \
  --dbname "$AE_RESTORE_DATABASE_URL" "$backup"
```

演练实例使用独立地址和凭据，保持游戏 OC 与 MaiBot 连接生产实例。恢复后检查 `schema_versions`、库存历史、未完成任务和子网规则，再用匹配版本的 API 检查 `/readyz` 和网页。连接真实 OC 或 QQ 之前，先核对备份时间之后已执行的合成、已发送的消息及待确认记录。

## 升级和回滚

1. 比较当前与候选提交，查看迁移、OC 文件和 MaiBot 清单是否变化。记录当前部署并验证备份。包含迁移时先在恢复的测试库启动候选服务。镜像重建前，可给当前容器实际使用的镜像留一个回滚标签。 [迁移目录](../internal/store/migrations/)

   ```bash
   api_container=$("${compose[@]}" ps -q api)
   prior_image=$(docker inspect --format '{{.Image}}' "$api_container")
   rollback_image="ae-orbit-api:rollback-$(date -u +%Y%m%dT%H%M%SZ)"
   docker image tag "$prior_image" "$rollback_image"
   ```
2. 切换到目标提交，用原 Compose 前缀构建 API，再只替换 API 容器。更新目录时确认同一组挂载仍存在。下方使用默认本机端口 `8080`，如果设置了 `AE_HTTP_PORT`，用实际值检查。

   ```bash
   "${compose[@]}" build api
   "${compose[@]}" up -d --no-deps api
   "${compose[@]}" logs --tail=80 api
   curl -fsS http://127.0.0.1:8080/readyz
   ```

3. 验证浏览器登录、库存与历史、原有订单、主副 OC 心跳和已启用插件的最近观测。用下面的只读检查验证主网设备恢复，`--expected-devices` 填实际预期设备数，`--origin` 填本机或真实入口。该脚本检查主网设备，蓝波顿和子网仍需要在各自页面查看时间戳。

   ```bash
   # 先在当前进程环境安全提供 AE_ADMIN_TOKEN 和 AE_NETWORK_ID
   python scripts/recovery_check.py --origin http://127.0.0.1:8080 --expected-devices 2 --wait 120
   ```

4. 涉及 OC 协议或模块的改动，按所需角色逐台更新。从已部署的后端重新下载 `/oc/install.lua`，重启 OpenOS 后用 `ae-orbit status` 确认状态，必要时 `ae-orbit start`。检查 ME 组件、协商能力和采集时间，再更新下一台。涉及 QQ 时按功能所需版本升级 MaiBot 插件，保留群授权、凭据和回执。 [OC 安装](../README.md#openos-安装) [QQ 联动](qq-notifications.md)

回滚时先判断候选服务是否已经执行迁移、处理订单或发出通知。旧镜像能使用当前 schema 时，恢复与旧版本匹配的配置及挂载，再用临时覆盖文件指定已保留的镜像。用之前记录的标签恢复 `rollback_image` 的值。

```bash
mkdir -p .local
printf 'services:\n  api:\n    image: %s\n' "$rollback_image" > .local/compose.rollback.yaml
"${compose[@]}" -f .local/compose.rollback.yaml up -d --no-build --no-deps --force-recreate api
```

随后重复健康检查，并在回滚期间对其他 Compose 操作保留该覆盖文件。数据库迁移按文件名记录，镜像切换无法撤回已执行的迁移。恢复旧数据库前保留故障现场的转储，再在独立库恢复并核对备份之后的业务副作用。待确认记录需先核实实际结果。

## 常见故障

| 现象 | 先检查 |
| --- | --- |
| 网页打不开，或 `/readyz` 返回 503 | `/healthz` 用于确认 HTTP 进程可响应，`/readyz` 还会 Ping 数据库。用上面的 Compose 前缀执行 `ps` 和 `logs --tail=80 api db`。服务未启动时先看迁移、连接串和数据库磁盘空间。 |
| 网页能读，登录或保存失败 | 核对浏览器地址、`AE_PUBLIC_URL`、Cookie 的 Secure 属性、Origin 与 CSRF。反向代理后用实际外网入口复查。 |
| 主网在线但库存慢、数量过期或缺少种类 | 先分清 API 响应耗时、OC 最后观测和候选完成度。确认主副 OC 都在上报心跳，比较队列、响应包和读取失败；管理员可查看 `/api/v1/admin/metrics`。查看页面不会补齐目录之外的身份。 |
| 中文或纹理突然消失 | 查 API 是否仍挂载 `AE_CATALOG_ROOT`，目录是否为有效 `elysium.source`，新目录与库存身份能否匹配。使用本地 ZIP 的浏览器重新加载同版纹理包。 [NESQL 目录升级](nesql-catalog-upgrade.md) |
| CPU 可见但合成过程一直等待 | 查主 OC 的 `cpu_details` 能力、目标 CPU 是否仍忙碌，以及是否有该网络的有效查看租约。副 OC 不采集合成过程。 [CPU 过程契约](../contracts/cpu-details.md) |
| 原生 AE 读取伴随游戏崩溃 | 暂停相关 OC 或详情采集，保存首个异常的完整调用栈、组件坐标和模组版本。主网核对 `native_scan_disabled`、`events_disabled` 和 `exact_quantities`。若涉及独立子网直接读取，先切回逐项精确模式。 [子网监控](subnet-monitoring.md#独立小子网直接读取) |
| 蓝波顿或子网显示 0 在线 | 先看专用 OC 的 `ae-orbit status`、绑定地址和上次心跳。在线但无数值时核对实际组件、所需方法和绑定状态，再区分未观测、过期和确认为零。 |
| QQ 没有收到通知 | 对照 AE Orbit 投递记录、MaiBot 插件状态及 SnowLuma 登录状态，确认发送开关、授权群、关键字或阈值条件。`uncertain` 先核对群内记录和 SQLite 回执，再决定如何处理。 [QQ 联动](qq-notifications.md#投递) |
| 重启后合成订单变为 `needs_review` | 在 AE 核对该订单对应的实际合成结果，再用原订单核对入口记录结论。原请求仍是追踪和审计依据。 [订单与恢复](../README.md#权限与恢复) |

例行检查关注 PostgreSQL 数据和备份目录增长、最近一次恢复演练、OC 观测延迟及 QQ 待确认积压。长期库存历史随实际变化增长，可在数据库中查看表体积并据此安排磁盘容量。 [历史保留说明](inventory-history.md)
