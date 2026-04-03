package contracts

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const erc20ABI = `[
	{
		"constant": true,
		"inputs": [{"name": "_owner", "type": "address"}],
		"name": "balanceOf",
		"outputs": [{"name": "balance", "type": "uint256"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [
			{"name": "_owner", "type": "address"},
			{"name": "_spender", "type": "address"}
		],
		"name": "allowance",
		"outputs": [{"name": "", "type": "uint256"}],
		"type": "function"
	},
	{
		"constant": false,
		"inputs": [
			{"name": "_spender", "type": "address"},
			{"name": "_value", "type": "uint256"}
		],
		"name": "approve",
		"outputs": [{"name": "", "type": "bool"}],
		"type": "function"
	},
	{
		"constant": false,
		"inputs": [
			{"name": "_to", "type": "address"},
			{"name": "_value", "type": "uint256"}
		],
		"name": "transfer",
		"outputs": [{"name": "", "type": "bool"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "decimals",
		"outputs": [{"name": "", "type": "uint8"}],
		"type": "function"
	}
]`

type ERC20 struct {
	address  common.Address
	client   *ethclient.Client
	abi      abi.ABI
	contract *bind.BoundContract
}

func NewERC20(address common.Address, client *ethclient.Client) (*ERC20, error) {
	parsed, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ERC20 ABI: %w", err)
	}

	contract := bind.NewBoundContract(address, parsed, client, client, client)

	return &ERC20{
		address:  address,
		client:   client,
		abi:      parsed,
		contract: contract,
	}, nil
}

func (e *ERC20) BalanceOf(ctx context.Context, owner common.Address) (*big.Int, error) {
	var result []interface{}
	err := e.contract.Call(&bind.CallOpts{Context: ctx}, &result, "balanceOf", owner)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	if len(result) == 0 {
		return big.NewInt(0), nil
	}

	balance, ok := result[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected balance type")
	}

	return balance, nil
}

func (e *ERC20) Allowance(ctx context.Context, owner, spender common.Address) (*big.Int, error) {
	var result []interface{}
	err := e.contract.Call(&bind.CallOpts{Context: ctx}, &result, "allowance", owner, spender)
	if err != nil {
		return nil, fmt.Errorf("failed to get allowance: %w", err)
	}

	if len(result) == 0 {
		return big.NewInt(0), nil
	}

	allowance, ok := result[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected allowance type")
	}

	return allowance, nil
}

func (e *ERC20) Approve(ctx context.Context, opts *bind.TransactOpts, spender common.Address, amount *big.Int) (*types.Transaction, error) {
	return e.contract.Transact(opts, "approve", spender, amount)
}

func (e *ERC20) Transfer(ctx context.Context, opts *bind.TransactOpts, to common.Address, amount *big.Int) (*types.Transaction, error) {
	return e.contract.Transact(opts, "transfer", to, amount)
}

func (e *ERC20) Decimals(ctx context.Context) (uint8, error) {
	var result []interface{}
	err := e.contract.Call(&bind.CallOpts{Context: ctx}, &result, "decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to get decimals: %w", err)
	}

	if len(result) == 0 {
		return 6, nil // Default to 6 for USDC
	}

	decimals, ok := result[0].(uint8)
	if !ok {
		return 6, nil
	}

	return decimals, nil
}

func (e *ERC20) Address() common.Address {
	return e.address
}

// EstimateGas estimates gas for a transaction
func (e *ERC20) EstimateApproveGas(ctx context.Context, from, spender common.Address, amount *big.Int) (uint64, error) {
	data, err := e.abi.Pack("approve", spender, amount)
	if err != nil {
		return 0, fmt.Errorf("failed to pack approve data: %w", err)
	}

	msg := ethereum.CallMsg{
		From: from,
		To:   &e.address,
		Data: data,
	}

	return e.client.EstimateGas(ctx, msg)
}

// MaxUint256 returns the max uint256 value for infinite approval
func MaxUint256() *big.Int {
	max := new(big.Int)
	max.SetString("115792089237316195423570985008687907853269984665640564039457584007913129639935", 10)
	return max
}
