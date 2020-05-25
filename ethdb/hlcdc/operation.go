package hlcdc

import (
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/core/state/snapshot"
  "github.com/ethereum/go-ethereum/core/types"
)

type operation struct {
  Op byte
  Data []byte
  Offset int64
  Topic string
  Timestamp time.Time
}

const (
  OpSUParent = 0
  OpSUDestruct = 1
  OpSUAccount = 2
  OpSUStorage = 3
  OpSUDone = 4
  OpWriteBody = 5
  OpWriteHeader = 6
  OpWriteReceipts = 7
  OpWriteTD = 8
  OpDeleteBlock = 9


func i64tob(val uint64) []byte {
	r := make([]byte, 8)
	for i := uint64(0); i < 8; i++ {
		r[i] = byte((val >> (i * 8)) & 0xff)
	}
	return r
}

func btoi64(val []byte) uint64 {
	r := uint64(0)
	for i := uint64(0); i < 8; i++ {
		r |= uint64(val[i]) << (8 * i)
	}
	return r
}

type stateUpdate {
  parentRoot common.Hash
  destructs map[common.Hash]struct{}
  account map[common.Hash][]byte
  storage map[common.Hash]map[common.Hash][]byte
}

type opProcessor struct {
  db ethdb.KeyValueStore
  snaps *snapshot.Tree
  pending map[common.Hash]stateUpdate
  recent map[common.Hash]struct{}
  older map[common.Hash]struct{}
  latestFullBlock *types.Block
  capacity int
}

func (processor *opProcessor) ensureExists(key common.Hash) {
  if _, ok := processor.pending[key]; !ok {
    processor.pending[key] = statusUpdate{
      destructs: make(map[common.Hash]struct{}),
      account: make(map[common.Hash][]byte),
      storage: make(map[common.Hash]map[common.Hash][]byte)
    }
  }
}



func (processor *opProcessor) Apply(op *operation) error {
  switch op.Op {
  case OpSUParent:
    blockRoot := common.BytesToHash(op.Data[0:32])
    processor.ensureExists(blockRoot)
    processor.pending[blockRoot].parentRoot = common.BytesToHash(op.Data[32:64])
  case OpSUDestruct:
    blockRoot := common.BytesToHash(op.Data[0:32])
    processor.ensureExists(blockRoot)
    processor.pending[blockRoot].destructs[common.BytesToHash(op.Data[32:64])] = struct{}
  case OpSUAccount:
    blockRoot := common.BytesToHash(op.Data[0:32])
    processor.ensureExists(blockRoot)
    processor.pending[blockRoot].accounts[common.BytesToHash(op.Data[32:64])] = op.Data[64:]
  case OpSUStorage:
    blockRoot := common.BytesToHash(op.Data[0:32])
    processor.ensureExists(blockRoot)
    account := common.BytesToHash(op.Data[32:64])
    if _, ok := processor.pending[blockRoot].storage[account]; !ok {
      processor.pending[blockRoot].storage[account] = make(map[common.Hash][]byte)
    }
    processor.pending[blockRoot].storage[account][common.BytesToHash(op.Data[64:96])] = op.Data[96:]
  case OpSUDone:
    blockRoot := common.BytesToHash(op.Data[0:32])
    if _, ok := processor.recent[blockRoot]; ok {
      // We've already written this block, don't do it again
      return nil
    }
    if _, ok := processor.older[blockRoot]; ok {
      // We've already written this block, don't do it again
      return nil
    }
    if _, ok := processor.pending[blockRoot]; !ok || processor.pending[blockRoot].parentRoot == (common.Hash{}) {
      log.Warn("Write for incomplete block", "root", blockRoot)
      return
    }
    if err := processor.snaps.Update(blockRoot, processor.pending[blockRoot].parentRoot, processor.pending[blockRoot].destructs, processor.pending[blockRoot].accounts, processor.pending[blockRoot].storage); err != nil {
      return err
    }
    delete(processor.pending, blockRoot)
    processor.recent[blockRoot] = struct{}{}
    if len(recent) > processor.capacity {
      processor.older = processor.recent
      processor.recent = make(map[common.Hash]struct{})
    }
  case OpWriteBody:
    hash := common.BytesToHash(op.Data[:32])
    number := btoi64(op.Data[32:40])
    data := rlp.RawValue(op.Data[40:])
    rawdb.WriteBodyRLP(processor.db, hash, number, data)
    rawdb.WriteCanonicalHash(processor.db, hash, number)
    if block := rawdb.ReadBlock(processor.db, hash, number); block != nil {
      // Header was already written, we have the whole block
      rawdb.WriteTxLookupEntries(processor.db, block)
      if processor.latestFullBlock.NumberU64() < block.NumberU64() {
        rawdb.WriteHeadHeaderHash(processor.db, block.Hash())
        rawdb.WriteHeadBlockHash(processor.db, block.Hash())
        rawdb.WriteHeadFastBlockHash(processor.db, block.Hash())
      }
    }
  case OpWriteHeader:
    hash := common.BytesToHash(op.Data[:32])
    number := btoi64(op.Data[32:40])
    header := &types.Header{}
    if err := rlp.DecodeRLP(op.Data[40:], header); err != nil {
      return err
    }
    rawdb.WriteHeader(processor.db, hash, number, header)
    if block := rawdb.ReadBlock(processor.db, hash, number); block != nil {
      // Header was already written, we have the whole block
      rawdb.WriteTxLookupEntries(processor.db, block)
      if processor.latestFullBlock.NumberU64() < block.NumberU64() {
        rawdb.WriteHeadHeaderHash(processor.db, block.Hash())
        rawdb.WriteHeadBlockHash(processor.db, block.Hash())
        rawdb.WriteHeadFastBlockHash(processor.db, block.Hash())
      }
    }
  case OpWriteTD:
    hash := common.BytesToHash(op.Data[:32])
    number := btoi64(op.Data[32:40])
    td := big.NewInt(0).SetBytes(op.Data[40:])
    rawdb.WriteTd(processor.db, hash, number, td)
  case OpWriteReceipts:
    hash := common.BytesToHash(op.Data[:32])
    number := btoi64(op.Data[32:40])
    storageReceipts := []*types.ReceiptForStorage{}
  	if err := rlp.DecodeBytes(op.Data[40:], &storageReceipts); err != nil {
  		log.Error("Invalid receipt array RLP", "hash", hash, "err", err)
  		return nil
  	}
  	receipts := make(types.Receipts, len(storageReceipts))
  	for i, storageReceipt := range storageReceipts {
  		receipts[i] = (*types.Receipt)(storageReceipt)
  	}
    rawdb.WriteReceipts(processor.db, hash, number, receipts)
  case OpDeleteBlock:
    hash := common.BytesToHash(op.Data[:32])
    number := btoi64(op.Data[32:40])
    rawdb.DeleteBlock(processor.db, hash, number)
  }
  return nil
}

func StateUpdate(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) ([]*operation) {
  output := []*operation{&operation{OpSUParent, append(blockRoot.Bytes(), parentRoot.Bytes()...)}}
  for account := range destructs {
    output = append(output, &operation{OpSUDestruct, append(blockRoot.Bytes(), account.Bytes()...)})
  }
  for account, data := range accounts {
    output = append(output, &operation{OpSUAccount, append(append(blockRoot.Bytes(), account.Bytes()...), data...)})
  }
  for account, s := range storage {
    for k, v := range s {
      output = append(output, &operation{OpSUStorage, append(append(append(blockRoot.Bytes(), account.Bytes()...), k...), v...)})
    }
  }
  output := []operation{&operation{OpSUDone, blockRoot.Bytes()}}
  return output
}
func WriteBody(hash common.Hash, number uint64, data []byte) *operation {
  return &operation{
    OpWriteBody,
    append(append(hash, i64tob(number)...), data...)
  }
}
func WriteHeader(hash common.Hash, number uint64, data []byte) *operation {
  return &operation{
    OpWriteHeader,
    append(append(hash, i64tob(number)...), data...)
  }
}
func WriteReceipts(hash common.Hash, number uint64, data []byte) *operation {
  return &operation{
    OpWriteReceipts,
    append(append(hash, i64tob(number)...), data...)
  }
}
func WriteTd(hash common.Hash, number uint64, td *big.Int) *operation {
  return &operation{
    OpWriteTD,
    append(append(hash, i64tob(number)...), td.Bytes()...)
  }
}
func DeleteBlock(hash common.Hash, number uint64) *operation {
  return &operation{
    OpDeleteBlock,
    append(hash, i64tob(number)...)
  }
}
