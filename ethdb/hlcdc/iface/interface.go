package hlcdc

import (
  "math/big"
  "github.com/ethereum/go-ethereum/common"
)


type StateUpdater interface {
  StateUpdate(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte)
}

type BlockPublisher interface {
  WriteBody(hash common.Hash, number uint64, data []byte)
  WriteTd(hash common.Hash, number uint64, td *big.Int)
  WriteHeader(hash common.Hash, number uint64, data []byte)
  WriteReceipts(hash common.Hash, number uint64, data []byte)
  DeleteBlock(hash common.Hash, number uint64)
}
