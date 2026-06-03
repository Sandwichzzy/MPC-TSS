package store

import (
	"encoding/hex"
	"encoding/json"

	"github.com/Sandwichzzy/tss/message"
)

// --------batchRoot → metadata----------------

// SetMessageHash stores the full message-hash metadata under the hash-derived key.
func (s *Storage) SetMessageHash(info message.MessageHashInfo) error {
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
	return s.db.Put(getMessageHashKey(root), bz, nil)
}

// GetMessageHash loads the message-hash metadata by batch root.
func (s *Storage) GetMessageHash(root [32]byte) (bool, message.MessageHashInfo) {
	bz, err := s.db.Get(getMessageHashKey(root), nil)
	if err != nil {
		return handleError2(message.MessageHashInfo{}, err)
	}
	var stateBatchInfo message.MessageHashInfo
	if err := json.Unmarshal(bz, &stateBatchInfo); err != nil {
		return handleError2(message.MessageHashInfo{}, err)
	}
	return true, stateBatchInfo
}

//-------------batchIndex → batchRoot---------------------------------

// IndexMessageHash stores a lookup entry from batch index to batch root.
func (s *Storage) IndexMessageHash(index uint64, root [32]byte) error {
	return s.db.Put(getIndexMessageHashKey(index), root[:], nil)
}

// GetIndexMessageHash resolves a batch root from its sequential index.
func (s *Storage) GetIndexMessageHash(index uint64) (bool, [32]byte) {
	bz, err := s.db.Get(getIndexMessageHashKey(index), nil)
	if err != nil {
		return handleError2([32]byte{}, err)
	}
	var stateRoot [32]byte
	// Copy into a fixed-size array because LevelDB returns a slice: []byte
	copy(stateRoot[:], bz)
	return true, stateRoot
}
