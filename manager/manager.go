package manager

import (
	"time"

	"github.com/Sandwichzzy/tss/manager/metrics"
	"github.com/Sandwichzzy/tss/manager/types"
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
	stopChan              chan struct{}
	metrics               *metrics.Metrics
}
