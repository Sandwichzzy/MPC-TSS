package keygen

import (
	"github.com/Sandwichzzy/tss/node/tsslib/common"
)

type Response struct {
	PubKey          string        `json:"pubKey"`            // PubKey 是 keygen 产出的聚合公钥，十六进制压缩格式。 上层会把它作为后续 keysign 请求里的 pool_pub_key
	Address         []byte        `json:"address"`           // Address 是根据聚合公钥推导出的地址原始字节。 当前实现使用的是 EVM 地址格式的 20 字节结果。
	PubKeyByte      []byte        `json:"pubKey_byte"`       // PubKeyByte 是未压缩公钥去掉 0x04 前缀后的原始字节，即 X||Y。
	Status          common.Status `json:"status"`            // Status 表示本轮 keygen 是否成功，取值来自 common.Success/common.Fail。
	FailReason      string        `json:"fail_reason"`       // FailReason 是失败原因文本；成功时通常为空字符串。
	AbnormalPubKeys []string      `json:"abnormal_pub_keys"` // AbnormalPubKeys 记录被异常检测模块归因出来的异常节点公钥。
}

func NewResponse(pubkey string, address, pubkeyByte []byte, status common.Status, failReason string, abnormalPubkeys []string) Response {
	return Response{
		PubKey:          pubkey,
		Address:         address,
		PubKeyByte:      pubkeyByte,
		Status:          status,
		FailReason:      failReason,
		AbnormalPubKeys: abnormalPubkeys,
	}
}
