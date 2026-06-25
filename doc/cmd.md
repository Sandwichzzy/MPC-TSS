# 命令与触发链路

本文只整理这个项目里实际存在的 Cobra 命令，以及它们启动后是被什么事件继续驱动的。
这里不展开业务协议细节，也不把 HTTP 路由、WebSocket method 或内部 goroutine 当作命令。

## 命令总览

| 命令 | 入口函数 | 启动后监听什么事件 | 谁触发它 |
| --- | --- | --- | --- |
| `tss` | [main()](../cmd.go) -> `rootCmd.ExecuteContext(ctx)` | 不直接监听业务事件；负责解析 CLI 参数、执行 `PersistentPreRunE`、分发子命令 | 用户在终端执行 `tss ...`，或 Docker 通过 `ENTRYPOINT ["tss"]` 启动进程 |
| `tss manager` | [manager.Command()](../manager/cmd.go) -> `run(cmd)` | Node 连接 `/ws`；外部 HTTP 请求 `/ping`、`/api/v1/keygen`、`/api/v1/sign`、`/api/v1/metrics`；系统信号 `SIGINT` / `SIGTERM` | 用户手工执行，或 Docker Compose 启动 manager 容器；运行后继续由 Node、HTTP 调用方、操作系统信号驱动 |
| `tss node` | [tssnode.Command()](../node/cmd/tssnode/cmd.go) -> `runNode(cmd)` | Manager 通过 WebSocket 下发 `askMessageHash`、`TransactionSign`、`keygen`；其他节点通过 libp2p 发送 TSS round message；本地 HTTP `/metrics`；系统信号 `SIGINT` / `SIGTERM` | 用户手工执行，或 Docker Compose 启动 node 容器；运行后继续由 Manager、其他 Node、操作系统信号驱动 |
| `tss parse-peer-id` | [tssnode.PeerIDCommand()](../node/cmd/tssnode/cmd.go) | 不监听；一次性读取 `--pub-key` 或 `--pri-key`，计算 peer id 后立即退出 | 用户手工执行 CLI |

## 统一启动链路

所有子命令都先经过根命令 [cmd.go](../cmd.go)：

1. `main()` 创建 `rootCmd`
2. 根命令注册全局 `-c/--config`
3. `PersistentPreRunE` 先读取配置文件
4. 配置被写入 Cobra context
5. 然后才分发到 `manager`、`node` 或 `parse-peer-id`

这意味着：

- 配置加载不是子命令各自完成的，而是根命令统一完成
- `manager` 和 `node` 都通过 `config.GetConfigFromCmd(cmd)` 读取已经注入的配置
- `parse-peer-id` 也会经过根命令分发，但它本身是一次性工具，不会进入长生命周期服务循环

## 运行后谁继续驱动它

可以把触发源分成三类：

- CLI / Docker：触发 Cobra 命令本身启动，例如 `tss manager -c ...` 或 Docker Compose 容器启动
- 网络事件：触发长生命周期服务继续运行，例如外部 HTTP 请求、Node 连接 Manager 的 `/ws`、Manager 向 Node 下发 WebSocket 指令、Node 之间的 libp2p TSS 消息
- 系统信号：触发优雅退出，例如 `SIGINT`、`SIGTERM`

按这个划分：

- `tss` 是命令分发器，不直接处理业务事件
- `tss manager` 是控制面服务，启动后主要由 HTTP 请求和 Node 的 WebSocket 连接驱动
- `tss node` 是执行面服务，启动后主要由 Manager 下发的 WebSocket 指令和节点间 libp2p 消息驱动
- `tss parse-peer-id` 是离线工具命令，执行一次后立即结束
