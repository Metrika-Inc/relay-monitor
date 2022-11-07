package data

import (
	"time"

	"github.com/Metrika-Inc/relay-monitor/pkg/types"
)

type Event struct {
	Payload any
}

type BidEvent struct {
	Context *types.BidContext
	Bid     *types.Bid
}

type ValidatorRegistrationEvent struct {
	Registrations []types.SignedValidatorRegistration
}

type AuctionTranscriptEvent struct {
	Transcript *types.AuctionTranscript
}

type Output struct {
	Timestamp time.Time
	Rtt       uint64
	Relay     string
	Bid       BidEvent
}
