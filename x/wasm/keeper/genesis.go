package keeper

import (
	"os"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	jsoniter "github.com/json-iterator/go"
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

func ExportGenesis(ctx sdk.Context, keeper *Keeper, filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	stream := jsoniter.ConfigDefault.BorrowStream(file)
	defer jsoniter.ConfigDefault.ReturnStream(stream)

	stream.WriteObjectStart()

	// params
	stream.WriteObjectField("params")
	stream.WriteVal(keeper.GetParams(ctx))

	// codes
	stream.WriteMore()
	stream.WriteObjectField("codes")
	stream.WriteArrayStart()
	firstCode := true
	keeper.IterateCodeInfos(ctx, func(codeID uint64, info types.CodeInfo) bool {
		if !firstCode {
			stream.WriteMore()
		}
		firstCode = false

		bytecode, err := keeper.GetByteCode(ctx, codeID)
		if err != nil {
			panic(err)
		}
		stream.WriteVal(types.Code{
			CodeID:    codeID,
			CodeInfo:  info,
			CodeBytes: bytecode,
			Pinned:    keeper.IsPinnedCode(ctx, codeID),
		})
		return false
	})
	stream.WriteArrayEnd()

	// contracts
	stream.WriteMore()
	stream.WriteObjectField("contracts")
	stream.WriteArrayStart()
	firstContract := true
	keeper.IterateContractInfo(ctx, func(addr sdk.AccAddress, contract types.ContractInfo) bool {
		if !firstContract {
			stream.WriteMore()
		}
		firstContract = false

		var state []types.Model
		keeper.IterateContractState(ctx, addr, func(key, value []byte) bool {
			state = append(state, types.Model{Key: key, Value: value})
			return false
		})

		contractCodeHistory := keeper.GetContractHistory(ctx, addr)
		stream.WriteVal(types.Contract{
			ContractAddress:     addr.String(),
			ContractInfo:        contract,
			ContractState:       state,
			ContractCodeHistory: contractCodeHistory,
		})
		return false
	})
	stream.WriteArrayEnd()

	// sequences
	stream.WriteMore()
	stream.WriteObjectField("sequences")
	stream.WriteArrayStart()
	firstSequence := true
	for _, k := range [][]byte{types.KeyLastCodeID, types.KeyLastInstanceID} {
		if !firstSequence {
			stream.WriteMore()
		}
		firstSequence = false
		stream.WriteVal(types.Sequence{
			IDKey: k,
			Value: keeper.PeekAutoIncrementID(ctx, k),
		})
	}
	stream.WriteArrayEnd()

	stream.WriteObjectEnd()
	stream.Flush()

	return stream.Error
}
