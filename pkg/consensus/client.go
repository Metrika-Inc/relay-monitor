package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/protolambda/eth2api"
	"github.com/protolambda/eth2api/client/beaconapi"
	"github.com/protolambda/eth2api/client/validatorapi"
	"github.com/protolambda/zrnt/eth2/beacon/bellatrix"
	"github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/r3labs/sse/v2"
	"github.com/ralexstokes/relay-monitor/pkg/types"
	"go.uber.org/zap"
)

const (
	clientTimeoutSec = 30
	cacheSize        = 128
)

type ValidatorInfo struct {
	publicKey types.PublicKey
	index     types.ValidatorIndex
}

type Client struct {
	logger *zap.Logger
	client *eth2api.Eth2HttpClient

	// slot -> ValidatorInfo
	proposerCache *lru.Cache
	// slot -> Hash
	executionCache *lru.Cache
	// publicKey -> Validator
	validatorCache *lru.Cache
}

var (
	proposerCacheGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "relay_monitor_proposer_cache_length",
		Help: "The size of the proposer cache",
	})
	executionCacheGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "relay_monitor_execution_cache_length",
		Help: "The size of the execution cache",
	})
	validatorCacheGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "relay_monitor_validator_cache_length",
		Help: "The size of the validator cache",
	})
)

func NewClient(ctx context.Context, endpoint string, logger *zap.Logger, currentSlot types.Slot, currentEpoch types.Epoch, slotsPerEpoch uint64) (*Client, error) {
	httpClient := &eth2api.Eth2HttpClient{
		Addr: endpoint,
		Cli: &http.Client{
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 128,
			},
			Timeout: clientTimeoutSec * time.Second,
		},
		Codec: eth2api.JSONCodec{},
	}

	proposerCache, err := lru.New(cacheSize)
	if err != nil {
		return nil, err
	}
	proposerCacheGauge.Set(0.0)

	executionCache, err := lru.New(cacheSize)
	if err != nil {
		return nil, err
	}
	executionCacheGauge.Set(0.0)

	validatorCache, err := lru.New(cacheSize)
	if err != nil {
		return nil, err
	}
	validatorCacheGauge.Set(0.0)

	client := &Client{
		logger:         logger,
		client:         httpClient,
		proposerCache:  proposerCache,
		executionCache: executionCache,
		validatorCache: validatorCache,
	}

	err = client.loadCurrentContext(ctx, currentSlot, currentEpoch, slotsPerEpoch)
	if err != nil {
		logger := logger.Sugar()
		logger.Warn("could not load the current context from the consensus client")
	}

	return client, nil
}

func (c *Client) loadCurrentContext(ctx context.Context, currentSlot types.Slot, currentEpoch types.Epoch, slotsPerEpoch uint64) error {
	logger := c.logger.Sugar()

	var baseSlot uint64
	if currentSlot > slotsPerEpoch {
		baseSlot = currentSlot - slotsPerEpoch
	}

	for i := baseSlot; i < slotsPerEpoch; i++ {
		_, err := c.FetchExecutionHash(ctx, i)
		if err != nil {
			logger.Warnf("could not fetch latest execution hash for slot %d: %v", currentSlot, err)
		}
	}

	err := c.FetchProposers(ctx, currentEpoch)
	if err != nil {
		logger.Warnf("could not load consensus state for epoch %d: %v", currentEpoch, err)
	}

	nextEpoch := currentEpoch + 1
	err = c.FetchProposers(ctx, nextEpoch)
	if err != nil {
		logger.Warnf("could not load consensus state for epoch %d: %v", nextEpoch, err)
	}

	err = c.FetchValidators(ctx)
	if err != nil {
		logger.Warnf("could not load validators: %v", err)
	}

	return nil
}

func (c *Client) GetProposer(slot types.Slot) (*ValidatorInfo, error) {
	val, ok := c.proposerCache.Get(slot)
	if !ok {
		return nil, fmt.Errorf("could not find proposer for slot %d", slot)
	}
	validator, ok := val.(ValidatorInfo)
	if !ok {
		return nil, fmt.Errorf("internal: proposer cache contains an unexpected type %T", val)
	}
	return &validator, nil
}

func (c *Client) GetExecutionHash(slot types.Slot) (types.Hash, error) {
	val, ok := c.executionCache.Get(slot)
	if !ok {
		return types.Hash{}, fmt.Errorf("could not find execution hash for slot %d", slot)
	}
	hash, ok := val.(types.Hash)
	if !ok {
		return types.Hash{}, fmt.Errorf("internal: execution cache contains an unexpected type %T", val)
	}
	return hash, nil
}

func (c *Client) GetValidator(publicKey *types.PublicKey) (*eth2api.ValidatorResponse, error) {
	val, ok := c.validatorCache.Get(publicKey)
	if !ok {
		return nil, fmt.Errorf("missing validator entry for public key %s", publicKey)
	}
	validator, ok := val.(eth2api.ValidatorResponse)
	if !ok {
		return nil, fmt.Errorf("internal: validator cache contains an unexpected type %T", val)
	}
	return &validator, nil
}

func (c *Client) GetParentHash(ctx context.Context, slot types.Slot) (types.Hash, error) {
	targetSlot := slot - 1
	parentHash, err := c.GetExecutionHash(targetSlot)
	if err != nil {
		return c.FetchExecutionHash(ctx, targetSlot)
	}
	return parentHash, nil
}

func (c *Client) GetProposerPublicKey(ctx context.Context, slot types.Slot) (*types.PublicKey, error) {
	validator, err := c.GetProposer(slot)
	if err != nil {
		// TODO consider fallback to grab the assignments for the missing epoch...
		return nil, fmt.Errorf("missing proposer for slot %d: %v", slot, err)
	}
	return &validator.publicKey, nil
}

func (c *Client) FetchProposers(ctx context.Context, epoch types.Epoch) error {
	var proposerDuties eth2api.DependentProposerDuty
	syncing, err := validatorapi.ProposerDuties(ctx, c.client, common.Epoch(epoch), &proposerDuties)
	if syncing {
		return fmt.Errorf("could not fetch proposal duties in epoch %d because node is syncing", epoch)
	} else if err != nil {
		return err
	}

	// TODO handle reorgs, etc.
	for _, duty := range proposerDuties.Data {
		c.proposerCache.Add(uint64(duty.Slot), ValidatorInfo{
			publicKey: types.PublicKey(duty.Pubkey),
			index:     uint64(duty.ValidatorIndex),
		})
		proposerCacheGauge.Add(1.0)
	}

	return nil
}

func (c *Client) backFillExecutionHash(slot types.Slot) (types.Hash, error) {
	for i := slot; i > 0; i-- {
		targetSlot := i - 1
		executionHash, err := c.GetExecutionHash(targetSlot)
		if err == nil {
			for i := targetSlot; i < slot; i++ {
				c.executionCache.Add(i+1, executionHash)
				executionCacheGauge.Add(1.0)
			}
			return executionHash, nil
		}
	}
	return types.Hash{}, fmt.Errorf("no execution hashes present before %d (inclusive)", slot)
}

func (c *Client) FetchExecutionHash(ctx context.Context, slot types.Slot) (types.Hash, error) {
	// TODO handle reorgs, etc.
	executionHash, err := c.GetExecutionHash(slot)
	if err == nil {
		return executionHash, nil
	}

	blockID := eth2api.BlockIdSlot(slot)

	var signedBeaconBlock eth2api.VersionedSignedBeaconBlock
	exists, err := beaconapi.BlockV2(ctx, c.client, blockID, &signedBeaconBlock)
	if !exists {
		// TODO move search to `GetParentHash`
		// TODO also instantiate with first execution hash...
		return c.backFillExecutionHash(slot)
	} else if err != nil {
		return types.Hash{}, err
	}

	bellatrixBlock, ok := signedBeaconBlock.Data.(*bellatrix.SignedBeaconBlock)
	if !ok {
		return types.Hash{}, fmt.Errorf("could not parse block %s", signedBeaconBlock)
	}
	executionHash = types.Hash(bellatrixBlock.Message.Body.ExecutionPayload.BlockHash)

	// TODO handle reorgs, etc.
	c.executionCache.Add(slot, executionHash)
	executionCacheGauge.Add(1.0)

	return executionHash, nil
}

type headEvent struct {
	Slot  string     `json:"slot"`
	Block types.Root `json:"block"`
}

func (c *Client) StreamHeads(ctx context.Context) <-chan types.Coordinate {
	logger := c.logger.Sugar()

	sseClient := sse.NewClient(c.client.Addr + "/eth/v1/events?topics=head")
	ch := make(chan types.Coordinate, 1)
	go func() {
		err := sseClient.SubscribeRawWithContext(ctx, func(msg *sse.Event) {
			var event headEvent
			err := json.Unmarshal(msg.Data, &event)
			if err != nil {
				logger.Warnf("could not unmarshal `head` node event: %v", err)
				return
			}
			slot, err := strconv.Atoi(event.Slot)
			if err != nil {
				logger.Warnf("could not unmarshal slot from `head` node event: %v", err)
				return
			}
			head := types.Coordinate{
				Slot: types.Slot(slot),
				Root: event.Block,
			}
			ch <- head
		})
		if err != nil {
			logger.Errorw("could not subscribe to head event", "error", err)
		}
	}()
	return ch
}

// TODO handle reorgs
func (c *Client) FetchValidators(ctx context.Context) error {
	var response []eth2api.ValidatorResponse
	exists, err := beaconapi.StateValidators(ctx, c.client, eth2api.StateHead, nil, nil, &response)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("could not fetch validators from remote endpoint because they do not exist")
	}

	for _, validator := range response {
		publicKey := validator.Validator.Pubkey
		c.validatorCache.Add(publicKey, validator)
		validatorCacheGauge.Add(1.0)
	}

	return nil
}

func (c *Client) GetValidatorStatus(publicKey *types.PublicKey) (ValidatorStatus, error) {
	validator, err := c.GetValidator(publicKey)
	if err != nil {
		return StatusValidatorUnknown, err
	}
	validatorStatus := string(validator.Status)
	if strings.Contains(validatorStatus, "active") {
		return StatusValidatorActive, nil
	} else if strings.Contains(validatorStatus, "pending") {
		return StatusValidatorPending, nil
	} else {
		return StatusValidatorUnknown, nil
	}
}
