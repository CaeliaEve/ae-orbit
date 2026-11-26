# 开发与贡献

建议使用与 CI 一致的 Go 1.27.3、Node.js 24、Python 3.14 和 PostgreSQL 18。首次启动见 [README 的本地运行步骤](README.md#运行本地实例)。服务、数据库、网页与 OC 的边界见 [技术架构](docs/architecture.md)。

## 本地验证

从仓库根目录运行。Python 检查依赖可装入本地虚拟环境，PostgreSQL 测试使用独立数据库。

```sh
npm ci
npm run test:web
npm run build

python -m venv .venv
# 激活 .venv 后执行
python -m pip install -r scripts/requirements-test.txt
python scripts/test_lua.py
python scripts/verify_contracts.py
python integrations/maibot-ae-orbit/test_bridge.py
python integrations/maibot-ae-orbit/test_inventory_plugin.py
```

网页源文件在 `web/src/`，静态 HTML 在 `web/static/index.html`。提交前端源码时一并提交构建生成的 `web/static/app.js`、`style.css`、`compact.css` 的对应变化。CI 会重新构建并检查差异。只改样式也要构建，因为 Go 和容器直接嵌入静态产物。`npm run dev -- --backend=http://127.0.0.1:8080` 可在本机预览，与开发后端联调。

在独立 PostgreSQL 创建 `aeorbit_test`，设置 `AE_TEST_DATABASE_URL`。测试用户需要创建临时 schema，并能访问已安装的 `pg_trgm` 扩展。以下连接串仅示例，按本地测试库替换。PowerShell 使用 `$env:AE_TEST_DATABASE_URL = 'postgres://...'`。

```sh
export AE_TEST_DATABASE_URL='postgres://postgres:local-test-password@127.0.0.1:5432/aeorbit_test?sslmode=disable'
go test -count=1 ./...
go vet ./...
python scripts/build.py
python scripts/smoke.py --psql "$(command -v psql)"
```

缺少 `AE_TEST_DATABASE_URL` 时，数据库集成测试会跳过。普通 CI 使用代码生成的 NESQL 测试数据。有完整导出时，可额外设置 `AE_TEST_PLAIN_CATALOG_ROOT` 验证真实目录兼容性。CI 的 Go 作业使用 `-race` 并要求全部用例执行。Windows 本地运行 `-race` 需要可用的 C 编译器和 `CGO_ENABLED=1`。 [CI 配置](.github/workflows/ci.yml)

## 修改跨组件接口

| 改动 | 一起核对 |
| --- | --- |
| API 路由、字段与权限 | 对应处理器和 [OpenAPI](contracts/openapi.yaml)。检查匿名、错误凭据及跨网络或服务器组访问。网页会话、设备、MaiBot 和应用密钥各自使用已有的鉴权入口。 |
| SQL 结构或数据迁移 | 新增一个按文件名排序的 SQL 迁移，保留已发布的迁移文件。先验证空库，再验证带旧数据的库升级。检查旧库存、未完成任务和历史仍可读。 |
| OC 字段、能力或模块 | 保留旧客户端省略字段时的行为，按握手能力派发。新增发行文件时核对 [安装器](oc/install.lua)、[后端嵌入清单](oc/embed.go) 及 [ZIP 构建](scripts/build.py)。验证重试、重启和任务状态的连续性。 |
| 网页列表、搜索、实时刷新 | 检查自动刷新时现有页码、定位和筛选是否保留。数量使用十进制字符串或 `BigInt` 做精确计算，未知状态保留最后观测与时间。 |
| 插件、预警或投递 | 通过已有采集预算和插件权限验证。QQ 先跑模拟适配器，核对群白名单、投递回执和重复尝试。真实游戏回调及实际 QQ 送达需要对应环境验收。 |

提交前用 `gofmt` 格式化改动的 Go 文件，用 `git diff --check` 检查提交内容。测试用网络、接收人和凭据使用测试数据；实际 `.env`、OC 配置与日志、NESQL 导出和前端审阅截图存放在忽略目录。历史设计记录可用于理解背景，当前行为以现行契约、测试和源码为准。

合并说明写明验证过的场景，以及部署是否涉及数据库迁移、目录重挂载、OC 或 MaiBot 更新。正式实例的备份、升级和回滚见 [运维手册](docs/operations.md)。
