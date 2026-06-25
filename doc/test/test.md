# TSS Docker 测试方案

## 目标

本目录提供一套最小可运行的本地联调方案，使用 Docker Compose 启动：

- 1 个 `tss manager`
- 3 个 `tss node`

用途是验证以下链路：

1. Manager HTTP 服务正常启动
2. 3 个 Node 通过 `/ws` 连接到 Manager
3. 3 个 Node 之间 libp2p 建立互联
4. 通过 Manager 的 `/api/v1/keygen` 生成门限公钥
5. 通过 Manager 的 `/api/v1/sign` 对 32 字节 digest 发起一次 threshold ECDSA 签名

## 文件说明

- 仓库根目录下的 `docker-compose.yml`：一键启动 4 个容器
- 仓库根目录下的 `tssmanage.docker.toml`：Manager 容器专用配置
- 仓库根目录下的 `tssnode0.docker.toml` 到 `tssnode2.docker.toml`：3 个 Node 容器专用配置
- `keygen_request.json`：`/api/v1/keygen` 示例请求
- `sign_request.json`：`/api/v1/sign` 示例请求
- 仓库根目录下的 `data/`：容器落盘目录，会保存 manager LevelDB、node LevelDB 和 keygen localstate

## 配置设计

### 1. 为什么使用静态 IP

当前实现里 Node 的 `bootstrap_peers` 和 `external_ip` 都要求写成明确的 IP/端口格式，示例：

```text
/ip4/172.28.0.20/tcp/8001/p2p/16Uiu2...
```

这里不能直接写 Compose 服务名。因此 `docker-compose.yml` 给 4 个容器分配了固定 IP：

- `tss-manager`: `172.28.0.10`
- `tss-node0`: `172.28.0.20`
- `tss-node1`: `172.28.0.21`
- `tss-node2`: `172.28.0.22`

### 2. 节点公钥和 Peer ID

这套配置沿用了仓库现有 3 个测试私钥，对应关系如下：

- `02fe2481...` -> `16Uiu2HAmAGco75s1oGSoBG9v6horeYMGueCfgTe2sdLX5pLfPT9p`
- `024695a4...` -> `16Uiu2HAmCXjpcTfpdedNcpKqUt3WiSVgBWR97yWw5di3b55QjkeX`
- `02dc8d79...` -> `16Uiu2HAkzBCs5xfgCmhDrKxWy3pXmQSf2XQ3Ucoo2d6NJBXJsvnd`

Manager 的 `manager.nodes` 与 3 个 Node 的私钥必须严格对应，否则 manager 虽然能看到连接，但不会把它们视为可用签名节点。

## 启动步骤

在仓库根目录执行：

```bash
docker compose -f docker-compose.yml up --build -d
```

查看容器状态：

```bash
docker compose -f docker-compose.yml ps
```

查看日志：

```bash
docker compose -f docker-compose.yml logs -f tss-manager
docker compose -f docker-compose.yml logs -f tss-node0
docker compose -f docker-compose.yml logs -f tss-node1
docker compose -f docker-compose.yml logs -f tss-node2
```

停止并清理：

```bash
docker compose -f docker-compose.yml down
rm -rf data/manager data/node0 data/node1 data/node2
```

## 启动成功的判据

### Manager

检查健康接口：

```bash
curl -i http://127.0.0.1:8080/ping
```

预期返回 `HTTP/1.1 200 OK`。

检查 metrics：

```bash
curl http://127.0.0.1:8080/api/v1/metrics | grep tssmanager_node_online_node_counter
```

预期能看到在线节点数最终变成 `3`。

### Node

每个节点都暴露了 `/metrics`：

```bash
curl http://127.0.0.1:8082/metrics
curl http://127.0.0.1:8083/metrics
curl http://127.0.0.1:8084/metrics
```

如果节点启动成功，日志里通常会看到：

- 本地公钥和 peer id
- `auth success!`
- 连接 bootstrap 节点成功
- `Starting the TSS servers`

### 一个需要知道的现象

Manager 现在不会在启动时自动做 keygen。必须等 3 个 Node 都连上以后，再手动调用 `/api/v1/keygen`。

## 实际测试：生成门限公钥

调用：

```bash
curl -sS \
  -X POST http://127.0.0.1:8080/api/v1/keygen \
  -H 'Content-Type: application/json' \
  --data @doc/test/keygen_request.json
```

预期返回一个压缩公钥字符串，形如：

```text
02abcdef...
```

这里有一个和后续 `/api/v1/sign` 强相关的约定：

- `keygen_request.json` 里的 `nodes` 会成为该 `election_id` 对应 committee 的持久化成员集
- 如果 `nodes` 省略，Manager 才会回退到仓库根目录 `tssmanage.docker.toml` 里的 `manager.nodes`
- 这套 Docker 联调建议显式保留 `keygen_request.json` 中的 3 个节点公钥，不要依赖隐式回退
- 后续 `/api/v1/sign` 必须使用同一个 `election_id`

### 关于 `threshold`

这里的 `threshold` 不是参与节点总数，而是 TSS 的门限参数 `t`。

对于 3 个节点：

- `threshold = 1` 表示 `2-of-3`
- `threshold = 2` 表示 `3-of-3`

建议先用 `threshold = 1` 做测试。

成功后可以直接检查本地落盘结果，例如：

```bash
find data/node0/base -maxdepth 1 -type f
find data/node1/base -maxdepth 1 -type f
find data/node2/base -maxdepth 1 -type f
```

正常情况下，每个节点都会出现 `localstate-<cpk>.json` 一类文件。

## 实际测试：发起签名

先准备一个 32 字节十六进制消息摘要。示例里已经给出：

```text
4d7a957fa90c86fed223e3aa999009d2ff6e4b16ed7c4f8a76ad4520668236a7
```

调用：

```bash
curl -sS \
  -X POST http://127.0.0.1:8080/api/v1/sign \
  -H 'Content-Type: application/json' \
  --data @doc/test/sign_request.json
```

预期返回 JSON，结构类似：

```json
{"signature":"..."}
```

这里的 `signature` 是 65 字节可恢复签名的字节序列经 JSON 编码后的结果。

## 排障重点

### 1. `online_node_counter` 一直不是 3

优先检查：

- `manager.nodes` 是否与 3 个 Node 私钥对应
- Node 日志里是否出现 `auth success!`
- `ws_addr` 是否写成 `tcp://tss-manager:8081`

### 2. Node 之间 P2P 连不上

优先检查：

- `bootstrap_peers` 里的 IP、端口、peer ID 是否完全匹配
- `external_ip` 是否写成容器固定 IP，而不是 `127.0.0.1`
- 宿主机是否已有端口冲突

### 3. `keygen` 超时

常见原因：

- 某个 Node 还没完成 preparams 生成
- 某个 Node 没有加入 websocket 或 P2P 网络
- `threshold` 配置过高

首次启动时 preparams 可能耗时较久，建议先观察 1 到 3 分钟日志。

### 4. `sign` 失败

优先检查：

- 是否先成功执行过相同 `election_id` 的 `keygen`
- `message_hash` 是否是合法 32 字节十六进制
- 3 个节点日志里是否都收到了 `askMessageHash` 和 `TransactionSign`

## 建议的联调顺序

1. `docker compose up --build -d`
2. 确认 `/ping` 正常
3. 确认 manager metrics 中在线节点数为 3
4. 调用 `/api/v1/keygen`
5. 调用 `/api/v1/sign`
6. 结合 manager 和 3 个 node 日志定位问题
