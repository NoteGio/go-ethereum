package snapshot

import (
  "math/big"
  "compress/gzip"
  "encoding/base64"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/Shopify/sarama"
  "sync"
  "fmt"
  "io"
  "os"
)

type MsgType byte

const (
  DeltaMsg MsgType = iota
  DestructMsg
  AccountMsg
  StorageMsg
)

type stateDeltaEmitter interface {
  Emit([]stateDeltaMessage) error
  Flush()
}

type compressedFileDeltaEmitter struct {
  file io.WriteCloser
  compressor *gzip.Writer
  wg *sync.WaitGroup
}

func FileCDCTree(tree SnapshotTree, path string) (SnapshotTree, error, func()) {
  fi, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0660)
  if err != nil { return nil, err, func() {} }
  compressor := gzip.NewWriter(fi)
  return &CDCTree{tree: tree, emitter: compressedFileDeltaEmitter{compressor: compressor, file: fi,  wg: &sync.WaitGroup{}}}, nil, func() {
    compressor.Close()
    fi.Close()
  }
}

func (sde compressedFileDeltaEmitter) Flush() {
  sde.wg.Wait()
  sde.compressor.Flush()
}

func (sde compressedFileDeltaEmitter) Emit(messages []stateDeltaMessage) error {
  sde.wg.Add(1)
  defer sde.wg.Done()
  data, err := rlp.EncodeToBytes(messages)
  if err != nil { return err }
  sde.compressor.Write([]byte(base64.StdEncoding.EncodeToString(data)))
  sde.compressor.Write([]byte("\n"))
  return nil
}



type kafkaStateDeltaEmitter struct {
  producer sarama.AsyncProducer
  topic string
  wg *sync.WaitGroup
}

func (producer kafkaStateDeltaEmitter) Emit(messages []stateDeltaMessage) error {
  producer.wg.Add(1)
  defer producer.wg.Done()
  inflight := 0
  for _, msg := range messages {
    // Send messages to Kafka or get errors from previous sends
    SEND_LOOP:
    for {
      select {
      case producer.producer.Input() <- &sarama.ProducerMessage{Topic: producer.topic, Key: sarama.ByteEncoder(msg.Key), Value: sarama.ByteEncoder(msg.Value)}:
        inflight++
        break SEND_LOOP
      case err := <-producer.producer.Errors():
        return err
      case <-producer.producer.Successes():
        inflight--
      }

    }
  }
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

func (producer kafkaStateDeltaEmitter) Flush() {
  producer.wg.Wait()
}

func KafkaCDCTree(tree SnapshotTree, producer sarama.AsyncProducer, topic string) SnapshotTree {
  return &CDCTree{tree: tree, emitter: kafkaStateDeltaEmitter{producer: producer, topic: topic, wg: &sync.WaitGroup{}}}
}

type SnapshotTree interface {
  Journal(common.Hash) (common.Hash, error)
  LegacyJournal(common.Hash) (common.Hash, error)
  Rebuild(common.Hash)
  AccountIterator(common.Hash, common.Hash) (AccountIterator, error)
  StorageIterator(common.Hash, common.Hash, common.Hash) (StorageIterator, error)
  Snapshot(common.Hash) Snapshot
  Update(common.Hash, common.Hash, map[common.Hash]struct{}, map[common.Hash][]byte, map[common.Hash]map[common.Hash][]byte) error
  Cap(common.Hash, int) error
}

type CDCTree struct {
  tree SnapshotTree
  emitter stateDeltaEmitter
}

func (t *CDCTree) Journal(root common.Hash) (common.Hash, error) {
  t.emitter.Flush()
  if t.tree != nil { return t.tree.Journal(root) }
  return common.Hash{}, nil
}

func (t *CDCTree) LegacyJournal(root common.Hash) (common.Hash, error) {
  t.emitter.Flush()
  if t.tree != nil { return t.tree.LegacyJournal(root) }
  return common.Hash{}, nil
}

func (t *CDCTree) Rebuild(root common.Hash) {
  if t.tree != nil { t.tree.Rebuild(root) }
}

func (t *CDCTree) AccountIterator(root common.Hash, seek common.Hash) (AccountIterator, error) {
  if t.tree != nil { return t.tree.AccountIterator(root, seek) }
  return nil, fmt.Errorf("unknown snapshot: %x", root)

}

func (t *CDCTree) StorageIterator(root common.Hash, account common.Hash, seek common.Hash) (StorageIterator, error) {
  if t.tree != nil { return t.tree.StorageIterator(root, account, seek) }
  return nil, fmt.Errorf("unknown snapshot: %x", root)
}

func (t *CDCTree) Snapshot(blockRoot common.Hash) Snapshot {
  var snap Snapshot
  if t.tree != nil { snap = t.tree.Snapshot(blockRoot) }
  return &CDCSnapshot{snap: snap, root: blockRoot}
}

type stateDeltaMessage struct {
  Key []byte
  Value []byte
}

type cdcHeader struct {
  ParentRoot common.Hash
  Destructs uint
  Accounts uint
  Storage uint
}

func (h cdcHeader) size() int {
  return int(h.Destructs + h.Accounts + h.Storage) + 1
}

func (t *CDCTree) Update(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) error {
  // DeltaMsg
  // DestructMsg
  // AccountMsg
  // StorageMsg
  storageSize := 0
  for _, v := range storage { storageSize += len(v) }
  header := cdcHeader{
    ParentRoot: parentRoot,
    Destructs: uint(len(destructs)),
    Accounts: uint(len(accounts)),
    Storage: uint(storageSize),
  }
  headerRLP, err := rlp.EncodeToBytes(header)
  if err != nil { panic(err.Error()) } // We shouldn't get RLP errors here
  messages := make([]stateDeltaMessage, header.size())
  messages[0] = stateDeltaMessage{
    Key: append([]byte{byte(DeltaMsg)}, blockRoot.Bytes()...),
    Value: headerRLP,
  }
  counter := int64(1)
  for k, _ := range destructs {
    messages[counter] = stateDeltaMessage{
      Key: append(append([]byte{byte(DestructMsg)}, blockRoot.Bytes()...), big.NewInt(counter).Bytes()...), // We don't actually care about counter, we just need to make the key unique
      Value: k.Bytes(),
    }
    counter++
  }
  for k, v := range accounts {
    messages[counter] = stateDeltaMessage{
      Key: append(append([]byte{byte(AccountMsg)}, blockRoot.Bytes()...), k.Bytes()...),
      Value: v,
    }
    counter++
  }
  for accountHash, accountStorage := range storage {
    for storageHash, value := range accountStorage {
      messages[counter] = stateDeltaMessage{
        Key: append(append(append([]byte{byte(StorageMsg)}, blockRoot.Bytes()...), accountHash.Bytes()...), storageHash.Bytes()...),
        Value: value,
      }
      counter++
    }
  }
  if err := t.emitter.Emit(messages); err != nil { panic(err) }
  if t.tree != nil { return t.tree.Update(blockRoot, parentRoot, destructs, accounts, storage) }
  return nil
}

func (t *CDCTree) Cap(root common.Hash, layers int) error {
  if t.tree != nil { return t.tree.Cap(root, layers) }
  return nil
}

type CDCSnapshot struct {
  snap Snapshot
  root common.Hash
}


// Root returns the root hash for which this snapshot was made.
func (t *CDCSnapshot) Root() common.Hash {
  return t.root
}

// Account directly retrieves the account associated with a particular hash in
// the snapshot slim data format.
func (t *CDCSnapshot) Account(hash common.Hash) (*Account, error) {
  if t.snap != nil { return t.snap.Account(hash) }
  return nil, ErrNotCoveredYet
}

// AccountRLP directly retrieves the account RLP associated with a particular
// hash in the snapshot slim data format.
func (t *CDCSnapshot) AccountRLP(hash common.Hash) ([]byte, error) {
  if t.snap != nil { return t.snap.AccountRLP(hash) }
  return []byte{}, ErrNotCoveredYet
}

// Storage directly retrieves the storage data associated with a particular hash,
// within a particular account.
func (t *CDCSnapshot) Storage(accountHash, storageHash common.Hash) ([]byte, error) {
  if t.snap != nil { return t.snap.Storage(accountHash, storageHash) }
  return []byte{}, ErrNotCoveredYet
}

type delta struct {
  parentRoot common.Hash
  destructs map[common.Hash]struct{}
  accounts map[common.Hash][]byte
  storage map[common.Hash]map[common.Hash][]byte
  pendingDestructs uint
  pendingAccounts uint
  pendingStorage uint
}

func (d *delta) ready() bool {
  return d.pendingDestructs == 0 && d.pendingAccounts == 0 && d.pendingStorage == 0
}

// TODO: We need a way to catch up with Kafka to fill in any out-of-order
// events that were pending when we shutdown. eg. Start from earlier kafka
// offsets without emitting completed events or erroring when parents hadn't
// been processed.
type deltaTracker struct {
  tree SnapshotTree
  deltas map[common.Hash]*delta
  pendingEmits map[common.Hash][]common.Hash
  deltaPartitions map[int32]int64
  finished map[common.Hash]bool
  oldFinished map[common.Hash]bool
  finishedLimit int
  earlyDestructs map[common.Hash]map[common.Hash]struct{}
  earlyAccounts map[common.Hash]map[common.Hash][]byte
  earlyStorage map[common.Hash]map[common.Hash]map[common.Hash][]byte
}

func (dt *deltaTracker) handleMessage(key, value []byte, partition int32, offset int64) (bool, error) {
  blockRoot := common.BytesToHash(key[1:33])
  // If we've already handled this root, we need go no further
  if dt.finished[blockRoot] || dt.oldFinished[blockRoot] { return false, nil }
  dt.finished[blockRoot] = false // Explicitly setting to false so it will eventually get garbage collected if we never see the whole block
  switch MsgType(key[0]) {
  case DeltaMsg:
    if _, ok := dt.deltas[blockRoot]; ok { return false, nil }
    header := cdcHeader{}
    if err := rlp.DecodeBytes(value, &header); err != nil { return false, err }
    dt.deltas[blockRoot] = &delta{
      parentRoot: header.ParentRoot,
      pendingDestructs: header.Destructs,
      pendingAccounts: header.Accounts,
      pendingStorage: header.Storage,
      destructs: make(map[common.Hash]struct{}),
      accounts: make(map[common.Hash][]byte),
      storage: make(map[common.Hash]map[common.Hash][]byte),
    }
    if destructs, ok := dt.earlyDestructs[blockRoot]; ok {
      dt.deltas[blockRoot].destructs = destructs
      dt.deltas[blockRoot].pendingDestructs -= uint(len(destructs))
      delete(dt.earlyDestructs, blockRoot)
    }
    if accounts, ok := dt.earlyAccounts[blockRoot]; ok {
      dt.deltas[blockRoot].accounts = accounts
      dt.deltas[blockRoot].pendingAccounts -= uint(len(accounts))
      delete(dt.earlyAccounts, blockRoot)
    }
    if storage, ok := dt.earlyStorage[blockRoot]; ok {
      dt.deltas[blockRoot].storage = storage
      for _, v := range storage {
        dt.deltas[blockRoot].pendingStorage -= uint(len(v))
      }
      delete(dt.earlyStorage, blockRoot)
    }

  case DestructMsg:
    destructHash := common.BytesToHash(value)
    delta, ok := dt.deltas[blockRoot]
    if !ok {
      // Early
      if _, ok := dt.earlyDestructs[blockRoot]; !ok {
        dt.earlyDestructs[blockRoot] = make(map[common.Hash]struct{})
      }
      dt.earlyDestructs[blockRoot][destructHash] = struct{}{}
      return false, nil
    }
    // On time
    if _, ok := delta.destructs[destructHash]; ok { return false, nil }
    delta.destructs[destructHash] = struct{}{}
    delta.pendingDestructs--
  case AccountMsg:
    accountHash := common.BytesToHash(key[33:])
    delta, ok := dt.deltas[blockRoot]
    if !ok {
      // Early
      if _, ok := dt.earlyAccounts[blockRoot]; !ok {
        dt.earlyAccounts[blockRoot] = make(map[common.Hash][]byte)
      }
      dt.earlyAccounts[blockRoot][accountHash] = value
      return false, nil
    }
    // On time
    if _, ok := delta.accounts[accountHash]; ok { return false, nil }
    delta.accounts[accountHash] = value
    delta.pendingAccounts--
  case StorageMsg:
    accountHash := common.BytesToHash(key[33:65])
    storageHash := common.BytesToHash(key[65:])
    delta, ok := dt.deltas[blockRoot]
    if !ok {
      // Early
      if _, ok := dt.earlyStorage[blockRoot]; !ok {
        dt.earlyStorage[blockRoot] = make(map[common.Hash]map[common.Hash][]byte)
      }
      if _, ok := dt.earlyStorage[blockRoot][accountHash]; !ok {
        dt.earlyStorage[blockRoot][accountHash] = make(map[common.Hash][]byte)
      }
      dt.earlyStorage[blockRoot][accountHash][storageHash] = value
      return false, nil
    }
    // On time
    if _, ok := delta.storage[accountHash]; !ok {
      delta.storage[accountHash] = make(map[common.Hash][]byte)
    }
    if _, ok := delta.storage[accountHash][storageHash]; ok { return false, nil }
    dt.deltas[blockRoot].storage[accountHash][storageHash] = value
    delta.pendingStorage--
  }
  delta := dt.deltas[blockRoot]
  if delta != nil && delta.ready() {
    if err := dt.handleUpdate(blockRoot); err != nil {
      return false, err
    }
    if dt.finishedLimit <= len(dt.finished) {
      for oldRoot := range dt.oldFinished {
        delete(dt.deltas, oldRoot)
      }
      dt.oldFinished = dt.finished
      dt.finished = make(map[common.Hash]bool)
    }
    return true, nil
  }
  return false, nil
}
func (dt *deltaTracker) handleUpdate(blockRoot common.Hash) error {
  delta := dt.deltas[blockRoot]
  if dt.finished[delta.parentRoot] || dt.oldFinished[delta.parentRoot] {
    // TODO: Track offsets for resumption
    if err := dt.tree.Update(blockRoot, delta.parentRoot, delta.destructs, delta.accounts, delta.storage); err != nil {
      return err
    }
    dt.finished[blockRoot] = true
    if children, ok := dt.pendingEmits[blockRoot]; ok {
      for _, child := range children {
        if err := dt.handleUpdate(child); err != nil { return err }
      }
      delete(dt.pendingEmits, blockRoot)
    }
  } else {
    if _, ok := dt.pendingEmits[delta.parentRoot]; !ok {
      dt.pendingEmits[delta.parentRoot] = []common.Hash{}
    }
    dt.pendingEmits[delta.parentRoot] = append(dt.pendingEmits[delta.parentRoot], blockRoot)
  }
  return nil
}
