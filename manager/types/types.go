package types

import (
	"context"
	"time"
)

type CpkData struct {
	Cpk          string    `json:"cpk"`
	ElectionId   uint64    `json:"election_id"`
	CreationTime time.Time `json:"creation_time"`
}

type TssCommitteeInfo struct {
	ElectionId    uint64   `json:"election_id"`
	ClusterPubKey string   `json:"cluster_pub_key"`
	TssMembers    []string `json:"tss_members"`
	Threshold     int      `json:"threshold"`
}

// Context ---------------------------------------------
type Context struct {
	ctx            context.Context
	requestId      string
	tssInfo        *TssCommitteeInfo
	availableNodes []string
	approvers      []string
	unApprovers    []string
	electionId     uint64
	MessageHash    string
}
