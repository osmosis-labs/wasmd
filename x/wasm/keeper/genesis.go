package keeper

import (
	"runtime"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	abci "github.com/tendermint/tendermint/abci/types"

	"github.com/CosmWasm/wasmd/x/wasm/types"
)

// ValidatorSetSource is a subset of the staking keeper
type ValidatorSetSource interface {
	ApplyAndReturnValidatorSetUpdates(sdk.Context) (updates []abci.ValidatorUpdate, err error)
}

// InitGenesis sets supply information for genesis.
//
// CONTRACT: all types of accounts must have been already initialized/created
func InitGenesis(ctx sdk.Context, keeper *Keeper, data types.GenesisState) ([]abci.ValidatorUpdate, error) {
	contractKeeper := NewGovPermissionKeeper(keeper)
	keeper.SetParams(ctx, data.Params)
	var maxCodeID uint64
	for i, code := range data.Codes {
		err := keeper.importCode(ctx, code.CodeID, code.CodeInfo, code.CodeBytes)
		if err != nil {
			return nil, sdkerrors.Wrapf(err, "code %d with id: %d", i, code.CodeID)
		}
		if code.CodeID > maxCodeID {
			maxCodeID = code.CodeID
		}
		if code.Pinned {
			if err := contractKeeper.PinCode(ctx, code.CodeID); err != nil {
				return nil, sdkerrors.Wrapf(err, "contract number %d", i)
			}
		}
	}

	for i, contract := range data.Contracts {
		contractAddr, err := sdk.AccAddressFromBech32(contract.ContractAddress)
		if err != nil {
			return nil, sdkerrors.Wrapf(err, "address in contract number %d", i)
		}
		err = keeper.importContract(ctx, contractAddr, &contract.ContractInfo, contract.ContractState, contract.ContractCodeHistory)
		if err != nil {
			return nil, sdkerrors.Wrapf(err, "contract number %d", i)
		}
	}

	for i, seq := range data.Sequences {
		err := keeper.importAutoIncrementID(ctx, seq.IDKey, seq.Value)
		if err != nil {
			return nil, sdkerrors.Wrapf(err, "sequence number %d", i)
		}
	}

	// sanity check seq values
	seqVal := keeper.PeekAutoIncrementID(ctx, types.KeyLastCodeID)
	if seqVal <= maxCodeID {
		return nil, sdkerrors.Wrapf(types.ErrInvalid, "seq %s with value: %d must be greater than: %d ", string(types.KeyLastCodeID), seqVal, maxCodeID)
	}

	// ensure next classic address is unused so that we know the sequence is good
	rCtx, _ := ctx.CacheContext()
	seqVal = keeper.PeekAutoIncrementID(rCtx, types.KeyLastInstanceID)
	addr := keeper.ClassicAddressGenerator()(rCtx, seqVal, nil)
	if keeper.HasContractInfo(ctx, addr) {
		return nil, sdkerrors.Wrapf(types.ErrInvalid, "value: %d for seq %s was used already", seqVal, string(types.KeyLastInstanceID))
	}
	return nil, nil
}

// ExportGenesis returns a GenesisState for a given context and keeper.
func ExportGenesis(ctx sdk.Context, keeper *Keeper) *types.GenesisState {
	var genState types.GenesisState

	genState.Params = keeper.GetParams(ctx)

	codes := make([]types.Code, 0)
	keeper.IterateCodeInfos(ctx, func(codeID uint64, info types.CodeInfo) bool {
		bytecode, err := keeper.GetByteCode(ctx, codeID)
		if err != nil {
			panic(err)
		}
		codes = append(codes, types.Code{
			CodeID:    codeID,
			CodeInfo:  info,
			CodeBytes: bytecode,
			Pinned:    keeper.IsPinnedCode(ctx, codeID),
		})
		if len(codes) >= 50 {
			genState.Codes = append(genState.Codes, codes...)
			codes = make([]types.Code, 0)
			runtime.GC()
		}
		return false
	})
	genState.Codes = append(genState.Codes, codes...)
	codes = nil
	runtime.GC()

	contracts := make([]types.Contract, 0)
	keeper.IterateContractInfo(ctx, func(addr sdk.AccAddress, contract types.ContractInfo) bool {
		var state []types.Model
		keeper.IterateContractState(ctx, addr, func(key, value []byte) bool {
			state = append(state, types.Model{Key: key, Value: value})
			return false
		})

		contractCodeHistory := keeper.GetContractHistory(ctx, addr)

		contracts = append(contracts, types.Contract{
			ContractAddress:     addr.String(),
			ContractInfo:        contract,
			ContractState:       state,
			ContractCodeHistory: contractCodeHistory,
		})

		if len(contracts) >= 50 {
			genState.Contracts = append(genState.Contracts, contracts...)
			contracts = make([]types.Contract, 0)
			runtime.GC()
		}
		return false
	})
	genState.Contracts = append(genState.Contracts, contracts...)
	contracts = nil
	runtime.GC()

	for _, k := range [][]byte{types.KeyLastCodeID, types.KeyLastInstanceID} {
		genState.Sequences = append(genState.Sequences, types.Sequence{
			IDKey: k,
			Value: keeper.PeekAutoIncrementID(ctx, k),
		})
	}

	return &genState
}
