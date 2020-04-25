package cdc

import (
  "bytes"
  "fmt"
  "encoding/binary"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/log"
  "github.com/pborman/uuid"
  "errors"
  "time"
  "sync"
)

type valueTracker struct {
  currentMap map[string]struct{}
  oldMap map[string]struct{}
  m sync.Mutex
  max int
}

func newValueTracker() *valueTracker {
  return &valueTracker{
    make(map[string]struct{}),
    make(map[string]struct{}),
    sync.Mutex{},
    1000,
  }
}

const (
  OpPut byte = 0
  OpDelete byte = 1
  OpWrite byte = 2
  OpHeartbeat byte = 3
  OpGet byte = 4
  OpHas byte = 5
  OpAppendAncient byte = 6
  OpTruncateAncients byte = 7
  OpSync byte = 8
  MinBlockAge = 80 * time.Millisecond
)

var (
  headerTracker = newValueTracker()
  blockTracker = newValueTracker()
  lastBlockUpdate = time.Now()
  lastBlockOffset int64
  lastBlockWrites int64
  lastLog = time.Time{}
  blocksSinceLastLog = 0
  applyTime = time.Since(time.Now())
  betweenTime = time.Since(time.Now())
  lastApply = time.Now()
)


type KeyValue struct {
  Key []byte
  Value []byte
}

func (kv *KeyValue) String() (string) {
  return fmt.Sprintf("KV{%#x(%v): %#x}", kv.Key, string(kv.Key), kv.Value)
}

type KVs []KeyValue

func (kvs KVs) String() string {
  output := "["
  for _, kv := range kvs {
    output += fmt.Sprintf("\n%v,", &kv)
  }
  output += "\n]"
  return output
}

type BatchOperation struct {
  Op byte
  Batch uuid.UUID
  Data []byte
}

type AncientData struct {
  Number uint64
  Hash []byte
  Header []byte
  Body []byte
  Receipt []byte
  Td []byte
}

func (t *valueTracker) add(v []byte) bool {
  t.m.Lock()
  defer t.m.Unlock()
  sv := string(v)
  if _, ok := t.currentMap[sv]; ok {
    return false
  }
  if _, ok := t.oldMap[sv]; ok {
    return false
  }
  t.currentMap[sv] = struct{}{}
  if len(t.currentMap) >= t.max {
    t.oldMap, t.currentMap = t.currentMap, make(map[string]struct{})
  }
  return true
}

func (op *BatchOperation) Bytes() ([]byte) {
  data := []byte{255, op.Op}
  data = append(data, op.Batch...)
  return append(data, op.Data...)
}

func BatchOperationFromBytes(data []byte, topic string, offset int64) (BatchOperation, error) {
  bop := BatchOperation{}
  if data[0] != 255 {
    return bop, errors.New("Batch operations must begin with 0xFF")
  }
  bop.Op = data[1]
  bop.Batch = make(uuid.UUID, 16)
  copy(bop.Batch[:], data[2:18])
  bop.Data = data[18:]
  return bop, nil
}


type Operation struct {
  Op byte
  Data []byte
  Offset int64
  Topic string
  Timestamp time.Time
}

func updateOffset(putter ethdb.KeyValueWriter, op *Operation) error {
  if op.Offset != 0 {
    buf := make([]byte, binary.MaxVarintLen64*2)
    binary.PutVarint(buf[0:binary.MaxVarintLen64], op.Offset)
    binary.PutVarint(buf[binary.MaxVarintLen64:], time.Now().Unix())
    return putter.Put(
      []byte(fmt.Sprintf("cdc-log-%v-offset", op.Topic)),
      buf,
    )
  }
  return nil
}

func logAndSleep(value []byte, timestamp time.Time, offset int64) {
  if time.Since(lastLog) > 1 * time.Second {
    log.Info("Recording LastBlock", "hash", common.BytesToHash(value), "delta", time.Since(timestamp), "lastBlock", time.Since(lastBlockUpdate), "offset", offset, "offsetDelta", offset - lastBlockOffset, "writes", lastBlockWrites, "blocks", blocksSinceLastLog, "applyTime", applyTime, "betweenTime", betweenTime)
    applyTime = time.Since(time.Now())
    betweenTime = time.Since(time.Now())
    blocksSinceLastLog = 0
    lastLog = time.Now()
  }
  blocksSinceLastLog++
  lastBlockUpdate = time.Now()
  lastBlockOffset = offset
  lastBlockWrites = 1
  // To help ensure consistency across replicas, don't apply this operation
  // until MinBlockAge (80ms) after it was emitted by the master. If this
  // number is <= 0, it will not pause.
  time.Sleep(MinBlockAge - time.Since(timestamp))
}

func (op *Operation) Apply(db ethdb.Database) ([]byte, error) {
  betweenTime += time.Since(lastApply)
  applyStart := time.Now()
  lastBlockWrites++
  switch op.Op {
  case OpPut:
    kv := &KeyValue{}
    if err := rlp.DecodeBytes(op.Data, kv); err != nil { return nil, err }
    if bytes.Equal(kv.Key, []byte("LastHeader")) && !headerTracker.add(kv.Value){
      // We have already recorded this header. Recording it again could create
      // inconsistencies.
      return nil, nil
    }
    if bytes.Equal(kv.Key, []byte("LastBlock")) && !blockTracker.add(kv.Value){
      // We have already recorded this block. Recording it again could create
      // inconsistencies.
      return nil, nil
    }
    if bytes.Equal(kv.Key, []byte("LastBlock")) {
      logAndSleep(kv.Value, op.Timestamp, op.Offset)
      batch := db.NewBatch()
      if err := batch.Put(kv.Key, kv.Value); err != nil { return nil, err }
      if err := updateOffset(batch, op); err != nil { return nil, err }
      if err := batch.Write(); err != nil { return nil, err }
      return kv.Value, nil
    }
    if err := db.Put(kv.Key, kv.Value); err != nil { return nil, err }
  case OpDelete:
    // For OpDelete, op.Data is the key to be deleted
    db.Delete(op.Data)
  case OpAppendAncient:
    a := &AncientData{}
    if err := rlp.DecodeBytes(op.Data, a); err != nil { return nil, err }
    if err := db.AppendAncient(a.Number, a.Hash, a.Header, a.Body, a.Receipt, a.Td); err != nil { return nil, err}
  case OpTruncateAncients:
    var n uint64
    if err := rlp.DecodeBytes(op.Data, &n); err != nil { return nil, err }
    if err := db.TruncateAncients(n); err != nil { return nil, err }
  case OpSync:
    if err := db.Sync(); err != nil { return nil, err }
  case OpWrite:
    batch := db.NewBatch()
    var operations []BatchOperation
    if err := rlp.DecodeBytes(op.Data[16:], &operations); err != nil { return nil, err }
    var headHash []byte
    for _, bop := range operations {
      switch bop.Op {
      case OpPut:
        kv := &KeyValue{}
        if err := rlp.DecodeBytes(bop.Data, kv); err != nil { return nil, err }

        if bytes.Equal(kv.Key, []byte("LastHeader")) && !headerTracker.add(kv.Value){
          // We have already recorded this header. Recording it again could create
          // inconsistencies.
          continue
        }
        if bytes.Equal(kv.Key, []byte("LastBlock")) && !blockTracker.add(kv.Value){
          // We have already recorded this block. Recording it again could create
          // inconsistencies.
          continue
        }
        if bytes.Equal(kv.Key, []byte("LastBlock")) {
          logAndSleep(kv.Value, op.Timestamp, op.Offset)
          if err := batch.Put(kv.Key, kv.Value); err != nil { return nil, err }
          headHash = kv.Value
        }
        if err := batch.Put(kv.Key, kv.Value); err != nil { return nil, err }
      case OpDelete:
        if err := batch.Delete(bop.Data); err != nil { return nil, err }
      default:
        fmt.Printf("Unsupported operation: %#x", bop.Op)
      }

    }
    applyTime += time.Since(applyStart)
    lastApply = time.Now()
    return headHash, batch.Write()
  case OpHeartbeat:
    return nil, updateOffset(db, op)
  default:
    fmt.Printf("Unknown operation: %v \n", op)
  }
  applyTime += time.Since(applyStart)
  lastApply = time.Now()
  return nil, nil
}

func (op *Operation) Bytes() ([]byte) {
  data := []byte{op.Op}
  return append(data, op.Data...)
}

func (op *Operation) String() (string) {
  switch op.Op {
  case OpPut:
    kv := &KeyValue{}
    if err := rlp.DecodeBytes(op.Data, kv); err != nil { return err.Error() }
    return fmt.Sprintf("PUT: %v", kv)
  case OpDelete:
    return fmt.Sprintf("DEL: %v", string(op.Data))
  case OpWrite:
    var puts []KeyValue
    rlp.DecodeBytes(op.Data, &puts)
    return fmt.Sprintf("WRITE: %v", KVs(puts))
  case OpHeartbeat:
    return "HEARTBEAT"
  }
  return "UNKNOWN"
}

func OperationFromBytes(data []byte, topic string, offset int64) (*Operation, error) {
  if len(data) == 0 {
    return nil, errors.New("OperationFromBytes requires a []byte of length > 0")
  }
  opData := make([]byte, len(data[1:]))
  copy(opData[:], data[1:])
  return &Operation{
    Op: data[0],
    Data: opData,
    Topic: topic,
    Offset: offset,
  }, nil
}

func PutOperation(key, value []byte) (*Operation, error) {
  op := &Operation{}
  op.Op = OpPut
  data, err := rlp.EncodeToBytes(KeyValue{key, value})
  if err != nil { return nil, err }
  op.Data = data
  return op, nil
}

func AppendAncientOperation(number uint64, hash, header, body, receipt, td []byte) (*Operation, error) {
  op := &Operation{}
  op.Op = OpAppendAncient
  data, err := rlp.EncodeToBytes(AncientData{number, hash, header, body, receipt, td})
  if err != nil { return nil, err }
  op.Data = data
  return op, nil
}

func TruncateAncientsOperation(n uint64) (*Operation, error) {
  op := &Operation{}
  op.Op = OpTruncateAncients
  data, err := rlp.EncodeToBytes(n)
  if err != nil { return nil, err }
  op.Data = data
  return op, nil
}

func SyncOperation() (*Operation, error) {
  op := &Operation{}
  op.Op = OpSync
  return op, nil
}

func DeleteOperation(key []byte) (*Operation, error) {
  return &Operation{OpDelete, key, 0, "", time.Now()}, nil
}

func HeartbeatOperation() (*Operation) {
  return &Operation{OpHeartbeat, []byte{}, 0, "", time.Now()}
}

func WriteOperation(batch Batch) (*Operation, error) {
  op := &Operation{}
  op.Op = OpWrite
  op.Data = batch.BatchId()
  return op, nil
}

func GetOperation(key []byte) (*Operation, error) {
  return &Operation{OpGet, key, 0, "", time.Now()}, nil
}

func HasOperation(key []byte) (*Operation, error) {
  return &Operation{OpHas, key, 0, "", time.Now()}, nil
}
