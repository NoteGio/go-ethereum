package replica

import (
  "bytes"
  "io"
  "io/ioutil"
  "github.com/Shopify/sarama"
  "github.com/openrelayxyz/drumline"
  "math/big"
  "fmt"
  "compress/zlib"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/event"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/log"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/ethereum/go-ethereum/ethdb"
  // "encoding/hex"
  "sync"
  "time"
)

type MsgType byte

const (
  BlockMsg MsgType = iota
  ReceiptMsg
  LogMsg
  TdMsg
)

func compress(data []byte) []byte {
  if len(data) == 0 { return data }
  var b bytes.Buffer
  w := zlib.NewWriter(&b)
  w.Write(data)
  w.Close()
  return b.Bytes()
}

func decompress(data []byte) ([]byte, error) {
  if len(data) == 0 { return data, nil }
  r, err := zlib.NewReader(bytes.NewBuffer(data))
  if err != nil { return []byte{}, err }
  return ioutil.ReadAll(r)
}

type ReceiptMeta struct {
  ContractAddress common.Address
  CumulativeGasUsed uint64
  GasUsed uint64
  LogsBloom types.Bloom
  Status uint64
  logCount uint
  logOffset uint
}

type rlpReceiptMeta struct {
  ContractAddress common.Address
  CumulativeGasUsed uint64
  GasUsed uint64
  LogsBloom []byte
  Status uint64
  LogCount uint
  LogOffset uint
}

func (r *ReceiptMeta) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, rlpReceiptMeta{
    ContractAddress: r.ContractAddress,
    CumulativeGasUsed: r.CumulativeGasUsed,
    GasUsed: r.GasUsed,
    Status: r.Status,
    LogCount: r.logCount,
    LogOffset: r.logOffset,
    LogsBloom: compress(r.LogsBloom.Bytes()),
  })
}

func (r *ReceiptMeta) DecodeRLP(s *rlp.Stream) error {
  var dec rlpReceiptMeta
	err := s.Decode(&dec)
	if err == nil {
		r.ContractAddress, r.CumulativeGasUsed, r.GasUsed, r.Status, r.logCount, r.logOffset = dec.ContractAddress, dec.CumulativeGasUsed, dec.GasUsed, dec.Status, dec.LogCount, dec.LogOffset
    var bloomBytes []byte
    bloomBytes, err = decompress(dec.LogsBloom)
    r.LogsBloom = types.BytesToBloom(bloomBytes)
	}
	return err
}

type ChainEvent struct {
  Block *types.Block
  ReceiptMeta map[common.Hash]*ReceiptMeta
  Logs map[common.Hash][]*types.Log
  Td *big.Int
}

type chainEventProvider interface {
  GetChainEvent(common.Hash, uint64) (core.ChainEvent, error)
  GetBlock(common.Hash) (*types.Block, error)
  GetHeadBlockHash() (common.Hash)
  GetFullChainEvent(ce core.ChainEvent) (*ChainEvent, error)
}

type dbChainEventProvider struct {
  db ethdb.Database
}

func (cep *dbChainEventProvider) GetHeadBlockHash() common.Hash {
  return rawdb.ReadHeadBlockHash(cep.db)
}

func (cep *dbChainEventProvider) GetBlock(h common.Hash) (*types.Block, error) {
  n := *rawdb.ReadHeaderNumber(cep.db, h)
  block := rawdb.ReadBlock(cep.db, h, n)
  if block == nil { return nil, fmt.Errorf("Error retrieving block %#x", h)}
  return block, nil
}
func (cep *dbChainEventProvider) GetChainEvent(h common.Hash, n uint64) (core.ChainEvent, error) {
  block := rawdb.ReadBlock(cep.db, h, n)
  if block == nil { return core.ChainEvent{}, fmt.Errorf("Block %#x missing from database", h)}
  genesisHash := rawdb.ReadCanonicalHash(cep.db, 0)
  chainConfig := rawdb.ReadChainConfig(cep.db, genesisHash)
  receipts := rawdb.ReadReceipts(cep.db, h, n, chainConfig)
  logs := []*types.Log{}
  if receipts != nil {
    // Receipts will be nil if the list is empty, so this is not an error condition
    for _, receipt := range receipts {
      logs = append(logs, receipt.Logs...)
    }
  }
  return core.ChainEvent{Block: block, Hash: block.Hash(), Logs: logs}, nil
}

func (cep *dbChainEventProvider) GetFullChainEvent(ce core.ChainEvent) (*ChainEvent, error) {
  genesisHash := rawdb.ReadCanonicalHash(cep.db, 0)
  chainConfig := rawdb.ReadChainConfig(cep.db, genesisHash)
  receipts := rawdb.ReadReceipts(cep.db, ce.Block.Hash(), ce.Block.NumberU64(), chainConfig)
  td := rawdb.ReadTd(cep.db, ce.Block.Hash(), ce.Block.NumberU64())
  logs := make(map[common.Hash][]*types.Log)
  rmeta := make(map[common.Hash]*ReceiptMeta)
  if receipts != nil {
    for _, receipt := range receipts {
      logs[receipt.TxHash] = receipt.Logs
      rmeta[receipt.TxHash] = &ReceiptMeta{
        ContractAddress: receipt.ContractAddress,
        CumulativeGasUsed: receipt.CumulativeGasUsed,
        GasUsed: receipt.GasUsed,
        Status: receipt.Status,
        LogsBloom: receipt.Bloom,
        logCount: uint(len(receipt.Logs)),
      }
      if len(receipt.Logs) > 0 {
        rmeta[receipt.TxHash].logOffset = receipt.Logs[0].Index
      }
    }
  }
  return &ChainEvent{Block: ce.Block, ReceiptMeta: rmeta, Logs: logs, Td: td}, nil
}

type chainEventMessage struct {
  key []byte
  value []byte
}

func (chainEvent *ChainEvent) getMessages() ([]chainEventMessage) {
  blockBytes, err := rlp.EncodeToBytes(chainEvent.Block)
  if err != nil { panic(err.Error()) }
  tdBytes, err := rlp.EncodeToBytes(chainEvent.Td)
  if err != nil { panic(err.Error()) }
  result := []chainEventMessage{
    chainEventMessage{
      key: append([]byte{byte(BlockMsg)}, chainEvent.Block.Hash().Bytes()...),
      value: blockBytes,
    },
    chainEventMessage{
      key: append([]byte{byte(TdMsg)}, chainEvent.Block.Hash().Bytes()...),
      value: tdBytes,
    },
  }
  receiptKeyPrefix := append([]byte{byte(ReceiptMsg)}, chainEvent.Block.Hash().Bytes()...)
  for _, transaction := range chainEvent.Block.Transactions() {
    rmetabytes, err := rlp.EncodeToBytes(chainEvent.ReceiptMeta[transaction.Hash()])
    if err != nil { panic(err.Error()) }
    txkey := append(receiptKeyPrefix, transaction.Hash().Bytes()...)
    result = append(result, chainEventMessage{
      key: txkey,
      value: rmetabytes,
    })
    logKeyPrefix := append([]byte{byte(LogMsg)}, chainEvent.Block.Hash().Bytes()...)
    for _, logRecord := range chainEvent.Logs[transaction.Hash()] {
      logNumberRlp, err := rlp.EncodeToBytes(big.NewInt(int64(logRecord.Index)))
      if err != nil { panic(err.Error()) }
      logBytes, err := rlp.EncodeToBytes(rlpLog{logRecord, logRecord.BlockNumber, logRecord.TxHash, logRecord.TxIndex})
      if err != nil { panic(err.Error()) }
      logKey := make([]byte, len(logKeyPrefix) + len(logNumberRlp))
      copy(logKey, append(logKeyPrefix, logNumberRlp...))
      result = append(result, chainEventMessage{
        key: logKey,
        value: logBytes,
      })
    }
  }
  return result
}

type chainEventTracker struct {
  topic string
  chainEvents map[common.Hash]*ChainEvent
  receiptCounter map[common.Hash]int
  logCounter map[common.Hash]int
  earlyReceipts map[common.Hash]map[common.Hash]*ReceiptMeta
  earlyLogs map[common.Hash]map[common.Hash]map[uint]*types.Log
  earlyTd map[common.Hash]*big.Int
  finished map[common.Hash]bool
  oldFinished map[common.Hash]bool
  skipped map[common.Hash]bool
  finishedLimit int
  lastEmittedBlock common.Hash
  pendingEmits map[common.Hash]map[common.Hash]struct{}
  pendingHashes map[common.Hash]struct{}
  // IF needed, break this out by block, tracking the smallest and largest
  // offset for each block. That way on resumption, we can resume from the
  // smallest offset from the last emitted block, and start emitting once we
  // reach the largest offset of the last emitted block. This may not be needed
  // if the most recent changes pan out.
  chainEventPartitions map[int32]int64
  blockTime map[common.Hash]time.Time
}

func (cet *chainEventTracker) report() {
  log.Debug("Chain Event Report",
    "chainEventCount", len(cet.chainEvents),
    "receiptCounters", len(cet.receiptCounter),
    "logCounters", len(cet.logCounter),
    "earlyReceiptsMaps", len(cet.earlyReceipts),
    "earlyLogsMaps", len(cet.earlyLogs),
    "earlyTdMaps", len(cet.earlyTd),
    "finished", len(cet.finished),
    "oldFinished", len(cet.oldFinished),
    "skipped", len(cet.skipped),
    "pendingEmits", len(cet.pendingEmits),
    "pendingHashes", len(cet.pendingHashes),
  )
}

func (cet *chainEventTracker) setBlockTime(h common.Hash) {
  if _, ok := cet.blockTime[h]; !ok { cet.blockTime[h] = time.Now() }
}

func (cet *chainEventTracker) HandleMessage(key, value []byte, partition int32, offset int64) (*ChainEvents, error) {
  cet.chainEventPartitions[partition] = offset
  var blockhash common.Hash
  switch MsgType(key[0]) {
  case BlockMsg:
    block := &types.Block{}
    if err := rlp.DecodeBytes(value, block); err != nil {
      return nil, fmt.Errorf("Error decoding block")
    }
    blockhash = block.Hash()
    if sentHash := common.BytesToHash(key[1:]); blockhash != sentHash {
      log.Warn("blockhash != senthash", "calculated", blockhash, "sent", sentHash)
    }
    if _, ok := cet.chainEvents[blockhash]; ok || cet.finished[blockhash] || cet.oldFinished[blockhash]  { return nil, nil } // We've already seen this block. Ignore
    cet.setBlockTime(blockhash)
    if ce, ok := cet.chainEvents[cet.lastEmittedBlock]; ok {
      if block.NumberU64() + uint64(cet.finishedLimit) < ce.Block.NumberU64() {
        log.Warn("Old block detected. Ignoring.", "number", block.NumberU64(), "hash", block.Hash())
        cet.skipped[blockhash] = true
        delete(cet.receiptCounter, blockhash)
        delete(cet.logCounter, blockhash)
        delete(cet.earlyReceipts, blockhash)
        delete(cet.earlyLogs, blockhash)
        delete(cet.earlyTd, blockhash)
      }
    }
    // cet.finished[block.Hash()] = false // Explicitly setting to false ensures it will be garbage collected if we never see the whole block
    cet.chainEvents[blockhash] = &ChainEvent{
      Block: block,
      ReceiptMeta: make(map[common.Hash]*ReceiptMeta),
      Logs: make(map[common.Hash][]*types.Log),
    }
    cet.receiptCounter[blockhash] = len(block.Transactions())
    if earlyReceipts, ok := cet.earlyReceipts[blockhash]; ok {
      for txhash, rmeta := range earlyReceipts {
        cet.HandleReceipt(block.Hash(), txhash, rmeta)
      }
      delete(cet.earlyReceipts, blockhash)
    }
    if td, ok := cet.earlyTd[blockhash]; ok {
      cet.chainEvents[blockhash].Td = td
      delete(cet.earlyTd, blockhash)
    }
  case TdMsg:
    blockhash = common.BytesToHash(key[1:33])
    if cet.skipped[blockhash] || cet.finished[blockhash] || cet.oldFinished[blockhash] { return nil, nil } // We're ignoring this block
    td := big.NewInt(0)
    if err := rlp.DecodeBytes(value, td); err != nil {
      return nil, fmt.Errorf("Error decoding td")
    }
    cet.setBlockTime(blockhash)
    if _, ok := cet.chainEvents[blockhash]; ok {
      cet.chainEvents[blockhash].Td = td
    } else {
      cet.earlyTd[blockhash] = td
      return nil, nil
    }
  case ReceiptMsg:
    blockhash = common.BytesToHash(key[1:33])
    if cet.skipped[blockhash] || cet.finished[blockhash] || cet.oldFinished[blockhash] { return nil, nil } // We're ignoring this block
    txhash := common.BytesToHash(key[33:65])
    rmeta := &ReceiptMeta{}
    if err := rlp.DecodeBytes(value, rmeta); err != nil {
      return nil, fmt.Errorf("Error decoding receipt: %v", err.Error())
    }
    if _, ok := cet.chainEvents[blockhash]; !ok {
      if _, ok := cet.earlyReceipts[blockhash]; !ok {
        cet.setBlockTime(blockhash)
        cet.earlyReceipts[blockhash] = make(map[common.Hash]*ReceiptMeta)
      }
      cet.earlyReceipts[blockhash][txhash] = rmeta
      return nil, nil
    }
    cet.HandleReceipt(blockhash, txhash, rmeta)
  case LogMsg:
    blockhash = common.BytesToHash(key[1:33])
    if cet.skipped[blockhash] || cet.finished[blockhash] || cet.oldFinished[blockhash] { return nil, nil } // We're ignoring this block
    logRlp := &rlpLog{}
    var logIndex big.Int
    err := rlp.DecodeBytes(key[33:], &logIndex)
    if err != nil { return nil, fmt.Errorf("Error decoding log key: %v", err.Error())}
    if err := rlp.DecodeBytes(value, logRlp); err != nil {
      return nil, fmt.Errorf("Error decoding log: %v", err.Error())
    }
    logRecord := logRlp.Log
    logRecord.BlockNumber = logRlp.BlockNumber
    logRecord.TxHash = logRlp.TxHash
    logRecord.TxIndex = logRlp.TxIndex
    logRecord.BlockHash = blockhash
    logRecord.Index = uint(logIndex.Int64())
    txhash := logRlp.TxHash
    if _, ok := cet.chainEvents[blockhash]; !ok {
      cet.setBlockTime(blockhash)
      cet.HandleEarlyLog(blockhash, txhash, logRecord)
      return nil, nil // Log is early, nothing else to do
    }
    if _, ok := cet.chainEvents[blockhash].ReceiptMeta[txhash]; !ok {
      cet.HandleEarlyLog(blockhash, txhash, logRecord)
      return nil, nil // Log is early, nothing else to do
    }
    rmeta := cet.chainEvents[blockhash].ReceiptMeta[txhash]
    if logRecord := cet.chainEvents[blockhash].Logs[txhash][logRecord.Index - rmeta.logOffset]; logRecord != nil {
      return nil, nil // Log is already present, nothing else to do
    }
    cet.chainEvents[blockhash].Logs[txhash][logRecord.Index - rmeta.logOffset] = logRecord
    cet.logCounter[blockhash]--
  }
  if !(cet.finished[blockhash] || cet.oldFinished[blockhash]) && cet.logCounter[blockhash] == 0 && cet.receiptCounter[blockhash] == 0 {
    // Last message of block. Emit the chain event on appropriate feeds.
    ce := cet.chainEvents[blockhash]
    if ce == nil || ce.Block.Hash() == cet.lastEmittedBlock {
      log.Debug("Waiting to emit (no block yet)", "block", blockhash)
      return nil, nil
    }
    if ce.Td == nil {
      // If Td is not set yet, we need to wait for it.
      log.Debug("Waiting to emit (no td yet)", "block", blockhash)
      return nil, nil
    }
    log.Debug("Proceeding to emit", "block", blockhash)
    return cet.HandleReadyCE(blockhash)
  }
  if !(cet.finished[blockhash] || cet.oldFinished[blockhash]) {
    log.Debug("Waiting to emit", "block", blockhash, "logsRemaining", cet.logCounter[blockhash], "receiptsRemaining", cet.receiptCounter[blockhash])
  }

  return nil, nil
}

func (cet *chainEventTracker) finishSiblings(ce *ChainEvent) {
  pendingSiblings, ok := cet.pendingEmits[ce.Block.ParentHash()]
  if !ok { return } // Wasn't pending a parent
  for hash := range pendingSiblings {
    if hash == ce.Block.Hash() { continue }
    cet.finishPendingChildren(hash)
  }
}

func (cet *chainEventTracker) finishPendingChildren(hash common.Hash) {
  pendingChildren, ok := cet.pendingEmits[hash]
  cet.finished[hash] = true
  delete(cet.pendingHashes, hash)
  delete(cet.pendingEmits, hash)
  delete(cet.logCounter, hash)
  delete(cet.receiptCounter, hash)
  if !ok { return } // No children
  for child := range pendingChildren {
    log.Debug("Marking block as finished (uncled)")
    cet.finished[child] = true
    cet.finishPendingChildren(child)
  }
}

func (cet *chainEventTracker) HandleReadyCE(blockhash common.Hash) (*ChainEvents, error) {
  ce := cet.chainEvents[blockhash]
  if ce == nil {
    panic(fmt.Sprintf("Trying to emit missing block %#x", blockhash[:]))
  }
  if ce.Block.ParentHash() == cet.lastEmittedBlock || cet.lastEmittedBlock == (common.Hash{}) {
    log.Debug("Emitting block without reorg", "block", blockhash, "parent", cet.lastEmittedBlock, "number", ce.Block.NumberU64())
    return cet.PrepareEmit([]*ChainEvent{ce}, []*ChainEvent{})
  }
  if bh := ce.Block.ParentHash(); !(cet.finished[bh] || cet.oldFinished[bh]) {
    // The parent has not been emitted, save for later.

    log.Debug("Holding until parent is emitted", "finished", cet.finished[bh], "oldFinished", cet.oldFinished[bh], "block", blockhash, "number", ce.Block.NumberU64(), "parent", bh, "lastEmitted", cet.lastEmittedBlock)
    ph := ce.Block.ParentHash()
    if _, ok := cet.pendingEmits[ph]; !ok {
      cet.pendingEmits[ph] = make(map[common.Hash]struct{})
    }
    cet.pendingEmits[ph][blockhash] = struct{}{}
    cet.pendingHashes[blockhash] = struct{}{}
    return nil, nil
  }
  lastce := cet.chainEvents[cet.lastEmittedBlock]
  if ce.Td.Cmp(lastce.Td) <= 0 {
    log.Debug("Holding new block because Td is low", "block", blockhash)
    // Don't emit reorgs until there's a block with a higher difficulty
    cet.finished[blockhash] = true
    delete(cet.logCounter, blockhash)
    delete(cet.receiptCounter, blockhash)
    if child, ok := cet.getPendingChild(blockhash); ok {
      // This block's child is already pending, process it instead
      return cet.HandleReadyCE(child)
    }
    return nil, nil
  }
  revertCEs, newCEs, err := cet.findCommonAncestor(ce, lastce)
  if err != nil {
    log.Error("Error finding common ancestor", "newBlock", ce.Block.Hash(), "oldBlock", cet.lastEmittedBlock, "error", err)
    return nil, err
  }
  if len(newCEs) > 0 {
    return cet.PrepareEmit(newCEs, revertCEs)
  }
  return nil, nil
}

type ChainEvents struct {
  Reverted []*ChainEvent
  New []*ChainEvent
  Partitions map[int32]int64
}

func (cet *chainEventTracker) getPendingChild(hash common.Hash) (common.Hash, bool) {
  log.Debug("Getting best child", "block", hash)
  children, ok := cet.pendingEmits[hash]
  if !ok { return common.Hash{}, ok }
  max := new(big.Int)
  var maxChild common.Hash
  for child := range children {
    td := cet.getPendingChainDifficulty(child)
    if max.Cmp(td) < 0 {
      max = td
      maxChild = child
    }
  }
  log.Debug("Getting best child", "block", hash, "child", maxChild)
  return maxChild, true
}

func (cet *chainEventTracker) getPendingChainDifficulty(hash common.Hash) (*big.Int) {
  log.Debug("Getting pending chain difficulty", "block", hash, "pending", cet.pendingEmits[hash])
  children, ok := cet.pendingEmits[hash]
  if !ok { return cet.chainEvents[hash].Td }
  max := new(big.Int)
  for child := range children {
    td := cet.getPendingChainDifficulty(child)
    if max.Cmp(td) < 0 {
      max = td
    }
  }
  return max
}

func (cet *chainEventTracker) PrepareEmit(new, revert []*ChainEvent) (*ChainEvents, error) {
  partitions := make(map[int32]int64)
  for k, v := range cet.chainEventPartitions {
    partitions[k] = v
  }
  if len(new) > 0 {
    cet.lastEmittedBlock = new[len(new) - 1].Block.Hash()
    for _, ce := range new {
      hash := ce.Block.Hash()
      log.Debug("Marking block as finished (new)", "blockhash", hash, "time", time.Since(cet.blockTime[hash]))
      cet.finishSiblings(ce)
      cet.finished[hash] = true
      delete(cet.logCounter, hash)
      delete(cet.receiptCounter, hash)
    }
  }
  for hash, ok := cet.getPendingChild(cet.lastEmittedBlock); ok; hash, ok = cet.getPendingChild(cet.lastEmittedBlock) {
    ce := cet.chainEvents[hash]
    if ce == nil { panic(fmt.Sprintf("Could not find block for %#x", hash[:]))}
    new = append(new, ce)
    cet.finishSiblings(ce)
    delete(cet.pendingEmits, cet.lastEmittedBlock)
    delete(cet.pendingHashes, hash)
    cet.lastEmittedBlock = hash
    log.Debug("Marking block as finished (pending)", "blockhash", hash, "parent", ce.Block.ParentHash(), "time", time.Since(cet.blockTime[hash]))
    cet.finished[hash] = true
  }
  if len(cet.finished) >= cet.finishedLimit {
    cet.report()
    for bh := range cet.oldFinished {
      if _, ok := cet.pendingEmits[bh]; !ok {
        if _, ok := cet.pendingHashes[bh]; !ok {
          // Don't delete events from chainEvents if they're still referenced
          // in pendingEmits
          delete(cet.chainEvents, bh)
          delete(cet.blockTime, bh)
        }
      }
    }
    cet.oldFinished = cet.finished
    cet.finished = cet.oldFinished
    cet.finished = make(map[common.Hash]bool)
  }
  return &ChainEvents{
    Reverted: revert,
    New: new,
    Partitions: partitions,
  }, nil
}

func (cet *chainEventTracker) findCommonAncestor(newHead, oldHead *ChainEvent) ([]*ChainEvent, []*ChainEvent, error) {
  reverted := []*ChainEvent{}
  newBlocks := []*ChainEvent{newHead}
  if oldHead == nil {
    return reverted, newBlocks, nil
  }
  for {
    for newHead.Block.NumberU64() > oldHead.Block.NumberU64() + 1 {
      parentHash := newHead.Block.ParentHash()
      newHead, _ = cet.chainEvents[parentHash]
      if newHead == nil {
        return reverted, newBlocks, fmt.Errorf("Block %#x missing from history", parentHash)
      }
      newBlocks = append([]*ChainEvent{newHead}, newBlocks...)
    }
    if(oldHead.Block.Hash() == newHead.Block.ParentHash())  {
      return reverted, newBlocks, nil
    }
    reverted = append([]*ChainEvent{oldHead}, reverted...)
    oldHead, _ = cet.chainEvents[oldHead.Block.ParentHash()]
    if oldHead == nil {
      return reverted, newBlocks, fmt.Errorf("Reached genesis without finding common ancestor")
    }
  }
}

func (cet *chainEventTracker) HandleEarlyLog(blockhash, txhash common.Hash, logRecord *types.Log) {
  if _, ok := cet.earlyLogs[blockhash]; !ok {
    cet.earlyLogs[blockhash] = make(map[common.Hash]map[uint]*types.Log)
  }
  if _, ok := cet.earlyLogs[blockhash][txhash]; !ok {
    cet.earlyLogs[blockhash][txhash] = make(map[uint]*types.Log)
  }
  cet.earlyLogs[blockhash][txhash][logRecord.Index] = logRecord
}

func (cet *chainEventTracker) HandleReceipt(blockhash, txhash common.Hash, rmeta *ReceiptMeta) {
  if _, ok := cet.chainEvents[blockhash].ReceiptMeta[txhash]; ok { return } // We already have this receipt
  cet.chainEvents[blockhash].ReceiptMeta[txhash] = rmeta
  cet.chainEvents[blockhash].Logs[txhash] = make([]*types.Log, rmeta.logCount)
  cet.logCounter[blockhash] += int(rmeta.logCount)
  if earlyLogs, ok := cet.earlyLogs[blockhash]; ok {
    if logs, ok := earlyLogs[txhash]; ok {
      for _, log := range logs {
        cet.chainEvents[blockhash].Logs[txhash][log.Index - rmeta.logOffset] = log
        cet.logCounter[blockhash]--
      }
    }
    delete(earlyLogs, txhash)
    if len(earlyLogs) == 0 {
      delete(cet.earlyLogs, blockhash) // This was the last receipt with early logs, so clean up
    }
  }
  cet.receiptCounter[blockhash]--
}

type KafkaEventProducer struct {
  producer sarama.AsyncProducer
  topic string
  closed bool
  cep chainEventProvider
}

func (producer *KafkaEventProducer) Close() {
  producer.closed = true
  producer.producer.Close()
}

type rlpLog struct {
  Log *types.Log
	BlockNumber uint64 `json:"blockNumber"`
	TxHash common.Hash `json:"transactionHash" gencodec:"required"`
	TxIndex uint `json:"transactionIndex" gencodec:"required"`
}

func (producer *KafkaEventProducer) Emit(chainEvent core.ChainEvent) error {
  ce, err := producer.cep.GetFullChainEvent(chainEvent)
  if err != nil { return err }
  events := ce.getMessages()
  inflight := 0
  for _, msg := range events {
    // Send events to Kafka or get errors from previous sends
    SEND_LOOP:
    for {
      select {
      case producer.producer.Input() <- &sarama.ProducerMessage{Topic: producer.topic, Key: sarama.ByteEncoder(msg.key), Value: sarama.ByteEncoder(msg.value)}:
        inflight++
        break SEND_LOOP
      case <-producer.producer.Successes():
        inflight--
      case err := <-producer.producer.Errors():
        return err
      }
    }
  }
  // We have `inflight` messages left to send for this event. Make sure we
  // don't get any errors.
  for inflight > 0 {
    select {
    case err := <-producer.producer.Errors():
      return err
    case <-producer.producer.Successes():
      inflight--
    }
  }
  return nil
}

type ChainEventSubscriber interface {
  SubscribeChainEvent(chan<- core.ChainEvent) event.Subscription
}

func (producer *KafkaEventProducer) ReprocessEvents(ceCh chan<- core.ChainEvent, n int) error {
  hash := producer.cep.GetHeadBlockHash()
  block, err := producer.cep.GetBlock(hash)
  if err != nil { return err }
  events := make([]core.ChainEvent, n)
  event, err := producer.cep.GetChainEvent(block.Hash(), block.NumberU64())
  events[n-1] = event
  if err != nil { return err }
  for i := n - 1; i > 0 && events[i].Block.NumberU64() > 0; i-- {
    events[i-1], err =  producer.cep.GetChainEvent(events[i].Block.ParentHash(), events[i].Block.NumberU64() - 1)
    if err != nil { return err }
  }
  for _, ce := range events {
    ceCh <- ce
  }
  return nil
}

func (producer *KafkaEventProducer) RelayEvents(bc ChainEventSubscriber) {
  go func() {
    ceCh := make(chan core.ChainEvent, 100)
    go producer.ReprocessEvents(ceCh, 10)
    subscription := bc.SubscribeChainEvent(ceCh)
    recentHashes := make(map[common.Hash]struct{})
    olderHashes := make(map[common.Hash]struct{})
    lastEmitted := common.Hash{}
    setTest := func (k common.Hash) bool {
      if _, ok := recentHashes[k]; ok { return true }
      _, ok := olderHashes[k]
      return ok
    }
    setAdd := func(k common.Hash) {
      recentHashes[k] = struct{}{}
      if len(recentHashes) > 128 {
        olderHashes = recentHashes
        recentHashes = make(map[common.Hash]struct{})
      }
      lastEmitted = k
    }
    first := true
    for ce := range ceCh {
      start := time.Now()
      if first || setTest(ce.Block.ParentHash()) {
        if err := producer.Emit(ce); err != nil {
          log.Error("Failed to produce event log: %v", err.Error())
        }
        setAdd(ce.Hash)
        first = false
      } else {
        newBlocks, err := producer.getNewBlockAncestors(ce, lastEmitted)
        if err != nil {
          log.Error("Failed to find new block ancestors", "block", ce.Hash, "parent", ce.Block.ParentHash(), "le", lastEmitted, "error", err)
          continue
        }
        for _, pce := range newBlocks {
          if !setTest(pce.Hash) {
            if err := producer.Emit(pce); err != nil {
              log.Error("Failed to produce event log: %v", err.Error())
            }
            setAdd(pce.Hash)
          }
        }
      }
      log.Debug("Emitted chain events", "time", time.Since(start), "block", ce.Block.Hash())
    }
    log.Warn("Event emitter shutting down")
    subscription.Unsubscribe()
  }()
}

func (producer *KafkaEventProducer) getNewBlockAncestors(ce core.ChainEvent, h common.Hash) ([]core.ChainEvent, error) {
  var err error
  oldBlock, err := producer.cep.GetBlock(h)
  if err != nil { return nil, err }
  newBlocks := []core.ChainEvent{ce}
  for {
    if oldBlock.Hash() == ce.Hash {
      // If we have a match, we're done, return them.
      return newBlocks, nil
    } else if ce.Block.NumberU64() <= oldBlock.NumberU64() {
      // oldBlock has a higher or equal number, but the blocks aren't equal.
      // Walk back the oldBlock
      oldBlock, err = producer.cep.GetBlock(oldBlock.ParentHash())
      if err != nil { return nil, err }
    } else if ce.Block.NumberU64() > oldBlock.NumberU64() {
      // the new block has a higher number, walk it back
      ce, err = producer.cep.GetChainEvent(ce.Block.ParentHash(), ce.Block.NumberU64() - 1)
      if err != nil { return nil, err }
      newBlocks = append([]core.ChainEvent{ce}, newBlocks...)
    }
  }
}

func NewKafkaEventProducerFromURLs(brokerURL, topic string, db ethdb.Database) (EventProducer, error) {
  configEntries := make(map[string]*string)
  brokers, config := cdc.ParseKafkaURL(brokerURL)
  if err := cdc.CreateTopicIfDoesNotExist(brokerURL, topic, -1, configEntries); err != nil {
    return nil, err
  }
  config.Producer.Return.Successes=true
  producer, err := sarama.NewAsyncProducer(brokers, config)
  if err != nil {
    return nil, err
  }
  return NewKafkaEventProducer(producer, topic, &dbChainEventProvider{db}), nil
}

func NewKafkaEventProducer(producer sarama.AsyncProducer, topic string, cep chainEventProvider) (EventProducer) {
  return &KafkaEventProducer{producer, topic, false, cep}
}

type KafkaEventConsumer struct {
  cet *chainEventTracker
  startingOffsets map[int32]int64
  consumers []sarama.PartitionConsumer
  topic string
  ready chan struct{}
  feed event.Feed
}

func (consumer *KafkaEventConsumer) SubscribeChainEvents(ch chan<- *ChainEvents) event.Subscription {
  return consumer.feed.Subscribe(ch)
}

func (consumer *KafkaEventConsumer) Ready() chan struct{} {
  return consumer.ready
}

func (consumer *KafkaEventConsumer) Close() {
  for _, c := range consumer.consumers {
    c.Close()
  }
}

type OffsetHash struct {
  Offset int64
  Hash common.Hash
}

func (consumer *KafkaEventConsumer) Start() {
  messages := make(chan *sarama.ConsumerMessage, 512) // 512 is totally arbitrary. Tune this?
  dl := drumline.NewDrumline(4000)
  var readyWg, warmupWg sync.WaitGroup
  for i, partitionConsumer := range consumer.consumers {
    readyWg.Add(1)
    warmupWg.Add(1)
    dl.Add(i)
    go func(readyWg, warmupWg *sync.WaitGroup, partitionConsumer sarama.PartitionConsumer, i int) {
      var once sync.Once
      warm := false
      for input := range partitionConsumer.Messages() {
        if !warm && input.Offset >= consumer.startingOffsets[input.Partition] {
          // Once we're caught up with the startup offsets, wait until the
          // other partition consumers are too before continuing.
          warmupWg.Done()
          warmupWg.Wait()
          warm = true
        }
        if consumer.ready != nil {
          if partitionConsumer.HighWaterMarkOffset() - input.Offset <= 1 {
            // Once we're caught up with the high watermark, let the ready
            // channel know
            once.Do(readyWg.Done)
          }
          dl.Step(i)
        }
        // Aggregate all of the messages onto a single channel
        messages <- input
      }
    }(&readyWg, &warmupWg, partitionConsumer, i)
  }
  var readych chan time.Time
  readyWaiter := func() <-chan time.Time  {
    // If readych isn't ready to receive, use it as the sender, eliminating any
    // possibility that this case will trigger, and avoiding the creation of
    // any timer objects that will have to get cleaned up when we don't need
    // them.
    if readych == nil { return readych }
    // Only if the readych is ready to receive should this return a timer, as
    // the timer will have to be created, run its course, and get cleaned up,
    // which adds overhead
    return time.After(time.Second)
  }

  go func(wg *sync.WaitGroup) {
    // Wait until all partition consumers are up to the high water mark and alert the ready channel
    wg.Wait()
    readych = make(chan time.Time)
    <-readych
    readych = nil
    consumer.ready <- struct{}{}
    consumer.ready = nil
    dl.Close()
  }(&readyWg)
  go func() {
    initialLEB := consumer.cet.lastEmittedBlock
    for {
      select {
      case input, ok := <-messages:
        if !ok { return }
        // log.Debug("Handling message", "offset", input.Offset, "partition", input.Partition, "starting", consumer.startingOffsets[input.Partition])
        chainEvents, err := consumer.cet.HandleMessage(input.Key, input.Value, input.Partition, input.Offset)
        if input.Offset < consumer.startingOffsets[input.Partition] {
          log.Debug("Offset < starting offset", "offset", input.Offset, "starting", consumer.startingOffsets[input.Partition])
          // If input.Offset < partition.StartingOffset, we're just populating
          // the CET, so we don't need to emit this or worry about errors
          consumer.cet.lastEmittedBlock = initialLEB // Set lastEmittedBlock back so it won't get hung up if it doesn't have the whole next block
          continue
        }
        if err != nil {
          log.Error("Error processing input:", "err", err, "key", input.Key, "msg", input.Value, "part", input.Partition, "offset", input.Offset)
          continue
        }
        if chainEvents != nil {
          consumer.feed.Send(chainEvents)
        }
      case <-dl.Reset(5 * time.Second):
        // Reset the drumline if we got no messages at all in a 5 second
        // period. This will be a no-op if the drumline is closed. Resetting if
        // we don't get any messages for five second should be safe, as the
        // purpose of the drumline here is to ensure no single partition gets
        // too far ahead of the others; if there are no messages being produced
        // by any of them, either there are no messages at all, or there are no
        // messages because the drumline has gotten streteched too far apart in
        // the course of normal operation, and it should be reset.
        log.Debug("Drumline reset")
      case v := <- readyWaiter():
        // readyWaiter() will wait 1 second if readych is ready to receive ,
        // which will trigger a message to get sent on consumer.ready. This
        // should only trigger when we are totally caught up processing all the
        // messages currently available from Kafka. Before we reach the high
        // watermark and after this has triggered once, readyWaiter() will be a
        // nil channel with no significant resource consumption.
        select {
        case readych <- v:
        default:
        }
      }
    }
  }()
}


func NewKafkaEventConsumerFromURLs(brokerURL, topic string, lastEmittedBlock common.Hash, offsets map[int32]int64, rollback int64) (EventConsumer, error) {
  brokers, config := cdc.ParseKafkaURL(brokerURL)
  if err := cdc.CreateTopicIfDoesNotExist(brokerURL, topic, -1, nil); err != nil {
    return nil, err
  }
  config.Version = sarama.V2_1_0_0
  client, err := sarama.NewClient(brokers, config)
  if err != nil { return nil, err }
  consumer, err := sarama.NewConsumerFromClient(client)
  if err != nil { return nil, err }

  partitions, err := consumer.Partitions(topic)
  if err != nil { return nil, err }

  partitionConsumers := make([]sarama.PartitionConsumer, len(partitions))
  startingOffsets := make(map[int32]int64)
  for i, part := range partitions {
    offset, ok := offsets[part]
    var startOffset int64
    if !ok {
      offset = sarama.OffsetOldest
      startOffset = offset
    } else {
      startOffset = offset - rollback
    }
    startingOffsets[part] = startOffset
    pc, err := consumer.ConsumePartition(topic, part, startOffset)
    if err != nil {
      // We may not have been able to roll back `rollback` messages, so just
      // try with the provided offset
      startingOffsets[part] = offset
      pc, err = consumer.ConsumePartition(topic, part, offset)
      if err != nil { return nil, err }
    }
    partitionConsumers[i] = pc
  }
  log.Info("Start offsets", "offsets", startingOffsets)

  return &KafkaEventConsumer{
    cet: &chainEventTracker{
      topic: topic,
      chainEvents: make(map[common.Hash]*ChainEvent),
      receiptCounter: make(map[common.Hash]int),
      logCounter: make(map[common.Hash]int),
      earlyReceipts: make(map[common.Hash]map[common.Hash]*ReceiptMeta),
      earlyLogs: make(map[common.Hash]map[common.Hash]map[uint]*types.Log),
      earlyTd: make(map[common.Hash]*big.Int),
      finished: make(map[common.Hash]bool),
      oldFinished: make(map[common.Hash]bool),
      skipped: make(map[common.Hash]bool),
      finishedLimit: 128,
      lastEmittedBlock: lastEmittedBlock,
      pendingEmits: make(map[common.Hash]map[common.Hash]struct{}),
      pendingHashes: make(map[common.Hash]struct{}),
      chainEventPartitions: offsets,
      blockTime: make(map[common.Hash]time.Time),
    },

    startingOffsets: startingOffsets,
    consumers: partitionConsumers,
    topic: topic,
    ready: make(chan struct{}),
  }, nil
}
