# AE Orbit 协议 v1

本文定义实现中的同步、一致性、调度和恢复语义。所有路由及 JSON 结构见 [openapi.yaml](openapi.yaml)。协议版本为 1。

## 传输与身份

成员使用管理员发放的 Bearer 令牌，或由成员令牌换取的浏览器会话。设备使用绑定到单个网络的独立 Bearer 令牌。凭据以摘要保存在数据库，创建/轮换时返回一次明文。

浏览器会话有效期 24 小时，每成员最多保留 20 个。Cookie 为 HttpOnly、SameSite=Strict，HTTPS 下设置 Secure。Cookie 写请求必须带匹配部署源地址的 Origin 和会话对应的 X-CSRF-Token。

JSON 请求发送 `Content-Type: application/json`，不使用 Content-Encoding。服务拒绝超限正文、未知 JSON 字段和多段 JSON。通用文本上限以 UTF-8 字节数为主；OpenAPI 字符长度不能绕过字节预算。

数量、短物品 ID、观察序号、传输序号、版本及事件 ID 均为精确十进制字符串：

~~~json
{"quantity":"9007199254740993","observation":"18","id":"405"}
~~~

单项数量范围 0..9223372036854775807；下单数量、短 ID 和观察/传输序号必须大于零。拒绝 JSON 数字、符号、小数、前导零、科学计数法和溢出。统计的 total_quantity 可以超过 int64。

网络、成员、会话、订单和尝试 ID 是不透明字符串。时间使用服务端 UTC 观察/提交时间，不以 OC 时钟作为跨设备排序依据。

## 建立会话

1. 新进程调用 `POST /bridge/v1/boot-id`，正文 `{}`，获得随机 boot_id。
2. 使用它发送 Hello，声明来源、能力和可承受的响应大小。
3. 保存 session_id、两条独立序列的 ACK 和协商后的限制。
4. 先进行控制轮询，再使用返回额度采集库存。

~~~json
{
  "protocol":1,
  "boot_id":"来自boot-id接口的新值",
  "source":"oc",
  "capabilities":{
    "inventory":true,
    "craft":true,
    "cpu_output":true,
    "exact_quantities":true,
    "max_craft_quantity":"9007199254740991",
    "enumeration":"volatile_iterator"
  },
  "max_response_bytes":16384
}
~~~

max_craft_quantity 必须反映当前 Lua 运行时能力，不应直接复制示例。Lua 5.3 / 5.4 具有原生 int64 时可声明更高上限；组件返回的浮点值仍按安全整数范围检查。

同一未退休 boot ID 重传 Hello，返回同一个会话。新 boot ID 退休旧会话、清理 CPU 快照、将可能执行的旧订单标为 needs_review；保留库存并递增版本，通知客户端重取。退休 boot ID 不能重新启用。

OC 实现在每个新进程中建立新会话，不从磁盘恢复 AE 合成 userdata。启动时网络错误会结束该次启动，排查后重新运行即可。来源 oc / simulator 必须与创建网络时一致。

## 库存身份和批次

身份由 kind + name + damage + nbt 决定，标签和数量不参与。nbt 是 **OC 压缩 tag 字节**的规范 Base64，不是 SNBT。服务按这些字段计算身份键，不声称规范化了 NBT 的语义或键顺序。

记录选择短 ID 或完整定义之一：

~~~json
{
  "session_id":"当前会话",
  "sequence":"1",
  "updates":[{
    "item":{"kind":"item","name":"minecraft:stone","damage":0,"label":"石头","exact":true},
    "quantity":"10","observation":"1","craftable":true
  }]
}
~~~

返回的绑定从 **1** 开始索引原始批次。每条携带完整定义的记录都有绑定，包括同批重复身份：

~~~json
{"ack":"1","revision":"2","bindings":[{"index":1,"id":"405"}]}
~~~

后续可使用短 ID：

~~~json
{
  "session_id":"当前会话",
  "sequence":"2",
  "updates":[{"id":"405","quantity":"74","observation":"2","craftable":true}]
}
~~~

quantity 与 craftable 必须显式出现，quantity 不能为 null。craftable 的布尔值表示已确认的配方状态；null 表示本次只观察到数量，保留当前会话上一次确认的状态，新会话尚未确认时为 false。数量与配方分别维护观察水位，同批或跨批到达时各自保留最新值；晚到但比已存配方更新的配方观察可以被接受，其携带的旧数量不会覆盖更新的数量。列表 API 的 craftable 仍为布尔值，observed_at 是服务端接受数量观察的时间；可选 craftable_observed_at 是接受配方观察的时间，仅数量更新不会刷新它。

省略数量绝不等同于零；零库存、可合成的条目必须保留。短 ID 只能引用本网络的条目。列表 API 省略原始 NBT，通过短 ID 下单时由后端恢复完整身份。升级此协议实现时先更新后端，再更新 OC；新后端仍接受已有桥接器发送的布尔值。

OC 的 getItemInNetwork 只确认数量，不将其 isCraftable 标记当作配方观察。枚举提供已知配方标记。周期复核发现已知配方库存为零时，额外核对配方是否仍存在，防止已删除的零库存配方永久保留；每次最多使用四个采集额度，后台检查至多每五秒一次。下单同样核对完整身份，单配方接口正常返回空时使用带完整过滤条件的 getCraftables。过滤结果至多允许一个候选，异常或身份不匹配时不调用 request；已调用 request 的不确定结果不能重试。过滤查询只向 Lua 返回目标配方，但模组内部可能遍历目录，不能视为常数时间查询。

### 传输与观察顺序

sequence 从 1 连续递增，每会话只保留一个未确认库存批次。observation 是进程内读取顺序，单调递增；重传不生成新的观察。

OC 可跨采集时间片保留一个未发送的小批次，条目数和编码字节仍受协商及内存预算限制。`batch_interval_seconds` 默认 0.25，允许 0–1；0 表示时间片结束就发送。满批、合批期限到达、额度用尽、当前任务空闲或枚举结束时发送尾批；带 verification 的观察立即触发发送。该期限只限制主动合批等待，原生调用、控制请求与网络阻塞仍可能增加实际时延。低内存会暂停采集并保留已有尾批。

同批同条目分别采用最新数量和最新已知配方观察。相同观察却给出不同数量或可合成性会冲突。早于对应字段已保存观察的值不能覆盖新值。

最新 sequence 可以重传，必须保持协议字段不变。服务对解码后规范化的协议内容计算摘要；排版和字段顺序不影响摘要。客户端仍应保留**原编码正文**，避免重编码的内存和差异。跳号、同序号改变协议内容返回 409 sequence_conflict，不无限保留历史批次 ACK。

定义解析、库存写入、观察更新、版本、持久事件和 ACK 在同一事务提交。失败不前移 ACK，提交成功但响应丢失时重传不会重复递增版本。

### 数据新鲜度

初始枚举是变化中的迭代器，不是原子快照。枚举结束可上传 scan_completed=true，允许 updates=[]。该标记不能证明覆盖了每个条目。

枚举中没见到条目不能推断归零，精确查询确认消失后才上传零。事件数量可能是增量，事件也可能丢失，因此仅作为精确重读提示；后端还会要求到期条目复核。

条目返回 observed_at、current_session，并在已知时返回 craftable_observed_at；网络返回 last_seen、last_inventory、last_scan。这些条目时间是后端接受观察的时间，包含采集后的排队和传输，不是游戏内采样时钟。craftable_observed_at 缺失表示配方核验时间未知，不能用 observed_at 代替；新会话首次仅数量观察会清空旧配方时间，current_session=false 则表示整条证据仍属于旧会话。online 只代表近期有心跳；quality=unverified 不保证目录完整，累计出现诊断时为 degraded。

库存行保存数值及独立的数量、配方核验证据，普通补查调度另表持久化。重复数值只更新未建索引的核验字段，不重写数值字段或延期普通任务；普通轮询最多检查 128 个到期候选，将同会话且数量、配方均近期核验的候选延至较早核验时间加复核间隔。其余候选仍按接收端容量派发，任务派发本身不代表已经核验。有限窗口可能需要多次轮询才能走到后面的冷物品，复核间隔不保证全目录在该时间内刷新。优先复核仍独立持久化，不因普通新鲜度而取消。

## 控制、CPU 和共享预算

Poll 使用独立序列：

~~~json
{
  "session_id":"当前会话","sequence":"1","reports":[],
  "cpus":[],
  "recheck_slots":16,"recheck_bytes":4096,
  "health":{"free_memory":"131072","diagnostics":[],
    "inventory":{"batches":"2","updates":"16","bytes":"2048","credits_used":"17","collect_millis":"1020"}}
}
~~~

~~~json
{"ack":"1","commands":[],"verify":[],"poll_millis":1000,"scan_budget":32}
~~~

同 server_group 共享额度周期。默认每 1000 ms 只向取得组额度的轮询发放 32 次读取，其余为零。额度同时用于迭代和精确查询，未使用部分在本轮作废。verify 是复核提示，也消耗该额度。

`recheck_slots`（0–16）和 `recheck_bytes`（0–16384）均为可选的普通补查接收容量，缺省兼容旧客户端。任一容量为零时，普通补查不下发，其到期状态保留。后端按原始身份键、解码 NBT、标签及固定描述开销估算接收空间；不合当前字节容量或响应预算的单项短暂推后五秒轮转，避免大描述阻塞其他条目。实际下发的普通项按正常周期再次复查。

OC 普通补查 FIFO 与变动提示、订单优先队列分开，最多 16 项、估算字节最多 16 KiB，进一步按启动空闲内存缩小。重复项不移动队列顺序，热事件不能淘汰已接收的普通补查。普通补查、热事件与目录发现共享授予额度并轮转；低内存或零额度不启动读取。普通容量为零不阻止后端下发订单优先复核。

`health.inventory` 为可选的本会话计数。`batches`、`updates`、`bytes` 仅累计已确认的库存请求、条数和正文字节，丢失 ACK 后原样重传不会重复累计；它们不包含 HTTP/TLS 开销和重试流量，也不代表不同物品种数。`credits_used` 是已消耗的采集额度，`collect_millis` 是相关读取的累计墙钟耗时，包含原生接口等待，不能当作游戏服务器 CPU 耗时。所有字段以十进制字符串传输，OC 计数在 9007199254740991 饱和；游戏库存数量仍按原有精确整数协议处理。

订单结束或人工核对后，verify 可携带可选的 token（32 位小写十六进制）。这是需确认的优先复核，客户端应与普通可淘汰事件分开排队；在收到请求后重新读取完整身份对应的数量及配方，将 token 放入该条 Update 的 verification 字段，并发送布尔 craftable。普通更新、配方未知、陈旧观察和旧 token 不会清除待复核项。新一次同物品订单结束会替换 token，重复报告不会重复安排。

后端每网络每物品最多保留一个待复核项，跨进程和会话重连保存；每次轮询最多选择 4 个优先项，verify 合计仍不超过 16 项，响应字节预算优先。未收到真实确认前每项至少间隔五秒轮换重试。OC 独立队列最多 16 项且受估算字节上限限制，满时由后端继续重试；重复请求不改变已排队项顺序。一次数量及配方核验预留四个采集额度，配方查询与普通零库存复核共享五秒间隔，轮换保留普通扫描机会。该功能要求 AE_SCAN_ITEMS 至少为 4，未获得足够额度时不会越过预算调用组件。

该字段为协议 v1 的可选扩展，升级顺序仍为后端、OC。旧 OC 能继续同步，但不能确认新的优先复核；更新后才具备订单定点刷新保障。后端不会根据订单数量推算或累加游戏库存。

共享合成规划预算默认 1，统计 assigned / computing / needs_review；每网络执行窗口默认 4，还计入 running。任务进入运行阶段后释放规划额度。抖动帮助多设备分摊机会，但当前没有严格公平队列。

控制响应重传保留 ACK；若原命令已变终态或待核对，该命令会从响应中移除。重传响应不必逐字节相同；同一 attempt_id 也不是重新执行指令。

cpus 省略表示保留旧快照，空数组表示此次为空。快照有会话与时间；原生 OC 的 CPU ID 是“快照代数:位置”，不是永久硬件 ID。名字也不保证唯一。

output 是接口可提供的最终产物，可能因缺少合成监控器而不可得，此时看 output_unavailable。truncated=true 表示只保留了部分 CPU。协议没有可信的完成百分比或 ETA。

每次控制请求最多 16 条回报、64 个 CPU、16 类诊断；OC 默认 CPU 上限为 16，还按正文大小裁剪。协商字节预算优先于数量上限。

## 订单与游戏副作用

下单接收 item_id、字符串 quantity、idempotency_key、可选 ttl_seconds。幂等作用域为成员 + 网络 + 键，参数必须相同。队列默认 TTL 为 3600 秒，最多 86400 秒；TTL 不会中断已派发的游戏任务。

命令包含订单 ID、尝试 ID、完整身份、**额外制作数量**及 current_quantity。后者仅供参考，不是自动补货目标。

~~~mermaid
stateDiagram-v2
    [*] --> queued
    queued --> assigned
    queued --> canceled
    queued --> expired
    queued --> failed
    assigned --> computing
    computing --> running
    assigned --> done
    computing --> done
    running --> done
    assigned --> needs_review
    computing --> needs_review
    running --> needs_review
    needs_review --> done
    needs_review --> failed
    needs_review --> canceled
~~~

任何派发后的活跃阶段，也可根据可靠设备回报进入 failed / canceled。API 只允许取消排队订单，改变数据库状态不代表取消了游戏任务。

桥接器在调用 AE 前先写入双槽日志，确认提交意图保存成功；失败时不调用 AE。随后精确查配方、核对产物并请求合成。重复 attempt_id 不再次执行，原生句柄只保存在内存。

日志校验和用于检测损坏。flush 不构成横跨磁盘、JVM 和 AE 的原子事务，因此提供的是幂等传输和防止自动重复执行的恢复策略，**不承诺游戏副作用 exactly-once**。

换会话、轮换凭据或离线超过阈值时，可能执行的订单进入 needs_review，并继续占用执行/规划额度：

- 原进程仍保留句柄时，可继续观察；中间状态被确认但不消除待核对，同会话同尝试的可信终态可以结束订单。
- 句柄丢失后由管理员核对 AE，再调用 /resolve 填写结果和原因。
- 系统不自动把不确定订单重新排队。需要再次制作时明确创建具有新幂等键的新单。

done 与库存观察通过独立通道到达；客户端不能看到 done 后自行给库存加数。

终态订单、审计及幂等键按 30 天保留策略清理；待核对订单不自动删除。过保留期后重用旧键可能创建新单，应始终使用唯一的新单键。

## 查询与 SSE

库存列表默认 50、最多 200 条，按名称升序或数量降序，以短 ID 打破平局。游标绑定网络、查询、排序、可合成筛选、一致性模式及对应版本。

分页默认使用 `consistency=strict`：库存 revision 变化后旧游标返回 409 view_changed，应从首屏重取。纯观察时间更新不增加版本。游标签名密钥属于当前服务进程，服务重启后旧游标返回 400 invalid_request，同样需要重取。不提供冻结整个目录的分页快照。

按名称浏览可显式使用 `consistency=live`。它允许数量变化后继续翻页，每页返回该次请求时后端可见的数量、核验时间和全局 revision；不同页不是同一时刻的数据。目录新增、标签变化或设备会话切换仍返回 409；使用 craftable 筛选时，配方标记变化也返回 409。这些失效检查暂按整个网络保守执行，即使变化位于当前搜索结果以外也可能要求重取。按数量排序只支持 strict，和 live 组合返回 400。搜索、筛选、排序、网络及 consistency 均绑定到签名游标，不能在翻页中途更换。

`GET /api/v1/networks/{network}/items/lookup?ids=405,406` 用于只读取当前显示的物品。只接受一个 ids 参数，内容为 1–200 个规范正整数短 ID，解码后最多 3,999 字节；原始个数在去重前检查，重复 ID 按首次出现顺序合并。返回 `items`、`missing_ids` 和 `revision`，两组 ID 各自保持请求顺序，未知或其他网络的 ID 只出现在 missing_ids，不能据此显示数量零。已知物品的真实零库存正常返回。一次读取使用短只读事务，权限逐次校验，不触发游戏采样、复核或合成，也不刷新任何观察时间。

lookup 返回的仍是后端已有数据，离线或旧会话标记与普通列表相同。它不会重新计算一个旧页面的筛选成员及排序；若要跟随数量排序或筛选变化，应重新查询对应列表。

不足 3 个 Unicode 字符的查询按前缀匹配，较长查询进行子串匹配并使用 trigram 索引；百分号和下划线当作普通字符。

成功的库存列表和 lookup 响应支持 `Accept-Encoding: gzip`，并返回 `Vary: Accept-Encoding`。默认只压缩至少 1024 字节且压缩后确实缩小的正文；客户端显式拒绝 identity 时可压缩小正文。gzip 和 identity 都不可接受时返回 406 encoding_not_acceptable。未协商压缩时保留原 JSON，数量和 ID 的字符串表示不变。该能力不改变 OC 上传/响应编码，也不压缩 SSE 或返回凭据的接口。

普通 Web API 的 `Server-Timing` 包含 `app`（到响应头生成）、`db_query`、`db_acquire`、`db_batch` 和 `db_lock_query`，单位为毫秒。阶段可能重叠：加锁语句属于查询，事务提交也包含在查询中，不能直接相加。它们不包含游戏采集或完整客户端下载时间。

网络 events 端点为 SSE，发送失效元数据：

~~~text
id: 123
event: change
data: {"id":"123","network_id":"...","kind":"inventory","revision":"8","created_at":"2026-11-04T08:00:00Z"}

~~~

首次连接收到 reset。重连带 Last-Event-ID；事件已淘汰、ID 位于未来或待回放超过 128 条时也发送 reset。每网络默认保留 1024 条事件。change.kind 为 inventory、orders、cpus 或 session。

慢消费者不会积累无界发送队列，SSE 在鉴权前受连接数预算控制。当前服务进程中的事件事务提交成功后按网络唤醒订阅者，每个订阅者最多积压一个唤醒信号；持久事件不会因信号合并而丢失。先订阅再读取事件，避免建立连接期间的变化被遗漏；回滚事务不通知。

默认每 15 秒补读持久事件并重查权限；收到通知也会重查权限后读取。进程外写入依靠这次周期补偿，因此多后端实例需要另行加入跨进程通知。会话到期和客户端取消直接结束连接；HEAD 只返回响应头，不占用持续订阅。事件仍逐次 flush，不为了压缩积攒。

online 会随时间自然过期，后续前端除处理事件外，还需低频更新网络状态及观察年龄。不要为每条 SSE 通知拉取整个库存。订单的 /events 端点返回 JSON 审计记录，最多 100 条，并非 SSE。

## 管理性能指标

`GET /api/v1/admin/metrics` 保留原有运行时/连接池字段，新增 `http`、`database`、`notifications` 和 `events`。仅管理员可以读取；不记录请求 URL 参数、SQL、令牌、物品标签或网络 ID。

- `http` 按已注册的固定路由模板聚合，未匹配请求统一为 `unmatched`，SSE 长连接不混入普通请求耗时。`duration` 是服务端处理到写出正文的耗时；`database_*` 是每个请求中的相应阶段累计耗时。`database_queries`、`database_acquires`、`database_batches` 记录调用数，即使操作短到计时器读数为零也计数。`response_bytes` 是交给 HTTP writer 的正文字节，`uncompressed_bytes` 在压缩页上记录序列化后的原文大小；均不包含 HTTP/TLS 或重传。`gzip_responses`、`client_errors`、`server_errors` 是响应计数。
- `database.query` 包含 pgx 查询往返及结果消费；`acquire` 包含获取连接、建连及等待，失败/取消也计数；`batch` 单独记录 SendBatch 整体耗时。`lock_query` 是显式 FOR UPDATE/FOR SHARE 语句的总耗时，包含执行成本，不能等同 PostgreSQL 的纯锁等待，也不涵盖所有隐式写锁。`transaction` / `commit` 记录存储层写事务及其提交。
- `notifications` 记录当前订阅网络/客户端数、事务发布的网络唤醒次数、已合并信号数。空闲网络不长期占用通知表内存。
- `events` 记录持久事件读取、唤醒、兜底检查、发送 change/reset 的次数及读取耗时。`event_age` 是事件 created_at 到服务端 flush 的时间，包含事务前段与历史回放，不是纯提交后延迟或游戏到网页延迟。

延迟直方图使用固定的累计上界桶（1、5、10、25、50、100、250、500、1000、5000、15000 毫秒），包含 `count`、`errors`、`total_ms`、`max_ms` 和 `buckets`。超过最后一个桶的样本仍进入总数、总时长和最大值。次数/字节使用十进制字符串，毫秒是数值；这些累计诊断在进程重启后清零，不提供精确分位数。

## 错误处理

| HTTP / code | 处理 |
| --- | --- |
| 400 invalid_request / invalid_reference | 修正字段、协议、游标或引用 |
| 401 unauthorized | 修正失效凭据或身份类型 |
| 403 forbidden | 权限不足，或 Origin/CSRF 不匹配 |
| 406 encoding_not_acceptable | 库存查询需允许 gzip 或 identity |
| 409 sequence_conflict / stale_session | 保留现场，排查重复进程、会话退休或丢失的待确认正文 |
| 409 conflict | 幂等参数冲突或不允许的状态变化 |
| 409 view_changed | 从首屏重取 |
| 409 capability_unavailable | 精度、配方或设备能力不足 |
| 413 body_too_large / 415 json_required | 修正大小或编码 |
| 429 capacity_exceeded | 退避，并检查是否永久超出协商预算 |
| 503 timeout / busy，网关 502/504 | 保留正文重试；不能据此认定游戏操作没有执行 |

OC 对非致命传输故障重试，对身份、ACK 和协议不一致停止。item_unrepresentable、entry_exceeds_batch_budget、dirty_queue_evicted 等诊断代表观察可能有缺口，不能用旧值冒充实时值。

## 运行边界

一个逻辑网络只运行一个活跃 OC 进程。连接到另一 AE 网格时建立新逻辑网络，避免混合目录。

数量增长、物品种类增长和累计历史是不同的规模。OC 队列有界不代表 JVM 原生分配也被限制。真实吞吐、峰值内存、区块卸载、事件丢失和硬断电恢复仍需游戏存档验证。

迁移按版本在启动时执行，已发布迁移不应原地修改。升级前备份数据库，停止桥接器，更新应用；只有版本涉及 OC 改动时才更新 Lua，再按待核对流程恢复。数据库卷和 OC 执行日志属于运行数据。

004 迁移分离普通调度表，并在事务内重建库存表、预留页内更新空间，保留旧数量、数量观察时间、会话、ACK、订单和待复核任务。大型目录需预留重建期间的临时磁盘空间和维护时间。旧版没有独立配方时间，升级后该时间保持未知，直到收到真实配方观察；旧数量水位作为历史配方的保守排序下限。此迁移不要求更新 OC。旧应用无法直接使用拆分后的结构，回退时应成对恢复升级前的应用和数据库备份，不能只换旧二进制。

005 迁移只在 networks 添加目录和配方的修订号，不重建库存或更改已有观察时间，不要求更新 OC。当前仍只支持一个应用实例，不能让未维护这些新修订号的旧版本与新版本同时写入同一数据库。
