# AE Orbit

**你的 AE 库存与合成控制中心。**

连接 GTNH 的 AE 网络与外部服务：查询库存、发起额外合成、查看合成 CPU。当前阶段交付 **Go 后端、PostgreSQL、OC Lua 桥接器、模拟器和接口契约**；前端留待下一阶段单独开发。

适合自己或固定小团队，一套服务连接多个 AE 网络。OC 主动通过因特网卡访问后端，游戏服务器不需要向外开放端口，也不要求安装本项目的服务端模组。

已验证真实 PostgreSQL、HTTP 服务进程、Go 模拟器和 Lua 5.2 / 5.3 / 5.4 组件替身的闭环；本地 GTNH 存档已完成库存读取和单笔实际合成回库测试。忙碌 CPU 的真实产物、区块卸载恢复及大规模 OC/JVM 内存和 TPS 仍需进一步实测。

## 已实现的范围

| 部分 | 行为 |
| --- | --- |
| 库存 | 迭代采集、事件提示后精确重读、后台复核、短 ID、分批提交、重复上传去重、观察时间、搜索、实时名称分页及按 ID 批量读取 |
| 订单 | 指定额外合成数量、成员与网络权限、幂等下单、执行窗口、排队取消、断线待核对、状态审计、完成后定点复核 |
| CPU | CPU 概览、忙闲状态、可用的最终产物、观察时间及截断标记 |
| 多网络 | 每网络一个活跃桥接器；同游戏服务器共享采集和合成规划预算 |
| HTTP | 成员/设备令牌隔离、浏览器会话与 CSRF、请求容量限制、SSE 失效通知 |
| 运行维护 | 自动迁移、健康检查、管理指标、设备令牌轮换、OC 安装文件、容器配置 |

第一版 OC 采集对象为**物品**。不包含流体全量采集、成品配送、详细 CPU 物料树、ETA、游戏中正在运行的任务强制取消。CPU 最终产物受 AE 接口和合成监控器能力限制。

## 运行本地实例

容器方案需要 Docker 和 Docker Compose；默认只发布到本机回环地址。在仓库根目录执行：

~~~sh
python scripts/init_env.py
docker compose --env-file .env -f deploy/compose.yaml up -d --build
curl http://127.0.0.1:8080/readyz
~~~

`init_env.py` 将两个随机凭据写入忽略的 `.env`，不会显示凭据或覆盖已有文件。初次构建需要下载 Go 依赖和容器镜像。数据库保存在 `pg_data` 卷，后端启动时自动执行迁移。

`/readyz` 正常返回 `{"status":"ready"}`。根路径没有网页，返回 404 是预期行为。日志和停止命令：

~~~sh
docker compose --env-file .env -f deploy/compose.yaml logs --tail=50 api
docker compose --env-file .env -f deploy/compose.yaml down
~~~

停止命令保留数据库卷。容器构建和启动由 GitHub Actions 的 `Container smoke` 作业检查；具体验证状态见对应提交的 CI 结果。

### 外部 OC 访问

准备指向部署机器的域名，并让机器的 80/443 端口可达。在 `.env` 设置 `AE_DOMAIN` 为域名本身，例如 `ae.example.com`：

~~~sh
docker compose --env-file .env -f deploy/compose.yaml -f deploy/compose.https.yaml up -d --build
~~~

Caddy 提供 HTTPS，并设置匹配的 `AE_PUBLIC_URL`。也可以使用已有 HTTPS 反向代理，转发到本机 8080 端口，关闭 SSE 响应缓冲。

`AE_PUBLIC_URL` 必须是无路径、无末尾斜杠的源地址。非回环访问要求 HTTPS；OC 的 `url` 使用这个外部地址。MC 服务器的因特网卡访问规则需要允许它。

### 原生进程

准备 Go 1.27.3 和专用 PostgreSQL 18 数据库。服务账号需要建表、建索引和安装 `pg_trgm` 扩展的权限；也可由管理员事先在 `public` schema 安装扩展。

~~~sh
python scripts/build.py --linux
~~~

脚本生成本机服务、模拟器和 OC ZIP，`--linux` 额外生成 Linux amd64 二进制。优先使用 PATH 中的 Go，本工作区也支持 `.tools/go` 工具链。

PowerShell 启动示例：

~~~powershell
$env:AE_DATABASE_URL = 'postgres://aeorbit:替换为数据库密码@127.0.0.1:5432/aeorbit?sslmode=disable'
$env:AE_ADMIN_TOKEN = '替换为至少32字符的随机管理员令牌'
$env:AE_PUBLIC_URL = 'http://127.0.0.1:8080'
$env:AE_LISTEN = '127.0.0.1:8080'
.\dist\ae-orbit.exe serve
~~~

Linux 执行 `dist/linux-amd64/ae-orbit serve`，通过环境变量传入相同配置。数据库 URL 中的密码需要 URL 编码；生成脚本使用十六进制密码以避免这一步。

原生进程**不自动读取 .env**。`ae-orbit migrate` 只执行迁移；`ae-orbit version` 显示协议版本。当前运行边界是一个应用实例。

## 创建网络、查询与下单

以下 PowerShell 示例使用本地 Compose 地址，在另一个终端执行。先用 `python scripts/build.py` 构建模拟器。

~~~powershell
$origin = 'http://127.0.0.1:8080'
$adminToken = ((Get-Content .env | Where-Object { $_ -like 'AE_ADMIN_TOKEN=*' }) -split '=', 2)[1]
$headers = @{ Authorization = "Bearer $adminToken" }
$created = Invoke-RestMethod -Method Post -Uri "$origin/api/v1/networks" -Headers $headers -ContentType 'application/json' -Body (@{
  name = '开发模拟网络'
  server_group = 'sim-local'
  source = 'simulator'
} | ConvertTo-Json)
$networkId = $created.network.id
$env:AE_DEVICE_TOKEN = $created.device_token
.\dist\simulator.exe -url $origin -items 10000 -batch 64 -cache 256
~~~

`source=simulator` 和 `source=oc` 严格区分。模拟器按索引生成目录，ID 缓存有上限；`-cycles` 限制轮数，`-interval` 设置最小轮询间隔。

模拟器数量随周期变化，订单按模拟状态流转；它不维护真实物料账本，也不证明游戏内性能。额外合成后库存增加由 Lua 组件替身联调另行验证。

在保留这些变量的终端中停止模拟器后，或在另一个配置了相同变量的终端中查询并下单：

~~~powershell
$page = Invoke-RestMethod -Uri "$origin/api/v1/networks/$networkId/items?craftable=true&limit=20" -Headers $headers
$order = Invoke-RestMethod -Method Post -Uri "$origin/api/v1/networks/$networkId/orders" -Headers $headers -ContentType 'application/json' -Body (@{
  item_id = $page.items[0].id
  quantity = '64'
  idempotency_key = [guid]::NewGuid().ToString()
} | ConvertTo-Json)
Invoke-RestMethod -Uri "$origin/api/v1/networks/$networkId/orders/$($order.id)" -Headers $headers
Invoke-RestMethod -Uri "$origin/api/v1/networks/$networkId/cpus" -Headers $headers
~~~

要观察订单完成，模拟器需要保持运行。数量必须是字符串，下单 `"64"` 表示**额外制作 64 个**。提交响应丢失时重用原幂等键和原参数，创建另一张订单时才生成新键。

## OpenOS 安装

先创建 `source=oc` 网络，保存返回的 `device_token`。同一个 MC 服务器上的网络使用相同 `server_group`，不同服务器分组。

游戏内准备 OpenOS 电脑、因特网卡、持久磁盘，以及可通过 OC 组件网络访问的 AE 控制器或完整 ME 接口。每个逻辑网络运行一个桥接器。重新接到不同 AE 网络时创建新的逻辑网络。

**适配目标为最新 GTNH 版 OpenComputers；截至 2026-11-06 为 1.13.0-GTNH，并需要与它配套的 AE2 和 GTNH 模组组合。** 当前桥接器依赖 `allItems()` 逐项迭代、接受物品描述表和 NBT 的 `getItemInNetwork(detail)`，以及 `getCraftable(detail, "item")`。仅更新本项目的 Lua 脚本不能补齐模组接口。安装模组时以游戏实际使用的目录为准；启用版本隔离的启动器通常使用 `versions/<实例>/mods`。建议使用匹配的完整 GTNH 实例。程序不会退回全库存数组采集。

在 OpenOS 终端粘贴这一整行，即可从 GitHub 安装，无需 Git、解压工具或 GitHub 令牌：

~~~sh
wget -f https://raw.githubusercontent.com/CaeliaEve/ae-orbit/main/oc/install.lua /tmp/ae-orbit-install.lua && lua /tmp/ae-orbit-install.lua
~~~

首次安装会创建 `/etc/ae-orbit.cfg`，安装后填写后端地址、**设备令牌**和 ME 组件地址：

~~~sh
edit /etc/ae-orbit.cfg
ae-orbit
~~~

后续升级先停止正在运行的桥接器，再重新执行上面的 GitHub 安装命令。这会同时获取当前安装器和完整模块清单；新增模块的版本不能依靠旧安装器的内置清单安装。已安装的快捷更新命令仍可用于清单未变的版本：

~~~sh
ae-orbit-install
~~~

默认跟随 `main`。也可执行 `ae-orbit-install github <分支、标签或完整提交 SHA>`；安装时先解析出一个固定提交，所有程序文件都从该提交下载，并打印提交 SHA。指定完整的 40 位 SHA 会跳过 GitHub API 查询。因特网卡需能访问 `api.github.com` 和 `raw.githubusercontent.com`；遇到 API 限流时可稍后重试或使用完整 SHA。

后端二进制也内嵌 OC 文件。需要让 OC 程序与已部署后端保持同一发行版本时，可以改用后端安装源：

~~~sh
wget -f https://ae.example.com/oc/install.lua /tmp/ae-orbit-install.lua && lua /tmp/ae-orbit-install.lua https://ae.example.com
edit /etc/ae-orbit.cfg
~~~

后端源的后续升级同样先停止桥接器，再重新执行该源的 `wget` 安装命令。安装期间保持该后端版本不变。也可复制 `dist/ae-orbit-oc.zip` 中的 `bin/`、`lib/` 到 OpenOS；离线首次安装再把包内的 `etc/ae-orbit.cfg.example` 复制为 `/etc/ae-orbit.cfg`。

安装器每次读取 1 KiB，每文件最多 128 KiB、超时 30 秒；只下载 OC 所需文件。它先检查全部下载的长度和 Lua 语法，再备份并替换程序。已有配置和执行日志会保留；下载或写入失败会清理本次暂存文件，替换失败会尝试恢复旧程序。安装锁会阻止两个安装器同时修改文件。

**安装中断的恢复：** 正常报错处理完后可直接重试。如果发生断电、强制关机或磁盘故障，跨文件替换无法保证原子性。保持桥接器停止，先保存并检查程序旁的 `.ae-orbit-old` 备份，将需要的旧文件恢复到原路径；移走遗留备份、清理该次安装的 `.ae-orbit-new` 文件，确认没有安装器运行后删除 `/tmp/ae-orbit-install.lock`，再重新安装完整版本。安装器遇到遗留文件会停止，避免覆盖可恢复的数据。

配置必须填写：

~~~lua
return {
  url = "https://ae.example.com",
  token = "此网络的设备令牌",
  me_address = "实际发现的 ME 组件完整地址",
  state_dir = "/var/lib/ae-orbit",
}
~~~

完整选项见 [config.example.lua](oc/config.example.lua)。可在 Lua 交互环境用 `component.list()` 查看组件，显式选择目标 ME 地址，然后运行 `ae-orbit`。程序会打印已上传行数、缓存规模和空闲内存。

复合驱动的事件来源可能与 ME 组件地址不同，可设置 `event_sources` 为实测的事件节点列表。未设置时，所有提示仍会针对**配置的 ME 组件**重新查询，事件数量不会直接当作库存。

首次连接、实际接口兼容性、事件订阅和区块卸载后的恢复需要游戏内验证。

## 权限与恢复

管理员通过 `POST /api/v1/members` 创建普通成员：

~~~json
{"name":"teammate","grants":[{"network_id":"替换为网络ID","can_craft":true}]}
~~~

`can_craft=false` 只允许查看。成员令牌只返回一次，与设备令牌不可互用。浏览器阶段可将成员令牌换取 HttpOnly 会话，写请求附带 Origin 和 CSRF 令牌。

| 情况 | 处理 |
| --- | --- |
| 库存请求超时 | 进程保留已编码批次，使用原序号和原内容重试 |
| OC 重启 | 获取新 boot ID；旧合成跟踪不能恢复，不自动再次执行 |
| 订单 `needs_review` | 在 AE 核对后，通过订单 `/resolve` 提交 done / failed / canceled 和原因 |
| 同一 OC 进程断网恢复 | 保留原句柄；中间状态不清除待核对，同会话可靠终态可以结束订单 |
| 丢失/更换设备令牌 | 管理员 POST `/api/v1/networks/{network}/device-token`，正文 `{}`；安装新令牌并重启 |
| 两个日志槽均损坏 | 先核对 AE 与后端订单，再备份损坏日志并建立新桥接会话 |
| stale_session / sequence_conflict | 排查重复桥接进程、会话切换和丢失的待确认批次 |

待核对订单继续占据执行窗口和服务器组规划额度。`/resolve` 只记录核对结果，不会操作 AE。令牌轮换保留库存、退休旧会话、清理 CPU 快照。

撤销成员会使其令牌和浏览器会话失效。修改 `AE_ADMIN_TOKEN` 并重启会替换旧管理员 Bearer 凭据；已有浏览器会话仍按其有效期存在。

## 性能预算

当前实现可检查和调整的资源边界，不承诺任意 AE 规模下的固定刷新率。

| 环境变量 | 默认 | 可配置范围 / 含义 |
| --- | --- | --- |
| AE_DB_CONNECTIONS | 16 | 1–128 个数据库连接 |
| AE_BATCH_BYTES | 32768 | 2048–131072 字节/设备请求 |
| AE_BATCH_ITEMS | 128 | 1–512 条/批；还受协商响应大小限制 |
| AE_RESPONSE_BYTES | 32768 | 2048–131072 字节/设备响应 |
| AE_SCAN_ITEMS | 32 | 1–512 次读取/服务器组/额度周期 |
| AE_POLL_MILLIS | 1000 | 100–30000 ms 控制及额度发放间隔 |
| AE_GROUP_PLANNING | 1 | 1–16 个共享规划/不确定任务额度 |
| AE_ACTIVE_WINDOW | 4 | 1–16 个每网络活跃订单 |
| AE_MAX_QUEUED | 256 | 1–4096 个每网络排队订单 |
| AE_MAX_CONCURRENT | 64 | 1–512 个普通 HTTP 并发 |
| AE_MAX_STREAMS | 32 | 1–128 个 SSE 连接 |
| AE_OFFLINE_SECONDS | 90 | 15–3600 秒离线阈值 |
| AE_RECHECK_SECONDS | 300 | 5–86400 秒重新具备复核资格的间隔 |

OC 默认请求/响应各 16 KiB，按空闲内存继续缩小。ID 缓存默认 256 条，脏条目队列 128 条，同时受字节预算约束；采集时间片默认 50 ms。CPU 默认每 10 秒采样，保留最多 16 个。未用完的读取额度不会累积，多个桥接器通过抖动分摊机会，当前没有严格公平队列。

普通库存跨时间片合批，`batch_interval_seconds` 默认 0.25 秒、范围 0–1，0 表示每个时间片及时发送。满批、期限到达、额度用尽或扫描结束会发送尾批；订单复核观察立即发送。网络和原生接口的阻塞时间不计入主动合批等待保证。普通后台补查另用最多 16 项、估算最多 16 KiB 的队列，向后端反馈剩余条目和字节容量，防止热事件把冷物品补查挤掉。

订单结束后，后端持久安排目标物品复核，每物品合并为一项，收到带凭据的真实数量与配方观察后才确认完成。OC 使用独立队列，最多 16 项、估算字节预算最多 16 KiB；普通事件不能挤掉它。该核验与普通采集共享额度，每次预留 4 个额度，配方查询至少间隔五秒；请保持 `AE_SCAN_ITEMS` 不低于 4。队列、原生查询和网络耗时仍会影响完成后的刷新延迟，订单完成通知不会直接修改库存数量。

后端不在应用内保存完整库存镜像；批次通过事务写入数据库。库存行保存数值及数量、配方各自的核验证据，普通补查调度单独存储。重复数值只更新无索引的核验字段，数值、标签和所属会话未变时不使分页版本失效。普通补查每次最多检查 128 个到期候选，跳过数量与配方均已近期核验的条目，调度本身不刷新核验时间。列表省略原始 NBT，SSE 只发失效通知。

按名称浏览可以使用 `GET /api/v1/networks/{network}/items?consistency=live`，数量变化时继续翻页；目录、标签、会话或相关配方筛选变化仍会使游标失效。默认 strict 模式保持原行为，按数量排序继续使用 strict。每页显示该次请求时后端可见的数据，不冻结整个目录。

`GET /api/v1/networks/{network}/items/lookup?ids=405,406` 每次按 ID 读取最多 200 个物品，适合只刷新当前显示的行，返回明确的 `missing_ids`。它不触发游戏读取或合成，不把未知物品当作零库存；原有核验时间及旧会话状态保留。

库存列表和按 ID 读取支持浏览器协商的 gzip：默认至少 1 KiB 且压缩后更小时启用，小响应保留原文；客户端显式拒绝原文编码时按协商处理。压缩不改变大整数、ID 或库存语义。OC 上传仍使用现有 JSON 协议。

同一后端进程内，库存等持久事件提交成功后立即唤醒相应网络的 SSE 订阅者；每个连接只保留一个待处理信号，事件从数据库回放。默认每 15 秒补读并复核权限，弥补进程外写入或漏接通知。SSE 不压缩，反向代理仍需关闭缓冲；多后端实例目前依靠周期补读。

网络详情的 `health.inventory` 提供会话内已确认批次、上传条数、正文字节、消耗采集额度和累计读取毫秒。可用相邻样本差值计算更新条数/秒、有效载荷字节/秒及平均每批条数。它不包含重试和 HTTP/TLS 开销，也不是不同物品总数或真实游戏线程 CPU 时间；旧脚本没有这些字段。先更新后端，再更新 OC 脚本。

`health.read_failures` 提供最近的数量/配方读取失败线索：物品名称、metadata、已知短 ID、错误原因、次数和会话内经过时间。OC 最多保留 4 项、编码总量不超过 2 KiB，不保存完整 NBT；相同线索合并，变化详情在成功确认后至少间隔 30 秒再发送，控制空间不足时延后或减少样本。后端在该会话内保留最近收到的详情，OC 重启后清空。缺失字段表示尚无详情，不能当作没有失败；仍需结合聚合计数判断。NBT 长度/校验与截短名称只是排查线索，不能用于认定物品身份或删除记录。完整语义见[协议说明](contracts/protocol-v1.md#有界读取失败样本)。

条目种类增加仍会延长枚举。默认每秒 32 次读取时，10 万条目单次遍历的理论下限已超过 52 分钟，还未计入原生调用、时间片、传输和复核。热点事件可以更快更新。`AE_RECHECK_SECONDS` 是复核资格间隔，不是全量刷新 SLA。

OC 不通过全量物品数组回退，也不缓存完整目录。原生 `allItems()` / `getCpus()` 仍可能在 JVM 分配集合，单次原生调用不能由 Lua 时间片强行抢占。Lua 5.2 不能精确表示所有 int64，无法精确表示的值会被诊断和跳过。

精确库存查询只更新数量，配方状态来自枚举或独立核验。下单前核对完整身份；若单配方接口因 AE 库存接口限制返回空，会使用完整身份过滤的 `getCraftables` 查询，最多接受一个候选。过滤查询仍可能在模组内部遍历目录，不在每次库存事件上执行。零库存的已知配方通过后台限速复核确认删除，额外核验共享采集额度，至多每五秒一次。`observed_at` 与可选的 `craftable_observed_at` 分别表示后端接受数量与配方观察的时间；数量变新不会让旧配方也变新，字段缺失表示配方核验时间未知。这些时间不能当作游戏内采样时钟。

升级到调度分表版本会自动执行 004 数据库迁移，已有数量、会话、订单和待复核任务保留，OC 无需更新。迁移会事务性重建库存表以预留核验更新空间，大型目录需要相应的临时磁盘空间和维护时间。升级前备份数据库并停止桥接器；回退必须成对恢复旧程序与数据库备份。旧记录没有独立配方核验时间，迁移不会伪造该时间。

实时浏览的 005 迁移只给网络添加目录和配方修订号，不重建库存，OC 无需更新。保持单应用实例部署，避免新旧版本同时写入同一数据库。

管理指标在 `/api/v1/admin/metrics`。`/inventory-stats` 是实时数据库聚合，成本随目录增长，后续前端应低频读取。Compose 的 `GOMEMLIMIT=256MiB` 是 Go 软目标，容器默认限制 512 MiB；数据库资源另计。

管理指标包含按固定路由汇总的耗时、数据库调用数、压缩前后正文字节，以及查询/批次、连接获取、显式加锁语句、写事务和提交的固定桶直方图。取消和失败也计数，连接池等待与实际 SQL 操作可分别观察。加锁语句耗时包含语句执行，不能当作纯锁等待；各阶段有重叠，不能相加。普通 Web API 另提供 `Server-Timing` 响应头。指标不记录令牌、SQL 参数或物品身份；重启后清零。完整字段口径见 [协议说明](contracts/protocol-v1.md#管理性能指标)。

## 开发验证

~~~sh
python -m venv .venv
# 激活 .venv 后：
python -m pip install -r scripts/requirements-test.txt
python scripts/test_lua.py
python scripts/verify_contracts.py
go test ./...
go vet ./...
~~~

数据库测试需要 `AE_TEST_DATABASE_URL` 指向专用测试库。没有此变量时会明确跳过，不能当作完整验证。测试只创建/删除随机 test_* schema。不要使用生产数据库。

~~~powershell
$env:AE_TEST_DATABASE_URL = 'postgres://postgres:测试密码@127.0.0.1:5432/aeorbit_test?sslmode=disable'
go test -count=1 ./...
go test -race -count=1 ./...
python scripts/build.py
python scripts/smoke.py --psql 'C:\PostgreSQL\bin\psql.exe'
~~~

Windows 竞争检测需要兼容 C 编译器和 `CGO_ENABLED=1`；发行二进制使用 `CGO_ENABLED=0`。`test_lua.py` 还会在真实临时目录运行三种 Lua 版本的安装器测试，覆盖安装、升级、断网、限流、截断、磁盘失败和回滚。冒烟脚本运行独立服务/模拟器和三种 Lua 组件替身，检查 10+64=74、精确整数、CPU、幂等与重启。它会清理自己的进程和 smoke_* schema，日志位于忽略的 .local/。

## 代码与文档

| 目录 | 职责 |
| --- | --- |
| cmd/ | 服务和模拟器入口 |
| internal/api/ | HTTP、鉴权、SSE、OC 下载 |
| internal/store/ | 数据库、库存事务、订单、共享调度 |
| internal/protocol/ | 精确整数、身份和协议校验 |
| oc/ | Lua 桥接器、OpenOS 入口和安装器 |
| contracts/ | HTTP / 设备协议 |
| deploy/、scripts/ | 部署、构建和验证 |

进一步阅读：[协议语义](contracts/protocol-v1.md)、[OpenAPI](contracts/openapi.yaml)。本阶段没有前端目录、依赖或 UI 产物。
