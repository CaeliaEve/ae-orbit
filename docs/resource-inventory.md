# 流体和源质库存

库存页面可切换物品、流体、源质。默认仅显示已确认有库存的资源；“全部名单候选”包含尚未查询和已确认数量为零的资源。NESQL 导出只提供身份、中文名称和纹理，数量必须来自 OC 的认证观测。

流体按注册名查询，显示 mB；源质按 aspect tag 查询，显示源质量。仅接纳 NESQL 名单中的无 NBT 身份。物品上的源质组成不是网络库存。三类数量分别统计；现有主页总量和库存趋势继续统计物品。

管理员在已启用 NESQL 无 NBT 物品名单的协作网络上调用 `POST /api/v1/networks/{network}/inventory-profile/resources`，幂等注册流体和源质名单。此操作不会重置物品扫描进度、启用时间和历史。新版 OC 明确上报 `fluids` / `essentia` 能力后，才会收到对应任务。旧版 OC 继续同步物品。

## 更新 OC

在主 OC 和支持对应组件的副 OC 上依次执行：

```sh
ae-orbit stop
ae-orbit-install http://125.77.172.25:8080
reboot
```

重启后执行 `ae-orbit start`（已设置开机启动的机器会自动运行）。更新器保留 `/etc/ae-orbit.cfg`。必须保留 `inventory_mode="exact_only"` 和关闭库存事件的配置；重启让 OpenOS 清除旧模块缓存并重新上报能力。

## 查询与稳定性边界

流体只调用 `getFluidInNetwork({name=registry,amount=0})`，源质只调用 `getEssentiaInNetwork({name=tag,amount=0})`。绝不调用 `getFluidsInNetwork()` 或无参数 `getEssentiaInNetwork()`。启动时也关闭流体、源质的事件订阅，避免监听回调重建整网列表。

资源共用原有全组读额度、队列内存、响应包和过期限制。后台通常每四个物品/资源查询机会分配一个资源查询；仅有资源到期时可连续查询。资源不参与合成、待提交清单或物品历史。查询结果身份不同、含标签或数量无法精确表达时，保留旧观测并报告失败。

这消除了本采集器主动枚举整网的路径，不能保证 AE2、跨网络存储总线或其他模组永远不崩溃。若采集后仍出现原崩溃，需要新的完整堆栈确认实际触发路径。
