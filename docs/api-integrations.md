# API 与集成

设置 → API 与集成提供接口地址、应用密钥、连接测试与请求示例。原网页会话和自动重连选项位于同页的「网页连接与会话」。

每位成员可为应用创建独立 `ac_api_` 密钥，最多 50 个。密钥只在创建或更换时显示一次，数据库仅保存 SHA-256 哈希及显示前缀；不会写入浏览器存储。有效期可选 30、90、365 天，修改权限或更换密钥不延长到期时间。停用立即阻止后续请求，更换使旧密钥失效。密钥管理仅限创建者，管理员不会通过管理页面读取其他成员的密钥。

主网库存与 CPU 按明确选定的网络授权；蓝波顿及子网按明确选定的服务器组授权，组授权覆盖该组的对应插件数据。没有选择资源不表示全部资源。权限还受创建者当前网站权限约束，移除网络访问或停用成员后，外部应用不能继续访问对应数据。

API 密钥不能登录网页、访问管理员接口、使用 OC 桥接或 MaiBot 通知桥接。现有 OC 和 MaiBot 凭据继续在原入口管理。首版不提供下单、刷新需求、CPU 过程采集、发送消息或机器控制。

## 只读接口

通过 `Authorization: Bearer API_KEY` 请求：

| 路径 | 权限 | 内容 |
| --- | --- | --- |
| `/api/integration/v1/check` | 有效密钥 | 验证连接与实际可访问范围 |
| `/api/integration/v1/networks` | 有效密钥 | 已授权且创建者可访问的网络 |
| `/api/integration/v1/networks/{network}/items` | `inventory.read` | 库存观测，支持 q、kind、limit、cursor |
| `/api/integration/v1/networks/{network}/cpus` | `cpu.read` | CPU 快照及最终产物 |
| `/api/integration/v1/groups/{group}/energy` | `energy.read` | 蓝波顿缓存电量与流量 |
| `/api/integration/v1/groups/{group}/subnets` | `subnet.read` | 子网清单 |
| `/api/integration/v1/groups/{group}/subnets/{subnet}/items` | `subnet.read` | 已选监控物品与关键词 |

全部查询读取服务端缓存，不给 OC 派发扫描或采集需求。库存默认每页 50 项，最多 200；返回 `next_cursor` 后用于下一页。kind 为 item、fluid、essentia。数量、储能与容量是十进制字符串，调用方不要转换为精度不足的浮点整数。

必须同时检查 `known`、`status`、`observed_at`。未知/过期/离线库存的 `quantity` 为 null，最后观测保存在 `last_quantity`；主网有效期默认 300 秒，并要求采集会话有效及网络在线。CPU 的 known=false 表示保留的历史快照。蓝波顿与子网使用自身配置的有效期，返回最后观测时间。

单个密钥最多同时 4 个请求；服务器总预算仍生效，繁忙时根据 `Retry-After` 重试。最后访问时间最多每 30 秒写入一次，避免高频测试带来额外数据库写入。凭据从 Authorization 头读取，不接受 URL 参数。

```sh
curl 'http://YOUR_SERVER/api/integration/v1/check' \
  -H 'Authorization: Bearer YOUR_API_KEY'
```
