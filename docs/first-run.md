# 首次启动流程（First-Run Bootstrap）

桌面版采用 **Go 单二进制 + 内置初始数据** 的交付方式：用户拿到一个可执行文件，
双击即跑，无需安装 Python / Node / 数据库，**首次启动也不需要联网**——
初始参考数据以 gzip 形式编译进二进制，启动时解压灌库。

## 启动时序

```
启动二进制
  │
  ├─ 1. config.Load()         读环境变量；数据目录默认取「可执行文件同级 data/」
  │
  ├─ 2. db.Open(DataDir)      打开/创建 <DataDir>/sanhu.db（SQLite，WAL）
  │
  ├─ 3. db.RunMigrations()    跑 migrations/001_init.sql 建表（幂等）
  │
  ├─ 4. bootstrap.EnsureSeed()  ← 首启装载初始数据（默认不联网）
  │        │
  │        ├─ SELECT COUNT(*) FROM funds
  │        ├─ >0 ？ → 跳过（老用户/重启不重复灌库）
  │        └─ =0 ？ → 解压内嵌 SeedSQLGz → 单事务灌库 → 按 fund_type 归一化 category
  │                   （设了 SANHU_SEED_URL 则改从该 URL 下载，覆盖内嵌数据）
  │
  ├─ 5. scheduler.Start()     启动 8 个定时任务
  │
  └─ 6. router.Run(:Port)     前端(embed) + API 一起对外服务
```

前端静态资源、AI prompts、SQL migrations、初始数据全部通过 `embed.go` 编译进二进制
（`StaticFS` / `PromptsFS` / `MigrationsFS` / `SeedSQLGz`），所以"统一打包前后端"即一个文件。

## 初始数据（seed）

- **内容**：参考数据表 —— `funds` / `fund_profile` / `fund_peer_ranks` /
  `index_pe_history` / `nav_history` / `fund_industry` / `fund_holdings`。
- **不含**：用户私有数据（holdings / decisions / watchlist / transactions / simulations）——
  seed 是公共参考数据，绝不含任何个人持仓。
- **来源文件**：`seed/seed.sql.gz`（约 1.2MB，仓库内），由 `embed.go` 编译进二进制；
  同时仍作为可选资产随 release 发布，供 `SANHU_SEED_URL` 指向内网镜像时使用。
- **分类归一化**：seed 是早期代码生成的，`category` 全为 mixed 而 `fund_type` 正确，
  灌库后由 `fundcat.NormalizeAll` 按 `fund_type` 重算一次。
- **格式**：gzip 压缩的纯 `INSERT` 语句（无 CREATE，依赖 migration 已建好的 schema）。

## 配置（环境变量）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SANHU_PORT` | `8000` | 监听端口 |
| `SANHU_DATA_DIR` | `./data` | 数据库与本地文件目录 |
| `SANHU_SEED_URL` | `https://github.com/pakoo/sanhu/releases/latest/download/seed.sql.gz` | 初始数据下载地址，可指向内网镜像 |
| `SANHU_DEBUG` | `false` | gin debug 模式 |

## 容错与幂等

- **幂等**：以 `funds` 是否有行为判据。已有数据 → 直接跳过，重启/老用户永不重复灌库。
- **离线容错**：下载或灌库失败 **非致命** —— 打 warning 后空库启动，下次启动（`funds` 仍为空）
  自动重试。不会因为没网就无法启动。
- **原子**：seed 在单个事务内灌入，失败整体回滚，不会留下半灌状态。

## 重新生成 seed

初始数据来自一份已填充的 `data/sanhu.db`，导出参考表为 `INSERT` 语句再 gzip：

```bash
REF="funds fund_profile fund_peer_ranks index_pe_history nav_history fund_industry fund_holdings"
{ echo "PRAGMA foreign_keys=OFF;"; for t in $REF; do
    sqlite3 data/sanhu.db ".mode insert $t" "SELECT * FROM \"$t\";"
  done; } | gzip -9 > seed/seed.sql.gz
```

> 注意：seed 的列顺序依赖 `migrations/001_init.sql` 的 schema。两者必须同源——
> migration 即由同一份 `data/sanhu.db` 的真实结构生成，保证代码 SQL、schema、seed 三者一致。
