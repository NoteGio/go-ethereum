package replica

import (
  "github.com/Shopify/sarama"
  "fmt"
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
)

const (
  BlockMsg = byte(0)
  LogMsg = byte(1)
  EmitMsg = byte(2)
)

type chainEventProvider interface {
  GetChainEvent(common.Hash, uint64) (core.ChainEvent, error)
  GetBlock(common.Hash) (*types.Block, error)
  GetHeadBlockHash() (common.Hash)
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


type KafkaEventProducer struct {
  producer sarama.SyncProducer
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
	BlockHash common.Hash `json:"blockHash"`
	Index uint `json:"logIndex" gencodec:"required"`
}

func (producer *KafkaEventProducer) getMessages(chainEvent core.ChainEvent) ([][]byte, error) {
  blockBytes, err := rlp.EncodeToBytes(chainEvent.Block)
  if err != nil { return nil, err }
  result := [][]byte{append([]byte{BlockMsg}, blockBytes...)}
  for _, logRecord := range chainEvent.Logs {
    logBytes, err := rlp.EncodeToBytes(rlpLog{logRecord, logRecord.BlockNumber, logRecord.TxHash, logRecord.TxIndex, logRecord.BlockHash, logRecord.Index})
    if err != nil { return result, err }
    result = append(result, append([]byte{LogMsg}, logBytes...))
  }
  result = append(result, append([]byte{EmitMsg}, chainEvent.Hash[:]...))
  return result, nil
}

func (producer *KafkaEventProducer) Emit(chainEvent core.ChainEvent) error {
  events, err := producer.getMessages(chainEvent)
  if err != nil { return err }
  for _, msg := range events {
    if _, _, err = producer.producer.SendMessage(&sarama.ProducerMessage{Topic: producer.topic, Value: sarama.ByteEncoder(msg)}); err != nil { return err }
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
  events[n-1], err = producer.cep.GetChainEvent(block.Hash(), block.NumberU64())
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
  if err := cdc.CreateTopicIfDoesNotExist(brokerURL, topic, 1, configEntries); err != nil {
    return nil, err
  }
  config.Producer.Return.Successes=true
  producer, err := sarama.NewSyncProducer(brokers, config)
  if err != nil {
    return nil, err
  }
  return NewKafkaEventProducer(producer, topic, &dbChainEventProvider{db}), nil
}

func NewKafkaEventProducer(producer sarama.SyncProducer, topic string, cep chainEventProvider) (EventProducer) {
  return &KafkaEventProducer{producer, topic, false, cep}
}

type KafkaEventConsumer struct {
  recoverySize int
  logsFeed event.Feed
  removedLogsFeed event.Feed
  chainFeed event.Feed
  chainHeadFeed event.Feed
  chainSideFeed event.Feed
  offsetFeed event.Feed
  startingOffset int64
  consumer sarama.PartitionConsumer
  oldMap map[common.Hash]*core.ChainEvent
  currentMap map[common.Hash]*core.ChainEvent
  topic string
  ready chan struct{}
  lastEmittedBlock common.Hash
  latestEmitOffset OffsetHash
}

func (consumer *KafkaEventConsumer) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
  return consumer.logsFeed.Subscribe(ch)
}
func (consumer *KafkaEventConsumer) SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription {
  return consumer.removedLogsFeed.Subscribe(ch)
}
func (consumer *KafkaEventConsumer) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
  return consumer.chainFeed.Subscribe(ch)
}
func (consumer *KafkaEventConsumer) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
  return consumer.chainHeadFeed.Subscribe(ch)
}
func (consumer *KafkaEventConsumer) SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription {
  return consumer.chainSideFeed.Subscribe(ch)
}
func (consumer *KafkaEventConsumer) SubscribeOffsets(ch chan<- OffsetHash) event.Subscription {
  return consumer.offsetFeed.Subscribe(ch)
}

func (consumer *KafkaEventConsumer) processEvent(msgType byte, msg []byte) error {
  if msgType == BlockMsg {
    // Message contains the block. Set up a ChainEvent for this block
    block := &types.Block{}
    if err := rlp.DecodeBytes(msg, block); err != nil {
      return fmt.Errorf("Error decoding block")
    }
    hash := block.Hash()
    if _, ok := consumer.currentMap[hash]; !ok {
      // First time we've seen the block.
      consumer.currentMap[hash] = &core.ChainEvent{Block: block, Hash: hash, Logs: []*types.Log{}}
    }
  } else if msgType == LogMsg{
    // Message contains a log. Add it to the chain event for the block.
    logRlp := &rlpLog{}
    if err := rlp.DecodeBytes(msg, logRlp); err != nil {
      return fmt.Errorf("Error decoding log")
    }
    logRecord := logRlp.Log
    logRecord.BlockNumber = logRlp.BlockNumber
    logRecord.TxHash = logRlp.TxHash
    logRecord.TxIndex = logRlp.TxIndex
    logRecord.BlockHash = logRlp.BlockHash
    logRecord.Index = logRlp.Index
    if _, ok := consumer.currentMap[logRecord.BlockHash]; !ok {
      if ce, ok := consumer.oldMap[logRecord.BlockHash]; ok {
        consumer.currentMap[logRecord.BlockHash] = ce
      } else {
        return fmt.Errorf("Received log for unknown block %#x", logRecord.BlockHash[:])
      }
    }
    for _, l := range consumer.currentMap[logRecord.BlockHash].Logs {
      // TODO: Consider some separate map for faster lookups.  Not an
      // immediate concern, as block log counts are in the low hundreds,
      // and this implementation should be O(n*log(n))
      if l.Index == logRecord.Index {
        // Log is already in the list, don't add it again
        return nil
      }
    }
    consumer.currentMap[logRecord.BlockHash].Logs = append(consumer.currentMap[logRecord.BlockHash].Logs, logRecord)
  } else if msgType == EmitMsg {
    // Last message of block. Emit the chain event on appropriate feeds.
    hash := common.BytesToHash(msg)
    event, ok := consumer.currentMap[hash]
    if !ok {
      event, ok = consumer.oldMap[hash]
      if !ok {
        return fmt.Errorf("Received emit for unknown block %#x", hash[:])
      }
    }
    emptyHash := common.Hash{}
    if event.Hash == consumer.lastEmittedBlock {
      // Given multiple masters, we'll see blocks repeat
      return nil
    }
    if event.Block.ParentHash() == consumer.lastEmittedBlock || consumer.lastEmittedBlock == emptyHash {
      // This is the next logical block or we're just booting up, just emit everything.
      consumer.Emit([]core.ChainEvent{*event}, []core.ChainEvent{})
    } else {
      lastEmittedEvent := consumer.currentMap[consumer.lastEmittedBlock]
      if event.Block.Number().Cmp(lastEmittedEvent.Block.Number()) <= 0 {
        // Don't emit reorgs until there's a new block
        return nil
      }
      revertBlocks, newBlocks, err := findCommonAncestor(event, lastEmittedEvent, []map[common.Hash]*core.ChainEvent{consumer.currentMap, consumer.oldMap})
      if err != nil {
        log.Error("Error finding common ancestor", "newBlock", event.Hash, "oldBlock", consumer.lastEmittedBlock, "error", err)
        return err
      }
      if len(newBlocks) > 0 {
        // If we have only revert blocks, this is just an out-of-order
        // block, and should be ignored.
        consumer.Emit(newBlocks, revertBlocks)
      }
      if len(consumer.currentMap) > consumer.recoverySize {
        consumer.oldMap = consumer.currentMap
        consumer.currentMap = make(map[common.Hash]*core.ChainEvent)
        consumer.currentMap[consumer.lastEmittedBlock] = consumer.oldMap[consumer.lastEmittedBlock]
      }
    }
  } else {
    return fmt.Errorf("Unknown message type %v", msgType)
  }
  return nil
}

func (consumer *KafkaEventConsumer) Ready() chan struct{} {
  return consumer.ready
}

type OffsetHash struct {
  Offset int64
  Hash common.Hash
}

func (consumer *KafkaEventConsumer) Start() {
  inputChannel := consumer.consumer.Messages()
  go func() {
    consumer.oldMap = make(map[common.Hash]*core.ChainEvent)
    consumer.currentMap = make(map[common.Hash]*core.ChainEvent)
    for input := range inputChannel {
      if consumer.ready != nil {
        if consumer.consumer.HighWaterMarkOffset() - input.Offset <= 1 {
          consumer.ready <- struct{}{}
          consumer.ready = nil
        }
      }
      msgType := input.Value[0]
      msg := input.Value[1:]
      if msgType == EmitMsg && input.Offset < consumer.startingOffset {
        // During the initial startup, we start several thousand or so messages
        // before we actually want to resume to make sure we have the blocks in
        // memory to handle a reorg. We don't want to re-emit these blocks, we
        // just want to populate our caches.
        continue
      }
      if msgType == EmitMsg {
        consumer.latestEmitOffset = OffsetHash{input.Offset, common.BytesToHash(msg)}
      }
      if err := consumer.processEvent(msgType, msg); err != nil {
        if input.Offset >= consumer.startingOffset {
          // Don't bother logging errors if we haven't reached the starting offset.
          log.Error("Error processing input:", "err", err, "msgType", msgType, "msg", msg, "offset", input.Offset)
        }
      }
    }
  }()
}

func getFromMappings(key common.Hash, mappings []map[common.Hash]*core.ChainEvent) *core.ChainEvent {
  for _, mapping := range mappings {
    if val, ok := mapping[key]; ok {
      return val
    }
  }
  return nil
}

func findCommonAncestor(newHead, oldHead *core.ChainEvent, mappings []map[common.Hash]*core.ChainEvent) ([]core.ChainEvent, []core.ChainEvent, error) {
  reverted := []core.ChainEvent{}
  newBlocks := []core.ChainEvent{*newHead}
  if oldHead == nil {
    return reverted, newBlocks, nil
  }
  for {
    for newHead.Block.NumberU64() > oldHead.Block.NumberU64() + 1 {
      parentHash := newHead.Block.ParentHash()
      newHead = getFromMappings(parentHash, mappings)
      if newHead == nil {
        return reverted, newBlocks, fmt.Errorf("Block %#x missing from history", parentHash)
      }
      newBlocks = append([]core.ChainEvent{*newHead}, newBlocks...)
    }
    if(oldHead.Block.Hash() == newHead.Block.ParentHash())  {
      return reverted, newBlocks, nil
    }
    reverted = append([]core.ChainEvent{*oldHead}, reverted...)
    oldHead = getFromMappings(oldHead.Block.ParentHash(), mappings)
    if oldHead == nil {
      return reverted, newBlocks, fmt.Errorf("Reached genesis without finding common ancestor")
    }
  }
}

func (consumer *KafkaEventConsumer) Emit(add []core.ChainEvent, remove []core.ChainEvent) {
  for _, revert := range remove {
    if len(revert.Logs) > 0 {
      consumer.removedLogsFeed.Send(core.RemovedLogsEvent{revert.Logs})
    }
    consumer.chainSideFeed.Send(core.ChainSideEvent{Block: revert.Block})
  }
  for _, newEvent := range add {
    if len(newEvent.Logs) > 0 {
      consumer.logsFeed.Send(newEvent.Logs)
    }
    consumer.chainHeadFeed.Send(core.ChainHeadEvent{Block: newEvent.Block})
    consumer.chainFeed.Send(newEvent)
    consumer.lastEmittedBlock = newEvent.Hash
  }
  consumer.offsetFeed.Send(consumer.latestEmitOffset)
}

func NewKafkaEventConsumerFromURLs(brokerURL, topic string, lastEmittedBlock common.Hash, offset int64) (EventConsumer, error) {
  brokers, config := cdc.ParseKafkaURL(brokerURL)
  if err := cdc.CreateTopicIfDoesNotExist(brokerURL, topic, 1, nil); err != nil {
    return nil, err
  }
  config.Version = sarama.V2_1_0_0
  client, err := sarama.NewClient(brokers, config)
  if err != nil {
    return nil, err
  }
  consumer, err := sarama.NewConsumerFromClient(client)
  if err != nil {
    return nil, err
  }
  startOffset := offset
  if startOffset > 5000 {
    startOffset -= 5000
  }
  partitionConsumer, err := consumer.ConsumePartition(topic, 0, startOffset)
  if err != nil {
    // We may not have been able to roll back 1000 messages, so just try with
    // the provided offset
    partitionConsumer, err = consumer.ConsumePartition(topic, 0, offset)
    if err != nil {
      return nil, err
    }
  }
  return &KafkaEventConsumer{
    recoverySize: 128, // Geth keeps 128 generations of state trie to handle reorgs, we'll keep at least 128 blocks in memory to be able to handle reorgs.
    consumer: partitionConsumer,
    oldMap: make(map[common.Hash]*core.ChainEvent),
    currentMap: make(map[common.Hash]*core.ChainEvent),
    ready: make(chan struct{}),
    lastEmittedBlock: common.Hash{},
    startingOffset: offset,
  }, nil
}
