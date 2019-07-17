package cdc_test

import (
  "fmt"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/pborman/uuid"
  "github.com/ethereum/go-ethereum/rlp"
  "testing"
  // "log"
  "bytes"
)

var test_values = []string{"", "a", "1251", "\x00123\x00"}

type MockBatch struct {
  operations []cdc.BatchOperation
  batchid uuid.UUID
}

func (batch *MockBatch) BatchId() ([]byte) {
  return batch.batchid[:]
}

func (batch *MockBatch) Put(key, value []byte) (error) {
  data, err := rlp.EncodeToBytes(cdc.KeyValue{key, value})
  if err != nil { return err }
  op := cdc.BatchOperation{cdc.OpPut, batch.batchid, data}
  batch.operations = append(batch.operations, op)
  return nil
}

func (batch *MockBatch) ValueSize() int {
  size := 0
  for _, op := range batch.operations {
    size += len(op.Data)
  }
  return size
}

func (batch *MockBatch) Write() error { return nil }

func (batch *MockBatch) Reset() {
  batch.operations = []cdc.BatchOperation{}
}

func (batch *MockBatch) Delete(key []byte) error {
  op := cdc.BatchOperation{cdc.OpDelete, batch.batchid, key}
  batch.operations = append(batch.operations, op)
  return nil
}

func opsEqual(op1, op2 *cdc.Operation) (bool) {
  if op1.Op != op2.Op {
    return false
  }
  return bytes.Compare(op1.Data, op2.Data) == 0
}

func checkOperations(op *cdc.Operation, t *testing.T) {
  op2, err := cdc.OperationFromBytes(op.Bytes(), "", 0)
  if err != nil {
    t.Fatalf("Getting operation from bytes failed: %v", err)
  }
  if !opsEqual(op, op2) {
    t.Errorf("Expected ops to be equal")
  }
}

func TestEncodePutOperation(t *testing.T) {
  for i, v := range test_values {
    op, err := cdc.PutOperation([]byte(v), []byte(v))
    op.Offset = int64(i)
    op.Topic = "test"
    if err != nil {
      t.Fatalf("put failed: %v", err)
    }
    fmt.Sprintf("%s\n", op)
    checkOperations(op, t)
    db := ethdb.NewMemDatabase()
    err = op.Apply(db)
    if err != nil {
      t.Fatalf("operation.Apply failed: %v", err)
    }
    value, err := db.Get([]byte(v))
    if err != nil {
      t.Fatalf("Failed to get value '%v'", err)
    }
    if bytes.Compare(value, []byte(v)) != 0 {
      t.Errorf("Got unexpected value '%v' != '%v'", value, v)
    }
  }
}
func TestEncodeDeleteOperation(t *testing.T) {
  for _, v := range test_values {
    op, err := cdc.DeleteOperation([]byte(v))
    if err != nil {
      t.Fatalf("delete failed: %v", err)
    }
    fmt.Sprintf("%s\n", op)
    checkOperations(op, t)
    db := ethdb.NewMemDatabase()
    db.Put([]byte(v), []byte(v))
    err = op.Apply(db)
    if err != nil {
      t.Fatalf("operation.Apply failed: %v", err)
    }
    value, err := db.Get([]byte(v))
    if value != nil {
      t.Fatalf("Expected key to be deleted from database")
    }
  }
}
func TestEncodeWriteOperation(t *testing.T) {
  batch := &MockBatch{[]cdc.BatchOperation{}, uuid.NewRandom()}
  for _, v := range test_values {
    batch.Put([]byte(v), []byte(v))
  }
  batch.Delete([]byte("gone"))
  op, err := cdc.WriteOperation(batch)
  if err != nil {
    t.Fatalf("batch write failed: %v", err)
  }
  data, err := rlp.EncodeToBytes(batch.operations)
  if err != nil {
    t.Fatalf(err.Error())
  }
  op.Data = append(op.Data, data...)
  fmt.Sprintf("%s\n", op)
  checkOperations(op, t)
  db := ethdb.NewMemDatabase()
  db.Put([]byte("gone"), []byte("deleted"))
  err = op.Apply(db)
  if err != nil {
    t.Fatalf("operation.Apply failed: %v", err)
  }
  for _, v := range test_values {
    value, err := db.Get([]byte(v))
    if err != nil {
      t.Errorf("error getting value from database: %v", err)
    }
    if bytes.Compare(value, []byte(v)) != 0 {
      t.Errorf("Got unexpected value '%v' != '%v'", value, v)
    }
  }
  if _, err := db.Get([]byte("gone")); err == nil {
    t.Errorf("Key should have been deleted")
  }
}

func TestEncodeGetOperation(t *testing.T) {
  for _, v := range test_values {
    op, err := cdc.GetOperation([]byte(v))
    if err != nil {
      t.Fatalf("read failed: %v", err)
    }
    checkOperations(op, t)
  }
}
func TestEncodeHasOperation(t *testing.T) {
  for _, v := range test_values {
    op, err := cdc.HasOperation([]byte(v))
    if err != nil {
      t.Fatalf("has failed: %v", err)
    }
    checkOperations(op, t)
  }
}
