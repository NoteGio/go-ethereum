package cdc_test

import (
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/ethereum/go-ethereum/rlp"
  "testing"
  "bytes"
  // "log"
  "time"
  "errors"
)

func getTestWrapper() (ethdb.Database, cdc.LogConsumer, cdc.LogConsumer) {
  writeProducer, writeConsumer := cdc.MockLogPair()
  readProducer, readConsumer := cdc.MockLogPair()
  <-writeConsumer.Ready()
  db := rawdb.NewMemoryDatabase()
  return cdc.NewDBWrapper(db, writeProducer, readProducer), writeConsumer, readConsumer
}

func getOpWithTimeout(ch <-chan *cdc.Operation) (*cdc.Operation, error) {
  for i := 0; i < 5; i++ {
    select {
    case op := <-ch:
      return op, nil
    default:
      time.Sleep(100 * time.Millisecond)
    }
  }
  return nil, errors.New("Timeout")
}

func TestPut(t *testing.T) {
  db, writeStream, _ := getTestWrapper()
  defer db.Close()
  err := db.Put([]byte("hello"), []byte("world"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  op, err := getOpWithTimeout(writeStream.Messages())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if op.Op != cdc.OpPut {
    t.Errorf("Got unexpected operation type: %v", op.Op)
  }
  kv := &cdc.KeyValue{}
  rlp.DecodeBytes(op.Data, kv)
  if bytes.Compare(kv.Key, []byte("hello")) != 0 || bytes.Compare(kv.Value, []byte("world")) != 0 {
    t.Errorf("Unexpected Key Pair: %v: %v", kv.Key, kv.Value)
  }
}

func TestGet(t *testing.T) {
  db, _, readStream := getTestWrapper()
  defer db.Close()
  err := db.Put([]byte("hello"), []byte("world"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  val, err := db.Get([]byte("hello"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  if bytes.Compare(val, []byte("world")) != 0 {
    t.Errorf("Unexpected value from db get: %v", val)
  }
  op, err := getOpWithTimeout(readStream.Messages())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if op.Op != cdc.OpGet {
    t.Errorf("Got unexpected operation type: %v", op.Op)
  }
  if bytes.Compare(op.Data, []byte("hello")) != 0 {
    t.Errorf("Unexpected Key: %v", op.Data)
  }
}

func TestHas(t *testing.T) {
  db, _, readStream := getTestWrapper()
  defer db.Close()
  err := db.Put([]byte("hello"), []byte("world"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  val, err := db.Has([]byte("hello"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  if !val {
    t.Errorf("Unexpected value from db get: %v", val)
  }
  val, err = db.Has([]byte("world"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  if val {
    t.Errorf("Unexpected value from db get: %v", val)
  }
  op, err := getOpWithTimeout(readStream.Messages())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if op.Op != cdc.OpHas {
    t.Errorf("Got unexpected operation type: %v", op.Op)
  }
  if bytes.Compare(op.Data, []byte("hello")) != 0 {
    t.Errorf("Unexpected Key: %v", op.Data)
  }
  op, err = getOpWithTimeout(readStream.Messages())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if op.Op != cdc.OpHas {
    t.Errorf("Got unexpected operation type: %v", op.Op)
  }
  if bytes.Compare(op.Data, []byte("world")) != 0 {
    t.Errorf("Unexpected Key: %v", op.Data)
  }
}

func TestDelete(t *testing.T) {
  db, writeStream, _ := getTestWrapper()
  defer db.Close()
  err := db.Put([]byte("hello"), []byte("world"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  _, err = getOpWithTimeout(writeStream.Messages())
  if err != nil {
    t.Fatalf(err.Error())
  }
  err = db.Delete([]byte("hello"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  op, err := getOpWithTimeout(writeStream.Messages())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if op.Op != cdc.OpDelete {
    t.Errorf("Got unexpected operation type: %v", op.Op)
  }
  if bytes.Compare(op.Data, []byte("hello")) != 0 {
    t.Errorf("Unexpected Key: %v", op.Data)
  }
  if val, _ := db.Has([]byte("hello")); val {
    t.Errorf("Key unexpectedly found in database")
  }
}

func TestBatchWrapper(t *testing.T) {
  db, writeStream, _ := getTestWrapper()
  defer db.Close()
  db.Put([]byte("gone"), []byte("nothere"))
  <-writeStream.Messages() // Throw away the initial
  batch := db.NewBatch()
  batch.Put([]byte("hello"), []byte("world"))
  select {
  case _ = <-writeStream.Messages():
    t.Errorf("No messages expected yet")
  default:
  }
  if size := batch.ValueSize(); size != 5 {
    t.Errorf("Unexpected value size %v", size)
  }
  batch.Put([]byte("goodbye"), []byte("world"))
  select {
  case _ = <-writeStream.Messages():
    t.Errorf("No messages expected yet")
  default:
  }
  batch.Delete([]byte("gone"))
  select {
  case _ = <-writeStream.Messages():
    t.Errorf("No messages expected yet")
  default:
  }
  if size := batch.ValueSize(); size != 11 {
    t.Errorf("Unexpected value size %v", size)
  }
  go batch.Write()
  success := false
  Loop:
    for i := 0; i < 11; i++ {
      select {
      case op := <-writeStream.Messages():
        if op.Op != cdc.OpWrite {
          t.Errorf("Got unexpected operation type: %v", op.Op)
        }
        operations := []cdc.BatchOperation{}
        if err := rlp.DecodeBytes(op.Data[16:], &operations); err != nil {
          t.Fatalf("Error decoding op: %v", err.Error())
        }
        kvs := []cdc.KeyValue{}
        for _, bop := range operations {
          switch bop.Op {
          case cdc.OpPut:
            kv := &cdc.KeyValue{}
            if err := rlp.DecodeBytes(bop.Data, kv); err != nil { t.Errorf(err.Error()) }
            kvs = append(kvs, *kv)
          case cdc.OpDelete:
            if string(bop.Data) != "gone" {
              t.Errorf("Unexpected delete operation")
            }
          default:
            t.Errorf("Expected put operation")
          }
        }
        if bytes.Compare(kvs[0].Key, []byte("hello")) != 0 || bytes.Compare(kvs[0].Value, []byte("world")) != 0 {
          t.Errorf("Unexpected Key Value Pair: %v, %v", kvs[0].Key, kvs[0].Value)
        }
        if bytes.Compare(kvs[1].Key, []byte("goodbye")) != 0 || bytes.Compare(kvs[1].Value, []byte("world")) != 0 {
          t.Errorf("Unexpected Key Value Pair: %v, %v", kvs[1].Key, kvs[1].Value)
        }
        success = true
        break Loop
      default:
        time.Sleep(100 * time.Millisecond)
      }
    }
    if !success {
      t.Errorf("Expected batch write")
    }
}

func TestBatchWrapperReset(t *testing.T) {
  db, _, _ := getTestWrapper()
  defer db.Close()
  batch := db.NewBatch()
  batch.Put([]byte("hello"), []byte("world"))
  batch.Reset()
  if size := batch.ValueSize(); size != 0 {
    t.Errorf("Unexpected value size %v", size)
  }
}
