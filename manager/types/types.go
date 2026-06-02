package types

import "time"

type CpkData struct {
	Cpk          string    `json:"cpk"`
	ElectionId   uint64    `json:"election_id"`
	CreationTime time.Time `json:"creation_time"`
}
