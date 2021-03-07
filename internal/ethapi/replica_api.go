package ethapi

import (
 "context"
 "github.com/ethereum/go-ethereum/common"
 "github.com/ethereum/go-ethereum/common/hexutil"
 "github.com/ethereum/go-ethereum/core/types"
 "github.com/ethereum/go-ethereum/core/vm"
 "github.com/ethereum/go-ethereum/rpc"
 "time"
)

type EtherCattleBlockChainAPI struct {
 b Backend
}


func NewEtherCattleBlockChainAPI(b Backend) *EtherCattleBlockChainAPI {
   return &EtherCattleBlockChainAPI{b}
}

// EstimateGasList returns an estimate of the amount of gas needed to execute list of
// given transactions against the current pending block.
func (s *EtherCattleBlockChainAPI) EstimateGasList(ctx context.Context, argsList []CallArgs, precise *bool) ([]hexutil.Uint64, error) {
  fast := precise == nil || !*precise
  blockNrOrHash := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
  var (
    gas       hexutil.Uint64
    err       error
    stateData *PreviousState
    gasCap    = s.b.RPCGasCap()
  )
  returnVals := make([]hexutil.Uint64, len(argsList))
  for idx, args := range argsList {
    gas, stateData, err = DoEstimateGas(ctx, s.b, args, stateData, blockNrOrHash, gasCap, fast)
    if err != nil {
      return nil, err
    }
    gasCap -= uint64(gas)
    returnVals[idx] = gas
  }
  return returnVals, nil
}


type CallDetails struct {
  Args *CallArgs `json:"args,omitempty"`
  Result interface{} `json:"result,omitempty"`
  Logs []*types.Log `json:"logs,omitempty"`
  Error error `json:"error,omitempty"`
}

// CallDetailsList runs a list of calls in succession, returning the result,
// access list, and gas of each call.
func (s *EtherCattleBlockChainAPI) CallDetailsList(ctx context.Context, argsList []CallArgs, blockNrOrHash *rpc.BlockNumberOrHash, estimateGas *bool) ([]CallDetails, error) {
  var (
    gas       hexutil.Uint64
    stateData *PreviousState
    gasCap    = s.b.RPCGasCap()
  )
  if blockNrOrHash == nil {
    pending := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
    blockNrOrHash = &pending
  }
  stateData = &PreviousState{}
  if err := stateData.Init(ctx, s.b, *blockNrOrHash); err != nil {
    return []CallDetails{}, err
  }
  returnVals := make([]CallDetails, len(argsList))
  for idx, args := range argsList {
    thash := common.BytesToHash([]byte{byte(idx)})
    stateData.Prepare(thash, common.Hash{}, idx)
    result, doCallState, err := DoCall(ctx, s.b, args, stateData.Copy(), *blockNrOrHash, nil, vm.Config{}, 5 * time.Second, gasCap)

    if err != nil {
      returnVals[idx] = CallDetails{Error: err}
      continue
    }
    // If the result contains a revert reason, try to unpack and return it.
    if len(result.Revert()) > 0 {
      returnVals[idx] = CallDetails{Error: newRevertError(result)}
      continue
    }
    args.AccessList = doCallState.AccessList()
    returnVals[idx] = CallDetails{
      Result: result.Return(),
      Logs: stateData.GetLogs(thash),
      Args: &args,
      Error: result.Err,
    }
    if estimateGas != nil && *estimateGas {
      gas, stateData, err = DoEstimateGas(ctx, s.b, args, stateData, *blockNrOrHash, gasCap, true)
      if err != nil {
        returnVals[idx] = CallDetails{Error: err}
        continue
      }
      gasCap -= uint64(gas)
      args.Gas = &gas
    } else {
      stateData = doCallState
      gasCap -= result.UsedGas
    }
  }
  return returnVals, nil
}
