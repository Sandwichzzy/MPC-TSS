package store

import (
	"encoding/hex"
	"encoding/json"

	"github.com/Sandwichzzy/tss/message"
)

// --------digest → metadata----------------

// SetSignedDigest stores signed digest metadata under the digest-derived key.
func (s *Storage) SetSignedDigest(info message.SignedDigestInfo) error {
	bz, err := json.Marshal(info)
	if err != nil {
		return err
	}
	msgByte, err := hex.DecodeString(info.MessageHash)
	if err != nil {
		return err
	}
	var root [32]byte
	copy(root[:], msgByte)
	return s.db.Put(getSignedDigestKey(root), bz, nil)
}

// GetSignedDigest loads signed digest metadata by the 32-byte digest.
func (s *Storage) GetSignedDigest(root [32]byte) (bool, message.SignedDigestInfo) {
	bz, err := s.db.Get(getSignedDigestKey(root), nil)
	if err != nil {
		return handleError2(message.SignedDigestInfo{}, err)
	}
	var signedDigestInfo message.SignedDigestInfo
	if err := json.Unmarshal(bz, &signedDigestInfo); err != nil {
		return handleError2(message.SignedDigestInfo{}, err)
	}
	return true, signedDigestInfo
}
