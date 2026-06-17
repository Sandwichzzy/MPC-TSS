package keygen

type Request struct {
	Keys      []string `json:"keys"`       // Keys 是本次参与 keygen 的全部节点公钥列表。 TSS 会基于这组公钥构造 party 集合，并参与生成本轮请求唯一的 msgID。
	ThresHold int      `json:"thres_hold"` // ThresHold 是门限参数 t，不是参与人数本身。
}

func NewRequest(keys []string, threshold int) Request {
	return Request{
		Keys:      keys,
		ThresHold: threshold,
	}
}
