# NESQL++ 目录升级

新版导出提供名称、说明和纹理，库存数量与可合成状态仍由 OC 实际查询决定。升级目录不会生成库存，也不会触发整网枚举。

## 保留旧版无 NBT 身份

当新版把某些原有无 NBT 物品导出为带空 compound NBT 的变体时，两者仍是不同身份。不要清空或归一化 NBT。合并脚本只补入新版缺少的旧版无 NBT `registry + metadata` 身份，以及这些记录原有的文字、asset 和 PNG 引用；新版已包含的无 NBT 身份始终使用新版数据。

```powershell
python scripts/merge_nesql_catalog.py `
  "E:\exports\new\datasets\<new-id>" `
  "E:\exports\old\datasets\<old-id>" `
  --output ".local\catalog\merged-<revision>"
```

如果旧版只有完整的 manifest 和 records，原 PNG 保存在纹理包内，可额外传入 `--fallback-textures "E:\ae-orbit-textures.zip"`。脚本会按旧版 manifest 校验 PNG，而不是按名称猜测图片。

脚本验证声明文件的长度和 SHA-256、压缩记录的解压长度与行数。两个原始导出保持不变，输出目录必须独立且不存在。新 manifest 保存主源、兼容源和补入记录的来源，旁边生成 `*-report.json`。导出数据与纹理包存放在忽略的 `.local/` 下，不提交到 Git。

## 更新运行实例

1. 用实际目录验证名称、身份和图片，备份数据库并保留旧目录、纹理包及 API 镜像。
2. 将只读 catalog 挂载指向新目录；使用本地纹理包时，从同一目录重新运行 `scripts/build_texture_pack.py`。
3. 使用原有 Compose 配置重新创建 API。更换符号链接或文件后也要重新创建容器，以更新绑定挂载；同时保留 catalog 和纹理包的覆盖配置。
4. 管理员调用 `POST /api/v1/networks/{network}/inventory-profile/nesql-plain`。已启用的网络会增量更新候选名称与搜索索引、注册新候选，不重置启用时间、已有查询计划或读取回执。
5. 检查候选数、原有库存与历史、图片接口及 OC 能力标志。重复导入相同目录不会重复增加候选或改变目录修订号。

刷新保留已有候选，即使它们在新目录中缺失。因此应生成包含兼容项的目录，确保这些候选的文字和纹理仍可解析。物品定义及历史记录的原始标签保持存档语义，当前库存页面采用更新后的候选名称与拼音搜索索引。

本次 2026-11-20 合并采用新版 `da7e6bdf…` 和旧版 `69af6327…`：新版无 NBT 身份 53,711 个，补入旧版 35 个，共 53,746 个；相对旧版增加 57 个候选。新增候选查询成功后才计入库存，默认视图仍只显示已确认数量大于零的物品。

## OC 与浏览器

已运行精确查询方案的 OC 无需更新。继续保持 `native_scan_disabled`、`events_disabled` 和 `exact_quantities` 能力检查，不启用原生网络内容监听或整网扫描。目录升级改善显示信息，不构成对游戏服务器所有崩溃的保证。

服务器按需图片会使用新内容键和对应版本地址，避免浏览器的长期缓存继续显示旧图。浏览器曾导入旧 ZIP 或使用固定纹理包 URL 的用户，需要在设置中的“纹理资源”重新加载对应新版包；同一份新目录生成的包与服务端图片一致。
