# TSS

[简体中文](README.md) | [English](README.en.md)

Go 实现的 MPC Threshold Signature Scheme 服务。项目由一个协调服务和多个签名节点组成：

- `manager`：提供 HTTP API，接收 Node 的 WebSocket 连接，协调 keygen 和 sign 流程。
- `node`：连接 Manager，运行本地 HTTP/metrics 服务，持久化本地状态，并通过 libp2p 执行 TSS 协议轮次。
- `parse-peer-id`：根据 secp256k1 公钥或私钥推导 libp2p peer ID 的辅助命令。

底层门限 ECDSA 协议执行基于 `github.com/bnb-chain/tss-lib`。

## 目录结构

```text
.
├── cmd.go                    # Cobra 入口：tss manager/node/parse-peer-id
├── manager/                  # Manager 服务、HTTP 路由、协调逻辑、LevelDB 存储
├── node/                     # Node 服务、签名流程、HTTP 服务、存储、tsslib 集成
├── ws/                       # Manager WebSocket 服务端和 Node WebSocket 客户端
├── common/                   # 共享请求/响应结构和工具函数
├── config/                   # TOML/env 配置加载
├── doc/                      # 协议说明和测试请求示例
├── docker-compose.yml        # 本地 1 manager + 3 node 联调环境
├── tssmanage.docker.toml     # Docker Manager 示例配置
└── tssnode*.docker.toml      # Docker Node 示例配置
```

相关文档：

- [命令与触发链路](doc/cmd.md)
- [WebSocket 结构](doc/ws_structure.md)
- [TSS 协议说明](doc/tsslib_protocol_explaination.md)

## 环境要求

- Go `1.25.7`，或与 `go.mod` 匹配的兼容版本
- `make`
- Docker 和 Docker Compose，用于本地多节点联调
- `golangci-lint`，仅在执行 `make lint` 时需要

常规构建会启用 CGO。

## 构建与测试

优先使用 make 目标：

```bash
make build
make test
make lint
```

等价的直接命令：

```bash
go build -v .
go test -v ./...
golangci-lint run ./...
```

`make build` 会通过 linker flags 写入 git commit 元数据，并在仓库根目录构建 `tss` 二进制。

## CLI 使用

所有子命令共享根命令的 `-c, --config` 配置文件参数。配置会在子命令执行前统一加载。

```bash
go run . manager -c tssmanage.docker.toml
go run . node -c tssnode0.docker.toml --debug
go run . parse-peer-id --pri-key <hex-private-key>
go run . parse-peer-id --pub-key <hex-public-key>
```

Node 额外参数：

- `--debug`, `-d`：开启 debug 日志
- `--non-prod`：开启非生产环境 Node HTTP 行为
- `--full`：等待完整 peer 互联后再启动 TSS

## 本地 Docker 联调

仓库内置了一套最小本地集群：

- `tss-manager`：HTTP `127.0.0.1:8080`，WebSocket `8081`
- `tss-node0`：HTTP `127.0.0.1:8082`，P2P 宿主机端口 `18001`
- `tss-node1`：HTTP `127.0.0.1:8083`，P2P 宿主机端口 `18002`
- `tss-node2`：HTTP `127.0.0.1:8084`，P2P 宿主机端口 `18003`

启动：

```bash
docker compose -f docker-compose.yml up --build -d
```

查看状态和日志：

```bash
docker compose -f docker-compose.yml ps
docker compose -f docker-compose.yml logs -f tss-manager
docker compose -f docker-compose.yml logs -f tss-node0
docker compose -f docker-compose.yml logs -f tss-node1
docker compose -f docker-compose.yml logs -f tss-node2
```

检查健康状态和 metrics：

```bash
curl -i http://127.0.0.1:8080/ping
curl http://127.0.0.1:8080/api/v1/metrics
curl http://127.0.0.1:8082/metrics
curl http://127.0.0.1:8083/metrics
curl http://127.0.0.1:8084/metrics
```

停止：

```bash
docker compose -f docker-compose.yml down
```

需要重新生成干净 keygen 状态时，清理本地测试数据：

```bash
rm -rf data/manager data/node0 data/node1 data/node2
```

## Manager HTTP API

### `GET /ping`

Manager 健康检查。服务正常时返回 `200 OK`。

```bash
curl -i http://127.0.0.1:8080/ping
```

### `POST /api/v1/keygen`

发起门限 keygen，成功后返回 plain text 格式的 cluster public key。

```bash
curl -sS \
  -X POST http://127.0.0.1:8080/api/v1/keygen \
  -H 'Content-Type: application/json' \
  --data @doc/test/keygen_request.json
```

示例请求：

```json
{
  "nodes": [
    "02fe2481a5431f9523286da7fcc2c1911cfede9cb038a27d24482080d46cf4e50c",
    "024695a4bfcaa11fa8428c0fae9ce9dc16093fb95adac3be949d13dd89b845e56a",
    "02dc8d79e4a67f1fdcad4b43f93d8f98feb4fc39c1113f64d491df1b68f93d2e17"
  ],
  "election_id": 10001,
  "request_id": "keygen-10001",
  "threshold": 1,
  "timestamp": 1750000000000
}
```

对于 3 个节点：

- `threshold = 1` 表示 `2-of-3`
- `threshold = 2` 表示 `3-of-3`

### `POST /api/v1/sign`

使用已生成的 committee 对 32 字节 digest 发起签名。`election_id` 必须与 keygen 请求一致。

```bash
curl -sS \
  -X POST http://127.0.0.1:8080/api/v1/sign \
  -H 'Content-Type: application/json' \
  --data @doc/test/sign_request.json
```

示例请求：

```json
{
  "message_hash": "4d7a957fa90c86fed223e3aa999009d2ff6e4b16ed7c4f8a76ad4520668236a7",
  "election_id": 10001,
  "request_id": "sign-10001"
}
```

### `GET /api/v1/metrics`

Manager Prometheus metrics。

```bash
curl http://127.0.0.1:8080/api/v1/metrics
```

## 配置

配置由 Viper 加载，优先级从低到高：

1. `config.DefaultConfiguration()` 中的默认值
2. `-c, --config` 指定的 TOML 配置文件
3. `TSS_` 前缀的环境变量

环境变量会把配置 key 里的点号和横线转换成下划线。例如：

```bash
TSS_MANAGER_HTTP_ADDR=0.0.0.0:8080
TSS_NODE_P2P_PORT=8001
```

Manager 关键配置：

- `manager.ws_addr`：Manager WebSocket 监听地址，Node 会连接这里
- `manager.http_addr`：Manager 对外 HTTP API 地址
- `manager.nodes`：期望参与的 Node 公钥列表
- `manager.db_dir`：Manager LevelDB 存储目录
- `manager.keygen_timeout`、`manager.ask_timeout`、`manager.sign_timeout`：协调流程超时

Node 关键配置：

- `node.private_key`：Node secp256k1 私钥
- `node.ws_addr`：Manager WebSocket 地址
- `node.http_addr`：Node HTTP/metrics 监听地址
- `node.db_dir`：Node LevelDB 存储目录
- `node.base_dir`：tss-lib local state 和 pre-parameter 目录
- `node.p2p_port`：libp2p 监听端口
- `node.bootstrap_peers`：逗号分隔的 libp2p multiaddr 列表
- `node.external_ip`：对其他节点广播的 IP
- `node.key_gen_timeout`、`node.key_sign_timeout`、`node.pre_param_timeout`：协议执行超时

不要提交生产私钥、JWT secret、AWS 凭证或真实客户状态数据。


