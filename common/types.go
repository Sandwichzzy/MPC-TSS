package common

import "fmt"

/*
这里定义了 Manager 和 Node 之间传输的请求/响应结构。
*/
type Method string

// 请求/事件类型，用于区分 manager 和 node 的协议动作。
const (
	AskMessageHash  Method = "askMessageHash"
	TransactionSign Method = "TransactionSign"

	CulpritErrorCode = 100 // CulpritErrorCode 标记某个节点被判定为作恶时的错误码。
)

func (m Method) String() string {
	return string(m)
}

type TransactionSignRequest struct {
	MessageHash string `json:"message_hash"` // 待签名消息哈希。
	ElectionId  uint64 `json:"election_id"`  // 选举或任务编号。
	RequestId   string `json:"request_id"`   // 上层业务请求 ID，便于幂等和追踪。
}

func (tsr TransactionSignRequest) String() string {
	return fmt.Sprintf("message_hash: %v, election_id: %d", tsr.MessageHash, tsr.ElectionId)
}

type AskResponse struct {
	Result bool `json:"result"` // 结果为 true 表示节点接受该请求。
}

type NodeSignRequest struct {
	ClusterPublicKey string      `json:"cluster_public_key"` // 聚合后的公钥。
	Timestamp        int64       `json:"timestamp"`
	Nodes            []string    `json:"nodes"`
	RequestBody      interface{} `json:"request_body"` // 业务层原始请求体，透传给具体 signer。
}

type SignResponse struct {
	// 返回给上层的最终签名结果。
	Signature []byte `json:"signature"`
}

type KeygenRequest struct {
	Nodes      []string `json:"nodes"`
	ElectionId uint64   `json:"election_id"`
	RequestId  string   `json:"request_id"`
	Threshold  int      `json:"threshold"`
	Timestamp  int64    `json:"timestamp"`
}

type KeygenResponse struct {
	ClusterPublicKey string `json:"cluster_public_key"`
}

type SignatureData struct {
	// Recoverable signature 的恢复字段。
	SignatureRecovery []byte `json:"signature_recovery,omitempty"`
	// ECDSA 签名的 r 分量。
	R []byte `json:"r,omitempty"`
	// ECDSA 签名的 s 分量。
	S []byte `json:"s,omitempty"`
	// 原始消息哈希。
	M []byte `json:"m,omitempty"`
}

type DigestSignResponse struct {
	// 任意 32 字节 digest 的阈值签名结果。
	Signature []byte `json:"signature"`
}
