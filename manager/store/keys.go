package store

import (
	"encoding/binary"

	"github.com/ethereum/go-ethereum/common"
)

// 用 byte prefix + 二进制编码，把 KV 存储“手动设计成结构化数据库”，并支持高效按前缀和排序查询
var (
	CPKDataKeyPrefix       = []byte{0x01}
	SignedDigestKeyPrefix  = []byte{0x02}
	SigningInfoKeyPrefix   = []byte{0x04}
	ScannedHeightKeyPrefix = []byte{0x07}
	CulpritsKeyPrefix      = []byte{0x08}
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

// getSignedDigestKey builds the key layout: prefix + 32-byte digest.
// 0x02 is kept for compatibility with existing signed digest records.
func getSignedDigestKey(digest [32]byte) []byte {
	return append(SignedDigestKeyPrefix, digest[:]...)
}

// getSigningInfoKey builds the key layout: prefix + signer address.
// 0x04 + address(20 bytes)
// 存某个节点/账户的签名信息
func getSigningInfoKey(address common.Address) []byte {
	return append(SigningInfoKeyPrefix, address.Bytes()...)
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
