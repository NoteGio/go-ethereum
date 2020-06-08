package cdc
import (
  "fmt"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/log"
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
          log.Warn("Failed to write batch item", "length", len(bop.Bytes()))
          log.Warn(fmt.Sprintf("Item bytes: %#x", bop.Bytes()))
          return err
        }
      }
      if err := batch.writeStream.Emit(op.Bytes()); err != nil {
        log.Warn("Failed to write batch", "length", len(op.Bytes()))
        log.Warn(fmt.Sprintf("Bytes: %#x", op.Bytes()))
        return err
      }
    }
  }
  return batch.batch.Write()
}

// Replay replays the batch contents.
func (batch *BatchWrapper) Replay(w ethdb.KeyValueWriter) error {
	return batch.batch.Replay(w)
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
      log.Warn("Failed to put item", "length", len(op.Bytes()))
      log.Warn(fmt.Sprintf("Item bytes: %#x", op.Bytes()))
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

// AppendAncient injects all binary blobs belong to block at the end of the
// append-only immutable table files.
func (db *DBWrapper) AppendAncient(number uint64, hash, header, body, receipt, td []byte) error {
  if db.writeStream != nil {
    op, err := AppendAncientOperation(number, hash, header, body, receipt, td)
    if err != nil { return err }
    if err = db.writeStream.Emit(op.Bytes()); err != nil {
      log.Warn("Failed to append ancient", "length", len(op.Bytes()))
      log.Warn(fmt.Sprintf("Item bytes: %#x", op.Bytes()))
      return err
    }
  }
  return db.db.AppendAncient(number, hash, header, body, receipt, td)
}

// TruncateAncients discards all but the first n ancient data from the ancient store.
func (db *DBWrapper) TruncateAncients(n uint64) error {
  if db.writeStream != nil {
    op, err := TruncateAncientsOperation(n)
    if err != nil { return err }
    if err = db.writeStream.Emit(op.Bytes()); err != nil {
      log.Warn("Failed to truncate ancient", "length", len(op.Bytes()))
      log.Warn(fmt.Sprintf("Item bytes: %#x", op.Bytes()))
      return err
    }
  }
  return db.db.TruncateAncients(n)
}

// Sync flushes all in-memory ancient store data to disk.
func (db *DBWrapper) Sync() error {
  if db.writeStream != nil {
    op, err := SyncOperation()
    if err != nil { return err }
    if err = db.writeStream.Emit(op.Bytes()); err != nil {
      log.Warn("Failed to sync", "length", len(op.Bytes()))
      log.Warn(fmt.Sprintf("Item bytes: %#x", op.Bytes()))
      return err
    }
  }
  return db.db.Sync()
}

// HasAncient returns an indicator whether the specified data exists in the
// ancient store.
func (db *DBWrapper) HasAncient(kind string, number uint64) (bool, error) {
  return db.db.HasAncient(kind, number)
}

// Ancient retrieves an ancient binary blob from the append-only immutable files.
func (db *DBWrapper) Ancient(kind string, number uint64) ([]byte, error) {
  return db.db.Ancient(kind, number)
}

// Ancients returns the ancient item numbers in the ancient store.
func (db *DBWrapper) Ancients() (uint64, error) {
  return db.db.Ancients()
}

// AncientSize returns the ancient size of the specified category.
func (db *DBWrapper) AncientSize(kind string) (uint64, error) {
  return db.db.AncientSize(kind)
}

func (db *DBWrapper) Compact(start []byte, limit []byte) error {
  // Note: We're not relaying the compact instruction to replicas. At present,
  // this seems to only be called from command-line tools and debug APIs. We
  // generally want to manage compaction of replicas at snapshotting time, and
  // not have it get triggered at runtime. This may need to be revisited in the
  // future.
  return db.db.Compact(start, limit)
}

func (db*DBWrapper) Stat(property string) (string, error) {
  return db.db.Stat(property)
}

func (db *DBWrapper) Close() error {
  if db.writeStream != nil {
    db.writeStream.Close()
  }
  if db.readStream != nil {
    db.readStream.Close()
  }
  return db.db.Close()
}

func (db *DBWrapper) NewBatch() ethdb.Batch {
  dbBatch := db.db.NewBatch()
  return &BatchWrapper{dbBatch, db.writeStream, []BatchOperation{}, uuid.NewRandom()}
}

// NewIterator creates a binary-alphabetical iterator over the entire keyspace
// contained within the key-value database.
func (db *DBWrapper) NewIterator(start, end []byte) ethdb.Iterator {
  return db.db.NewIterator(start, end)
}

func NewDBWrapper(db ethdb.Database, writeStream, readStream LogProducer) ethdb.Database {
  return &DBWrapper{db, writeStream, readStream}
}
