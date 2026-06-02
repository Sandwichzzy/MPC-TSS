package message

type IMessageStore interface {
	IMessageHashStore
}

type IMessageHashStore interface {
	SetMessageHash(MessageHashInfo) error
	GetMessageHash([32]byte) (bool, MessageHashInfo)
	IndexMessageHash(uint64, [32]byte) error
	GetIndexMessageHash(index uint64) (bool, [32]byte)
}

type MessageHashInfo struct {
	MessageHash  string   `json:"message_hash"`
	ElectionId   uint64   `json:"election_id"`
	AbsentNodes  []string `json:"absent_nodes"`
	WorkingNodes []string `json:"working_nodes"`
}
