package store

import "github.com/ethereum/go-ethereum/common"

var (
	SignedDigestKeyPrefix      = []byte{0x02}
	SigningInfoKeyPrefix       = []byte{0x04}
	ScannedHeightKeyPrefix     = []byte{0x07}
	CulpritsKeyPrefix          = []byte{0x08}
	TssInactiveMemberKeyPrefix = []byte{0x09}
	TssActiveMemberKeyPrefix   = []byte{0x10}
)

func getSignedDigestKey(digest [32]byte) []byte {
	return append(SignedDigestKeyPrefix, digest[:]...)
}

func getSigningInfoKey(address common.Address) []byte {
	return append(SigningInfoKeyPrefix, address.Bytes()...)
}

func getScannedHeightKey() []byte {
	return ScannedHeightKeyPrefix
}

func getCulpritsKey() []byte {
	return CulpritsKeyPrefix
}

func getInactiveMemberKey() []byte {
	return TssInactiveMemberKeyPrefix
}

func getActiveMemberKey() []byte {
	return TssActiveMemberKeyPrefix
}
