# 按需合成 CPU 清单

CPU 监控的“查看合成过程”打开独立面板：active（AE 已派发、等待产物返回）、pending（待执行步骤产物）、stored（CPU 内材料）。这些清单不能证明某台加工机器正在运行，也不能推导完成百分比。

浏览器每 5 秒续租一个 15 秒 viewer；每网络只允许一台目标 CPU，查看同一目标的用户共享采集。关闭、切换网络/页面、隐藏浏览器停止续租并删除 viewer。已经派发的一次读取可能仍完成；无有效租约时结果不入库。

主 OC 在协议 2 协商 cpu_details 能力。poll ACK 按需携带 cpu_detail assignment，期限 8 秒，消耗一个采集额度。三种清单轮流派发，每次相隔至少 10 秒；重放 ACK 不重新派发，OC 去重并保留不可变上报包。副 OC 不参与。更新主 OC 后重启协商能力；旧客户端保持原有最终产物监控。

升级时从后端重新下载安装器，确保主 OC 得到完整模块清单。先将示例地址替换为当前实例地址。

```sh
ae-orbit stop
wget -f https://ae.example.com/oc/install.lua /tmp/ae-orbit-install.lua && lua /tmp/ae-orbit-install.lua https://ae.example.com && reboot
```

重启后执行 `ae-orbit start`。基础 CPU 上报只保留有限尺寸产物字段，不依赖详情模块的加载和哈希；单台产物信息异常只标记该台产物不可用，其他 CPU 的状态仍上报。

目标使用当前启动会话与 CPU 快照 ID，OC 缓存并固定原 CPU userdata，绝不根据新枚举下标重新选择。可用时在读取前后校验最终产物签名，并检查忙碌状态；任务变化/引用过期会停止采集。未安装合成监控器或连续任务最终产物相同时，接口不能严格识别任务边界，应将清单理解为此 CPU 在采样时刻的状态。

每栏最多保留 64 个条目、上报结果最多 12 KiB。数量使用十进制字符串，NBT 只保留有限尺寸指纹，不上报原始 tag；纹理仅通过已验证目录匹配，NBT 基础纹理明确标记。错误保留最后成功数据及时间；成功空列表清空旧条目。每栏独立采样，不构成同一时刻的原子快照。

三个 OC 回调仍会在 Java 内创建 CPU 局部 AE ItemList，然后才返回 Lua。Lua 的条目/字节上限不能限制 Java 列表构建，也无法捕获所有 Java 原生故障。这避开既有整网 NetworkMonitor.getStorageList 崩溃路径，但不能承诺绝对不会崩溃；若再次崩溃，停止详情采集并保留新堆栈，不启用全网枚举兜底。

POST /api/v1/networks/{network}/cpu-details 创建或续租并返回缓存；DELETE /api/v1/networks/{network}/cpu-details/{viewer} 关闭自己的租约。当前会话、网络读权限、主 OC 在线和能力是前置条件；另一台 CPU 占用返回 capacity_exceeded。图片 endpoint 只服务当前启动且最近两分钟成功观测中的已核验资产，不写入库存。
