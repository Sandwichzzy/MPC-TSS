package keysign

type Request struct {
	// PoolPubKey 指定要使用哪一把 TSS 聚合公钥来签名。
	// 服务端会据此加载对应的 keygen 本地状态，并校验 signer 是否属于该密钥的参与者集合。
	PoolPubKey string `json:"pool_pub_key"`
	Message    []byte `json:"message"` // Message 是待签名的消息字节。
	// SignerPubKeys 是本轮实际参与签名的节点公钥列表。
	// 它必须是该 PoolPubKey 对应参与者集合的子集，同时会用于在线检查和生成本轮 msgID。
	SignerPubKeys []string `json:"signer_pub_keys"`
}

func NewRequest(pk string, msg []byte, signers []string) Request {
	return Request{
		PoolPubKey:    pk,
		Message:       msg,
		SignerPubKeys: signers,
	}
}
