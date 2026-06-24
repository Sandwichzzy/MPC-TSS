package message

type IMessageStore interface {
	ISignedDigestStore
}

type ISignedDigestStore interface {
	SetSignedDigest(SignedDigestInfo) error
	GetSignedDigest([32]byte) (bool, SignedDigestInfo)
}

type SignedDigestInfo struct {
	MessageHash  string   `json:"message_hash"`
	ElectionId   uint64   `json:"election_id"`
	AbsentNodes  []string `json:"absent_nodes"`
	WorkingNodes []string `json:"working_nodes"`
}
