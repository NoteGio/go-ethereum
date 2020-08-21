package ethapi

import (
 "context"
 "github.com/ethereum/go-ethereum/common/hexutil"
 "github.com/ethereum/go-ethereum/rpc"

)

type EtherCattleBlockChainAPI struct {
 b Backend
}


func NewEtherCattleBlockChainAPI(b Backend) *EtherCattleBlockChainAPI {
   return &EtherCattleBlockChainAPI{b}
}

// EstimateGasList returns an estimate of the amount of gas needed to execute list of
// given transactions against the current pending block.
func (s *EtherCattleBlockChainAPI) EstimateGasList(ctx context.Context, argsList []CallArgs, precise ...bool) ([]hexutil.Uint64, error) {
  fast := len(precise) == 0 || !precise[0]
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
