package store

import (
	"encoding/json"

	"github.com/Sandwichzzy/tss/manager/types"
)

// Insert stores CPK data keyed by election id.
func (s *Storage) Insert(cpkData types.CpkData) error {
	bz, err := json.Marshal(cpkData)
	if err != nil {
		return err
	}
	return s.db.Put(getCPKDataKey(cpkData.ElectionId), bz, nil)
}

// GetByElectionId retrieves the stored CPK data for a given election.
func (s *Storage) GetByElectionId(electionId uint64) (types.CpkData, error) {
	bz, err := s.db.Get(getCPKDataKey(electionId), nil)
	if err != nil {
		return handleError(types.CpkData{}, err)
	}
	var cpkData types.CpkData
	err = json.Unmarshal(bz, &cpkData)
	if err != nil {
		return handleError(types.CpkData{}, err)
	}
	return cpkData, nil
}
