# NetDream! It's MyGO!!!!!

**一个使用 [MyGO](https://github.com/egoist/mygo) 开发的 Postman 离线替代品**

English: [README.en.md](README.en.md)

NetDream 是一个跑在桌面上的 API 调试工具：Go 后端直接发包，系统 webview 渲染界面——没有 Electron，没有云端，没有账号，完全离线。

## 特性

- **HTTP 调试**：方法 / Header / Query / Path 参数 / Body（form-data、urlencoded、raw、binary）、Basic / Bearer 认证、Cookie、超时
- **HTTP/2**：`https://` 自动协商，`http://` 可强制 h2c，响应协议版本可见
- **SSE 流式响应**：事件实时列表、随时停止
- **WebSocket / TCP 调试**：连接 / 断开、握手请求头 / 查询参数 / 子协议、消息模板（Text / JSON / XML / HTML / Binary）、实时收发日志、握手响应头
- **响应视图**：美化（JSON / HTML 语法高亮）、原始报文、HTML 沙箱预览、一键复制
- **断言面板**：状态码、包含 / 不包含、响应耗时
- **工作区（.band）**：一请求一文件的 zip 压缩包，支持打开 / 保存 / 另存为 / 拖入导入；请求执行即自动入册内存工作区（成败都收），method / URL 变更后执行自动派生新条目，别名保留
- **自动保存**：连接建立即入册，断开后自动刷新，保存 = 内存工作区整体落盘

## 技术栈

- [MyGO](https://github.com/egoist/mygo)：Go 后端 + 系统 webview，绑定用 `mygo.Bind`，前端类型由 `mygo generate` 生成
- 前端：原生 TypeScript（strict、零运行时依赖）+ Vite，UI 参照 ApiPost 调试页
- 契约文档：[`docs/architecture.md`](docs/architecture.md)

## 快速开始

需要 [Go](https://go.dev/)、[Bun](https://bun.sh/) 和 mygo 的平台依赖（见 `mygo doctor`）。

```bash
bun install                  # 安装 mygo-cli
bun install --cwd frontend   # 安装 vite / typescript
bun run dev                  # 开发模式（热重载）
```

辅助脚本：

```bash
bun run generate      # 重新生成前端 IPC 绑定（frontend/src/mygo.ts）
bun run typecheck     # 前端类型检查
bun run sse:demo      # 本地 SSE 测试服务器（http://localhost:8123/sse）
bun run socket:demo   # 本地 WS/TCP 回显服务器（ws://localhost:8124，tcp://localhost:8125）
```

## 构建

```bash
bun run build                                    # 当前平台，产物在 build/
bunx mygo build -platform darwin/arm64           # 交叉构建（MyGO 不需要 cgo）
bunx mygo build -platform windows/amd64 -skip-installer
```

## 发布

在 GitHub 上 **创建 Release**（`Release` → `Draft a new release` → 填 tag 如 `v0.1.0` 并发布）后，[release.yml](.github/workflows/release.yml) 会自动构建并上传：

- Windows x64 / ARM64：`.exe` + `.zip` + NSIS 安装包（`-setup.exe`）
- macOS ARM64：`.dmg` + `.app.zip`（ad-hoc 签名，首次打开需右键 → 打开）

发布前把 `mygo.config.ts` 里的 `version` 改成与 tag 一致。

## 已知限制

- 工作区路径不跨重启记忆；列表暂无搜索 / 排序
- 预执行 / 后执行脚本页为占位
- form-data 仅支持文本字段（无文件上传）
- 拖入导入的工作区保存时转「另存为」（webview 拿不到拖入文件的路径）

## License

MIT
