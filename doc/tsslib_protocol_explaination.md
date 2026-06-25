# TSS 项目 Node 内部协议与 `tsslib` 架构说明

本文只解释这个项目里真正跑 TSS 协议的那一层，也就是：

- `node/signer` 调用 `tssServer.Keygen(...)` / `tssServer.KeySign(...)` 之后，Node 内部到底发生了什么
- `node/tsslib` 是如何组织 keygen / keysign / p2p / 本地状态管理的
- Node 与 Node 之间的 libp2p 消息是怎么封装、路由、验签、落到本地 `tss-lib` party 的
- 当前实现里哪些逻辑是真正参与主流程的，哪些只是辅助能力或半成品

这份文档描述的是当前仓库里的实际实现，不是抽象设计稿。

## 1. 先说结论：真正的 TSS 协议发生在 Node 内部，不在 Manager 上

这个项目的整体通信分两层：

1. `Manager <-> Node`：走 WebSocket，负责控制面调度
2. `Node <-> Node`：走 `node/tsslib/p2p` 的 libp2p，负责协议面消息

这意味着：

- Manager 不参与门限密码学计算
- Manager 不理解 keygen/keysign 的 round message
- Manager 只负责挑节点、发任务、收结果
- 真正的多方计算，由各个 Node 在本地起 `tss-lib` 的 local party，再通过 libp2p 互相交换 round message 完成

可以把它理解成下面这个分层：

```text
HTTP 调用方
   -> Manager HTTP API
   -> Manager WebSocket Server
   -> Node signer
   -> tssServer.Keygen / tssServer.KeySign
   -> node/tsslib/common + keygen/keysign
   -> node/tsslib/p2p (Node <-> Node)
   -> binance-chain/tss-lib local party
```

如果只抓一句话，那就是：

> WebSocket 是控制面，`node/tsslib` 才是协议面。

## 2. 相关代码入口

如果只看 Node 内部的 TSS 协议实现，建议重点看这些文件：

- `node/signer/keygen.go`
- `node/signer/sign.go`
- `node/tsslib/server.go`
- `node/tsslib/tss.go`
- `node/tsslib/keygen.go`
- `node/tsslib/keysign.go`
- `node/tsslib/keygen/tss_keygen.go`
- `node/tsslib/keysign/tss_keysign.go`
- `node/tsslib/common/tss.go`
- `node/tsslib/common/tss_helper.go`
- `node/tsslib/p2p/communication.go`
- `node/tsslib/p2p/stream_helper.go`
- `node/tsslib/conversion/*.go`
- `node/tsslib/storage/localstate_mgr.go`
- `node/tsslib/abnormal/*.go`

这些文件之间的职责划分如下：

| 位置 | 作用 |
| --- | --- |
| `node/signer/keygen.go` | Node 侧 keygen 控制入口，把 WebSocket 请求翻译成 `tsslib` keygen 请求 |
| `node/signer/sign.go` | Node 侧 sign 控制入口，把业务签名请求翻译成 `tsslib` keysign 请求 |
| `node/tsslib/server.go` | `TssServer` 能力接口定义 |
| `node/tsslib/tss.go` | `TssServer` 总装配，负责 p2p、preparams、状态存储、请求校验 |
| `node/tsslib/keygen.go` | `TssServer.Keygen` 业务编排 |
| `node/tsslib/keysign.go` | `TssServer.KeySign` 业务编排 |
| `node/tsslib/keygen/tss_keygen.go` | keygen local party 生命周期、出站消息和结果处理 |
| `node/tsslib/keysign/tss_keysign.go` | keysign local party 生命周期、出站消息和结果处理 |
| `node/tsslib/common/tss.go` | TSS 公共协议壳：消息封装、签名验签、入站处理、task done 协调 |
| `node/tsslib/p2p/communication.go` | libp2p host、stream、消息广播、订阅路由 |
| `node/tsslib/storage/localstate_mgr.go` | keygen 产物、公钥状态、地址簿、preparams 的本地存储 |
| `node/tsslib/abnormal/*.go` | 异常信息、作恶归因、round share 记录 |

### 2.1 这个项目是如何直接使用 `binance-chain/tss-lib` 的

先把边界说清楚：

- `binance-chain/tss-lib` 负责真正的门限 ECDSA 密码学状态机
- 这个项目没有重写 keygen / signing 算法，而是把 `tss-lib` 当作每个 Node 本地运行的 `local party`
- 每个参与节点各自启动一份本地 party，通过交换 `wire bytes` 一起把协议推进到结束

如果把当前仓库里对 `tss-lib` 的直接使用压缩成最小闭环，其实就是下面这几步：

1. 先准备好参与方集合、阈值和 preparams
2. 用参与方公钥创建 `tss.PartyID` 列表
3. 创建 `tss.NewPeerContext(...)` 和 `tss.NewParameters(...)`
4. 创建 `keygen.NewLocalParty(...)` 或 `signing.NewLocalParty(...)`
5. 调 `party.Start()` 启动本地密码学状态机
6. 从 `outCh` 读取本地 round 消息并发给其他节点
7. 收到其他节点消息后，调用 `party.UpdateFromBytes(...)` 喂回本地 party
8. 从 `endCh` 读取最终结果

对应到当前代码，直接触达 `tss-lib` 的地方主要只有下面这些：

| 直接调用点 | 当前项目怎么用 |
| --- | --- |
| `ecdsa/keygen.GeneratePreParams(...)` | Node 启动时预生成或复用 keygen 预计算材料，见 `node/tsslib/tss.go` |
| `tss.NewPartyID(...)` / `tss.SortPartyIDs(...)` | 把参与节点公钥转成 `tss-lib` 认识的参与方身份，见 `node/tsslib/conversion/conversion.go` |
| `tss.NewPeerContext(...)` | 为本轮 keygen / keysign 建立参与方上下文，见 `node/tsslib/keygen/tss_keygen.go` 和 `node/tsslib/keysign/tss_keysign.go` |
| `tss.NewParameters(...)` | 传入曲线、参与人数、阈值、本地 party 身份，构造本轮协议参数 |
| `keygen.NewLocalParty(...)` | 创建 keygen 的本地 party，输入是参数、`outCh`、`endCh` 和 preparams |
| `signing.NewLocalParty(...)` | 创建 keysign 的本地 party，输入是待签消息 `m`、参数、本地 key share、`outCh`、`endCh` |
| `party.Start()` | 真正启动 `tss-lib` 的本地状态机 |
| `msg.WireBytes()` | 从 `tss-lib` 产出的 `tss.Message` 中拿到底层 wire bytes，供 Node 间传输 |
| `party.UpdateFromBytes(...)` | 把远端节点发来的 wire bytes 喂回本地 party，推进 round |
| `tss.ParseWireMessage(...)` | 解析消息类型，恢复 round 名称，用于日志、归因和消息分类 |

具体到两条主流程：

- keygen：
  - `node/tsslib/tss.go` 在启动阶段调用 `bkeygen.GeneratePreParams(...)`
  - `node/tsslib/keygen/tss_keygen.go` 调 `keygen.NewLocalParty(...)`
  - 本地完成后从 `endCh` 拿到 `keygen.LocalPartySaveData`
- keysign：
  - `node/tsslib/keysign/tss_keysign.go` 先把待签摘要转成 `big.Int`
  - 再调用 `signing.NewLocalParty(...)`
  - 本地完成后从 `endCh` 拿到 `tss-lib/common.SignatureData`

换句话说，`tss-lib` 在这个项目里承担的是“协议引擎”角色：

- 它负责 round 怎么推进
- 它负责每轮该产出什么消息
- 它负责最终怎么得到公钥份额或签名结果

但它并不负责：

- Manager/Node 之间的控制面通信
- Node/Node 之间的网络传输
- 谁和谁是一组参与方
- 本地结果存到哪里
- 并发协议实例怎么隔离
- 消息验签、异常归因、指标统计

这些都是当前项目自己补上的。

### 2.2 在 `tss-lib` 基础上，这个项目额外做了哪些封装

如果只保留 `binance-chain/tss-lib`，你能得到的只是一个“会吐出 round 消息、也能吃进 round 消息的本地状态机”。

而当前项目真正补的是把这个本地状态机变成一个可部署、可联网、可复用的分布式服务运行时。主要封装层如下：

| 封装层 | 当前项目做了什么 | 主要位置 |
| --- | --- | --- |
| 控制面封装 | 把 Manager 下发的 WebSocket / JSON-RPC 请求翻译成内部 `keygen.Request`、`keysign.Request`，再把结果翻译回上层响应 | `node/signer/keygen.go`、`node/signer/sign.go` |
| 服务编排封装 | 提供 `TssServer` 统一入口，负责请求校验、生命周期管理、metrics、资源清理 | `node/tsslib/tss.go`、`node/tsslib/keygen.go`、`node/tsslib/keysign.go` |
| 身份转换封装 | 把“节点压缩公钥”转换成 `tss.PartyID`、再转换成 libp2p `peer.ID`，打通业务身份、协议身份、网络身份三层映射 | `node/tsslib/conversion/conversion.go` |
| 网络传输封装 | 用 libp2p 代替 `tss-lib` 默认不提供的网络层，负责 peer discovery、stream 建立、广播、订阅路由 | `node/tsslib/p2p/*.go` |
| 协议消息封装 | 在 `tss-lib` 原始 wire bytes 外，再包 `WrappedMessage` 和 `WireMessage`，附带 `msgID`、`MessageType`、`RoundInfo`、`Routing`、`Sig` | `node/tsslib/messages/p2p_message.go`、`node/tsslib/common/tss.go` |
| 消息认证封装 | 每条 Node 间协议消息都用节点私钥再签一层，接收方先验签，再决定是否交给 `party.UpdateFromBytes(...)` | `node/tsslib/common/tss.go` |
| 并发隔离封装 | 用 `(messageType, msgID)` 给每次 keygen / keysign 建独立订阅和 stream 生命周期，避免多轮任务串台 | `node/tsslib/keygen.go`、`node/tsslib/keysign.go`、`node/tsslib/p2p/message_id_subscriber.go` |
| 本地状态封装 | 持久化 `LocalPartySaveData`、preparams、地址簿，并支持本地文件、secrets manager、shamir 三种承载方式 | `node/tsslib/storage/*.go` |
| 业务约束封装 | 维护 `poolPubKey -> participants` 关系，校验 signer 集合是否合法，限制 keygen 串行执行 | `node/tsslib/tss.go`、`node/tsslib/keysign.go` |
| 异常观测封装 | 记录 round、缓存 share、抓 culprit、保留坏消息与签名证据，便于排障和归因 | `node/tsslib/abnormal/*.go`、`node/tsslib/common/tss_helper.go` |
| 结果适配封装 | 把 `tss-lib` 的 `SignatureData` 再整理成业务层使用的 65 字节 ECDSA 签名，并做 low-s 规范化 | `node/signer/sign.go` |

可以把这个封装关系理解成：

```text
binance-chain/tss-lib
   = 本地密码学协议引擎

node/tsslib
   = 协议运行时封装
   = 网络层 + 身份映射 + 消息封装 + 状态持久化 + 异常处理

node/signer
   = 上层业务适配
   = 把外部控制请求翻译成 tsslib 能执行的任务
```

其中最关键的封装，不是“又包了一层接口”这么简单，而是补了 `tss-lib` 本身没有打包给你的那些工程问题：

- 多节点之间怎么找到彼此并交换 round 消息
- 同一台机器同时跑多轮协议时，消息如何精准路由到正确实例
- 一轮 keygen 结束后，本地 key share 存哪里、下次如何恢复
- 某个远端节点发来错误 share 时，怎么留下可归因证据
- 业务方只关心 `keygen` / `sign` 时，怎样屏蔽底层 round 细节

所以从实现边界上看：

- `binance-chain/tss-lib` 提供的是“算法能力”
- 当前仓库里的 `node/tsslib` 提供的是“把算法能力落成分布式节点服务的工程封装”

## 3. 整体架构图

```text
                    +--------------------------------------+
                    |             Node signer              |
                    |                                      |
Manager WS request  |  keygen.go / sign.go                |
------------------->|  - parse request                    |
                    |  - build tsslib request             |
                    |  - call tssServer.Keygen/KeySign    |
                    +------------------+-------------------+
                                       |
                                       v
                    +--------------------------------------+
                    |             TssServer                |
                    |                                      |
                    |  tss.go / keygen.go / keysign.go    |
                    |  - requestCheck                     |
                    |  - load/save local state            |
                    |  - init msgID subscriptions         |
                    |  - create keygen/keysign instance   |
                    +------------------+-------------------+
                                       |
             ---------------------------------------------------------------
             |                                                             |
             v                                                             v
 +-----------------------------+                           +-----------------------------+
 |      TssKeyGen/TssKeySign   |                           |        Communication        |
 |                             |                           |                             |
 |  - build local party        |<---- TssMsg chan ------->|  - libp2p host             |
 |  - read outCh/endCh         |                           |  - stream handler          |
 |  - call ProcessOutCh        |                           |  - BroadcastMsgChan        |
 |  - wait task done           |                           |  - subscriber by msgID     |
 +---------------+-------------+                           +---------------+-------------+
                 |                                                             |
                 v                                                             v
       +-----------------------+                                +--------------------------+
       |  binance tss-lib      |                                |    Other TSS Nodes       |
       |  keygen/signing party |<-------- libp2p streams ------>|  same stack             |
       +-----------------------+                                +--------------------------+
```

这个结构里有一个很重要的事实：

- `node/signer` 不直接操作 libp2p
- `TssServer` 不直接实现密码学 round，而是做“协议运行时装配”
- 真正的 round 推进，发生在 `tss-lib` local party 产出 `outCh` 消息、再被 `TssCommon` 封装发给其他 Node 的过程中

## 4. 协议模型：Node 间 libp2p 上传输的到底是什么

### 4.1 不是直接裸发 `tss-lib` 消息，而是包了两层壳

Node 之间 libp2p 上传输的最外层对象是 `messages.WrappedMessage`，定义在 `node/tsslib/messages/p2p_message.go`。

**1.WrappedMessage** 它包含：

| 字段          | 含义                                        |
| ------------- | ------------------------------------------- |
| `MessageType` | 当前消息属于 keygen、keysign 还是 task done |
| `MsgID`       | 当前这次协议实例的唯一标识                  |
| `Payload`     | 真正的负载                                  |

**2. WireMessage**

如果 `MessageType` 是 `TSSKeyGenMsg` 或 `TSSKeySignMsg`，那么 `Payload` 里又是一个 `messages.WireMessage`：

| 字段        | 含义                                             |
| ----------- | ------------------------------------------------ |
| `Routing`   | `tss.MessageRouting`，表示 from / to / broadcast |
| `RoundInfo` | 当前 round 名称，例如 `KGRound1Message`          |
| `Message`   | 真正的 round 二进制                              |
| `Sig`       | 发送节点对该消息做的 ECDSA 签名                  |

**3. BulkWireMsg**

作用：这是 WireMessage.Message 里面真正装着的内容 。它又包了一层：

| 字段            | 含义                                                         |
| --------------- | ------------------------------------------------------------ |
| `WiredBulkMsg`  | []byte 最内层真正要喂给 tss-lib 的原始 wire bytes            |
| `MsgIdentifier` | string                                                       |
| `Routing `      | `tss.MessageRouting `表示tss 的 from / to / broadcast也就是说，当前消息封装有三层： |

```text
  WrappedMessage.Payload
    -> json(WireMessage)

  WireMessage.Message
    -> json(BulkWireMsg)

  BulkWireMsg.WiredBulkMsg
    -> tss-lib 原始 wire bytes
```

为什么不直接把 `tss-lib` 的 `raw wire bytes` 放进 `WireMessage.Message`，还要多包一层 `BulkWireMsg`？

原因是当前实现想把“真正的 TSS 原始消息”和“当前项目自己额外需要的元信息”分开：

- WireMessage 这一层负责网络传输壳
    - 路由
    - round 名称
    - 外层签名

- BulkWireMsg 这一层负责和 TSS 原始消息绑定的内部元信息
    - 原始 tss-lib wire bytes
    - MsgIdentifier
    - 再带一份 routing

- wireMsg 是“网络上传输的 TSS 封包”
- bulkMsg 是“封包里面那段真正的 TSS **业务载荷**”
- bulkMsg.WiredBulkMsg 才是“**最终交给 tss-lib 状态机消费的原始消息**”

### 4.2 当前使用的消息类型

当前 Node 间 p2p 消息主要有三类：

| MessageType | 含义 |
| --- | --- |
| `TSSKeyGenMsg` | keygen round 消息 |
| `TSSKeySignMsg` | keysign round 消息 |
| `TSSTaskDone` | 某个节点本地 party 已结束，用于通知其他节点收尾 |

### 4.3 `msgID` 的作用

`msgID` 是整个 p2p 协议路由的核心键。

它由 `TssServer.requestToMsgId(...)` 生成：

- keygen：按参与公钥列表排序后做 hash
- keysign：按消息摘要 + 参与签名公钥列表排序后做 hash

这意味着同一轮 keygen / 同一笔签名任务，在所有参与节点上会得到同一个 `msgID`。

后续它会用于：

- `Communication.SetSubscribe(messageType, msgID, channel)` 建立订阅
- 收到 libp2p 消息后，按 `(messageType, msgID)` 找回对应的协议实例
- 清理该实例占用的 stream 和订阅

所以这个系统不是按“连接对象”区分不同协议实例，而是按 `msgID` 把并发协议流程隔离开。

### 4.4 round 名称不是自定义字符串，而是从 `tss-lib` 类型解析出来的

`node/tsslib/common/tss_helper.go` 的 `GetMsgRound(...)` 会用 `tss.ParseWireMessage(...)` 把原始消息解析成具体 round 类型，例如：

- keygen：
  - `KGRound1Message`
  - `KGRound2Message1`
  - `KGRound2Message2`
  - `KGRound3Message`
- keysign：
  - `SignRound1Message1`
  - `SignRound1Message2`
  - `SignRound2Message`
  - ...
  - `SignRound9Message`

所以 round 信息不是业务层手填的，而是根据 `tss-lib` 实际消息类型恢复出来的。

## 5. Node 内部启动和运行时装配

### 5.1 `NewTss(...)` 做了什么

`node/tsslib/tss.go` 的 `NewTss(...)` 是 Node 内部 TSS 能力的总入口。

它主要做这些事情：

1. 从节点私钥导出压缩公钥和 libp2p peer id
2. 初始化本地状态存储 `FileStateMgr`
3. 按配置决定是否启用：
   - 本地文件状态存储
   - secrets manager
   - shamir manager
4. 读取历史地址簿 `address_book.seed`，和配置里的 bootstrap peers 合并
5. 创建 `Communication`
6. 加载或生成 keygen preparams
7. 启动 libp2p host / DHT / bootstrap 连接
8. 组装成 `TssServer`

这里有两个关键点：

1. `NewTss(...)` 已经把 p2p 网络建起来了，后面的 keygen/keysign 只是复用这个通信层。
2. preparams 不是每次 keygen 才生成，而是 Node 启动时就尽量准备好。

### 5.2 preparams 是什么，当前代码怎么处理

`preParams` 来自 `binance-chain/tss-lib/ecdsa/keygen.GeneratePreParams(...)`。

它本质上是 TSS ECDSA 协议运行前的预计算材料，生成成本较高，所以当前实现优先复用已有值：

1. 如果显式指定了 `preParamsFile`，从文件加载
2. 如果启用了 shamir / secrets，从对应 manager 中取一个已有状态
3. 否则尝试从本地任意一个 `localstate-*.json` 中提取已有 preparams
4. 如果都没有，再现场生成

最后还会调用 `preParams.Validate()` 做合法性校验。

### 5.3 `Communication.Start(...)` 做了什么

`node/tsslib/p2p/communication.go` 的 `Start(...)` 实际做的是：

1. 用节点私钥创建 libp2p host
2. 注册 `/p2p/tss` 的 stream handler
3. 启动 Kademlia DHT
4. 尝试连接 bootstrap peers
5. 用 rendezvous 广播自身
6. 做 bootstrap connectivity check
7. 启动 `ProcessBroadcast()` 消费出站广播队列

也就是说，这里不是“发现 peer 后直接常驻一个 gossip 通道”，而是：

- 用 DHT 做发现
- 用按需 `NewStream(...)` 打开的 stream 做消息收发

### 5.4 `waitFullConnected` 的真实含义

`Communication.connectToBootstrapPeers()` 对 `waitFullConnected` 的处理是：

- `false`：只要任意一个 bootstrap peer 连通就算成功
- `true`：要求所有 bootstrap peers 都连通，否则报错

所以这个开关控制的不是“全网所有 TSS 节点必须就绪”，而是“配置里的 bootstrap peers 是否要求全部连通”。

## 6. `TssServer` 这一层到底负责什么

`TssServer` 不是密码学算法本身，而是 Node 内部的协议运行时。

它最重要的成员包括：

| 字段 | 含义 |
| --- | --- |
| `p2pCommunication` | Node 间 libp2p 通信层 |
| `preParams` | keygen 预计算参数 |
| `participants` | `poolPubKey -> participant pubkeys` 缓存 |
| `stateManager` / `secretsManager` / `shamirManager` | keygen 结果和预计算材料存储 |
| `tssKeyGenLocker` | 串行化 keygen，避免多次并发 keygen |
| `stopChan` | 协议退出信号 |

### 6.1 `requestCheck(...)` 做什么

在真正启动协议前，`TssServer` 会先做输入校验：

- keygen：
  - `len(keys) > threshold`
- keysign：
  - message 不能为空
  - `pool_pub_key` 长度必须是压缩 secp256k1 公钥长度 66
  - `signer_pub_keys` 不能为空
  - 当前 Node 必须在 signer 集合中

注意这里的 keysign 只校验“当前节点是否在请求的 signer 集合里”，不校验“这个 signer 集合是否足够在线”。在线性和成员一致性会在后面继续处理。

### 6.2 `participants` 缓存的作用

`participants` 是一个 `map[poolPubKey][]string`。

它的语义是：

- 对某个聚合公钥，这个 keygen 是由哪些 participant 公钥生成出来的

在 keysign 时，如果内存里还没有这条映射，会从本地 `KeygenLocalState` 里恢复到 `participants`。

这个缓存主要服务于：

- 检查某次签名请求里的 `SignerPubKeys` 是否确实属于该聚合公钥的参与方
- 对外暴露 `GetParticipants(poolPubKey)` 给 Node signer 或其他上层逻辑使用

### 6.3 `msgID` 订阅的生命周期

无论是 keygen 还是 keysign，`TssServer` 在真正开始之前都会：

1. 创建一个具体的 `TssKeyGen` 或 `TssKeySign` 实例
2. 取出它的 `TssMsg` channel
3. 调用：
   - `SetSubscribe(TSSKeyGenMsg/TSSKeySignMsg, msgID, channel)`
   - `SetSubscribe(TSSTaskDone, msgID, channel)`
4. 协议结束后再：
   - `CancelSubscribe(...)`
   - `ReleaseStream(msgID)`

这意味着每一次协议实例都有自己独立的订阅通道，协议结束后显式回收。

## 7. keygen 主流程

### 7.1 Node 侧调用链

Node 侧 keygen 的入口链路是：

```text
node/signer/keygen.go
   -> tssServer.Keygen(keygen.Request)
   -> node/tsslib/keygen.go
   -> keygen.NewTssKeyGen(...)
   -> TssKeyGen.GenerateNewKey(...)
   -> tss-lib keygen local party
```

### 7.2 `TssServer.Keygen(...)` 做什么

`node/tsslib/keygen.go` 的 `TssServer.Keygen(...)` 做这些事情：

1. 先持有 `tssKeyGenLocker`，确保同一时刻只跑一个 keygen
2. 基于请求计算 `msgID`
3. 调用 `requestCheck(...)`
4. 创建 `TssKeyGen`
5. 建立该 `msgID` 对应的 p2p 订阅
6. 调用 `GenerateNewKey(...)`
7. 如果成功，把 `ECDSAPub` 转成：
   - 压缩公钥字符串
   - EVM 地址
   - 64 字节裸公钥
8. 返回 `keygen.Response`

### 7.3 `GenerateNewKey(...)` 如何启动 local party

`node/tsslib/keygen/tss_keygen.go` 的 `GenerateNewKey(...)` 是 keygen 的核心。

它会依次完成：

1. 用参与公钥列表调用 `conversion.GetParties(...)`
   - 生成所有 `*tss.PartyID`
   - 找到本地对应的 `localPartyID`
2. 组装 `storage.KeygenLocalState` 的基础字段
3. 创建：
   - `tss.NewPeerContext(partiesID)`
   - `tss.NewParameters(...)`
   - `outCh`
   - `endCh`
4. 用 `keygen.NewLocalParty(...)` 创建 `tss-lib` 的 keygen party
5. 建立 `partyID -> peer.ID` 映射
6. 把除自己之外的所有 peer 放入 `TssCommon.P2PPeers`
7. 并发启动两条协程：
   - `keyGenParty.Start()`
   - `TssCommon.ProcessInboundMessages(...)`
8. 主协程进入 `processKeyGen(...)` 事件循环

### 7.4 `processKeyGen(...)` 如何推进协议

`processKeyGen(...)` 只盯三个核心 channel：

| channel | 含义 |
| --- | --- |
| `outCh` | 本地 party 产出的 round message，需要发给其他节点 |
| `endCh` | keygen 成功完成，产出最终 `LocalPartySaveData` |
| `errChan` | local party 启动失败 |

它的处理逻辑是：

1. `outCh` 来消息：
   - 记录 `lastMsg`
   - 调 `TssCommon.ProcessOutCh(...)` 封装并发出去
2. `endCh` 来消息：
   - 说明本地 keygen 完成
   - 调 `NotifyTaskDone()` 广播 task done
   - 把 `LocalPartySaveData` 连同 `ParticipantKeys`、`Threshold`、`PubKey` 封装成 `KeygenLocalState`
   - 落到 shamir / secrets / 本地文件
   - 导出地址簿保存到 `address_book.seed`
   - 返回最终公钥点
3. 超时：
   - 返回 `abnormal.ErrTssTimeOut`
4. stop 信号：
   - 退出

### 7.5 keygen 的本地产物是什么

最终 keygen 结果会保存为 `storage.KeygenLocalState`，核心字段是：

| 字段 | 含义 |
| --- | --- |
| `PubKey` | 聚合压缩公钥 |
| `LocalData` | `tss-lib` 的 `LocalPartySaveData` |
| `ParticipantKeys` | 参与这次 keygen 的所有节点公钥 |
| `LocalPartyKey` | 当前节点自己的公钥 |
| `Threshold` | 阈值 |

如果使用本地文件存储，文件名是：

```text
localstate-<pubKey>.json
```

这份文件就是后续 keysign 的本地私钥份额来源。

## 8. keysign 主流程

### 8.1 Node 侧调用链

Node 侧 sign 的入口链路是：

```text
node/signer/sign.go
   -> tssServer.KeySign(keysign.Request)
   -> node/tsslib/keysign.go
   -> keysign.NewTssKeySign(...)
   -> TssKeySign.SignMessage(...)
   -> tss-lib signing local party
```

### 8.2 `TssServer.KeySign(...)` 做什么

`node/tsslib/keysign.go` 的 `KeySign(...)` 会做这些事情：

1. 调 `requestCheck(...)`
2. 按存储模式加载对应 `poolPubKey` 的 `KeygenLocalState`
3. 如果 `participants[poolPubKey]` 还没缓存，则从 `KeygenLocalState.ParticipantKeys` 恢复
4. 校验 `SignerPubKeys` 是否都属于这个 `poolPubKey` 的参与方
5. 计算 `msgID`
6. 创建 `TssKeySign`
7. 建立 keysign + task done 的 `msgID` 订阅
8. 把 `SignerPubKeys` 转成 `peer.ID`
9. 调 `generateSignature(...)`
10. 记录 metrics 并返回 `keysign.Response`

### 8.3 为什么先做 `CheckPubKeys(...)`

`CheckPubKeys(...)` 做两件事：

1. 校验 signer 数量至少达到 threshold
2. 把 signer 公钥转成 `peer.ID`

当前实现没有在这里额外探测“这些 signer 当前是否真的在线”；它只是把签名参与者集合翻译成可以用于 p2p 建流的 peer id 列表。

### 8.4 `generateSignature(...)` 的关键约束

`generateSignature(...)` 首先会检查当前 Node 是否属于 `onlinePeers`：

- 如果不是，直接返回 `not active signer`
- 如果是，才继续创建本地 keysign party

这一步的意思是：

- 本次签名并不是所有已知节点都必须参与
- 只有请求里指定的 signer 集合里的节点，才真正跑 keysign

### 8.5 `SignMessage(...)` 如何启动 local party

`node/tsslib/keysign/tss_keysign.go` 的 `SignMessage(...)` 会：

1. 根据参与 signer 公钥恢复 `partiesID` 和本地 `localPartyID`
2. 校验当前节点确实在本轮 signer 集合里
3. 用 `MsgToHashInt(msgToSign)` 把待签 digest 转成大整数 `m`
4. 构造：
   - `tss.NewPeerContext(partiesID)`
   - `tss.NewParameters(...)`
   - `signing.NewLocalParty(m, params, localStateItem.LocalData, outCh, endCh)`
5. 建立 `partyID -> peer.ID` 映射和 `P2PPeers`
6. 并发启动：
   - `keySignParty.Start()`
   - `TssCommon.ProcessInboundMessages(...)`
7. 主协程进入 `processKeySign(...)`

### 8.6 `processKeySign(...)` 如何推进协议

它和 keygen 的模式一致，也是盯三个核心 channel：

1. `outCh`：
   - 本地 party 产出一条 round message
   - 调 `ProcessOutCh(...)` 发给其他节点
2. `endCh`：
   - 本地 party 已拿到完整签名结果
   - 调 `NotifyTaskDone()`
   - 导出地址簿并保存
   - 返回 `SignatureData`
3. 超时：
   - 返回 `abnormal.ErrTssTimeOut`

### 8.7 keysign 最终返回的是什么

`tss-lib` 先产出 `SignatureData`：

- `R`
- `S`
- `M`
- `SignatureRecovery`

Node signer 层再把它整理成 65 字节签名：

```text
r(32) || s(32) || v(1)
```

同时会做 low-s 规范化，避免 `S` 落在曲线半阶以上。

## 9. `TssCommon`：这套协议真正的公共运行时

`TssCommon` 是整个 `node/tsslib` 最关键的公共层。

它不是具体的 keygen/keysign 算法实现，但负责：

- 把 local party 的 `outCh` 消息转成 p2p 可发的结构
- 把 libp2p 收到的消息验签、解包、更新回 local party
- 管理 task done 通知
- 管理 partyID / peerID 映射
- 记录 abnormal 信息

### 9.1 `ProcessOutCh(...)` 做什么

当 `tss-lib` local party 从 `outCh` 产出一条 `tss.Message` 时，`TssCommon.ProcessOutCh(...)` 会：

1. 调 `msg.WireBytes()` 拿到：
   - 原始 wire bytes
   - `tss.MessageRouting`
2. 生成 `BulkWireMsg`
3. 按广播/单播缓存到：
   - `cachedWireBroadcastMsgLists`
   - `cachedWireUnicastMsgLists`
4. 逐个调用 `sendBulkMsg(...)`

虽然这里用到了 cache，但当前主流程里是“收到一条就发一条”，不是做复杂批处理。

### 9.2 `sendBulkMsg(...)` 如何封装和签名消息

真正发出前，它会：

1. 把 `BulkWireMsg` JSON 序列化
2. 用本节点私钥对 `message + msgID` 做 `Keccak256` 后签名
3. 构造 `WireMessage{Routing, RoundInfo, Message, Sig}`
4. 再包成 `WrappedMessage{MessageType, MsgID, Payload}`
5. 决定目标 peer：
   - `Routing.To` 为空：发给所有 `P2PPeers`
   - 否则：按 `partyID -> peer.ID` 找目标
6. 写入 `BroadcastMsgChan`

所以 Node 间每一条 round message 都带了发送者自己的 ECDSA 签名，用于接收端做消息真实性校验。

### 9.3 `ProcessInboundMessages(...)` 做什么

这是每个协议实例的入站主循环。

它持续从 `TssMsg` 读消息，而这个 channel 的上游来自 `Communication` 根据 `(messageType, msgID)` 做的订阅路由。

收到消息后它会：

1. 反序列化 `WrappedMessage`
2. 调 `ProcessOneMessage(...)`

### 9.4 `ProcessOneMessage(...)` 如何区分业务类型

它按 `WrappedMessage.MessageType` 分三类：

1. `TSSKeyGenMsg` / `TSSKeySignMsg`
   - 解成 `WireMessage`
   - 进入 `processTSSMsg(...)`
2. `TSSTaskDone`
   - 解成 `TssTaskNotifier`
   - 统计哪些 peer 已经完成
   - 当完成通知数达到 `len(partyInfo.PartyIDMap)-1` 时关闭 `taskDone`

这里的 `taskDone` 不是协议结果本身，而是一个“大家都做完了，可以收尾”的同步信号。

### 9.5 `processTSSMsg(...)` 如何处理入站 round message

这是最关键的入站处理函数。

它的流程是：

1. 基本合法性检查：`wireMsg`、`Routing`、`From` 不能为空
2. 根据 `Routing.From.Id` 找到发送者 `PartyID`
3. 取出发送者公钥 `GetKey()`
4. 校验 `WireMessage.Sig` 是否真的是该发送者对 `wireMsg.Message + msgID` 签出来的
5. 如果是单播消息：
   - 直接 `updateLocal(wireMsg)`
6. 如果是广播消息：
   - 以 `from + round_info` 作为缓存 key
   - 计算 `wireMsg.Message` 的 hash
   - 保存或更新 `LocalCacheItem`
   - 把本地节点对这个 hash 的确认写入 `ConfirmedList`
   - 直接 `applyShare(...)`

注意一点：当前代码里虽然存在 `hashCheck(...)`、`getMsgHash(...)` 这套“多数确认 hash 一致再应用 share”的辅助逻辑，但 `processTSSMsg(...)` 当前主流程并没有调用它，而是广播消息在完成本地缓存后直接 `applyShare(...)`。

所以文档上要把这点说清楚：

- 有一套更复杂的 hash-majority 设计痕迹
- 但当前实际主路径里，广播 share 是直接落本地 party 的

### 9.6 `updateLocal(...)` 如何把消息喂回 `tss-lib`

`updateLocal(...)` 做的事情是：

1. 通过 `partyInfo` 找到当前协议实例对应的 local party
2. 根据 `Routing.From.Id` 找到发送方的 `PartyID`
3. 对 `WireMessage.Message` 做 `GetMsgRound(...)`，解析出当前 round
4. 做重复消息过滤
5. 如果该发送方已在 culprit 列表里，则拒绝
6. 调 `party.UpdateFromBytes(wireBytes, partyID, isBroadcast)`
7. 若成功，记录 accepted share
8. 若失败，进入 `processInvalidMsg(...)`

这一步才是真正意义上的：

> “收到其他 Node 的 TSS 协议消息，并推进本地密码学状态机”

### 9.7 `processInvalidMsg(...)` 和 abnormal 归因

如果 `party.UpdateFromBytes(...)` 返回 `*tss.Error`，当前实现会：

1. 从 `err.Culprits()` 里拿到可疑 party
2. 基于 `RoundMgr` 找回这些可疑 party 在对应 round 发过的 `WireMessage`
3. 提取：
   - blamed node 的公钥
   - 原始消息体
   - 该消息签名
4. 写入 `Abnormal`

这样做的目的不是自动惩罚，而是尽可能保留“哪一轮、哪个节点、发了什么坏 share”的归因证据。

## 10. libp2p 通信层如何工作

### 10.1 `Communication` 是什么

`Communication` 是 Node 内部所有协议实例共享的一层 libp2p 运行时。

它管理：

- libp2p host
- DHT bootstrap
- stream 打开和关闭
- 出站广播队列
- `(messageType, msgID) -> subscriber channel` 路由表

### 10.2 入站消息路径

Node 间消息进入本地的路径是：

```text
remote peer -> libp2p stream -> handleStream -> readFromStream
   -> WrappedMessage
   -> getSubscriber(messageType, msgID)
   -> channel <- p2p.Message
   -> TssCommon.ProcessInboundMessages
```

这里最关键的是：

- 一个 Node 可能同时跑多轮 keygen/keysign
- 所以必须按 `(MessageType, MsgID)` 精确分发

### 10.3 出站消息路径

本地协议消息发出去的路径是：

```text
tss-lib outCh
   -> TssCommon.ProcessOutCh
   -> sendBulkMsg
   -> BroadcastMsgChan
   -> Communication.ProcessBroadcast
   -> Broadcast / writeToStream
   -> remote peer stream
```

### 10.4 stream 管理策略

当前实现不是为每个 peer 长期保留一条专用复用流，而是：

1. 发消息时 `host.NewStream(...)`
2. 写完后把 stream 暂存在 `StreamMgr.unusedStreams[msgID]`
3. 协议结束时 `ReleaseStream(msgID)`，统一 `Reset()` 掉这些 stream

这说明当前 stream 生命周期和协议实例绑定，而不是和 peer 长连接绑定。

### 10.5 为什么有 ack

`readFromStream(...)` 收到消息后会异步回一个字符串 `"ack"`。

发送端如果 `needHandleResponse = true`，会在写完后等这个 ack。

但 `TSSTaskDone` 不等 ack。

所以这里的 ack 不是业务确认，只是一个轻量的 stream 写入成功反馈。

### 10.6 成员校验

`handleStream(...)` 在真正处理消息前会调用 `VerifyPeerId(peerID)`：

1. 先从 `TssMemberStore` 里读取 active members
2. 如果 active 为空，再读 inactive members
3. 把 `peerID` 反推出公钥
4. 检查这个公钥是否在成员列表里

这意味着 Node 侧 p2p 入站流不是无条件接受的，而是会尝试校验对端是否属于已知成员集合。

### 10.7 以三方 MPC 为例，P2P 协作到底是怎么做的

如果把一轮典型的三方 TSS 看成 `Node A`、`Node B`、`Node C` 三个参与者，当前实现的协作方式不是“先把消息发给 Manager 再转发”，而是三方直接点对点协作：

1. Manager 只负责把同一个 `keygen` 或 `keysign` 任务下发给这三个 Node
2. 三个 Node 各自本地启动自己的 `tss-lib local party`
3. 三个 Node 根据同一份参与者列表，独立计算出相同的 `msgID`
4. 每个 Node 都建立：
   - `(TSSKeyGenMsg or TSSKeySignMsg, msgID)` 的订阅
   - `(TSSTaskDone, msgID)` 的订阅
5. 每个 Node 都把参与者公钥映射成：
   - `tss.PartyID`
   - libp2p `peer.ID`
6. 协议开始后，本地 `tss-lib` 一旦从 `outCh` 产出 round message，就根据 `tss.MessageRouting` 决定怎么发：
   - 如果 `Routing.IsBroadcast = true`，发给另外两个节点
   - 如果 `Routing.To` 指向某个 party，就只单播给对应那个节点
7. 远端节点收到消息后，验签、验成员、按 `msgID` 路由，再调用 `party.UpdateFromBytes(...)` 推进自己的本地状态机
8. 所有 round 完成后，本地 party 从 `endCh` 产出最终结果，并发送 `TSSTaskDone`

三方协作下，数据面大致长这样：

```text
Manager
   | 只下发任务，不转发 round message
   v
Node A <--------------------> Node B
  ^  \                      /  ^
  |   \                    /   |
  |    \                  /    |
  |     \                /     |
  v      \              /      v
Node C <----------------------/

每条边都是 libp2p stream
每个节点都各自运行一份 tss-lib local party
```

这里最关键的一点是：

- Manager 不是 MPC round 的中继站
- 真正推进协议的是三方 Node 之间直接交换 `tss-lib` wire bytes

所以所谓“三方通信”，本质上是：

- 三个节点各自维护本地状态机
- 按 `tss-lib` 给出的广播/单播路由互发消息
- 任何一个节点都不持有完整私钥，只有本地 share 和当前 round 所需消息

### 10.8 这套三方 P2P 协作实际用了哪些协议

这里有三层协议，不要混在一起看：

#### 10.8.1 最上层：MPC 协议本身

最上层真正跑的门限密码学协议，来自 `binance-chain/tss-lib`：

- keygen：`ecdsa/keygen`
- keysign：`ecdsa/signing`

也就是说：

- 三方之间交换的 round message 内容，是 `tss-lib` 定义的 ECDSA TSS 协议消息
- 当前仓库自己不定义 keygen / signing 的密码学 round，只负责把这些 round message 送到对端

#### 10.8.2 中间层：Node 间应用协议

Node 间实际打开的 libp2p stream protocol id 是：

```text
/p2p/tss
```

也就是 `node/tsslib/p2p/communication.go` 里的：

```go
var TSSProtocolID protocol.ID = "/p2p/tss"
```

在这条协议流上，当前项目自定义了一层应用消息格式：

1. 最外层：`WrappedMessage`
2. 中间层：`WireMessage`
3. 最里层：`tss-lib` 的 raw wire bytes

并且 stream 里的负载不是 protobuf，也不是 HTTP，而是：

- 先用 4 字节小端长度头做 framing
- 再写 JSON 序列化后的 `WrappedMessage`

所以如果从“自定义协议格式”角度看，它是：

```text
length-prefixed binary stream
   -> JSON WrappedMessage
      -> JSON WireMessage
         -> tss-lib wire bytes
```

#### 10.8.3 最底层：P2P 发现与连接协议

底层网络协作依赖 libp2p 运行时，当前代码里明确用了这些能力：

- libp2p host
- Kademlia DHT：`go-libp2p-kad-dht`
- bootstrap peers 直连
- rendezvous 广播发现
- ping 连通性检查

所以如果把“三方怎么找到彼此、怎么建立连接”拆开看，就是：

1. Node 启动时先连接 bootstrap peers
2. 启动本地 DHT
3. 在 rendezvous 点做 announce
4. 需要发消息时，对目标 peer 打开 `/p2p/tss` stream
5. 在这个 stream 上发送一条具体的 round message

总结一下，三方协作实际同时叠了三类协议：

- 密码学协议：`tss-lib` 的 ECDSA keygen / keysign round protocol
- 应用层协议：项目自定义的 `/p2p/tss` + `WrappedMessage/WireMessage`
- 网络层协作：libp2p + Kademlia DHT + bootstrap + rendezvous

### 10.9 这套三方协作是怎么保证安全的

这里要分清楚，“安全”不是靠单一一层保证，而是几层一起兜底。

#### 10.9.1 成员身份校验

入站 stream 先过 `VerifyPeerId(...)`：

- 从 `TssMemberStore` 读取 active / inactive member 列表
- 把远端 `peer.ID` 反推出公钥
- 只有这个公钥属于已知成员集合，才继续处理消息

这意味着：

- 不是任何能连上 libp2p 的节点都能参与 TSS 消息处理
- 至少在应用层，先做了一道“是否是已知成员”的白名单检查

#### 10.9.2 每条协议消息都再签一层

当前实现不会直接相信收到的 `tss-lib` 消息，而是要求每条 Node 间协议消息都带业务层签名：

1. 发送端用节点私钥对 `message + msgID` 做 `Keccak256` 后签名
2. 接收端根据 `Routing.From.Id` 找到该 party 对应的公钥
3. 用这个公钥校验 `WireMessage.Sig`
4. 验签通过后，才会继续 `UpdateFromBytes(...)`

这层机制解决的是：

- 这条 round message 到底是谁发的
- 消息体在传输过程中有没有被替换
- 同一条旧消息能不能脱离原任务上下文复用

其中把 `msgID` 也签进去尤其重要，因为它把消息和“这一次具体任务实例”绑定在一起了。

#### 10.9.3 协议实例隔离，降低串台和重放风险

当前实现不是只靠“连接来自哪个 peer”区分任务，而是靠 `(messageType, msgID)` 路由：

- keygen A 的消息不会被送到 keysign B
- 同时进行的两轮不同签名，不会共用同一个订阅通道
- 协议结束后会取消订阅并释放 stream

这相当于给每次 MPC 任务加了一层实例级隔离。

#### 10.9.4 重复消息、坏消息和可疑方归因

收到消息后，当前实现还会做这些事情：

- 用 round 信息做重复消息检查
- 对坏 share 记录 culprit
- 缓存对应 round 的原始消息和签名
- 在 `party.UpdateFromBytes(...)` 失败时，把 blamed node、公钥、消息体、签名写入 `Abnormal`

这不能阻止所有恶意行为，但它至少做到两点：

- 尽快拒绝明显不合法或重复的消息
- 给后续排障、治理、惩罚留下证据

#### 10.9.5 传输层的基础防护

当前代码在传输实现上还做了几层基础保护：

- 读写 deadline，避免 stream 无限卡住
- `MaxPayload` 限制，避免超大消息直接打爆内存
- 收不到 ack 时断开连接并重试一次
- 协议结束后主动 `Reset()` 相关 stream

这些不是密码学安全，但属于工程上的抗故障和资源保护。

#### 10.9.6 底层连接安全由 libp2p 负责，但当前代码没有显式自定义安全协议参数

当前项目是通过 `libp2p.New(...)` 创建 host，并在其上跑 `/p2p/tss` stream。

从当前仓库代码能明确看见的是：

- 它复用了 libp2p 的连接与身份体系
- Node 身份和 libp2p peer identity 绑定到同一把 secp256k1 私钥

但需要实话实说：

- 当前代码没有显式配置某个特定的安全传输插件名称
- 所以文档上不应把“项目自己显式启用了某种传输安全协议”说得过头

比较准确的说法是：

- 应用层消息完整性和发送者身份绑定，是当前项目自己显式实现的
- 底层连接握手与传输保护，依赖 libp2p 默认提供的安全连接机制

#### 10.9.7 当前安全边界和限制

为了避免误解，这里把当前实现的边界也写清楚：

- `CheckPubKeys(...)` 只把 signer 公钥转成 `peer.ID`，并不强保证这些 signer 当下真的在线
- 广播消息虽然有 `hashCheck(...)` / `getMsgHash(...)` 这套更强一致性设计痕迹，但主流程里目前没有启用多数确认后再应用 share
- `ack` 只是轻量写入反馈，不是强一致确认协议
- 成员校验依赖本地 `TssMemberStore` 的正确性和新鲜度

所以更准确地讲，当前这套实现的安全性来自：

- `tss-lib` 本身的门限密码学协议
- libp2p 提供的 P2P 连接基础设施
- 当前项目自己补上的成员校验、消息签名、实例隔离、异常归因

而不是某一个单点机制“绝对保证安全”。

## 11. keygen / keysign 的完整时序

### 11.1 keygen 时序

```text
Node signer        TssServer          TssKeyGen/TssCommon         Communication/libp2p        Other Nodes
    |                  |                      |                            |                        |
    | Keygen(req)      |                      |                            |                        |
    |----------------->| requestCheck         |                            |                        |
    |                  | make msgID           |                            |                        |
    |                  | subscribe(msgID)     |                            |                        |
    |                  | new TssKeyGen        |                            |                        |
    |                  |--------------------->| GenerateNewKey             |                        |
    |                  |                      | new local party            |                        |
    |                  |                      | start ProcessInbound       |                        |
    |                  |                      | party.Start()              |                        |
    |                  |                      | outCh message              |                        |
    |                  |                      |--------------------------->| write stream           |
    |                  |                      |                            |----------------------->|
    |                  |                      |                            |<-----------------------|
    |                  |                      | inbound wrapped msg        |                        |
    |                  |                      | party.UpdateFromBytes      |                        |
    |                  |                      | ... multiple rounds ...    |                        |
    |                  |                      | endCh LocalPartySaveData   |                        |
    |                  |                      | save local state           |                        |
    |                  |                      | notify task done           |                        |
    |<-----------------| keygen.Response      |                            |                        |
```

这张图描述 Node 内部执行 keygen 的完整链路。`node/signer/keygen.go` 收到
Manager 通过 WebSocket 下发的 keygen 请求后，会把它转换成 `keygen.Request`
并调用 `TssServer.Keygen(...)`。`TssServer` 先做请求校验，确认本节点属于参与方，
再根据参与节点集合生成本轮 `msgID`，并在 `Communication` 上订阅该 `msgID`，
这样后续收到的 p2p round message 才能被路由回这一轮 keygen。

订阅建立后，`TssServer` 创建 `TssKeyGen`，进入 `GenerateNewKey(...)`。
`TssKeyGen` 会基于参与方列表、阈值、本地 preparams 和 `tss-lib` 参数创建
keygen local party，然后启动 `ProcessInbound` 和 `party.Start()`。本地 party
从 `outCh` 产出的 round message 会先经过 `TssCommon` 包装、签名，再交给
`Communication/libp2p` 写入 stream，发送给其他参与节点。其他节点发回的消息
则通过 libp2p stream 进入本节点，经过解包、验签、按 `msgID` 投递后，由
`ProcessInbound` 调用 `party.UpdateFromBytes(...)` 喂给本地 party。

多轮 round message 交换完成后，`tss-lib` 会在 `endCh` 产出
`LocalPartySaveData`。这份数据包含本节点的 key share、公钥和参与方信息，是后续
keysign 的长期依赖。当前节点会把它转换为 `KeygenLocalState` 并写入本地存储，
同时广播 task done，让其他节点知道本轮 keygen 已完成。最后 `TssServer` 把
聚合公钥包装成 `keygen.Response` 返回给 `node/signer/keygen.go`，再由 signer
通过 WebSocket 回传给 Manager。

### 11.2 keysign 时序

```text
Node signer        TssServer          TssKeySign/TssCommon        Communication/libp2p        Other Nodes
    |                  |                      |                            |                        |
    | KeySign(req)     |                      |                            |                        |
    |----------------->| requestCheck         |                            |                        |
    |                  | load localstate      |                            |                        |
    |                  | check participants   |                            |                        |
    |                  | make msgID           |                            |                        |
    |                  | subscribe(msgID)     |                            |                        |
    |                  | new TssKeySign       |                            |                        |
    |                  |--------------------->| SignMessage                |                        |
    |                  |                      | new signing local party    |                        |
    |                  |                      | start ProcessInbound       |                        |
    |                  |                      | outCh message              |                        |
    |                  |                      |--------------------------->| write stream           |
    |                  |                      |                            |----------------------->|
    |                  |                      |                            |<-----------------------|
    |                  |                      | inbound wrapped msg        |                        |
    |                  |                      | party.UpdateFromBytes      |                        |
    |                  |                      | ... multiple rounds ...    |                        |
    |                  |                      | endCh SignatureData        |                        |
    |                  |                      | notify task done           |                        |
    |<-----------------| keysign.Response     |                            |                        |
```

这张图描述 Node 内部执行 keysign 的完整链路。`node/signer/sign.go` 在完成
ask 前置检查后，会把待签的 32 字节 digest、参与签名节点列表和聚合公钥转换成
`keysign.Request`，并调用 `TssServer.KeySign(...)`。`TssServer` 先做基础校验，
再根据聚合公钥加载对应的 `KeygenLocalState`，也就是 keygen 阶段保存下来的本地
key share。如果内存中的 `participants` 缓存缺失，keysign 路径也会尝试从
`KeygenLocalState.ParticipantKeys` 恢复参与方信息。

状态加载完成后，`TssServer` 会确认本节点确实属于本次 signer 集合，然后基于
待签 digest 和 signer 集合生成本轮 keysign `msgID`，并建立 keysign 消息订阅。
随后创建 `TssKeySign` 并进入 `SignMessage(...)`。`TssKeySign` 使用本地
`KeygenLocalState`、待签 digest、参与方参数和 `tss-lib` signing party 创建
本地签名 party，启动入站处理，然后由 local party 产生出站 round message。

keysign 的 p2p 消息流转方式和 keygen 一致：本地 `outCh` 的消息会被包装、签名、
按目标 peer 发送；远端消息进入本节点后会被验签、按 `msgID` 投递，并通过
`party.UpdateFromBytes(...)` 推进本地 signing party。多轮 round 完成后，
`endCh` 会产出 `SignatureData`，其中包含 ECDSA 签名的 `R`、`S`、恢复字段和
原始 digest。节点随后广播 task done，并把 `SignatureData` 包装成
`keysign.Response` 返回给 `node/signer/sign.go`。上层 signer 再把它转换成业务层
使用的 65 字节签名 `[R || S || V]`，通过 WebSocket 回传给 Manager。

## 12. 这套 `tsslib` 设计的几个关键特点

### 12.1 `TssServer` 是运行时装配层，不是算法层

`TssServer` 负责的是：

- 请求校验
- 状态加载
- p2p 订阅
- local party 生命周期管理

真正的 ECDSA 门限算法，仍然在 `binance-chain/tss-lib` 内部。

### 12.2 协议实例按 `msgID` 隔离

整个 Node 内部不依赖全局单例 channel 去跑所有协议，而是：

- 每次 keygen / keysign 各自建立订阅
- 按 `msgID` 路由
- 结束后取消订阅并释放 stream

这是它能支持多协议实例并发的基础。

### 12.3 Node 间消息有额外业务签名

除了 `tss-lib` 自己的密码学消息结构，当前实现还要求：

- 每一条 `WireMessage` 都由发送节点再签一层
- 接收方先验这层签名，再决定是否交给 local party

这层签名的作用是补足“谁把这条 p2p 消息发出来的”这一层身份绑定。

### 12.4 keygen 结果是长期状态，keysign 只是读取和使用它

keygen 会产出 `KeygenLocalState` 并长期保存。

keysign 并不重新生成密钥份额，它只是：

1. 根据 `poolPubKey` 加载之前的 `KeygenLocalState`
2. 用里面的 `LocalPartySaveData` 去跑新一轮签名

这也是为什么 keysign 一定依赖之前成功完成的 keygen。

### 12.5 当前代码里有“更复杂一致性检查”的痕迹，但主流程没完全接上

例如：

- `hashCheck(...)`
- `getMsgHash(...)`
- `ShareMgr`

这些看起来像是为了“广播 share 先收集多数 hash 确认，再真正应用 share”准备的。

但当前主路径里：

- `processTSSMsg(...)` 对广播消息是直接 `applyShare(...)`
- `hashCheck(...)` 并未在主流程里调用

所以如果你在读代码时觉得“好像本来想设计成更复杂的一致性确认协议”，这个感觉是对的，只是当前实现并没有完全走到那里。

## 13. 当前实现里的注意点和限制

下面这些是理解 `node/tsslib` 时要特别注意的现状。

### 13.1 `TssServer.Start()` 基本是空壳

`TssServer.Start()` 当前只打日志，不额外启动后台协程。

真正重要的启动工作，其实已经在 `NewTss(...)` 里完成：

- preparams 加载/生成
- p2p host 启动
- DHT bootstrap

### 13.2 keysign 的“在线节点”判断并不强

当前 `CheckPubKeys(...)` 只是把公钥集合转成 `peer.ID`，并不确认这些 peer 当前可达。

因此：

- 某个 signer 如果名义上在集合里、实际不在线
- 协议最终还是会在 round 中超时失败

### 13.3 `participants` 只在内存里缓存

`participants[poolPubKey]` 是内存缓存，进程重启后会丢。

不过 keysign 可以从 `KeygenLocalState.ParticipantKeys` 恢复，所以不是致命问题。

### 13.4 stream 生命周期偏粗

当前是协议结束后 `ReleaseStream(msgID)` 一次性 reset 所有相关 stream。

这实现简单，但不是最细粒度的 stream 管理方式。

### 13.5 异常归因信息主要用于观测，不等于完整惩罚闭环

`Abnormal`、`RoundMgr`、culprit 记录做到了：

- 尽量识别坏 share
- 保留消息和签名证据

但这并不自动等于：

- 立刻剔除节点
- 自动重组 signer 集合
- 自动链上惩罚

它更像是上层治理或排障所需的“证据收集层”。

## 14. 推荐阅读顺序

如果你准备继续深入 `node/tsslib`，建议按下面顺序看：

1. `node/signer/sign.go`
2. `node/signer/keygen.go`
3. `node/tsslib/server.go`
4. `node/tsslib/tss.go`
5. `node/tsslib/keygen.go`
6. `node/tsslib/keysign.go`
7. `node/tsslib/keygen/tss_keygen.go`
8. `node/tsslib/keysign/tss_keysign.go`
9. `node/tsslib/common/tss.go`
10. `node/tsslib/common/tss_helper.go`
11. `node/tsslib/p2p/communication.go`
12. `node/tsslib/p2p/stream_helper.go`
13. `node/tsslib/storage/localstate_mgr.go`
14. `node/tsslib/abnormal/*.go`

这个顺序的好处是：

- 先看入口和编排
- 再看协议实例如何跑起来
- 最后再看 p2p、存储和异常归因

## 15. 一句话总结

这个项目里真正的 TSS 协议，不在 Manager，也不在 WebSocket。

它本质上是：

- 由 `node/signer` 触发
- 由 `TssServer` 装配运行时
- 由 `TssKeyGen` / `TssKeySign` 驱动本地 `tss-lib` party
- 由 `TssCommon` 封装消息、验签、喂回 local party
- 由 `Communication` 通过 libp2p 把 round message 在 Node 之间传递
- 由 `storage` 保存 keygen 本地状态
- 由 `abnormal` 记录协议异常和作恶线索

的 Node 内部多方协议执行系统。

如果只抓住一句话，那就是：

> `node/tsslib` 才是这个仓库真正跑门限协议的地方，WebSocket 只是把任务送到这里。
