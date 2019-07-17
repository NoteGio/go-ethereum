package cdc
import (
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/syndtr/goleveldb/leveldb/iterator"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/pborman/uuid"
)

type BatchWrapper struct {
  batch ethdb.Batch
  writeStream LogProducer
  operations []BatchOperation
  batchid uuid.UUID
}

func (batch *BatchWrapper) BatchId() ([]byte) {
  return batch.batchid[:]
}

func (batch *BatchWrapper) Put(key, value []byte) (error) {
  if batch.writeStream != nil {
    data, err := rlp.EncodeToBytes(KeyValue{key, value})
    if err != nil { return err }
    op := BatchOperation{OpPut, batch.batchid, data}
    batch.operations = append(batch.operations, op)
  }
  return batch.batch.Put(key, value)
}

func (batch *BatchWrapper) Reset() {
  batch.operations = []BatchOperation{}
  batch.batch.Reset()
}

func (batch *BatchWrapper) Delete(key []byte) (error) {
  if batch.writeStream != nil {
    op := BatchOperation{OpDelete, batch.batchid, key}
    batch.operations = append(batch.operations, op)
  }
  return batch.batch.Delete(key)
}

func (batch *BatchWrapper) ValueSize() int {
  return batch.batch.ValueSize()
}

func (batch *BatchWrapper) Write() error {
  if batch.writeStream != nil {
    op, err := WriteOperation(batch)
    if err != nil { return err }
    if len(batch.operations) > 0 {
      for _, bop := range batch.operations {
        if err := batch.writeStream.Emit(bop.Bytes()); err != nil {
          return err
        }
      }
      if err := batch.writeStream.Emit(op.Bytes()); err != nil {
        return err
      }
    }
  }
  return batch.batch.Write()
}

type DBWrapper struct {
  db ethdb.Database
  writeStream LogProducer
  readStream LogProducer
}

func (db *DBWrapper) Put(key, value []byte) error {
  if db.writeStream != nil {
    op, err := PutOperation(key, value)
    if err != nil { return err }
    if err = db.writeStream.Emit(op.Bytes()); err != nil {
      return err
    }
  }
  return db.db.Put(key, value)
}

func (db *DBWrapper) Get(key []byte) ([]byte, error) {
  if db.readStream != nil {
    op, _ := GetOperation(key)
    db.readStream.Emit(op.Bytes())
  }
  return db.db.Get(key)
}

func (db *DBWrapper) Has(key []byte) (bool, error) {
  if db.readStream != nil {
    op, _ := HasOperation(key)
    db.readStream.Emit(op.Bytes())
  }
  return db.db.Has(key)
}

func (db *DBWrapper) Delete(key []byte) error {
  if db.writeStream != nil {
    op, err := DeleteOperation(key)
    if err != nil { return err }
    if err = db.writeStream.Emit(op.Bytes()); err != nil {
      return err
    }
  }
  return db.db.Delete(key)
}

func (db *DBWrapper) Close() {
  if db.writeStream != nil {
    db.writeStream.Close()
  }
  if db.readStream != nil {
    db.readStream.Close()
  }
  db.db.Close()
}

func (db *DBWrapper) NewBatch() ethdb.Batch {
  dbBatch := db.db.NewBatch()
  return &BatchWrapper{dbBatch, db.writeStream, []BatchOperation{}, uuid.NewRandom()}
}

func (db *DBWrapper) NewIterator() iterator.Iterator {
  return db.db.(ethdb.IterableDatabase).NewIterator()
}

func NewDBWrapper(db ethdb.Database, writeStream, readStream LogProducer) ethdb.Database {
  return &DBWrapper{db, writeStream, readStream}
}
