# KeyGen / KeySign Workflow Bugs

## Critical

### `node/tsslib/p2p/communication.go` ✅
- Workflow: `shared`
- Path: `node/tsslib/common/tss.go -> node/tsslib/p2p/communication.go`
- Problem: `writeToStream()` 在真正写流前就用 `defer` 注册 stream，导致写失败、等响应失败、重试失败的 stream 也会被登记到 `StreamMgr`。
- Impact: 失败连接会被错误挂到 `msgID` 上，重试时还会叠加更多无效 stream，最终污染连接生命周期并增加清理复杂度。
- Evidence: [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:183), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:195), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:200), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:205)
- Suggested Fix: 去掉 `defer` 注册，改成只有在 `WriteStreamWithBuffer` 成功后才显式登记 stream；失败和重试前显式 `Reset()` 当前 stream。

### `node/tsslib/common/tss.go` ✅
- Workflow: `shared`
- Path: `node/tsslib/keygen/tss_keygen.go -> node/tsslib/common/tss.go -> node/tsslib/p2p/communication.go`
- Problem: P2P 出站通道 `broadcastChannel` 是同步写入；当 `Communication.BroadcastMsgChan` 被塞满时，keygen/keysign 主流程会阻塞在 `renderToP2P()`。
- Impact: 一旦广播消费跟不上，`ProcessOutCh()` 无法继续发 share，协议主循环会卡死并最终超时。
- Evidence: [node/tsslib/common/tss.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/common/tss.go:379), [node/tsslib/common/tss.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/common/tss.go:546), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:91), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:542)
- Suggested Fix: 把写入改成带超时或显式背压控制的非无限阻塞模型，并在上层把“广播队列满”作为可观测错误暴露出来。
-  现在 renderToP2P() 不再对 broadcastChannel 做无限阻塞写入，而是改成最多等待 2s。如果 Communication.BroadcastMsgChan 一直满着，会返回一个明确错误，里面带上 msg_id、消息类型和队列长度信息。sendBulkMsg() 和 NotifyTaskDone() 都会把这个错误向上返回；ProcessOutCh() 也不再只是记录日志后继续，而是遇到出站排队失败就直接返回，让 keygen/keysign 主循环把它当成真实协议失败处理，而不是最后伪装成超时。

### `node/tsslib/p2p/communication.go` ✅
- Workflow: `shared`
- Path: `node/tsslib/p2p/communication.go -> keygen/keysign inbound processing`
- Problem: `getSubscriber()` + retry loop 不是原子投递语义。读取侧先取出 channel 指针，解锁后再阻塞发送，期间订阅可能已被取消或消费者已经卡住。
- Impact: 消息可能被投递到逻辑上已取消的订阅；如果 channel 没有消费方，`readFromStream()` 会被阻塞，拖死整个入站流处理。
- Evidence: [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:247), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:249), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:252), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:507), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:518)
- Suggested Fix: 
-  现在入站处理不再先拿出 channel 指针再在锁外裸发，而是改成统一走 Deliver() 原子接口，返回三种结果：SubscriberDelivered、SubscriberNotFound、
  SubscriberBusy。外层 retry 只对 not_found 生效；如果订阅已存在但消费者卡住，投递会在 2s 超时后返回 busy 并记录告警，不会再把 readFromStream() 无限卡死，也不会再把消息送进逻辑上已经取消的订阅。

### `node/tsslib/p2p/communication.go`✅
- Workflow: `shared`
- Path: `node/tsslib/p2p/communication.go -> Stop()`
- Problem: `ProcessBroadcast()` 在停机期间仍可能调用 `Broadcast()`，而 `Stop()` 关闭 `stopChan` 后立刻执行 `wg.Wait()`；这会让 `Broadcast()` 的 `wg.Add(1)` 与 `Wait()` 并发发生。
- Impact: 存在 `WaitGroup misuse: Add called concurrently with Wait` 的 panic 风险，停机时可能直接炸进程。
- Evidence: [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:115), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:480), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:488), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:553)
- Suggested Fix: 
-   在 node/tsslib/p2p/communication.go:121 我给 Broadcast() 加了停机门禁：记账前先通过 tryTrackBroadcast() 在互斥保护下检查 stopping，只有还没进入停机态才会 wg.Add(1)。对应地，node/tsslib/p2p/communication.go:493 现在先 beginStop() 置位 stopping 并关闭 stopChan，然后才 wg.Wait()，这样 Wait() 开始后不会再有新的 Broadcast() 做 Add()。

## High

### `node/tsslib/keygen/tss_keygen.go` ✅
- Workflow: `keygen`
- Path: `node/tsslib/keygen.go -> node/tsslib/keygen/tss_keygen.go -> node/tsslib/common/tss.go`
- Problem: keygen 路径构造完 peer 集后调用了 `conversion.GetPeersID(...)`，但没有把结果赋回 `tssCommonStruct.P2PPeers`。
- Impact: 广播 share 时 `P2PPeers` 为空，`sendBulkMsg()` 会向空 peer 列表发消息，keygen 网络分发无法正常推进。
- Evidence: [node/tsslib/keygen/tss_keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/keygen/tss_keygen.go:135), [node/tsslib/keygen/tss_keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/keygen/tss_keygen.go:137), [node/tsslib/common/tss.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/common/tss.go:365)
- Suggested Fix: 
-  node/tsslib/keygen/tss_keygen.go:75 现在新增了 refreshP2PPeers()，并在生成 partyID -> peer.ID 映射后显式把 conversion.GetPeersID(...) 的结果写回
    tssCommonStruct.P2PPeers，不再只是调用后丢弃返回值。这样 keygen 广播路径在 node/tsslib/common/tss.go:365 读取到的 peer 列表就不再是空的。

### `manager/keygen.go` ✅
- Workflow: `keygen`
- Path: `manager/manager.go -> manager/keygen.go -> node/signer/keygen.go -> node/tsslib/keygen.go`
- Problem: `callKeygen()` 组装发给节点的 `KeygenRequest` 时使用的是 `m.tssMembers`，不是本次请求/本次流程实际传入的 `tssMembers`。
- Impact: 只要 manager 全局成员集和当前 election/调用指定成员集不一致，节点就会按错误的参与者集合去构造 keygen party，造成 `msgID`、参与方和协议成员不一致。
- Evidence: [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:53), [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:111), [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:136)
- Suggested Fix: 
- callKeygen() 现在显式接收本轮 tssMembers，并把它写入 KeygenRequest.Nodes，不再错误使用 m.tssMembers。同时给节点下发的 KeygenRequest 补上了RequestId，这样 manager 和 node 对同一条 keygen 业务请求会共享一致标识。

### `manager/keygen.go` ✅
- Workflow: `keygen`
- Path: `manager/router/registry.go -> manager/manager.go -> manager/keygen.go`
- Problem: manager 发送给节点的 `KeygenRequest` 没有填充 `RequestId`。
- Impact: 节点侧拿不到上层业务请求 ID，破坏 keygen 请求的链路追踪与幂等语义；同时请求体内容与 manager 注册响应 channel 的 `requestId` 不一致，排障会很困难。
- Evidence: [common/types.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/common/types.go:49), [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:135), [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:145)
- Suggested Fix: 在 `KeygenRequest` 里带上当前 `requestId`，让节点和 manager 对同一业务请求共享一致标识。

### `manager/keygen.go` ✅
- Workflow: `keygen`
- Path: `manager/keygen.go -> sendError handling`
- Problem: `sendError` 是无缓冲 channel，多个节点并发发送失败时，多余的发送 goroutine 会阻塞在 `sendError <- struct{}{}` 上；同时接收侧一旦返回，后续发送者没有消费者。
- Impact: 发送失败路径会泄漏 goroutine，严重时可能把 manager 的 keygen 调用拖住或制造悬挂 goroutine。
- Evidence: [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:66), [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:82), [manager/keygen.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/keygen.go:147)
- Suggested Fix: 
- sendError 改成容量为 1 的缓冲通道，发送失败时改为非阻塞上报，避免多个节点同时发送失败时卡死在 sendError <- struct{}{}，从而泄漏 goroutine 或把 keygen 调用拖住。

### `manager/sign.go` ✅
- Workflow: `keysign`

- Path: `manager/manager.go -> manager/sign.go`

- Problem: `sendSignReqToNodes()` 的 `errSendChan` 也是无缓冲 channel，多节点同时发送失败时会有 goroutine 阻塞在报错路径上。

- Impact: 签名请求发送失败不仅会提前返回，还会遗留被卡住的发送 goroutine，影响 manager 的稳定性。

- Evidence: [manager/sign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/sign.go:73), [manager/sign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/sign.go:91), [manager/sign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/sign.go:193)

- Suggested Fix: 

   在 manager/sign.go:73 里，errSendChan 改成了容量 1 的缓冲 channel，sendSignReqToNodes() 里的报错路径也改成非阻塞上报，只消费第一次发送失败。这样多节点同时发送失败时，不会再有 goroutine 卡死在 errSendChan <- struct{}{} 上。

### `manager/agreement.go` ✅
- Workflow: `keysign`

- Path: `manager/manager.go -> manager/agreement.go`

- Problem: `agreement()` 的 `errSendChan` 先在 goroutine 退出时 `close(errSendChan)`，但外层多个 `askNodes()` 发送 goroutine 可能仍在 `errSendChan <- struct{}{}`。

- Impact: 停机/超时/提早返回场景下可能触发 `send on closed channel` panic。

- Evidence: [manager/agreement.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/agreement.go:50), [manager/agreement.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/agreement.go:59), [manager/agreement.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/agreement.go:147)

- Suggested Fix: 不要由接收方关闭错误上报 channel；改成只由创建方在确认没有发送者后关闭

  在 manager/agreement.go:50 里，我去掉了“接收方关闭 errSendChan”的做法，改为由接收协程在退出时关闭 stopChan，并把 errSendChan 设为按 availableNodes 数量分配的缓冲 channel。askNodes() 里的失败上报也改成非阻塞发送。这里没有收敛成“只报第一次失败”，因为现有逻辑要靠失败次数去递减 expectedResponseCount；现在保留这个语义，同时避免了 send on closed channel 和发送方阻塞。

### `node/signer/sign.go` ✅
- Workflow: `keysign`
- Path: `manager/manager.go -> manager/sign.go -> node/signer/sign.go`
- Problem: 节点签名缓存 `cacheSign` 的 key 只用 `message_hash`，没有包含 `election_id` 或 `cluster_public_key`。
- Impact: 如果不同 election/不同 TSS committee 恰好对同一摘要签名，节点可能复用上一轮缓存签名，把错误委员会的签名返回给 manager。
- Evidence: [node/signer/sign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/signer/sign.go:95), [node/signer/sign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/signer/sign.go:119), [node/signer/sign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/signer/sign.go:153)
- Suggested Fix: 
- 现在节点签名缓存不再只用 message_hash。SignGo() 和 checkMessages() 已统一改成使用 message_hash + election_id + cluster_public_key 生成缓存键，并对cluster_public_key 做了基础规范化处理。这样即使不同 election 或不同 committee 恰好签同一个摘要，也不会错误复用上一轮缓存签名。waitSignMsgs 的 ask 前置校验仍然保持原来的哈希键语义，这次没有改那一层流程。

## Medium

### `node/signer/process.go` ✅
- Workflow: `keygen`

- Path: `manager/keygen.go -> node/signer/deal_msg.go -> node/signer/keygen.go`

- Problem: `keygenRequestChan` 缓冲只有 `1`，而 `writeChan()` 采用非阻塞发送；多个 keygen 请求近距离到达时，多余请求会被直接丢弃并只记日志。

- Impact: manager 看到的是节点无响应/超时，但节点本地没有明确失败响应，问题会被伪装成 keygen 超时。

- Evidence: [node/signer/process.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/signer/process.go:72), [node/signer/deal_msg.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/signer/deal_msg.go:41), [node/signer/deal_msg.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/signer/deal_msg.go:53)

- Suggested Fix: 

  在 node/signer/deal_msg.go:41 里，keygen 请求入队失败不再只是本地打日志。现在当 keygenRequestChan 已满时，节点会立刻回一个显式 RPC 错误给 manager，错误文案是节点正忙于处理前一个 keygen 请求。这样 manager 看到的是明确失败，而不是伪装成后续超时。

### `node/tsslib/keysign.go` ✅
- Workflow: `keysign`

- Path: `node/tsslib/keysign.go -> participants cache`

- Problem: `participants` map 在 keysign 路径里直接读写，没有任何锁保护。

- Impact: 当前实现没有全局 keysign 串行化，未来只要同一节点并发处理多个 keysign，就会触发数据竞争。

- Evidence: [node/tsslib/tss.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/tss.go:35), [node/tsslib/keysign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/keysign.go:50), [node/tsslib/keysign.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/keysign.go:113)

- Suggested Fix: 

  在 node/tsslib/tss.go:32 和 node/tsslib/keysign.go:50 里，participants 现在有专用 RWMutex 保护，keysign 路径的写入和 GetParticipants() 的读取都走了加锁逻辑；同时返回值和写入值都做了切片拷贝，避免并发下的数据竞争和别名问题。

### `node/tsslib/p2p/communication.go` ✅
- Workflow: `shared`
- Path: `node/tsslib/p2p/communication.go -> inbound stream handling`
- Problem: `readFromStream()` 每次收包都无界起一个 ACK goroutine，并且忽略写 ACK 的任何错误。
- Impact: 高并发或对端异常时会制造额外 goroutine 堆积，且 ACK 写失败完全不可观测。
- Evidence: [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:226), [node/tsslib/p2p/communication.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/node/tsslib/p2p/communication.go:228)
- Suggested Fix: 
- readFromStream() 现在不再为每个入站包额外起一个 ACK goroutine，而是在成功读出 payload 后同步写回 ack。同时，ACK 写失败不再被忽略，会记录明确日志 fail to write ack to stream，这样对端异常或流状态异常时就有可观测性了。读包失败时则不会再误发 ACK。

### `manager/router/registry.go` ✅
- Workflow: `shared`
- Path: `HTTP API -> manager/router/registry.go`
- Problem: keygen/sign 的 HTTP 错误文案混乱，`KeygenHandler()` 失败也返回 `"failed to sign state"`，`SignTxHandler()` 的 `MessageHash` 校验报错文案也与实际字段无关。
- Impact: 上层调用方和运维排障时很难从 HTTP 返回信息判断到底是 keygen 失败、keysign 失败还是请求参数错误。
- Evidence: [manager/router/registry.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/router/registry.go:35), [manager/router/registry.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/router/registry.go:53), [manager/router/registry.go](/home/sandwichzzy/Wallet/mpc-wallet/tss/manager/router/registry.go:58)
- Suggested Fix: 按接口语义分别返回准确错误文案，并让参数校验信息和真实字段一致。
