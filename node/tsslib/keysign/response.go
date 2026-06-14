package keysign

import (
	"github.com/Sandwichzzy/tss/node/tsslib/common"
	common2 "github.com/bnb-chain/tss-lib/common"
)

type Response struct {
	SignatureData *common2.SignatureData `json:"signature_data"`
	Status        common.Status          `json:"status"`
	FailReason    string                 `json:"failReason"`
}

func NewResponse(signature *common2.SignatureData, status common.Status, failReason string) Response {
	return Response{
		SignatureData: signature,
		Status:        status,
		FailReason:    failReason,
	}
}
