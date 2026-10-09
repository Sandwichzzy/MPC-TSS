# Go 后端开发工程师｜MPC 钱包 / TSS 方向

## 项目：MPC Threshold Signature Service（TSS）

## 项目简介

参与企业级 MPC 钱包阈值签名服务的设计与开发，基于 Go、bnb-chain/tss-lib、libp2p、WebSocket 等技术实现分布式密钥生成 KeyGen 与阈值签名 KeySign 能力。系统通过多节点协同完成 ECDSA 阈值签名流程，私钥份额仅保存在各参与节点本地或受控托管存储中，完整私钥在任何阶段均不落地，为数字资产托管、钱包签名和多方审批场景提供安全的分布式签名基础设施。

## 工作内容

1. 负责 TSS 核心协议服务开发，基于 bnb-chain/tss-lib 封装 ECDSA 多方计算能力，实现 KeyGen、KeySign 等核心流程，将底层本地 Party、round message、end channel 等协议细节抽象为可被业务系统调用的签名服务接口。
2. 设计并实现 Manager 与 Node 双层架构：Manager 负责节点连接管理、任务分发、审批节点路由、签名结果验签及 HTTP API 暴露；Node 负责本地 TSS Party 执行、协议消息收发、本地份额状态管理及签名结果生成，清晰拆分控制面与协议面职责。
3. 实现 TSS Session 生命周期管理，围绕 msgID、消息类型和参与方集合构建独立任务上下文，支持任务创建、订阅注册、状态流转、超时控制、资源释放与并发隔离，避免多轮 KeyGen / KeySign 任务之间消息串扰。
4. 封装节点间 P2P 协议通信层，基于 libp2p 实现节点发现、stream 建立、广播 Broadcast、单播 Unicast、消息订阅和异步发送队列，支撑 TSS 多轮协议消息在多节点间可靠传递。
5. 设计协议消息封装与认证机制，在 tss-lib 原始 wire message 外增加业务层 WrappedMessage / WireMessage 包装，携带 msgID、消息类型、round 信息、路由信息和节点签名；接收端完成消息反序列化、签名验签、发送方校验后再投递给本地 Party，降低跨任务重放和伪造消息风险。
6. 实现 KeyGen 本地状态管理，持久化 LocalPartySaveData、参与方公钥集合、聚合公钥、门限参数及预计算参数，支持后续 KeySign 按聚合公钥恢复本地私钥份额，并校验签名参与节点是否合法。
7. 负责 KeySign 签名流程开发，将业务签名请求转换为 TSS 签名任务，完成参与节点校验、本地份额加载、签名消息分发、最终 SignatureData 组装及 65 字节 ECDSA 签名适配，并在 Manager 层基于聚合公钥对签名结果进行二次验签。
8. 开发协议异常归责 Blame 机制，记录 TSS round 信息、最近一次单播节点、异常 share、错误消息和签名证据，对无效消息、协议执行失败、超时及疑似恶意节点进行定位与归责，提升多节点签名场景下的问题排查和容错能力。
9. 实现任务结果、节点关系与协议状态的持久化能力，基于 LevelDB 存储签名摘要、聚合公钥、TSS 成员关系和节点状态，基于本地文件保存 KeyGen 本地状态、预计算参数和 P2P 地址簿，并扩展 AWS Secrets Manager 托管存储、Shamir + KMS + S3/Secrets Manager 分片托管两种私钥份额保护方案，支持重复请求识别、签名结果查询、幂等处理和节点重启后的状态恢复。
10. 完善工程化部署与调试能力，使用 Docker Compose 搭建 Manager + 多 Node 本地联调环境，编写多节点配置文件和测试请求样例，完成 KeyGen、KeySign、节点断连、超时、异常消息等场景验证，提高开发、联调和故障定位效率。
11. 接入 Prometheus 指标与结构化日志，补充关键链路日志，包括任务创建、节点连接、消息收发、签名成功、验签失败、超时退出和异常归责结果，提升 TSS 服务在多节点协作场景下的可观测性。

## 项目亮点

1. 将 tss-lib 的底层密码学状态机封装为可独立部署的分布式签名服务，补齐网络通信、消息认证、状态管理、任务隔离和异常归责等工程能力。
2. 采用控制面与协议面分离架构，Manager 专注业务请求编排和签名结果校验，Node 专注 TSS 协议执行和本地份额保护，整体职责边界清晰，便于扩展和排障。
3. 基于 msgID + MessageType 实现多任务并发隔离，结合订阅路由和超时释放机制，保证 KeyGen / KeySign 多轮协议消息不会跨任务污染。
4. 对节点间协议消息增加签名验签和 round 解析能力，在消息进入 tss-lib 前完成来源校验、完整性校验和异常记录，提高协议通信安全性。
5. 支持本地状态恢复和签名结果验证，KeyGen 生成的本地份额可被后续 KeySign 复用，Manager 对最终签名进行聚合公钥验签，避免盲信单个节点返回结果。
6. 设计多模式私钥份额存储方案，支持本地文件、AWS Secrets Manager 整体托管，以及 Shamir 分片后结合 KMS 加密并分散存储到 S3 / Secrets Manager 的高安全托管模式，在保证私钥份额不集中暴露的同时提升节点重启恢复和生产环境密钥托管能力。

## 技术栈

Go、bnb-chain/tss-lib、ECDSA Threshold Signature、Shamir Secret Sharing、libp2p、WebSocket、JSON-RPC、LevelDB、AWS Secrets Manager、AWS KMS、AWS S3、Docker Compose、Prometheus、Cobra、go-ethereum crypto

## 简历精简版

### MPC Threshold Signature Service（TSS）｜Go 后端开发工程师

负责企业级 MPC 钱包阈值签名服务开发，基于 Go、bnb-chain/tss-lib、libp2p 和 WebSocket 实现 ECDSA 分布式密钥生成 KeyGen 与阈值签名 KeySign，为数字资产托管和钱包系统提供私钥不落地的多方签名能力。

- 基于 bnb-chain/tss-lib 封装 TSS 协议执行流程，实现 KeyGen、KeySign、本地 Party 创建、round message 处理、签名结果组装及业务接口适配。
- 设计 Manager / Node 双层架构，Manager 负责任务分发、节点路由和签名结果验签，Node 负责本地 TSS 协议执行、P2P 消息通信和私钥份额状态管理。
- 实现 TSS Session 生命周期管理，基于 msgID + MessageType 建立独立任务上下文，支持任务订阅、状态流转、超时控制、资源释放和多任务并发隔离。
- 基于 libp2p 实现节点间广播、单播、stream 管理和异步消息队列，支撑 TSS 多轮协议消息在多节点间可靠传输。
- 设计协议消息封装与认证机制，在原始 tss-lib wire message 外增加路由信息、round 信息、任务 ID 和节点签名，接收端完成验签与反序列化后再投递给本地 Party。
- 实现 KeyGen 本地状态持久化，保存聚合公钥、本地私钥份额、参与节点集合、门限参数和预计算参数，支持 KeySign 阶段按聚合公钥恢复签名状态。
- 开发异常归责 Blame 机制，记录 round、异常 share、最近单播节点和错误消息，对无效协议消息、超时和疑似恶意节点进行定位，提高故障排查效率。
- 基于 LevelDB / 本地文件实现签名摘要、TSS 成员关系、协议状态和 KeyGen 本地份额持久化，并扩展 AWS Secrets Manager 托管存储、Shamir + KMS + S3/Secrets Manager 分片托管两种私钥份额保护方案，支持重复请求识别、幂等处理和节点重启后的状态恢复。
- 使用 Docker Compose 搭建 Manager + 多 Node 本地测试环境，完成 KeyGen、KeySign、节点异常、超时和签名验签等场景联调验证。
