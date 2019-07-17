package badger

import (
  badger "github.com/dgraph-io/badger"
  "github.com/dgraph-io/badger/options"
  "github.com/ethereum/go-ethereum/ethdb"
  // "github.com/ethereum/go-ethereum/log"
)

type badgerDatabase struct {
  db *badger.DB
}

func (bdb *badgerDatabase) Put(key []byte, value []byte) error {
  // log.Info("Start bdb.Put()")
  // defer log.Info("End bdb.Put()")
  return bdb.db.Update(func(txn *badger.Txn) error {
    return txn.Set(key, value)
  })
}

func (bdb *badgerDatabase) Delete(key []byte) error {
  // log.Info("Start bdb.Delete()")
  // defer log.Info("End bdb.Delete()")
  return bdb.db.Update(func(txn *badger.Txn) error {
    return txn.Delete(key)
  })
}

func (bdb *badgerDatabase) Get(key []byte) ([]byte, error) {
  // log.Info("Start bdb.Get()")
  // defer log.Info("End bdb.Get()")
  res := make(chan []byte, 2)
  err := bdb.db.View(func(txn *badger.Txn) error {
    // log.Info("Start View()")
    // defer log.Info("End View()")
    item, err := txn.Get(key)
    if err != nil {
      res <- []byte{}
      return err
    }
    val, err := item.ValueCopy(nil)
    res <- val
    return err
  })
  return <-res, err
}

func (bdb *badgerDatabase) Has(key []byte) (bool, error) {
  // log.Info("Start bdb.Has()")
  // defer log.Info("End bdb.Has()")
  res := make(chan bool, 2)
  err := bdb.db.View(func(txn *badger.Txn) error {
    _, err := txn.Get(key)
    if err != nil {
      res <- false
      if err == badger.ErrKeyNotFound {
        return nil
      }
      return err
    }
    res <- true
    return nil
  })
  return <-res, err
}

func (bdb *badgerDatabase) Close() {
  bdb.db.Close()
}

func (bdb *badgerDatabase) NewBatch() ethdb.Batch {
  return &badgerBatch{bdb.db.NewWriteBatch(), bdb.db, 0}
}

func NewDatabase(path string) (ethdb.Database, error) {
  db, err := badger.Open(badger.DefaultOptions(path).WithValueLogLoadingMode(options.FileIO))
  return &badgerDatabase{db}, err
}

type badgerBatch struct {
  batch *badger.WriteBatch
  db *badger.DB
  size int
}

func (bb *badgerBatch) Put(key []byte, value []byte) error {
  // log.Info("Start bb.Put()")
  // defer log.Info("End bb.Put()")
  bb.size += len(value)
  return bb.batch.Set(key, value)
}

func (bb *badgerBatch) Delete(key []byte) error {
  // log.Info("Start bb.Delete()")
  // defer log.Info("End bb.Delete()")
  return bb.batch.Delete(key)
}

func (bb *badgerBatch) ValueSize() int {
  return bb.size
}

func (bb *badgerBatch) Write() error {
  // log.Info("Start bb.Write()")
  // defer log.Info("End bb.Write()")
  return bb.batch.Flush()
}

func (bb *badgerBatch) Reset() {
  // log.Info("Start bb.Reset()")
  // defer log.Info("End bb.Reset()")
  bb.batch = bb.db.NewWriteBatch()
  bb.size = 0
}
