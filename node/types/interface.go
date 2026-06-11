package types

import "github.com/Sandwichzzy/tss/message"

type TssMembers struct {
	TssMembers []string `json:"tss_members"`
}

type NodeStore interface {
	message.IMessageHashStore
	TssMemberStore
}

type TssMemberStore interface {
	SetInactiveMembers(TssMembers) error
	GetInactiveMembers() (TssMembers, error)
	SetActiveMembers(TssMembers) error
	GetActiveMembers() (TssMembers, error)
}
