# TSS

[简体中文](README.md) | [English](README.en.md)

A Go-based MPC Threshold Signature Scheme service. The project consists of one coordinator and multiple signing nodes:

- `manager`: Provides an HTTP API, accepts WebSocket connections from nodes, and coordinates key generation and signing.
- `node`: Connects to the manager, runs local HTTP and metrics services, persists local state, and executes TSS protocol rounds over libp2p.
- `parse-peer-id`: A helper command that derives a libp2p peer ID from a secp256k1 public or private key.

The underlying threshold ECDSA protocol uses `github.com/bnb-chain/tss-lib`.

## Project structure

```text
.
├── cmd.go                    # Cobra entry point: tss manager/node/parse-peer-id
├── manager/                  # Manager service, HTTP routes, coordination, LevelDB storage
├── node/                     # Node service, signing, HTTP service, storage, tsslib integration
├── ws/                       # Manager WebSocket server and Node WebSocket client
├── common/                   # Shared request/response types and utilities
├── config/                   # TOML/env configuration loading
├── doc/                      # Protocol notes and example test requests
├── docker-compose.yml        # Local 1 manager + 3 node test environment
├── tssmanage.docker.toml     # Example Docker Manager configuration
└── tssnode*.docker.toml      # Example Docker Node configurations
```

Related documentation:

- [Commands and execution flow](doc/cmd.md)
- [WebSocket structure](doc/ws_structure.md)
- [TSS protocol notes](doc/tsslib_protocol_explaination.md)

## Requirements

- Go `1.25.7`, or a compatible version matching `go.mod`
- `make`
- Docker and Docker Compose for local multi-node testing
- `golangci-lint` only when running `make lint`

The standard build enables CGO.

## Build and test

Prefer the Make targets:

```bash
make build
make test
make lint
```

Equivalent direct commands:

```bash
go build -v .
go test -v ./...
golangci-lint run ./...
```

`make build` embeds the git commit metadata through linker flags and builds the `tss` binary in the repository root.

## CLI usage

All subcommands share the root command's `-c, --config` option. Configuration is loaded before the subcommand runs.

```bash
go run . manager -c tssmanage.docker.toml
go run . node -c tssnode0.docker.toml --debug
go run . parse-peer-id --pri-key <hex-private-key>
go run . parse-peer-id --pub-key <hex-public-key>
```

Additional Node options:

- `--debug`, `-d`: Enable debug logging
- `--non-prod`: Enable non-production Node HTTP behavior
- `--full`: Wait for full peer connectivity before starting TSS

## Local Docker testing

The repository includes a minimal local cluster:

- `tss-manager`: HTTP on `127.0.0.1:8080`, WebSocket on port `8081`
- `tss-node0`: HTTP on `127.0.0.1:8082`, P2P host port `18001`
- `tss-node1`: HTTP on `127.0.0.1:8083`, P2P host port `18002`
- `tss-node2`: HTTP on `127.0.0.1:8084`, P2P host port `18003`

Start the cluster:

```bash
docker compose -f docker-compose.yml up --build -d
```

Check status and logs:

```bash
docker compose -f docker-compose.yml ps
docker compose -f docker-compose.yml logs -f tss-manager
docker compose -f docker-compose.yml logs -f tss-node0
docker compose -f docker-compose.yml logs -f tss-node1
docker compose -f docker-compose.yml logs -f tss-node2
```

Check health and metrics:

```bash
curl -i http://127.0.0.1:8080/ping
curl http://127.0.0.1:8080/api/v1/metrics
curl http://127.0.0.1:8082/metrics
curl http://127.0.0.1:8083/metrics
curl http://127.0.0.1:8084/metrics
```

Stop the cluster:

```bash
docker compose -f docker-compose.yml down
```

To start key generation with clean local state, remove the local test data:

```bash
rm -rf data/manager data/node0 data/node1 data/node2
```

## Manager HTTP API

### `GET /ping`

Manager health check. Returns `200 OK` when the service is healthy.

```bash
curl -i http://127.0.0.1:8080/ping
```

### `POST /api/v1/keygen`

Starts threshold key generation. On success, returns the cluster public key as plain text.

```bash
curl -sS \
  -X POST http://127.0.0.1:8080/api/v1/keygen \
  -H 'Content-Type: application/json' \
  --data @doc/test/keygen_request.json
```

Example request:

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

For three nodes:

- `threshold = 1` means `2-of-3`.
- `threshold = 2` means `3-of-3`.

### `POST /api/v1/sign`

Uses an existing committee to sign a 32-byte digest. `election_id` must match the key generation request.

```bash
curl -sS \
  -X POST http://127.0.0.1:8080/api/v1/sign \
  -H 'Content-Type: application/json' \
  --data @doc/test/sign_request.json
```

Example request:

```json
{
  "message_hash": "4d7a957fa90c86fed223e3aa999009d2ff6e4b16ed7c4f8a76ad4520668236a7",
  "election_id": 10001,
  "request_id": "sign-10001"
}
```

### `GET /api/v1/metrics`

Manager Prometheus metrics.

```bash
curl http://127.0.0.1:8080/api/v1/metrics
```

## Configuration

Viper loads configuration in ascending order of precedence:

1. Defaults from `config.DefaultConfiguration()`
2. The TOML file specified with `-c, --config`
3. Environment variables prefixed with `TSS_`

Dots and hyphens in configuration keys become underscores in environment variable names. For example:

```bash
TSS_MANAGER_HTTP_ADDR=0.0.0.0:8080
TSS_NODE_P2P_PORT=8001
```

Key Manager settings:

- `manager.ws_addr`: WebSocket listen address that nodes connect to
- `manager.http_addr`: Manager HTTP API address
- `manager.nodes`: Expected Node public keys
- `manager.db_dir`: Manager LevelDB directory
- `manager.keygen_timeout`, `manager.ask_timeout`, `manager.sign_timeout`: Coordination timeouts

Key Node settings:

- `node.private_key`: Node secp256k1 private key
- `node.ws_addr`: Manager WebSocket address
- `node.http_addr`: Node HTTP/metrics listen address
- `node.db_dir`: Node LevelDB directory
- `node.base_dir`: tss-lib local state and pre-parameter directory
- `node.p2p_port`: libp2p listen port
- `node.bootstrap_peers`: Comma-separated list of libp2p multiaddrs
- `node.external_ip`: IP announced to other nodes
- `node.key_gen_timeout`, `node.key_sign_timeout`, `node.pre_param_timeout`: Protocol execution timeouts

Do not commit production private keys, JWT secrets, AWS credentials, or real customer state data.
