package types

import (
	tss "github.com/Sandwichzzy/tss/common"

	"github.com/Sandwichzzy/tss/message"
)

type ISignService interface {
	TransactionSign(request tss.TransactionSignRequest) ([]byte, error)
	Keygen(request tss.KeygenRequest) (string, error)
}

type ICPKStore interface {
	Insert(data CpkData) error
	GetByElectionId(id uint64) (CpkData, error)
}

type IManagerStore interface {
	ICPKStore
	message.IMessageHashStore
}
