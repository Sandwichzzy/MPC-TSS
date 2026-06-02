package manager

import (
	"time"

	"github.com/Sandwichzzy/tss/manager/metrics"
)

type Manager struct {
	store                 types.ManagerStore
	tssMembers            []string
	taskInterval          time.Duration
	confirmReceiptTimeout time.Duration
	keygenTimeout         time.Duration
	cpkConfirmTimeout     time.Duration
	askTimeout            time.Duration
	signTimeout           time.Duration
	stopGenKey            bool
	metrics               *metrics.Metrics
}
