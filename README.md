# OpenPenetrate - 内网穿透可视化管理工具

OpenPenetrate（IntranetPenetration）是一款基于 [Wails3](https://v3.wails.io/) + Vue3 构建的跨平台内网穿透管理工具，底层集成 [frp](https://github.com/fatedier/frp)，让你无需手动编写配置文件，通过图形化界面即可完成内网穿透的全部操作。

## 功能特性

- **本地穿透管理**：可视化管理本机 frpc 客户端，添加、编辑、启停穿透隧道，实时查看 frpc 运行日志与流量统计。
- **穿透服务器管理**：通过 SSH 一键远程安装/部署 frps 服务端，实时推送安装日志，自动发现与管理已有的穿透服务器。
- **客户端节点部署**：将客户端节点一键部署到远程机器，实时查看节点部署日志。
- **流量监控**：内置流量统计面板，直观展示各隧道的上下行速率。
- **开箱即用**：内置 frp 0.71.1（Windows/Linux amd64）发行包，无需单独下载。
- **跨平台**：支持 Windows、macOS、Linux 桌面端，并预留 Android/iOS 构建配置。

## 技术栈

| 层级 | 技术 |
| ---- | ---- |
| 桌面框架 | Wails3 (Go) |
| 后端 | Go、SSH、frp |
| 前端 | Vue3 + TypeScript + Vue Router |
| 打包 | Taskfile、NSIS（Windows 安装包）、nfpm（Linux） |

## 快速开始

### 环境要求

- Go 1.24+
- Node.js 18+ / npm
- [Wails3 CLI](https://v3.wails.io/learn/get-started/)

### 开发模式

```bash
wails3 dev
```

启动后支持前端与后端的热重载，实时查看修改效果。

### 生产构建

```bash
wails3 build
```

构建完成后，可在 `build/bin` 目录找到可执行文件。

## 项目结构

```
OpenPenetrate/
├── main.go                  # 应用入口，注册服务与事件
├── proxyservice.go          # 核心服务，绑定给前端调用的 API
├── frpcmanager.go           # 本机 frpc 进程与日志管理
├── frpsinstall.go           # 远程 SSH 安装 frps
├── frpsdiscover.go          # 穿透服务器自动发现
├── serverdiscover.go        # 服务端发现辅助逻辑
├── clientnode.go            # 客户端节点管理
├── clientnode_local.go      # 本地客户端节点逻辑
├── sshclient.go             # SSH 客户端封装
├── traffic.go               # 流量统计
├── frp_bundle.go            # 内置 frp 发行包
├── assets/frp/              # frp 0.71.1 发行包（Windows/Linux）
└── frontend/
    ├── src/views/
    │   ├── LocalProxyView.vue    # 本地穿透页面
    │   ├── ProxyServerView.vue   # 穿透服务器页面
    │   └── ClientNodeView.vue    # 客户端节点页面
    ├── src/components/      # 流量弹窗、主题切换等组件
    └── bindings/            # Wails 自动生成的 TS 绑定
```

## 使用说明

1. **部署穿透服务器**：打开「穿透服务器」页面，输入一台有公网 IP 的服务器 SSH 信息，一键安装 frps。
2. **配置本地穿透**：打开「本地穿透」页面，添加隧道（如将本机 3389 远程桌面、22 SSH、80 Web 服务暴露到公网），选择穿透服务器并启动。
3. **管理客户端节点**：在「客户端节点」页面，可将客户端一键部署到其他内网机器，统一管理多台设备的隧道。

## 教程视频

### 国内视频（哔哩哔哩）

| 教程 | 链接 | 说明 |
| ---- | ---- | ---- |
| 快速上手：10 分钟完成第一次内网穿透 | 【待补充视频链接】 | 安装、部署 frps、添加第一条隧道 |
| 穿透服务器一键部署详解 | 【待补充视频链接】 | SSH 远程安装 frps 与常见问题排查 |
| 多设备节点管理实战 | 【待补充视频链接】 | 客户端节点部署与批量管理 |

<!-- 
在此填入哔哩哔哩视频链接，格式示例：
- [快速上手：10 分钟完成第一次内网穿透](https://www.bilibili.com/video/BVxxxxxx)
也可以直接嵌入播放器：
<iframe src="//player.bilibili.com/player.html?bvid=BVxxxxxx&page=1" scrolling="no" border="0" frameborder="no" framespacing="0" allowfullscreen="true"></iframe>
-->

### 国外视频（YouTube）

| Tutorial | Link | Description |
| -------- | ---- | ----------- |
| Getting Started | 【TODO: video link】 | Setup & first tunnel in 10 minutes |
| Deploying frps Server | 【TODO: video link】 | One-click remote frps installation |
| Managing Client Nodes | 【TODO: video link】 | Multi-device node deployment |

<!-- 
在此填入 YouTube 视频链接（视频由项目维护者提供），格式示例：
- [Getting Started](https://www.youtube.com/watch?v=xxxxxx)
也可以直接嵌入播放器：
<iframe width="560" height="315" src="https://www.youtube.com/embed/xxxxxx" title="YouTube video player" frameborder="0" allowfullscreen></iframe>
-->

## 常见问题

**Q: 穿透服务器安装 frps 失败？**
确认服务器 SSH 账号具备 root 或 sudo 权限，且防火墙已放行 frps 绑定端口（默认 7000）及隧道端口范围。

**Q: frpc 启动后无法连接服务器？**
检查服务器公网 IP、frps 端口与 token 是否填写正确，查看「本地穿透」页面的实时日志定位原因。

**Q: 支持哪些平台？**
桌面端支持 Windows、macOS、Linux；项目内置了 Android/iOS 构建配置，可按 Wails3 文档自行构建移动端。

## 参与贡献

欢迎提交 Issue 与 Pull Request。开发时使用 `wails3 dev` 进行调试，提交前请确保前端可正常构建。

## 许可证

本项目采用 [Apache-2.0](./LICENSE) 许可证，作者 [puweixiang](https://github.com/pwxpwxtop)。

核心穿透能力基于 [frp](https://github.com/fatedier/frp)（Apache-2.0）实现，内嵌其官方发行包并遵循原许可证。
