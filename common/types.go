package common

type Method string

const (
	AskMessageHash  Method = "askMessageHash"
	TransactionSign Method = "TransactionSign"
	SignSlash       Method = "signSlash"

	CulpritErrorCode = 100
)
