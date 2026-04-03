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

const diamondABI = `[
	{
		"type": "function",
		"name": "getPredictionConfig",
		"stateMutability": "view",
		"inputs": [],
		"outputs": [
			{
				"name": "",
				"type": "tuple",
				"components": [
					{"name": "minBetUsd", "type": "uint256"},
					{"name": "isActive", "type": "bool"},
					{"name": "autoSettle", "type": "bool"}
				]
			}
		]
	},
	{
		"type": "function",
		"name": "getTokenForPrediction",
		"stateMutability": "view",
		"inputs": [{"name": "tokenAddress", "type": "address"}],
		"outputs": [
			{
				"name": "",
				"type": "tuple",
				"components": [
					{"name": "token", "type": "address"},
					{"name": "switch", "type": "bool"},
					{"name": "decimals", "type": "uint8"},
					{"name": "price", "type": "uint256"}
				]
			}
		]
	},
	{
		"type": "function",
		"name": "predictAndBet",
		"stateMutability": "nonpayable",
		"inputs": [
			{
				"name": "pi",
				"type": "tuple",
				"components": [
					{"name": "recipient", "type": "address"},
					{"name": "predictionPairBase", "type": "address"},
					{"name": "isUp", "type": "bool"},
					{"name": "period", "type": "uint8"},
					{"name": "tokenIn", "type": "address"},
					{"name": "amountIn", "type": "uint96"},
					{"name": "price", "type": "uint64"},
					{"name": "broker", "type": "uint24"}
				]
			}
		],
		"outputs": []
	},
	{
		"type": "function",
		"name": "predictRelativeAndBet",
		"stateMutability": "nonpayable",
		"inputs": [
			{
				"name": "input",
				"type": "tuple",
				"components": [
					{"name": "recipient", "type": "address"},
					{"name": "ruleType", "type": "uint8"},
					{"name": "period", "type": "uint8"},
					{"name": "sideIndex", "type": "uint8"},
					{"name": "tokenIn", "type": "address"},
					{"name": "amountIn", "type": "uint96"},
					{"name": "broker", "type": "uint24"},
					{"name": "assets", "type": "address[]"},
					{"name": "acceptableEntryPrices", "type": "uint64[]"}
				]
			}
		],
		"outputs": []
	},
	{
		"type": "function",
		"name": "getRelativeMarkets",
		"stateMutability": "view",
		"inputs": [
			{"name": "start", "type": "uint256"},
			{"name": "size", "type": "uint8"}
		],
		"outputs": [
			{
				"name": "",
				"type": "tuple[]",
				"components": [
					{"name": "name", "type": "string"},
					{"name": "marketHash", "type": "bytes32"},
					{"name": "ruleType", "type": "uint8"},
					{"name": "assets", "type": "address[]"},
					{"name": "pythPriceIds", "type": "bytes32[]"},
					{
						"name": "periods",
						"type": "tuple[]",
						"components": [
							{"name": "period", "type": "uint8"},
							{"name": "status", "type": "uint8"},
							{"name": "winRatio", "type": "uint16"},
							{"name": "openFeeP", "type": "uint16"},
							{"name": "winCloseFeeP", "type": "uint16"},
							{"name": "loseCloseFeeP", "type": "uint16"},
							{"name": "maxSideUsd", "type": "uint256[]"}
						]
					}
				]
			}
		]
	},
	{
		"type": "event",
		"name": "PredictAndBetPending",
		"inputs": [
			{"name": "user", "type": "address", "indexed": true},
			{"name": "id", "type": "uint256", "indexed": true},
			{
				"name": "prediction",
				"type": "tuple",
				"indexed": false,
				"components": [
					{"name": "tokenIn", "type": "address"},
					{"name": "amountIn", "type": "uint96"},
					{"name": "predictionPairBase", "type": "address"},
					{"name": "openFee", "type": "uint96"},
					{"name": "user", "type": "address"},
					{"name": "price", "type": "uint64"},
					{"name": "broker", "type": "uint24"},
					{"name": "isUp", "type": "bool"},
					{"name": "blockNumber", "type": "uint128"},
					{"name": "period", "type": "uint8"}
				]
			}
		]
	},
	{
		"type": "event",
		"name": "PredictRelativePending",
		"inputs": [
			{"name": "user", "type": "address", "indexed": true},
			{"name": "id", "type": "uint256", "indexed": true},
			{
				"name": "prediction",
				"type": "tuple",
				"indexed": false,
				"components": [
					{"name": "tokenIn", "type": "address"},
					{"name": "amountIn", "type": "uint96"},
					{"name": "openFee", "type": "uint96"},
					{"name": "user", "type": "address"},
					{"name": "marketHash", "type": "bytes32"},
					{"name": "period", "type": "uint8"},
					{"name": "sideIndex", "type": "uint8"},
					{"name": "ruleType", "type": "uint8"},
					{"name": "broker", "type": "uint24"},
					{"name": "blockNumber", "type": "uint128"},
					{"name": "acceptableEntryPrices", "type": "uint64[]"}
				]
			}
		]
	}
]`

// PredictionConfig represents the prediction configuration
type PredictionConfig struct {
	MinBetUsd  *big.Int
	IsActive   bool
	AutoSettle bool
}

// TokenInfo represents token information for prediction
type TokenInfo struct {
	Token    common.Address
	Switch   bool
	Decimals uint8
	Price    *big.Int
}

// PredictAndBetParams represents parameters for up/down prediction
type PredictAndBetParams struct {
	Recipient          common.Address
	PredictionPairBase common.Address
	IsUp               bool
	Period             uint8
	TokenIn            common.Address
	AmountIn           *big.Int
	Price              uint64
	Broker             *big.Int
}

// PredictRelativeAndBetParams represents parameters for relative prediction
type PredictRelativeAndBetParams struct {
	Recipient             common.Address
	RuleType              uint8
	Period                uint8
	SideIndex             uint8
	TokenIn               common.Address
	AmountIn              *big.Int
	Broker                *big.Int
	Assets                []common.Address
	AcceptableEntryPrices []uint64
}

// RelativeMarket represents an on-chain relative market
type RelativeMarket struct {
	Name         string
	MarketHash   [32]byte
	RuleType     uint8
	Assets       []common.Address
	PythPriceIds [][32]byte
	Periods      []PeriodConfig
}

// PeriodConfig represents period configuration
type PeriodConfig struct {
	Period        uint8
	Status        uint8
	WinRatio      uint16
	OpenFeeP      uint16
	WinCloseFeeP  uint16
	LoseCloseFeeP uint16
	MaxSideUsd    []*big.Int
}

type Diamond struct {
	address  common.Address
	client   *ethclient.Client
	abi      abi.ABI
	contract *bind.BoundContract
}

func NewDiamond(address common.Address, client *ethclient.Client) (*Diamond, error) {
	parsed, err := abi.JSON(strings.NewReader(diamondABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Diamond ABI: %w", err)
	}

	contract := bind.NewBoundContract(address, parsed, client, client, client)

	return &Diamond{
		address:  address,
		client:   client,
		abi:      parsed,
		contract: contract,
	}, nil
}

func (d *Diamond) GetPredictionConfig(ctx context.Context) (*PredictionConfig, error) {
	var result []interface{}
	err := d.contract.Call(&bind.CallOpts{Context: ctx}, &result, "getPredictionConfig")
	if err != nil {
		return nil, fmt.Errorf("failed to get prediction config: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("empty result from getPredictionConfig")
	}

	// The result is a struct, we need to extract fields
	configStruct, ok := result[0].(struct {
		MinBetUsd  *big.Int `abi:"minBetUsd"`
		IsActive   bool     `abi:"isActive"`
		AutoSettle bool     `abi:"autoSettle"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected config type")
	}

	return &PredictionConfig{
		MinBetUsd:  configStruct.MinBetUsd,
		IsActive:   configStruct.IsActive,
		AutoSettle: configStruct.AutoSettle,
	}, nil
}

func (d *Diamond) GetTokenForPrediction(ctx context.Context, tokenAddress common.Address) (*TokenInfo, error) {
	var result []interface{}
	err := d.contract.Call(&bind.CallOpts{Context: ctx}, &result, "getTokenForPrediction", tokenAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get token info: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("empty result from getTokenForPrediction")
	}

	tokenStruct, ok := result[0].(struct {
		Token    common.Address `abi:"token"`
		Switch   bool           `abi:"switch"`
		Decimals uint8          `abi:"decimals"`
		Price    *big.Int       `abi:"price"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected token info type")
	}

	return &TokenInfo{
		Token:    tokenStruct.Token,
		Switch:   tokenStruct.Switch,
		Decimals: tokenStruct.Decimals,
		Price:    tokenStruct.Price,
	}, nil
}

func (d *Diamond) PredictAndBet(ctx context.Context, opts *bind.TransactOpts, params PredictAndBetParams) (*types.Transaction, error) {
	// Pack the struct for the contract call
	packedParams := struct {
		Recipient          common.Address
		PredictionPairBase common.Address
		IsUp               bool
		Period             uint8
		TokenIn            common.Address
		AmountIn           *big.Int
		Price              uint64
		Broker             *big.Int
	}{
		Recipient:          params.Recipient,
		PredictionPairBase: params.PredictionPairBase,
		IsUp:               params.IsUp,
		Period:             params.Period,
		TokenIn:            params.TokenIn,
		AmountIn:           params.AmountIn,
		Price:              params.Price,
		Broker:             params.Broker,
	}

	return d.contract.Transact(opts, "predictAndBet", packedParams)
}

func (d *Diamond) PredictRelativeAndBet(ctx context.Context, opts *bind.TransactOpts, params PredictRelativeAndBetParams) (*types.Transaction, error) {
	// Pack the struct for the contract call
	packedParams := struct {
		Recipient             common.Address
		RuleType              uint8
		Period                uint8
		SideIndex             uint8
		TokenIn               common.Address
		AmountIn              *big.Int
		Broker                *big.Int
		Assets                []common.Address
		AcceptableEntryPrices []uint64
	}{
		Recipient:             params.Recipient,
		RuleType:              params.RuleType,
		Period:                params.Period,
		SideIndex:             params.SideIndex,
		TokenIn:               params.TokenIn,
		AmountIn:              params.AmountIn,
		Broker:                params.Broker,
		Assets:                params.Assets,
		AcceptableEntryPrices: params.AcceptableEntryPrices,
	}

	return d.contract.Transact(opts, "predictRelativeAndBet", packedParams)
}

func (d *Diamond) Address() common.Address {
	return d.address
}

// RuleType constants
const (
	RuleTypeUpLeader   uint8 = 0 // Best performer wins
	RuleTypeDownLeader uint8 = 1 // Worst performer wins
)

// Period status constants
const (
	PeriodStatusAvailable uint8 = 0
	PeriodStatusCloseOnly uint8 = 1
	PeriodStatusClosed    uint8 = 2
)
