package store

import (
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
)

// 用 byte prefix + 二进制编码，把 KV 存储“手动设计成结构化数据库”，并支持高效按前缀和排序查询
var (
	CPKDataKeyPrefix                 = []byte{0x01}
	MessageHashKeyPrefix             = []byte{0x02}
	IndexMessageHashKeyPrefix        = []byte{0x03}
	SigningInfoKeyPrefix             = []byte{0x04}
	NodeMissedBatchBitArrayKeyPrefix = []byte{0x05}
	SlashingInfoKeyPrefix            = []byte{0x06}
	ScannedHeightKeyPrefix           = []byte{0x07}
	CulpritsKeyPrefix                = []byte{0x08}
)

// getCPKDataKey builds the key layout: prefix + electionId(big endian).
// 0x01 + electionId(8 bytes)
// electionId 对应某次选举的 CPK 数据
func getCPKDataKey(electionId uint64) []byte {
	electionIdBz := make([]byte, 8)
	// Big-endian encoding preserves numeric ordering in lexicographical scans.
	binary.BigEndian.PutUint64(electionIdBz, uint64(electionId))
	return append(CPKDataKeyPrefix, electionIdBz...)
}

// getMessageHashKey builds the key layout: prefix + 32-byte batch root.
// 0x02 + batchRoot(32 bytes)
// 存 batch root → message hash 的映射
func getMessageHashKey(batchRoot [32]byte) []byte {
	return append(MessageHashKeyPrefix, batchRoot[:]...)
}

// getIndexMessageHashKey builds the key layout: prefix + batch index(big endian).
// 0x03 + index(8 bytes)
// 按 index 查询 message hash
func getIndexMessageHashKey(index uint64) []byte {
	indexBz := make([]byte, 8)
	binary.BigEndian.PutUint64(indexBz, index)
	return append(IndexMessageHashKeyPrefix, indexBz...)
}

// getSigningInfoKey builds the key layout: prefix + signer address.
// 0x04 + address(20 bytes)
// 存某个节点/账户的签名信息
func getSigningInfoKey(address common.Address) []byte {
	return append(SigningInfoKeyPrefix, address.Bytes()...)
}

// getNodeMissedBatchBitArrayKey builds the key layout: prefix + address + missed-batch index.
// 0x05 + address + batchIndex(8 bytes)
// 记录某个节点在某个 batch 的“缺席情况”
func getNodeMissedBatchBitArrayKey(address common.Address, index uint64) []byte {
	indexBz := make([]byte, 8)
	binary.BigEndian.PutUint64(indexBz, index)
	return append(getNodeMissedBatchBitArrayAddressPrefixKey(address), indexBz...)
}

// getNodeMissedBatchBitArrayAddressPrefixKey builds the address-scoped prefix for missed-batch entries.
func getNodeMissedBatchBitArrayAddressPrefixKey(address common.Address) []byte {
	return append(NodeMissedBatchBitArrayKeyPrefix, address.Bytes()...)
}

// getSlashingInfoKey builds the key layout: prefix + address + batch index.
// 0x06 + address + batchIndex
// 记录某个节点在哪个 batch 被 slash（惩罚）
func getSlashingInfoKey(address common.Address, batchIndex uint64) []byte {
	indexBz := make([]byte, 8)
	binary.BigEndian.PutUint64(indexBz, batchIndex)
	return append(getSlashingInfoAddressKey(address), indexBz...)
}

// getSlashingInfoAddressKey builds the address-scoped prefix for slashing records.
func getSlashingInfoAddressKey(address common.Address) []byte {
	return append(SlashingInfoKeyPrefix, address.Bytes()...)
}

// getScannedHeightKey returns the singleton key used to persist the current scan height.
// 区块扫描进度（height checkpoint）
func getScannedHeightKey() []byte {
	return ScannedHeightKeyPrefix
}

// getCulpritsKey returns the singleton key used to persist culprit information.
// 存“作恶者列表”
func getCulpritsKey() []byte {
	return CulpritsKeyPrefix
}
