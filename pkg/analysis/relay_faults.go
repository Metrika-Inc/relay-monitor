package analysis

import "github.com/ralexstokes/relay-monitor/pkg/types"

type FaultRecord = map[types.PublicKey]*Faults

type Faults struct {
	TotalBids uint `json:"total_bids"`

	ConsensusInvalidBids   uint `json:"consensus_invalid_bids"`
	IgnoredPreferencesBids uint `json:"ignored_preferences_bids"`

	PaymentInvalidBids       uint `json:"payment_invalid_bids"`
	MalformedPayloads        uint `json:"malformed_payloads"`
	ConsensusInvalidPayloads uint `json:"consensus_invalid_payloads"`
	UnavailablePayloads      uint `json:"unavailable_payloads"`
}
