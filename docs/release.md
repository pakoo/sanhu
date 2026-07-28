# 发版流程

目标：用户在 GitHub Releases 下载对应平台的包，**解压双击就能用**，不接触源码、不装任何依赖。

---

## 一、版本号规则

用语义化版本 `vX.Y.Z`，tag 即版本号，CI 通过 `-ldflags` 注入到二进制里。

| 位 | 什么时候加 | 例子 |
|---|---|---|
| X 主版本 | 数据不兼容、需要用户手动干预 | Python → Go 重写 = v2.0.0 |
| Y 次版本 | 新功能、新页面 | 新增设置页、录入流程改造 |
| Z 修订号 | 只修 bug，无新功能 | 修解析器、修渲染错乱 |

约束（`internal/updater` 依赖这套规则）：

- **必须带 `v` 前缀**，CI 的触发条件是 `tags: v*`
- **必须三段数字**。自动更新用 `compareSemver` 比较，只有严格新于当前版本才提示升级；
  含预发布标签（如 `v2.1.0-beta1`）会退化成字符串比较，**不要用于正式发布**
- **不要重复发布同一个 tag**，也不要把 tag 移到别的 commit 上 ——
  已经装了旧版的用户是靠版本号判断是否有更新的

## 二、发布一个版本

```bash
# 1. 确认 main 是干净的、CI 能过
git checkout main && git pull
go build ./... && go vet ./... && go test ./...

# 2. 打 tag 并推送（推 tag 会触发 Release workflow）
git tag v2.1.0
git push origin v2.1.0
```

推送 tag 后 GitHub Actions 自动完成：交叉编译 6 个平台 → 打包 → 创建 Release →
上传全部资产 → 用 commit 记录生成 release notes。

**没有别的手动步骤。** 不需要本地编译，不需要手工上传文件。

## 三、产物清单

| 资产 | 平台 | 内容 |
|---|---|---|
| `sanhu-darwin-arm64.zip` | macOS Apple 芯片 | `sanhu.app` + 使用说明.txt |
| `sanhu-darwin-amd64.zip` | macOS Intel | 同上 |
| `sanhu-windows-amd64.zip` | Windows 64 位 | `sanhu.exe` + 使用说明.txt |
| `sanhu-windows-arm64.zip` | Windows ARM | 同上 |
| `sanhu-linux-amd64.tar.gz` | Linux x86_64 | `sanhu` |
| `sanhu-linux-arm64.tar.gz` | Linux ARM64 | `sanhu` |
| `seed.sql.gz` | — | 初始数据，**可选**（已内嵌进二进制，仅供内网镜像用 `SANHU_SEED_URL` 指过来） |

打包细节见 `.github/workflows/release.yml`，macOS 的 `.app` 描述文件在 `packaging/macos/Info.plist`。

## 四、「下载即用」靠哪几件事支撑

发版方案不只是打包脚本，下面每一条缺了都会让用户卡住：

1. **初始数据内嵌**（`embed.go` 的 `SeedSQLGz`）——首启不联网，不依赖 GitHub 可达性
2. **自动打开浏览器**（`cmd/sanhu/launch.go`）——双击后不需要用户自己去敲 `localhost:8000`
3. **重复双击不崩**——检测到已有实例就只把浏览器带过去，正常退出；
   端口被别的程序占用时给出人话提示，而不是 `bind: address already in use`
4. **macOS 打成 `.app`**——否则用户拿到的是个裸二进制，只能开终端跑
5. **数据目录与程序分离**——`.app` 内部不写数据（覆盖升级不会丢），
   Windows 放 exe 同级（绿色软件的习惯）
6. **压缩包内附中文使用说明**——首次运行必然被 Gatekeeper / SmartScreen 拦，
   得提前告诉用户怎么过

## 五、已知限制

- **没有代码签名与公证**。macOS 首次打开要右键→打开，Windows 要点「仍要运行」。
  解决需要 Apple 开发者账号（$99/年）+ Windows 代码签名证书（约 $200-400/年），
  纯粹是花钱买体验，不影响功能。
- **Windows 会显示一个命令行窗口**。它同时充当「关闭程序」的入口，
  要去掉需要改成 GUI 子系统（`-H windowsgui`），但那样用户就没有可见的退出方式了。
- **`.app` 未做 universal binary**。Intel 和 Apple 芯片是两个包，用户需要选对。
  合并成一个 universal 包需要 `lipo`，得在 macOS runner 上跑，成本更高。

## 六、发版后自检

```bash
# 确认资产齐全（应有 6 个平台包 + seed）
gh release view v2.1.0
```

再抽一个平台实测：下载 → 解压 → 双击 → 浏览器自动打开 → 录一只基金看看数据对不对。
