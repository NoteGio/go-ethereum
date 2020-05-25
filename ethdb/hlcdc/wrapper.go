package hlcdc
import (
  "fmt"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/log"
  "github.com/pborman/uuid"
)

type BatchWrapper struct {
  batch ethdb.Batch
  writeStream LogProducer
  ops []*operation
  // TODO: HL operations to apply
}

func (batch *BatchWrapper) Put(key, value []byte) (error) {
  return batch.batch.Put(key, value)
}

func (batch *BatchWrapper) Reset() {
  // TODO: Reset HL Operations
  batch.batch.Reset()
}

func (batch *BatchWrapper) Delete(key []byte) (error) {
  return batch.batch.Delete(key)
}

func (batch *BatchWrapper) ValueSize() int {
  return batch.batch.ValueSize()
}

func (batch *BatchWrapper) StateUpdate(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) {
  batch.ops = append(batch.ops, StateUpdate(blockRoot, parentRoot, destructs, accounts, storage)...)
}

func (batch *BatchWrapper) WriteBody(hash common.Hash, number uint64, data []byte) {
  batch.ops = append(batch.ops, WriteBody(hash, number, data))
}
func (batch *BatchWrapper) WriteTd(hash common.Hash, number uint64, td *big.Int) {
  batch.ops = append(batch.ops, WriteTd(hash, number, td))
}
func (batch *BatchWrapper) WriteHeader(hash common.Hash, number uint64, data []byte) {
  batch.ops = append(batch.ops, WriteHeader(hash, number, data))
}
func (batch *BatchWrapper) WriteReceipts(hash common.Hash, number uint64, data []byte) {
  batch.ops = append(batch.ops, WriteReceipts(hash, number, data))
}
func (batch *BatchWrapper) DeleteBlock(hash common.Hash, number uint64) {
  batch.ops = append(batch.ops, DeleteBlock(hash, number))
}

func (batch *BatchWrapper) Write() error {
  for _, op := range batch.ops {
    batch.writeStream.Emit(op.Bytes())
  }
  return batch.batch.Write()
}

// Replay replays the batch contents.
func (batch *BatchWrapper) Replay(w ethdb.KeyValueWriter) error {
  // NOTE: HLCDC ops are not being replayed at this time
	return batch.batch.Replay(w)
}

type DBWrapper struct {
  db ethdb.Database
  writeStream LogProducer
}

func (db *DBWrapper) Put(key, value []byte) error {
  return db.db.Put(key, value)
}

func (db *DBWrapper) Get(key []byte) ([]byte, error) {
  return db.db.Get(key)
}

func (db *DBWrapper) Has(key []byte) (bool, error) {
  return db.db.Has(key)
}

func (db *DBWrapper) Delete(key []byte) error {
  return db.db.Delete(key)
}

// AppendAncient injects all binary blobs belong to block at the end of the
// append-only immutable table files.
func (db *DBWrapper) AppendAncient(number uint64, hash, header, body, receipt, td []byte) error {
  return db.db.AppendAncient(number, hash, header, body, receipt, td)
}

// TruncateAncients discards all but the first n ancient data from the ancient store.
func (db *DBWrapper) TruncateAncients(n uint64) error {
  return db.db.TruncateAncients(n)
}

// Sync flushes all in-memory ancient store data to disk.
func (db *DBWrapper) Sync() error {
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

func (db *DBWrapper) StateUpdate(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) {
  db.writeStream.Emit(StateUpdate(blockRoot, parentRoot, destructs, accounts, storage).Bytes())
}

func (db *DBWrapper) WriteBody(hash common.Hash, number uint64, data []byte) {
  db.writeStream.Emit(WriteBody(hash, number, data).Bytes())
}
func (db *DBWrapper) WriteTd(hash common.Hash, number uint64, td *big.Int) {
  db.writeStream.Emit(WriteTd(hash, number, td).Bytes())
}
func (db *DBWrapper) WriteHeader(hash common.Hash, number uint64, data []byte) {
  db.writeStream.Emit(WriteHeader(hash, number, data).Bytes())
}
func (db *DBWrapper) WriteReceipts(hash common.Hash, number uint64, data []byte) {
  db.writeStream.Emit(WriteReceipts(hash, number, data).Bytes())
}
func (db *DBWrapper) DeleteBlock(hash common.Hash, number uint64) {
  db.writeStream.Emit(DeleteBlock(hash, number).Bytes())
}

func (db*DBWrapper) Stat(property string) (string, error) {
  return db.db.Stat(property)
}

func (db *DBWrapper) Close() error {
  if db.writeStream != nil {
    db.writeStream.Close()
  }
  return db.db.Close()
}

func (db *DBWrapper) NewBatch() ethdb.Batch {
  dbBatch := db.db.NewBatch()
  return &BatchWrapper{dbBatch, db.writeStream, []BatchOperation{}}
}

// NewIterator creates a binary-alphabetical iterator over the entire keyspace
// contained within the key-value database.
func (db *DBWrapper) NewIterator(start, end []byte) ethdb.Iterator {
  return db.db.NewIterator(start, end)
}

func NewDBWrapper(db ethdb.Database, writeStream LogProducer) ethdb.Database {
  return &DBWrapper{db, writeStream, readStream}
}
