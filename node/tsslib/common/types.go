package common

import "time"

type TssConfig struct {
	KeyGenTimeout   time.Duration // KeyGenTimeoutSeconds defines how long do we wait the keygen parties to pass messages along
	KeySignTimeout  time.Duration // KeySignTimeoutSeconds defines how long do we wait keysign
	PreParamTimeout time.Duration // Pre-parameter define the pre-parameter generations timeout
	EnableMonitor   bool          // enable the tss monitor
}
