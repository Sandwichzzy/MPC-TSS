# TSS 项目 WebSocket 框架说明

本文只解释这个项目里的 WebSocket 体系，包括：

- WebSocket 在整个项目中的职责边界
- `ws/server` 和 `ws/client` 的代码结构
- Manager 作为服务端、Node 作为客户端是如何建立连接、发送消息、收消息、保活和重连的
- 签名与 keygen 两条业务链路如何使用 WebSocket

这份文档描述的是当前仓库里的实际实现，不是抽象设计稿。

## 1. 先说结论：WebSocket 在这个项目里是“控制面”

这个项目里有两类网络通信：

1. `Manager <-> Node`：走 WebSocket
2. `Node <-> Node`：走 `node/tsslib/p2p` 的 libp2p 通信

也就是说，WebSocket 不是用来跑 TSS 算法分片消息的。  
它只负责 Manager 和 Node 之间的任务调度与结果回传，例如：

- Manager 让 Node 先做一次 `askMessageHash`
- Manager 让 Node 正式执行 `TransactionSign`
- Manager 让 Node 执行 `keygen`
- Node 把 `AskResponse`、`SignResponse`、`KeygenResponse` 回给 Manager

真正的 TSS 协议细节，还是发生在 Node 内部调用 `tssServer.Keygen(...)` / `tssServer.KeySign(...)` 之后，由 Node 间的 p2p 层去完成。

可以把它理解成下面这个分层：

```text
HTTP 调用方
   -> Manager HTTP API
   -> Manager WebSocket Server
   -> Node WebSocket Client
   -> Node signer
   -> node/tsslib
   -> node/tsslib/p2p (Node <-> Node)
```

## 2. 相关代码入口

如果只看 WebSocket 相关实现，建议重点看这些文件：

- `manager/cmd.go`
- `ws/server/server.go`
- `ws/server/handler.go`
- `ws/server/types.go`
- `ws/client/client.go`
- `ws/client/tm/client.go`
- `node/signer/process.go`
- `node/signer/deal_msg.go`
- `node/signer/verify.go`
- `node/signer/sign.go`
- `node/signer/keygen.go`

这些文件之间的职责划分如下：

| 位置 | 作用 |
| --- | --- |
| `manager/cmd.go` | 启动 manager，拉起 WebSocket 服务和 HTTP API |
| `ws/server/server.go` | Manager 侧 WebSocket 服务的外层启动器 |
| `ws/server/handler.go` | Manager 侧连接管理、鉴权、读写循环、消息路由 |
| `ws/client/client.go` | Node 侧 WebSocket 业务包装层 |
| `ws/client/tm/client.go` | Node 侧底层 WebSocket 客户端、重连和读写协程 |
| `node/signer/process.go` | Node 侧 Processor，创建并持有 WebSocket 客户端 |
| `node/signer/deal_msg.go` | 把收到的 WebSocket 请求按 method 分发到不同业务通道 |
| `node/signer/verify.go` | 处理 `askMessageHash` |
| `node/signer/sign.go` | 处理 `TransactionSign` |
| `node/signer/keygen.go` | 处理 `keygen` |

## 3. 整体架构图

```text
                         +-----------------------------+
                         |         Manager             |
                         |                             |
HTTP /api/v1/sign -----> |  business flow             |
HTTP /api/v1/keygen ---> |  agreement/sign/keygen     |
                         |            |                |
                         |            v                |
                         |   WebsocketManager          |
                         |   - aliveNodes             |
                         |   - sendChan[nodePubKey]   |
                         |   - recvChanMap[requestId] |
                         +-------------+---------------+
                                       |
                                       | /ws
                                       | JSON-RPC over WebSocket
                                       |
               ---------------------------------------------------------
               |                           |                           |
               v                           v                           v
      +------------------+       +------------------+       +------------------+
      |      Node A      |       |      Node B      |       |      Node C      |
      | WSClients        |       | WSClients        |       | WSClients        |
      | tm.WSClient      |       | tm.WSClient      |       | tm.WSClient      |
      | Processor        |       | Processor        |       | Processor        |
      | verify/sign/...  |       | verify/sign/...  |       | verify/sign/...  |
      +---------+--------+       +---------+--------+       +---------+--------+
                |                          |                          |
                +----------- libp2p / TSS p2p communication ----------+
```

这个结构里有一个很重要的事实：

- Manager 是 WebSocket 服务端
- Node 是 WebSocket 客户端
- 所有 Node 都主动拨号连接 Manager 的 `/ws`
- Manager 不会反向去连接 Node

## 4. 协议模型：WebSocket 上传输的到底是什么

### 4.1 不是自由格式消息，而是 Tendermint JSON-RPC

这个项目没有自己发明一套 WebSocket 帧格式，而是直接使用 Tendermint 的 JSON-RPC 类型：

- `tmtypes.RPCRequest`
- `tmtypes.RPCResponse`

所以 WebSocket 上真正传的，是：

| 方向 | 外层对象 | 关键字段 |
| --- | --- | --- |
| Manager -> Node | `RPCRequest` | `ID`、`Method`、`Params` |
| Node -> Manager | `RPCResponse` | `ID`、`Result`、`Error` |

其中：

- `ID` 是整个请求/响应关联的核心键
- `Method` 用来表示业务动作
- `Params` 或 `Result` 承载真正的业务 JSON

### 4.2 当前使用的 method

当前 WebSocket 业务动作主要有三类：

| Method | 方向 | 含义 |
| --- | --- | --- |
| `askMessageHash` | Manager -> Node | 先询问节点是否愿意参与本次签名 |
| `TransactionSign` | Manager -> Node | 正式执行签名 |
| `keygen` | Manager -> Node | 执行分布式生成聚合公钥 |

### 4.3 业务 payload

这些业务结构定义在 `common/types.go`：

| 结构 | 用途 |
| --- | --- |
| `TransactionSignRequest` | 签名请求，包含 `message_hash`、`election_id`、`request_id` |
| `AskResponse` | ask 阶段的响应，只有一个 `Result bool` |
| `NodeSignRequest` | 发给节点的正式签名请求，包含 `cluster_public_key`、参与节点列表、原始请求体 |
| `SignResponse` | 节点返回的签名结果 |
| `KeygenRequest` | keygen 请求，包含 `nodes`、`election_id`、`request_id`、`threshold` |
| `KeygenResponse` | 节点返回的聚合公钥 |

### 4.4 request ID 的使用方式

这个项目的 WebSocket 路由极度依赖 `ID` 字段。

当前约定如下：

- ask 阶段的请求 ID 是：`"ASK_" + requestId`
- sign 阶段的请求 ID 是：`requestId`
- keygen 阶段的请求 ID 是：调用方传入的 `requestId`

Manager 先把 `requestId -> recvChan` 注册到 `recvChanMap`，Node 返回响应时，服务端再根据响应里的 `ID` 找到等待中的业务协程。

所以可以说：  
**这个项目不是按连接回调做业务，而是按 `requestId` 做响应路由。**

## 5. 连接建立流程

连接建立发生在 Node 启动阶段。

### 5.1 Manager 如何启动 WebSocket 服务

在 `manager/cmd.go` 中，Manager 启动时会：

1. 初始化本地存储
2. 调用 `server.NewWSServer(config.Manager.WsAddr)`
3. 创建 `Manager` 业务对象
4. 再启动 HTTP API

这里说明一件事：

- Manager 的 HTTP API 和 WebSocket 服务是两条独立链路
- 对外部调用方暴露的是 HTTP
- 对 Node 暴露的是 `/ws`

### 5.2 `NewWSServer` 做了什么

`ws/server/server.go` 的 `NewWSServer` 主要做 5 件事：

1. 创建 Tendermint 的 `rpcserver.Config`
2. 创建 `WebsocketManager`
3. 把 `/ws` 路由挂到 `WebsocketManager.WebsocketHandler`
4. 用 Tendermint 的 `rpcserver.Listen` 创建监听器
5. 在 goroutine 里调用 `rpcserver.Serve(...)`

这里说明该项目没有自己手写一套完整 HTTP 服务器壳子，而是复用了 Tendermint 的 JSON-RPC server 框架来承载 `/ws`。

### 5.3 Node 如何发起连接

Node 的连接入口在 `node/signer/process.go` 的 `NewProcessor(...)`：

1. 创建 `Processor`
2. 调用 `client.NewWSClient(cfg.Node.WsAddr, "/ws", privKey, pubKeyHex)`
3. `NewWSClient` 内部立刻 `Start()` 底层 `tm.WSClient`
4. 一旦连接失败，`NewProcessor` 就会直接返回错误

这意味着：

- WebSocket 连接是 Node 启动必需条件之一
- Node 不是懒连接
- 连接不是在收到第一笔业务时才建立，而是在 Processor 初始化时就建立

### 5.4 握手鉴权是怎么做的

Manager 侧鉴权逻辑在 `ws/server/handler.go` 的 `WebsocketHandler` 中。  
Node 侧发 header 的逻辑在 `ws/client/tm/client.go` 的 `dial()` 中。

Node 连接时会在握手 header 带上三项：

| Header | 含义 |
| --- | --- |
| `pubKey` | 节点公钥的十六进制字符串 |
| `sig` | 节点私钥对时间戳摘要的签名 |
| `time` | 当前 unix 时间戳 |

鉴权过程是：

1. Node 生成当前时间戳 `timeStr`
2. 计算 `Keccak256(timeStr)`
3. 使用节点私钥对摘要签名
4. 把 `pubKey`、`sig`、`time` 放入 WebSocket 握手 header
5. Manager 收到连接后：
    - 检查 `pubKey`、`sig`、`time` 是否存在且长度合法
    - 检查时间戳是否在允许窗口内
    - 使用 `pubKey` 验证签名是否匹配 `Keccak256(timeStr)`

这一套的目的，是证明发起连接的对端确实持有该公钥对应的私钥。

### 5.5 时间戳限制

Manager 侧有个 `legalTimeStampPeriod = 5`，表示时间戳超过 5 秒就拒绝。  
这是一种简单的防重放机制。

也就是说，Node 不能长期复用旧签名去连 Manager。

### 5.6 鉴权通过后会发生什么

鉴权通过后，Manager 会：

1. 把底层 `gorilla/websocket.Conn` 包装为 `wsConnection`
2. 应用连接选项，例如 `OnConnect` 和 `OnDisconnect`
3. 调用 `Start()` 启动这条连接的读写循环

其中 `OnConnect` 很关键，它会做两件事：

1. 把 `nodePublicKey -> requestChan` 注册进 `WebsocketManager.sendChan`
2. 启动一个响应转发协程，把 Node 回来的 `RPCResponse` 根据 `ID` 转发到 `recvChanMap[id]`

所以，一个 Node 真正“上线可用”的标准不是 TCP 建立，而是：

- 已通过 WebSocket 鉴权
- 已在 `sendChan` 中注册了该节点对应的发送通道
- 已被加入 `aliveNodes`

## 6. Manager 侧服务端框架

Manager 侧的核心对象有两个：

1. `WebsocketManager`
2. `wsConnection`

二者关系是：

- `WebsocketManager` 负责管理多条连接
- `wsConnection` 负责管理单条连接

### 6.1 `WebsocketManager` 是什么

`WebsocketManager` 可以理解成“Manager 的 WebSocket 总调度器”。  
它自己不直接承载业务逻辑，而是提供连接管理与消息路由能力。

它最重要的成员是这两张表：

| 字段 | 含义 |
| --- | --- |
| `sendChan map[string]chan types.RPCRequest` | `nodePubKey -> 该节点连接的发送通道` |
| `recvChanMap map[string]chan ResponseMsg` | `requestId -> 等待该响应的业务通道` |

另外还有：

- `aliveNodes map[string]struct{}`：当前在线节点集合
- 两把锁：分别保护收响应路由表和发消息路由表

### 6.2 `WebsocketManager` 提供的三个业务能力

Manager 业务层真正依赖的是 `IWebsocketManager` 接口，也就是三件事：

1. `AliveNodes()`
2. `RegisterResChannel(id, responseChan, stopChan)`
3. `SendMsg(requestMsg)`

#### `AliveNodes()`

返回当前在线节点公钥列表。

业务上主要用于：

- Manager 从配置的 TSS 节点列表中筛出当前在线节点

#### `RegisterResChannel(...)`

业务层在发请求前先注册一个等待响应的通道：

```text
requestId -> respChan
```

随后，`stopChan` 关闭时，这个映射会被自动清理，防止 `recvChanMap` 泄漏。

这是 Manager 所有 WebSocket 请求都必须做的准备步骤。

#### `SendMsg(...)`

业务层并不直接写 socket，而是：

1. 先根据 `TargetNode` 找到该节点的 `requestChan`
2. 把 `RPCRequest` 投递进去
3. 由该连接自己的写协程统一写到真实 socket 上

这是典型的“业务线程不直接碰 socket”的设计。

好处是：

- 所有 socket 写操作串行化
- 避免 gorilla/websocket 的并发写问题
- 业务层只关心“把消息交给哪一条连接”

## 7. Manager 侧单连接模型：`wsConnection`

`wsConnection` 表示一条具体的 Node 长连接。

它最重要的两个通道是：

| 字段 | 方向 | 含义 |
| --- | --- | --- |
| `requestChan` | Manager -> Node | Manager 要发给这个 Node 的请求先进入这里 |
| `responseChan` | Node -> Manager | 从 socket 读到的节点响应先进入这里 |

### 7.1 为什么要拆成读协程和写协程

`wsConnection.OnStart()` 的策略是：

- 开一个 `readRoutine()` 专门读
- 当前 goroutine 进入 `writeRoutine()` 专门写

这么做的原因非常明确：  
**WebSocket 的读写需要串行化管理，尤其写操作不能多个 goroutine 并发对同一个连接写。**

因此该项目把所有写动作集中到 `writeRoutine()` 中完成，包括：

- 业务请求发送
- ping 发送
- pong 回复

### 7.2 `readRoutine()` 做什么

服务端的 `readRoutine()` 只做一件核心事情：

- 持续从 socket 读取 Node 发回来的 `RPCResponse`

具体行为：

1. 设置 `readDeadline`
2. 从底层连接读取下一条消息
3. JSON 解码成 `types.RPCResponse`
4. 检查 `response.ID` 是否为空
5. 把响应写入 `responseChan`

后续 `OnConnect` 中启动的转发协程会继续把它按 `ID` 投递到 `recvChanMap`

### 7.3 `writeRoutine()` 做什么

服务端的 `writeRoutine()` 负责：

1. 定时发送 ping
2. 收到对端 ping 时串行写 pong
3. 消费 `requestChan` 中的业务请求
4. 把业务请求序列化后写入 socket

所以真正的“Manager -> Node 发消息”路径是：

```text
Manager business code
   -> WebsocketManager.SendMsg
   -> sendChan[nodePubKey]
   -> wsConnection.requestChan
   -> wsConnection.writeRoutine
   -> websocket.Conn.WriteMessage
```

### 7.4 心跳与超时

服务端默认参数在 `ws/server/handler.go` 顶部：

- `defaultWSWriteWait = 10s`
- `defaultWSReadWait = 30s`
- `defaultWSPingPeriod = 27s`

这表示：

- 服务端每 27 秒主动发一次 ping
- 如果 30 秒内没有收到数据或 pong，则认为读超时
- 写操作有 10 秒超时

此外：

- `SetPongHandler` 收到 pong 后会刷新读超时
- `SetPingHandler` 并不直接在 handler 里写 pong，而是把 pong 请求转给 `writeRoutine` 串行写出

这一点很重要，因为它避免了“控制帧写”和“业务帧写”并发冲突。

### 7.5 断线处理

如果 `readRoutine()` 读 socket 出错：

1. 调用 `Stop()`
2. 关闭 `readRoutineQuit`
3. 让写协程收到退出信号

`OnStop()` 又会继续做两件事：

1. 执行 `onDisconnect`
2. 取消连接级 context

而 `onDisconnect` 最终会调用 `clientDisconnected(pubkey)`，把该节点从：

- `aliveNodes`
- `sendChan`

中清除掉。

所以服务端断线处理的核心目标是：  
**一旦连接失效，就立刻撤销路由状态，避免 Manager 继续往失效节点发请求。**

## 8. Node 侧客户端框架

Node 侧分为两层：

1. `WSClients`：给业务层用的包装
2. `tm.WSClient`：真正的底层长连接实现

### 8.1 `WSClients`：业务适配层

`ws/client/client.go` 中的 `WSClients` 是一个很薄的包装层。

它主要做三件事：

1. 创建并启动底层 `tm.WSClient`
2. 给底层客户端注入 `PubKey` 和 `PriKey`
3. 把底层 `RequestsCh` 桥接到 `Processor` 的业务通道

也就是说，`WSClients` 不是重新实现一套 WebSocket，而是把底层能力翻译成 Node signer 更容易消费的接口。

### 8.2 `tm.WSClient`：底层连接实现

`ws/client/tm/client.go` 才是 Node 侧真正复杂的部分。

它内部维护了这些关键通道：

| 字段 | 含义 |
| --- | --- |
| `RequestsCh` | 对上暴露的 Manager -> Node 请求流 |
| `send chan RPCResponse` | Node -> Manager 待发送响应 |
| `backlog chan RPCResponse` | 断线期间最多缓存 1 条待重发响应 |
| `reconnectAfter chan error` | 触发重连的错误通知 |
| `readRoutineQuit chan struct{}` | 读协程退出后通知写协程停止 |

### 8.3 Node 侧启动流程

`WSClient.OnStart()` 会：

1. 先执行一次 `dial()`
2. 创建 `RequestsCh`
3. 创建 `send` / `backlog` / `reconnectAfter`
4. 启动读协程和写协程
5. 启动 `reconnectRoutine()`

所以 Node 客户端从一开始就是“长连接 + 后台重连守护”的模型。

### 8.4 `dial()` 做什么

`dial()` 是 Node 侧建立 WebSocket 连接的关键步骤。

它会：

1. 创建 `websocket.Dialer`
2. 生成当前时间戳
3. 计算 `Keccak256(timeStr)`
4. 用 `PriKey` 对摘要签名
5. 设置 `pubKey` / `sig` / `time` 三个 header
6. 连接到 `ws://<address><endpoint>`

这与服务端 `WebsocketHandler` 的校验逻辑严格对称。

### 8.5 Node 侧读协程

客户端的 `readRoutine()` 只负责从 Manager 收请求。

具体行为：

1. 从底层连接 `ReadMessage()`
2. 反序列化为 `types.RPCRequest`
3. 校验 `ID` 是否是合法的 JSON-RPC ID
4. 把请求投递到 `RequestsCh`

这说明 Node 客户端在这条 WebSocket 上只接受一类业务消息：  
**Manager 发来的 JSON-RPC 请求。**

### 8.6 Node 侧写协程

客户端的 `writeRoutine()` 只负责把 Node 业务生成的 `RPCResponse` 发回 Manager。

所以 Node 侧发送路径是：

```text
verify/sign/keygen business
   -> WSClients.SendMsg
   -> tm.WSClient.Send
   -> send chan RPCResponse
   -> writeRoutine
   -> websocket.Conn.WriteJSON
```

注意一点：

- `WSClient.Send(...)` 名字看起来像“发请求”
- 但在本项目里它发送的其实是 `RPCResponse`

也就是说，Node 在这条连接里是“只接请求，只回响应”的角色。

### 8.7 Node 侧重连机制

Node 客户端带有自动重连能力。

触发条件：

- 读 socket 出错
- 写 socket 出错

重连流程：

1. 把错误投递到 `reconnectAfter`
2. 等待读协程和写协程都退出
3. 执行指数退避重连
4. 重连成功后先处理 `backlog`
5. 再重新启动新的读写协程

重连策略的特点：

- 默认最大重连次数是 25 次
- 退避时间按 `1s, 2s, 4s, 8s...` 增长
- 最大 sleep 上限是 1 小时

### 8.8 backlog 的作用

客户端只维护一个容量为 1 的 `backlog`。

它的语义是：

- 如果 Node 正在向 Manager 回响应，结果在写 socket 时断线
- 那么这条响应会先存入 `backlog`
- 等重连成功后优先重发这一条响应

这个设计能减少“节点已经完成签名，但结果正好在断线瞬间丢失”的概率。

但也要注意，这只是一个很轻量的兜底：

- 只能缓存 1 条响应
- 只覆盖 Node -> Manager 的响应补发
- Manager -> Node 的请求发送失败，不会由服务端做同等重放

## 9. Node 侧如何消费 WebSocket 消息

Node 的 WebSocket 消费入口在 `node/signer/deal_msg.go` 的 `ProcessMessage()`。

它做的事情可以概括成两步：

1. 调用 `wsClient.RegisterResChannel(reqChan, stopChan)`
2. 从 `reqChan` 读请求，按 `Method` 分发到不同业务通道

分发规则是：

| Method | 投递通道 | 后续处理器 |
| --- | --- | --- |
| `askMessageHash` | `askRequestChan` | `Verify()` |
| `TransactionSign` | `signRequestChan` | `Sign()` |
| `keygen` | `keygenRequestChan` | `Keygen()` |

这说明 Node 侧的 WebSocket 层本身不做业务判断，它只做“消息分发器”。

### 9.1 `Verify()` 做什么

`node/signer/verify.go` 的 `Verify()` 处理 ask 阶段请求。

它主要做这些事情：

1. 解析 `TransactionSignRequest`
2. 校验 `message_hash` 是否为合法 32 字节十六进制，且 `election_id > 0`
3. 把 `(message_hash, election_id)` 记入 `waitSignMsgs`
4. 回一个 `AskResponse{Result: true}`

这一阶段不做真正签名，只做“是否接受进入签名流程”的确认。

`waitSignMsgs` 的作用很重要：  
它是 sign 阶段的前置检查依据。

### 9.2 `Sign()` 做什么

`node/signer/sign.go` 的 `Sign()` 和 `SignGo()` 处理正式签名。

它的关键逻辑是：

1. 先解析 `NodeSignRequest`
2. 再从 `RequestBody` 中解析出原始 `TransactionSignRequest`
3. 调用 `checkMessages()` 检查该请求是否已经在 ask 阶段被记录到 `waitSignMsgs`
4. 如果没有 ask 记录，拒绝签名
5. 如果有记录，则调用 `p.tssServer.KeySign(...)`
6. 把签名整理成 `SignResponse`
7. 通过 WebSocket 回给 Manager

也就是说，Node 侧把 `ask -> sign` 做成了两阶段流程：

- ask 阶段把消息加入待签名集合
- sign 阶段只允许处理已经通过 ask 的请求

这也是 WebSocket 流程中最重要的业务约束之一。

### 9.3 `Keygen()` 做什么

`node/signer/keygen.go` 的 `Keygen()` 逻辑更直接：

1. 解析 `KeygenRequest`
2. 组装 `keygen.Request`
3. 调用 `p.tssServer.Keygen(...)`
4. 把返回的聚合公钥包装成 `KeygenResponse`
5. 通过 WebSocket 回给 Manager

## 10. Manager 业务层如何使用 WebSocket

Manager 的业务层并不直接操作连接对象，而是统一通过 `IWebsocketManager` 来驱动。

当前有三条典型业务链路：

1. `agreement()`
2. `sign()`
3. `generateKey()`

### 10.1 签名前置协商：`agreement()`

`manager/agreement.go` 的核心流程是：

1. 创建 `respChan`
2. 用 `ASK_<requestId>` 调用 `RegisterResChannel(...)`
3. 把原始请求 JSON 序列化
4. 调用 `askNodes(...)` 给所有候选节点发送 `askMessageHash`
5. 等待各节点通过 `respChan` 返回 `AskResponse`
6. 根据返回结果整理出：
    - `Approvers`
    - `UnApprovers`

这里最重要的不是“广播”，而是“先注册响应通道，再发请求”。

因为如果不先注册，Node 回来的响应就找不到对应等待者。

### 10.2 正式签名：`sign()`

`manager/sign.go` 的流程是：

1. 用 `requestId` 注册响应通道
2. 构造 `NodeSignRequest`
3. 给 approvers 发送 `TransactionSign`
4. 从 `respChan` 收各个节点的响应
5. 如果收到成功签名，校验签名是否能被 `ClusterPublicKey` 验证
6. 返回合法的 `SignResponse`

这里还有两个重要点：

1. Manager 不是拿到任何一个响应就盲信，而是会用聚合公钥再次验签
2. 返回错误时，会根据错误码区分是否是 `CulpritErrorCode`

### 10.3 分布式生成公钥：`generateKey()`

`manager/keygen.go` 的流程是：

1. 先调用 `availableNodes(...)` 筛出在线节点
2. 用 `requestId` 注册响应通道
3. 给每个在线节点发送 `keygen`
4. 收集所有 `KeygenResponse`
5. 检查所有节点返回的 `ClusterPublicKey` 是否完全一致
6. 一致才认为 keygen 成功

这里的关键点是：

- keygen 不是只拿一个节点的返回值
- Manager 需要确认所有参与节点产出的聚合公钥一致
- 本轮参与成员优先使用 `KeygenRequest.nodes`；如果调用方没传，才回退到 `manager.nodes`

## 11. 完整时序：从启动到业务执行

### 11.1 连接建立时序

```text
Node Processor                 WSClients/tm.WSClient           Manager WSServer/WebsocketManager
     |                                   |                                   |
     | NewWSClient(...)                  |                                   |
     |---------------------------------->|                                   |
     |                                   | dial()                            |
     |                                   | - time                            |
     |                                   | - sig = sign(Keccak256(time))     |
     |                                   |---------------------------------->|
     |                                   |   GET /ws + pubKey/sig/time       |
     |                                   |                                   | Upgrade to WebSocket
     |                                   |                                   | verify header/signature
     |                                   |                                   | new wsConnection
     |                                   |                                   | OnConnect:
     |                                   |                                   | - register sendChan[pubKey]
     |                                   |                                   | - add aliveNodes
     |                                   |<----------------------------------|
     |                                   | Start read/write/reconnect loops  |
     | websocket ready                   |                                   |
```

### 11.2 签名流程时序

```text
HTTP Caller          Manager                 Node                    node/tsslib / p2p
    |                  |                       |                             |
    | POST /api/v1/sign|                       |                             |
    |----------------->|                       |                             |
    |                  | agreement()          |                             |
    |                  | register ASK_id      |                             |
    |                  | send askMessageHash  |                             |
    |                  |--------------------->| ProcessMessage -> Verify     |
    |                  |                       | cache waitSignMsgs           |
    |                  |<---------------------| AskResponse(true/false)      |
    |                  | select approvers     |                             |
    |                  | sign()               |                             |
    |                  | register requestId   |                             |
    |                  | send TransactionSign |                             |
    |                  |--------------------->| ProcessMessage -> Sign       |
    |                  |                       | check waitSignMsgs          |
    |                  |                       | tssServer.KeySign(...)      |
    |                  |                       |---------------------------->|
    |                  |                       |   TSS protocol among nodes  |
    |                  |                       |<----------------------------|
    |                  |<---------------------| SignResponse                 |
    |                  | verify signature     |                             |
    |<-----------------| return signature     |                             |
```

### 11.3 keygen 流程时序

```text
HTTP Caller          Manager                 Node                    node/tsslib / p2p
    |                  |                       |                             |
    | POST /api/v1/keygen                     |                             |
    |----------------->|                       |                             |
    |                  | generateKey()        |                             |
    |                  | register requestId   |                             |
    |                  | send keygen          |                             |
    |                  |--------------------->| ProcessMessage -> Keygen     |
    |                  |                       | tssServer.Keygen(...)       |
    |                  |                       |---------------------------->|
    |                  |                       |   TSS keygen among nodes    |
    |                  |                       |<----------------------------|
    |                  |<---------------------| KeygenResponse(CPK)          |
    |                  | compare all CPKs     |                             |
    |<-----------------| return CPK           |                             |
```

## 12. 这个 WebSocket 设计的几个关键特点

### 12.1 用公钥标识节点，而不是连接 ID

Manager 端所有路由都围绕节点公钥展开：

- `aliveNodes` 记录在线公钥
- `sendChan` 以节点公钥为 key
- 业务层也用节点公钥决定发给谁

所以在这个项目里，节点身份不是“第几条连接”，而是“哪个公钥”。

### 12.2 响应路由靠 request ID，不靠回调对象

Manager 发请求前注册 `requestId -> recvChan`，Node 回响应时再按 `ID` 找回去。

这种方式适合当前场景，因为：

- 一个 Manager 会同时与很多 Node 长连
- 同时会跑很多个 ask/sign/keygen 流程
- 用 `requestId` 做路由比“按连接回调”更容易把业务协程和网络层解耦

### 12.3 服务端和客户端都采用“读写分离协程”模型

无论是 Manager 端 `wsConnection`，还是 Node 端 `tm.WSClient`，本质上都是：

- 一个读协程
- 一个写协程

这样设计的本质原因是：

- 降低并发写 socket 的风险
- 把保活、业务发送、关闭逻辑纳入统一写出口

### 12.4 WebSocket 是控制平面，不是数据平面

Manager 下发的是：

- ask
- sign
- keygen

Node 回来的是：

- 是否接受
- 签名结果
- 聚合公钥

而阈值签名、分布式 keygen 的真实多方协议消息，不在这条 WebSocket 中传输。

这使得：

- Manager 不需要理解 TSS 细节消息
- WebSocket 层可以保持相对简单
- TSS 算法升级可以主要集中在 `node/tsslib`

## 13. 当前实现里的注意点和限制

下面这些是理解代码时应注意的现状。

### 13.1 当前没有启用节点白名单接入校验

`WebsocketManager` 里有 `JudgeWssConnectPermission(...)`，但在 `WebsocketHandler` 中对应调用被注释掉了。

这意味着当前握手只做：

- 时间戳校验
- 签名校验

并没有在连接建立阶段严格校验“这个公钥是否属于允许接入的节点集合”。

### 13.2 Manager 的在线节点状态只保存在内存

当前 `aliveNodes` 和 `sendChan` 都是纯内存结构。

所以：

- Manager 重启后需要 Node 重新连入
- 在线状态不是持久化注册中心

### 13.3 服务端不做请求持久化和重发

Manager 发消息时，如果节点已经不在线或者发送失败：

- 会直接返回错误
- 不会像客户端那样缓存请求再补发

当前只有 Node 客户端对“响应发送中断”提供了 1 条 `backlog` 级别的重试能力。

### 13.4 客户端默认不主动发 ping

Node 侧 `tm.WSClient` 的默认：

- `writeWait = 0`
- `readWait = 0`
- `pingPeriod = 0`

也就是说，默认情况下客户端本身不主动开启读超时和心跳发送。  
当前连接保活主要依赖服务端周期性发送 ping，以及 gorilla/websocket 的默认 ping/pong 行为。

### 13.5 ask 和 sign 是两阶段协议

这不是“多一次无意义请求”，而是当前业务实现里的核心约束。

Node 在 `Verify()` 中把消息加入 `waitSignMsgs`；  
Node 在 `Sign()` 中又必须检查该消息是否已经通过 ask 记录。

因此：

- 如果跳过 ask 直接发 sign
- 或 ask 与 sign 的上下文不一致

Node 侧会拒绝签名。


## 14. 一句话总结

这个项目的 WebSocket 框架，本质上是一个：

- 由 Manager 承载的中心化连接管理器
- 由 Node 主动回连的长连接客户端
- 以节点公钥做路由
- 以 JSON-RPC `requestId` 做响应关联
- 用于 ask/sign/keygen 控制消息传递
- 与 Node 间 TSS p2p 通道分层解耦

的控制面通信系统。

如果只抓住一句话，那就是：

> Manager 通过 WebSocket 调度 Node，Node 通过 WebSocket 回结果，真正的 TSS 多方协议消息不走这条 WebSocket。
