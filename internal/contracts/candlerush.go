package contracts

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const candleRushABI = `[
	{
		"type": "function",
		"name": "placeBet",
		"stateMutability": "nonpayable",
		"inputs": [
			{
				"name": "input",
				"type": "tuple",
				"components": [
					{"name": "recipient", "type": "address"},
					{"name": "asset", "type": "address"},
					{"name": "intervalSeconds", "type": "uint32"},
					{"name": "openTime", "type": "uint64"},
					{"name": "side", "type": "uint8"},
					{"name": "tokenIn", "type": "address"},
					{"name": "amountIn", "type": "uint96"},
					{"name": "broker", "type": "uint24"}
				]
			}
		],
		"outputs": []
	},
	{
		"type": "function",
		"name": "batchPlaceBet",
		"stateMutability": "nonpayable",
		"inputs": [
			{
				"name": "inputs",
				"type": "tuple[]",
				"components": [
					{"name": "recipient", "type": "address"},
					{"name": "asset", "type": "address"},
					{"name": "intervalSeconds", "type": "uint32"},
					{"name": "openTime", "type": "uint64"},
					{"name": "side", "type": "uint8"},
					{"name": "tokenIn", "type": "address"},
					{"name": "amountIn", "type": "uint96"},
					{"name": "broker", "type": "uint24"}
				]
			}
		],
		"outputs": []
	},
	{
		"type": "function",
		"name": "claim",
		"stateMutability": "nonpayable",
		"inputs": [
			{"name": "asset", "type": "address"},
			{"name": "intervalSeconds", "type": "uint32"},
			{"name": "openTime", "type": "uint64"},
			{"name": "betId", "type": "uint256"}
		],
		"outputs": []
	},
	{
		"type": "function",
		"name": "batchClaim",
		"stateMutability": "nonpayable",
		"inputs": [
			{
				"name": "inputs",
				"type": "tuple[]",
				"components": [
					{"name": "asset", "type": "address"},
					{"name": "intervalSeconds", "type": "uint32"},
					{"name": "openTime", "type": "uint64"},
					{"name": "betId", "type": "uint256"}
				]
			}
		],
		"outputs": []
	}
]`

// CandleRushSide represents the bet side (UP=Green=0, DOWN=Red=1)
type CandleRushSide uint8

const (
	CandleRushSideUp   CandleRushSide = 0 // Green - betting candle will be green (close > open)
	CandleRushSideDown CandleRushSide = 1 // Red - betting candle will be red (close < open)
)

func (s CandleRushSide) String() string {
	if s == CandleRushSideUp {
		return "GREEN"
	}
	return "RED"
}

// CandleRushInterval represents the candle interval in seconds
type CandleRushInterval uint32

const (
	CandleRushInterval5m  CandleRushInterval = 300  // 5 minutes
	CandleRushInterval15m CandleRushInterval = 900  // 15 minutes
	CandleRushInterval30m CandleRushInterval = 1800 // 30 minutes
)

func (i CandleRushInterval) String() string {
	switch i {
	case CandleRushInterval5m:
		return "5m"
	case CandleRushInterval15m:
		return "15m"
	case CandleRushInterval30m:
		return "30m"
	default:
		return fmt.Sprintf("%ds", i)
	}
}

// CandleRushBetInput represents the input for placing a candle rush bet
type CandleRushBetInput struct {
	Recipient       common.Address // Who receives the winnings
	Asset           common.Address // The asset being bet on (e.g., BTC address)
	IntervalSeconds uint32         // Candle interval (300, 900, 1800)
	OpenTime        uint64         // Unix timestamp of candle open time
	Side            uint8          // 0=UP/Green, 1=DOWN/Red
	TokenIn         common.Address // Payment token (USDC)
	AmountIn        *big.Int       // Bet amount in token base units
	Broker          uint32         // Broker ID
}

// CandleRushClaimInput represents the input for claiming a bet
type CandleRushClaimInput struct {
	Asset           common.Address
	IntervalSeconds uint32
	OpenTime        uint64
	BetId           *big.Int
}

// CandleRush is the contract wrapper for Candle Rush betting
type CandleRush struct {
	address  common.Address
	client   *ethclient.Client
	abi      abi.ABI
	contract *bind.BoundContract
}

// NewCandleRush creates a new Candle Rush contract wrapper
func NewCandleRush(address common.Address, client *ethclient.Client) (*CandleRush, error) {
	parsed, err := abi.JSON(strings.NewReader(candleRushABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse CandleRush ABI: %w", err)
	}

	contract := bind.NewBoundContract(address, parsed, client, client, client)

	return &CandleRush{
		address:  address,
		client:   client,
		abi:      parsed,
		contract: contract,
	}, nil
}

// PlaceBet places a single candle rush bet
func (c *CandleRush) PlaceBet(ctx context.Context, opts *bind.TransactOpts, input CandleRushBetInput) (*types.Transaction, error) {
	packedInput := struct {
		Recipient       common.Address
		Asset           common.Address
		IntervalSeconds uint32
		OpenTime        uint64
		Side            uint8
		TokenIn         common.Address
		AmountIn        *big.Int
		Broker          *big.Int
	}{
		Recipient:       input.Recipient,
		Asset:           input.Asset,
		IntervalSeconds: input.IntervalSeconds,
		OpenTime:        input.OpenTime,
		Side:            input.Side,
		TokenIn:         input.TokenIn,
		AmountIn:        input.AmountIn,
		Broker:          big.NewInt(int64(input.Broker)),
	}

	return c.contract.Transact(opts, "placeBet", packedInput)
}

// BatchPlaceBet places multiple candle rush bets in a single transaction
func (c *CandleRush) BatchPlaceBet(ctx context.Context, opts *bind.TransactOpts, inputs []CandleRushBetInput) (*types.Transaction, error) {
	packedInputs := make([]struct {
		Recipient       common.Address
		Asset           common.Address
		IntervalSeconds uint32
		OpenTime        uint64
		Side            uint8
		TokenIn         common.Address
		AmountIn        *big.Int
		Broker          *big.Int
	}, len(inputs))

	for i, input := range inputs {
		packedInputs[i] = struct {
			Recipient       common.Address
			Asset           common.Address
			IntervalSeconds uint32
			OpenTime        uint64
			Side            uint8
			TokenIn         common.Address
			AmountIn        *big.Int
			Broker          *big.Int
		}{
			Recipient:       input.Recipient,
			Asset:           input.Asset,
			IntervalSeconds: input.IntervalSeconds,
			OpenTime:        input.OpenTime,
			Side:            input.Side,
			TokenIn:         input.TokenIn,
			AmountIn:        input.AmountIn,
			Broker:          big.NewInt(int64(input.Broker)),
		}
	}

	return c.contract.Transact(opts, "batchPlaceBet", packedInputs)
}

// Claim claims a single bet
func (c *CandleRush) Claim(ctx context.Context, opts *bind.TransactOpts, input CandleRushClaimInput) (*types.Transaction, error) {
	return c.contract.Transact(opts, "claim", input.Asset, input.IntervalSeconds, input.OpenTime, input.BetId)
}

// BatchClaim claims multiple bets in a single transaction
func (c *CandleRush) BatchClaim(ctx context.Context, opts *bind.TransactOpts, inputs []CandleRushClaimInput) (*types.Transaction, error) {
	packedInputs := make([]struct {
		Asset           common.Address
		IntervalSeconds uint32
		OpenTime        uint64
		BetId           *big.Int
	}, len(inputs))

	for i, input := range inputs {
		packedInputs[i] = struct {
			Asset           common.Address
			IntervalSeconds uint32
			OpenTime        uint64
			BetId           *big.Int
		}{
			Asset:           input.Asset,
			IntervalSeconds: input.IntervalSeconds,
			OpenTime:        input.OpenTime,
			BetId:           input.BetId,
		}
	}

	return c.contract.Transact(opts, "batchClaim", packedInputs)
}

// Address returns the contract address
func (c *CandleRush) Address() common.Address {
	return c.address
}
