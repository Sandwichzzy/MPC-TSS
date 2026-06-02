package types

import tss "github.com/Sandwichzzy/tss/common"

type ISignService interface {
	TransactionSign(request tss.TransactionSignRequest) ([]byte, error)
	Keygen(request tss.KeygenRequest) (string, error)
}

type ManagerStore interface {
	CPKStore
	message.MessageHashStore
}
