// Copyright 2021 Evmos Foundation
// This file is part of Evmos' Ethermint library.
//
// The Ethermint library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The Ethermint library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the Ethermint library. If not, see https://github.com/evmos/ethermint/blob/main/LICENSE
package keeper

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	ethermint "github.com/evmos/ethermint/types"
	"github.com/evmos/ethermint/x/evm/statedb"
	"github.com/evmos/ethermint/x/evm/types"

	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
)

// NewEVM generates a go-ethereum VM from the provided Message fields and the chain parameters
// (ChainConfig and module Params). It additionally sets the validator operator address as the
// coinbase address to make it available for the COINBASE opcode, even though there is no
// beneficiary of the coinbase transaction (since we're not mining).
//
// NOTE: the RANDOM opcode is currently not supported since it requires
// RANDAO implementation. See https://github.com/evmos/ethermint/pull/1520#pullrequestreview-1200504697
// for more information.
func (k *Keeper) NewEVM(
	ctx sdk.Context,
	msg *core.Message,
	cfg *EVMConfig,
	stateDB vm.StateDB,
) *vm.EVM {
	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    statedb.Transfer,
		GetHash:     k.GetHashFn(ctx),
		Coinbase:    cfg.CoinBase,
		GasLimit:    ethermint.BlockGasLimit(ctx),
		BlockNumber: cfg.BlockNumber,
		Time:        cfg.BlockTime,
		Difficulty:  cfg.Difficulty,
		BaseFee:     cfg.BaseFee,
		Random:      cfg.Random,
	}
	if cfg.BlockOverrides != nil {
		cfg.BlockOverrides.Apply(&blockCtx)
	}
	txCtx := core.NewEVMTxContext(msg)
	if cfg.Tracer == nil {
		cfg.Tracer = k.Tracer(msg, cfg.Rules)
	}
	vmConfig := k.VMConfig(ctx, cfg)
	contracts := make(map[common.Address]vm.PrecompiledContract)
	active := make([]common.Address, 0)
	for addr, c := range vm.DefaultPrecompiles(cfg.Rules) {
		contracts[addr] = c
		active = append(active, addr)
	}
	for _, fn := range k.customContractFns {
		c := fn(ctx, cfg.Rules)
		addr := c.Address()
		contracts[addr] = c
		active = append(active, addr)
	}
	sort.SliceStable(active, func(i, j int) bool {
		return bytes.Compare(active[i].Bytes(), active[j].Bytes()) < 0
	})
	evm := vm.NewEVM(blockCtx, txCtx, stateDB, cfg.ChainConfig, vmConfig)
	evm.WithPrecompiles(contracts, active)
	return evm
}

// GetHashFn implements vm.GetHashFunc for Ethermint. It returns hash for 3 cases:
//  1. The requested height matches current block height from the context.
//  2. The requested height is within the valid range, retrieve the hash from GetHeaderHash for heights after sdk50.
//  3. The requested height is within the valid range, retrieve the hash from GetHistoricalInfo for heights before sdk50.
func (k Keeper) GetHashFn(ctx sdk.Context) vm.GetHashFunc {
	return func(num64 uint64) common.Hash {
		h, err := ethermint.SafeInt64(num64)
		if err != nil {
			return common.Hash{}
		}
		upper, err := ethermint.SafeUint64(ctx.BlockHeight())
		if err != nil {
			return common.Hash{}
		}
		if upper == num64 {
			headerHash := ctx.HeaderHash()
			if len(headerHash) > 0 {
				return common.BytesToHash(headerHash)
			}
		}
		// Align check with https://github.com/ethereum/go-ethereum/blob/release/1.11/core/vm/instructions.go#L433
		headerNum := k.GetParams(ctx).HeaderHashNum
		var lower uint64
		if upper <= headerNum {
			lower = 0
		} else {
			lower = upper - headerNum
		}
		if num64 < lower || num64 >= upper {
			return common.Hash{}
		}
		hash := k.GetHeaderHash(ctx, num64)
		if len(hash) > 0 {
			return common.BytesToHash(hash)
		}
		histInfo, err := k.stakingKeeper.GetHistoricalInfo(ctx, h)
		if err != nil {
			k.Logger(ctx).Debug("historical info not found", "height", h, "err", err.Error())
			return common.Hash{}
		}
		header, err := cmttypes.HeaderFromProto(&histInfo.Header)
		if err != nil {
			k.Logger(ctx).Error("failed to cast tendermint header from proto", "error", err)
			return common.Hash{}
		}
		return common.BytesToHash(header.Hash())
	}
}

// ApplyTransaction runs and attempts to perform a state transition with the given transaction (i.e Message), that will
// only be persisted (committed) to the underlying KVStore if the transaction does not fail.
//
// # Gas tracking
//
// Ethereum consumes gas according to the EVM opcodes instead of general reads and writes to store. Because of this, the
// state transition needs to ignore the SDK gas consumption mechanism defined by the GasKVStore and instead consume the
// amount of gas used by the VM execution. The amount of gas used is tracked by the EVM and returned in the execution
// result.
//
// Prior to the execution, the starting tx gas meter is saved and replaced with an infinite gas meter in a new context
// in order to ignore the SDK gas consumption config values (read, write, has, delete).
// After the execution, the gas used from the message execution will be added to the starting gas consumed, taking into
// consideration the amount of gas returned. Finally, the context is updated with the EVM gas consumed value prior to
// returning.
//
// For relevant discussion see: https://github.com/cosmos/cosmos-sdk/discussions/9072
func (k *Keeper) ApplyTransaction(ctx sdk.Context, msgEth *types.MsgEthereumTx) (*types.MsgEthereumTxResponse, error) {
	ethTx := msgEth.AsTransaction()
	cfg, err := k.EVMConfig(ctx, k.eip155ChainID, ethTx.Hash())
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to load evm config")
	}

	msg := msgEth.AsMessage(cfg.BaseFee)
	// snapshot to contain the tx processing and post processing in same scope
	var commit func()
	tmpCtx := ctx
	if k.hooks != nil {
		// Create a cache context to revert state when tx hooks fails,
		// the cache context is only committed when both tx and hooks executed successfully.
		// Didn't use `Snapshot` because the context stack has exponential complexity on certain operations,
		// thus restricted to be used only inside `ApplyMessage`.
		tmpCtx, commit = ctx.CacheContext()
	}

	// pass true to commit the StateDB
	res, err := k.ApplyMessageWithConfig(tmpCtx, msg, cfg, true)
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to apply ethereum core message")
	}

	logs := types.LogsToEthereum(res.Logs)

	// Compute block bloom filter
	if len(logs) > 0 {
		k.SetTxBloom(tmpCtx, new(big.Int).SetBytes(ethtypes.LogsBloom(logs)))
	}

	var contractAddr common.Address
	if msg.To == nil {
		contractAddr = crypto.CreateAddress(msg.From, msg.Nonce)
	}

	receipt := &ethtypes.Receipt{
		Type:            ethTx.Type(),
		PostState:       nil, // TODO: intermediate state root
		Logs:            logs,
		TxHash:          cfg.TxConfig.TxHash,
		ContractAddress: contractAddr,
		GasUsed:         res.GasUsed,
		BlockHash:       cfg.TxConfig.BlockHash,
		BlockNumber:     cfg.BlockNumber,
	}

	if !res.Failed() {
		receipt.Status = ethtypes.ReceiptStatusSuccessful
		// Only call hooks if tx executed successfully.
		if err = k.PostTxProcessing(tmpCtx, msg, receipt); err != nil {
			// If hooks return error, revert the whole tx.
			res.VmError = types.ErrPostTxProcessing.Error()
			k.Logger(ctx).Error("tx post processing failed", "error", err)

			// If the tx failed in post processing hooks, we should clear the logs
			res.Logs = nil
		} else if commit != nil {
			// PostTxProcessing is successful, commit the tmpCtx
			commit()
			// Since the post-processing can alter the log, we need to update the result
			res.Logs = types.NewLogsFromEth(receipt.Logs)
		}
	}

	// refund gas in order to match the Ethereum gas consumption instead of the default SDK one.
	if err = k.RefundGas(ctx, msg, msg.GasLimit-res.GasUsed, cfg.Params.EvmDenom); err != nil {
		return nil, errorsmod.Wrapf(err, "failed to refund leftover gas to sender %s", msg.From)
	}

	totalGasUsed, err := k.AddTransientGasUsed(ctx, res.GasUsed)
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to add transient gas used")
	}

	// reset the gas meter for current cosmos transaction
	k.ResetGasMeterAndConsumeGas(ctx, totalGasUsed)
	return res, nil
}

// ApplyMessage calls ApplyMessageWithConfig with an empty TxConfig.
func (k *Keeper) ApplyMessage(ctx sdk.Context, msg *core.Message, tracer vm.EVMLogger, commit bool) (*types.MsgEthereumTxResponse, error) {
	cfg, err := k.EVMConfig(ctx, k.eip155ChainID, common.Hash{})
	if err != nil {
		return nil, errorsmod.Wrap(err, "failed to load evm config")
	}

	cfg.Tracer = tracer
	return k.ApplyMessageWithConfig(ctx, msg, cfg, commit)
}

// ApplyMessageWithConfig computes the new state by applying the given message against the existing state.
// If the message fails, the VM execution error with the reason will be returned to the client
// and the transaction won't be committed to the store.
//
// # Reverted state
//
// The snapshot and rollback are supported by the `statedb.StateDB`.
//
// # Different Callers
//
// It's called in three scenarios:
// 1. `ApplyTransaction`, in the transaction processing flow.
// 2. `EthCall/EthEstimateGas` grpc query handler.
// 3. Called by other native modules directly.
//
// # Prechecks and Preprocessing
//
// All relevant state transition prechecks for the MsgEthereumTx are performed on the AnteHandler,
// prior to running the transaction against the state. The prechecks run are the following:
//
// 1. the nonce of the message caller is correct
// 2. caller has enough balance to cover transaction fee(gaslimit * gasprice)
// 3. the amount of gas required is available in the block
// 4. the purchased gas is enough to cover intrinsic usage
// 5. there is no overflow when calculating intrinsic gas
// 6. caller has enough balance to cover asset transfer for **topmost** call
//
// The preprocessing steps performed by the AnteHandler are:
//
// 1. set up the initial access list (iff fork > Berlin)
//
// # Tracer parameter
//
// It should be a `vm.Tracer` object or nil, if pass `nil`, it'll create a default one based on keeper options.
//
// This is expected used in debug_trace* where AnteHandler is not executed
//
// # Commit parameter
//
// If commit is true, the `StateDB` will be committed, otherwise discarded.
//
// # debugTrace parameter
//
// The message is applied with steps to mimic AnteHandler
//  1. the sender is consumed with gasLimit * gasPrice in full at the beginning of the execution and
//     then refund with unused gas after execution.
//  2. sender nonce is incremented by 1 before execution
func (k *Keeper) ApplyMessageWithConfig(
	ctx sdk.Context,
	msg *core.Message,
	cfg *EVMConfig,
	commit bool,
) (*types.MsgEthereumTxResponse, error) {
	var (
		ret   []byte // return bytes from evm execution
		vmErr error  // vm errors do not effect consensus and are therefore not assigned to err
	)

	// return error if contract creation or call are disabled through governance
	if !cfg.Params.EnableCreate && msg.To == nil {
		return nil, errorsmod.Wrap(types.ErrCreateDisabled, "failed to create new contract")
	} else if !cfg.Params.EnableCall && msg.To != nil {
		return nil, errorsmod.Wrap(types.ErrCallDisabled, "failed to call contract")
	}

	stateDB := statedb.NewWithParams(ctx, k, cfg.TxConfig, cfg.Params.EvmDenom)
	var evm *vm.EVM
	if cfg.Overrides != nil {
		if err := cfg.Overrides.Apply(stateDB); err != nil {
			return nil, errorsmod.Wrap(err, "failed to apply state override")
		}
	}
	evm = k.NewEVM(ctx, msg, cfg, stateDB)
	// Allow the tracer captures the tx level events, mainly the gas consumption.
	leftoverGas := msg.GasLimit
	sender := vm.AccountRef(msg.From)
	tracer := cfg.GetTracer()
	debugFn := func() {
		if tracer != nil && cfg.DebugTrace {
			stateDB.AddBalance(sender.Address(), new(big.Int).Mul(msg.GasPrice, new(big.Int).SetUint64(leftoverGas)))
		}
	}
	if tracer != nil {
		if cfg.DebugTrace {
			amount := new(big.Int).Mul(msg.GasPrice, new(big.Int).SetUint64(msg.GasLimit))
			stateDB.SubBalance(sender.Address(), amount)
			if err := stateDB.Error(); err != nil {
				return nil, err
			}
			stateDB.SetNonce(sender.Address(), stateDB.GetNonce(sender.Address())+1)
		}
		tracer.CaptureTxStart(leftoverGas)
		defer func() {
			debugFn()
			tracer.CaptureTxEnd(leftoverGas)
		}()
	}

	rules := cfg.Rules
	contractCreation := msg.To == nil
	intrinsicGas, err := k.GetEthIntrinsicGas(msg, rules, contractCreation)
	if err != nil {
		// should have already been checked on Ante Handler
		return nil, errorsmod.Wrap(err, "intrinsic gas failed")
	}

	// Should check again even if it is checked on Ante Handler, because eth_call don't go through Ante Handler.
	if leftoverGas < intrinsicGas {
		// eth_estimateGas will check for this exact error
		return nil, errorsmod.Wrap(core.ErrIntrinsicGas, "apply message")
	}
	leftoverGas -= intrinsicGas

	// access list preparation is moved from ante handler to here, because it's needed when `ApplyMessage` is called
	// under contexts where ante handlers are not run, for example `eth_call` and `eth_estimateGas`.
	// Check whether the init code size has been exceeded.
	if rules.IsShanghai && contractCreation && len(msg.Data) > params.MaxInitCodeSize {
		return nil, fmt.Errorf("%w: code size %v limit %v", core.ErrMaxInitCodeSizeExceeded, len(msg.Data), params.MaxInitCodeSize)
	}

	// Execute the preparatory steps for state transition which includes:
	// - prepare accessList(post-berlin)
	// - reset transient storage(eip 1153)
	stateDB.Prepare(rules, msg.From, cfg.CoinBase, msg.To, vm.DefaultActivePrecompiles(rules), msg.AccessList)

	if contractCreation {
		// take over the nonce management from evm:
		// - reset sender's nonce to msg.Nonce() to generate correct contract address.
		// - set the nonce back to the original value after contract creation.
		oldNonce := stateDB.GetNonce(sender.Address())
		stateDB.SetNonce(sender.Address(), msg.Nonce)
		ret, _, leftoverGas, vmErr = evm.Create(sender, msg.Data, leftoverGas, msg.Value)
		stateDB.SetNonce(sender.Address(), oldNonce)
	} else {
		ret, leftoverGas, vmErr = evm.Call(sender, *msg.To, msg.Data, leftoverGas, msg.Value)
	}

	refundQuotient := params.RefundQuotient

	// After EIP-3529: refunds are capped to gasUsed / 5
	if rules.IsLondon {
		refundQuotient = params.RefundQuotientEIP3529
	}

	// calculate gas refund
	if msg.GasLimit < leftoverGas {
		return nil, errorsmod.Wrap(types.ErrGasOverflow, "apply message")
	}
	// refund gas
	temporaryGasUsed := msg.GasLimit - leftoverGas
	leftoverGas += GasToRefund(stateDB.GetRefund(), temporaryGasUsed, refundQuotient)

	// EVM execution error needs to be available for the JSON-RPC client
	var vmError string
	if vmErr != nil {
		vmError = vmErr.Error()
	}

	// calculate a minimum amount of gas to be charged to sender if GasLimit
	// is considerably higher than GasUsed to stay more aligned with Tendermint gas mechanics
	// for more info https://github.com/evmos/ethermint/issues/1085
	limit, err := ethermint.SafeInt64(msg.GasLimit)
	if err != nil {
		return nil, err
	}
	gasLimit := sdkmath.LegacyNewDec(limit)
	minGasMultiplier := cfg.FeeMarketParams.MinGasMultiplier
	if minGasMultiplier.IsNil() {
		// in case we are executing eth_call on a legacy block, returns a zero value.
		minGasMultiplier = sdkmath.LegacyZeroDec()
	}
	minimumGasUsed := gasLimit.Mul(minGasMultiplier)

	if msg.GasLimit < leftoverGas {
		return nil, errorsmod.Wrapf(types.ErrGasOverflow, "message gas limit < leftover gas (%d < %d)", msg.GasLimit, leftoverGas)
	}
	tempGasUsed, err := ethermint.SafeInt64(temporaryGasUsed)
	if err != nil {
		return nil, err
	}

	gasUsed := sdkmath.LegacyMaxDec(minimumGasUsed, sdkmath.LegacyNewDec(tempGasUsed)).TruncateInt().Uint64()
	// reset leftoverGas, to be used by the tracer
	leftoverGas = msg.GasLimit - gasUsed

	debugFn()
	debugFn = func() {}

	// The dirty states in `StateDB` is either committed or discarded after return
	if commit {
		if err := stateDB.Commit(); err != nil {
			return nil, errorsmod.Wrap(err, "failed to commit stateDB")
		}
	}

	return &types.MsgEthereumTxResponse{
		GasUsed:   gasUsed,
		VmError:   vmError,
		Ret:       ret,
		Logs:      types.NewLogsFromEth(stateDB.Logs()),
		Hash:      cfg.TxConfig.TxHash.Hex(),
		BlockHash: ctx.HeaderHash(),
	}, nil
}
