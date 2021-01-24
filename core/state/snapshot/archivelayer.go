// The archive layer stores changes to state data, along with the necessary
// metadata to determine what state data was current for a particular state
// root.

// The fundamental new concept introduced here is the strand. A strand is a
// linear set of state roots, with each state root reflecting some set of data.
// Within a strand, each state root can have only one parent and one child.
// However, if a state root must have a second child, a new strand can be
// created with a parent of the previous strand.

// State data is stored with respect to a strand. The archive layer tracks how
// many times a particular key has been changed within a strand, and keeps track
// of the range of strand heads for which each version of the key was current.
// When a key is updated, the previous record for the same key is updated to
// reflect the last root for which it was valid. When looking up a key within a
// strand, we do a binary search for the instance of the key that existed at the
// specified root. If we get to the beginning of the strand without finding the
// current version, we go to the strand's parent. If we get to a strand with no
// parent without finding the key, then it does not exist.

package snapshot

import (
  "fmt"
  "github.com/VictoriaMetrics/fastcache"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/crypto"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/rlp"
  "sync"
)

// Snapshot represents the functionality supported by a snapshot storage layer.
// type Snapshot interface {
//   Root() common.Hash
//   Account(hash common.Hash) (*Account, error)
//   AccountRLP(hash common.Hash) ([]byte, error)
//   Storage(accountHash, storageHash common.Hash) ([]byte, error)
//   Snapshot
//   Parent() snapshot
//   Update(blockRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) *diffLayer
//   Journal(buffer *bytes.Buffer) (common.Hash, error)
//   Stale() bool
//   AccountIterator(seek common.Hash) AccountIterator
//   StorageIterator(account common.Hash, seek common.Hash) (StorageIterator, bool)
// }

var (
  rootPrefix = []byte("_R")
  strandPrefix = []byte("_s")
  stateCounterPrefix = []byte("_c")
  stateRangesPrefix = []byte("_r")
  stateValuesPrefix = []byte("_v")
  // These prefixes won't collide, as they're key prefixes within the versioned
  // index, not straight leveldb keys
  destructsPrefix = []byte("d")
  accountsPrefix = []byte("a")
  // emptyRoot = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
)

type archiveRoot struct {
  Strand common.Hash
  ParentRoot common.Hash
  Index uint64
  hash common.Hash
}

func (r *archiveRoot) Store(db ethdb.KeyValueWriter) error {
  data, err := rlp.EncodeToBytes(r)
  if err != nil { return err }
  return db.Put(rootKey(r.hash), data)
}

type strand struct {
  Head uint64
  ParentStrand common.Hash
  id common.Hash
}

func (s *strand) Store(db ethdb.KeyValueStore) error {
  data, err := rlp.EncodeToBytes(s)
  if err != nil { return err }
  return db.Put(strandKey(s.id), data)
}

type strandKeyRange struct {
  Lo uint64
  Hi uint64
  Value common.Hash
}

func rootKey(root common.Hash) []byte {
  return append(rootPrefix, root.Bytes()...)
}

func strandKey(strand common.Hash) []byte {
  return append(strandPrefix, strand.Bytes()...)
}

func stateCounterKey(strand common.Hash, key []byte) []byte {
  return append(append(stateCounterPrefix, strand.Bytes()...), key...)
}

func stateRangesKey(strand common.Hash, key []byte, n uint64) []byte {
  nBytes, _ := rlp.EncodeToBytes(n)
  return append(append(append(stateRangesPrefix, strand.Bytes()...), key...), nBytes...)
}

func stateValueKey(key common.Hash) []byte{
  return append(stateValuesPrefix, key.Bytes()...)
}

type archiveStore struct {
  diskdb ethdb.KeyValueStore
  cache  *fastcache.Cache
  lock   sync.RWMutex
}

func getRoot(db ethdb.KeyValueReader, root common.Hash) *archiveRoot {
  data, _ := db.Get(rootKey(root))
  if len(data) == 0 {
    return nil
  }
  item := &archiveRoot{}
  if err := rlp.DecodeBytes(data, item); err != nil {
    return nil
  }
  item.hash = root
  return item
}

func getStrand(db ethdb.KeyValueReader, id common.Hash) *strand {
  result := &strand{}
  data, _ := db.Get(strandKey(id))
  if len(data) == 0 {
    return nil
  }
  if err := rlp.DecodeBytes(data, result); err != nil {
    return nil
  }
  result.id = id
  return result
}

func getStateCount(db ethdb.KeyValueReader, strand common.Hash, key []byte) (uint64) {
  var count uint64
  data, _ := db.Get(stateCounterKey(strand, key))
  if len(data) == 0 { return 0 }
  rlp.DecodeBytes(data, &count)
  return count
}

func bumpStateCount(reader ethdb.KeyValueReader, writer ethdb.KeyValueWriter, strand common.Hash, key []byte) (uint64) {
  count := getStateCount(reader, strand, key)
  data, _ := rlp.EncodeToBytes(count + 1)
  writer.Put(stateCounterKey(strand, key), data)
  return count
}

func getKeyRange(db ethdb.KeyValueReader, strand common.Hash, key []byte, n uint64) (*strandKeyRange, error) {
  keyRange := &strandKeyRange{}
  rangeData, _ := db.Get(stateRangesKey(strand, key, n))
  if len(rangeData) == 0 {
    return nil, fmt.Errorf("Key range not found")
  }
  err := rlp.DecodeBytes(rangeData, keyRange)
  return keyRange, err
}

func addKey(reader ethdb.KeyValueReader, writer ethdb.KeyValueWriter, s *strand, key, value []byte) error {
  count := bumpStateCount(reader, writer, s.id, key)
  if count > 0 {
    // Set the high end of the key range to the strand head
    oldKeyRange, err := getKeyRange(reader, s.id, key, count - 1)
    if err != nil { return err }
    oldKeyRange.Hi = s.Head
    data, err := rlp.EncodeToBytes(oldKeyRange)
    if err != nil { return err }
    writer.Put(stateRangesKey(s.id, key, count - 1), data)
  }
  newKeyRange := &strandKeyRange{
    Lo: s.Head,
    Hi: ^uint64(0),
    Value: crypto.Keccak256Hash(value),
  }
  data, err := rlp.EncodeToBytes(newKeyRange)
  if err != nil { return err }
  writer.Put(stateRangesKey(s.id, key, count), data)
  writer.Put(stateValueKey(newKeyRange.Value), value)
  return nil
}

func getKeyStrand(db ethdb.KeyValueReader, rootStrand *strand, key []byte, minHead uint64) ([]byte, error) {
  if rootStrand == nil { return []byte{}, fmt.Errorf("Strand not found") }
  high := getStateCount(db, rootStrand.id, key)
  // Look up the latest value first, then binary search. This adjusts for the high likelihood that the latest value is most likely to be accessed
  n := high - 1
  low := uint64(0)
  for high != low {
    keyRange, err := getKeyRange(db, rootStrand.id, key, n)
    if err != nil { return nil, err }
    if keyRange.Lo < minHead {
      // If minHead > 0, that indicates we're getting a value for a destructed
      // contract. If this value existed before that contract was destructed, we
      // consider it invalid.
      low = n
    }
    if keyRange.Lo < n && n < keyRange.Hi {
      return db.Get(stateValueKey(keyRange.Value))
    } else if n < keyRange.Lo {
      if n == low {
        // Not found in this strand, check the parent strand
        if rootStrand.ParentStrand != (common.Hash{}) {
          return getKeyStrand(db, getStrand(db, rootStrand.ParentStrand), key, 0)
        }
      }
      low = n
    } else {
      high = n
    }
    n = (low + high) / 2
  }
  return []byte{}, fmt.Errorf("Strand not found")
}

func getKey(db ethdb.KeyValueReader, rootHash common.Hash, key []byte) ([]byte, error) {
  root := getRoot(db, rootHash)
  rootStrand := getStrand(db, root.Strand)
  return getKeyStrand(db, rootStrand, key, 0)
}

func (as *archiveStore) Update(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) error {
  if ok, _ := as.diskdb.Has(rootKey(blockRoot)); ok {
    // We already have this root. Short circuit.
    return nil
  }
  as.lock.RLock()
  defer as.lock.RUnlock()
  batch := as.diskdb.NewBatch()
  var rootStrand *strand
  if parentRoot != emptyRoot {
    parentItem := getRoot(as.diskdb, parentRoot)
    if parentItem == nil { return fmt.Errorf("Parent root %#x not available", parentRoot) }
    rootStrand = getStrand(as.diskdb, parentItem.Strand)
    if rootStrand == nil { return fmt.Errorf("Parent strand %#x not available", parentItem.Strand)}
    if rootStrand.Head > parentItem.Index {
      // The strand has moved past the parent root, so we're starting a new
      // strand.
      rootStrand.id = crypto.Keccak256Hash(append(parentRoot.Bytes(), blockRoot.Bytes()...))
    }
  } else {
    // New strand off of the empty root
    rootStrand = &strand{
      Head: 0,
      ParentStrand: common.Hash{},
      id: blockRoot,
    }
  }
  rootStrand.Head++
  itemRoot := &archiveRoot{
    Strand: rootStrand.id,
    ParentRoot: parentRoot,
    Index: rootStrand.Head,
    hash: blockRoot,
  }
  // TODO: When an account is listed in destructs, does its data field get
  // emptied out? If not, we should empty it. If so, we should clear out the
  // destructs entry when the account gets updated. We also need to figure out
  // how to keep storage entries from showing back up on a contract that was
  // destructed then recreated.
  headBytes, _ := rlp.EncodeToBytes(rootStrand.Head)
  for account := range destructs {
    // Store the strand head at which the account was destructed
    addKey(as.diskdb, batch, rootStrand, append(destructsPrefix, account.Bytes()...), headBytes)
  }
  for account, data := range accounts {
    addKey(as.diskdb, batch, rootStrand, append(accountsPrefix, account.Bytes()...), data)
  }
  for account, mapping := range storage {
    for key, value := range mapping {
      addKey(as.diskdb, batch, rootStrand, append(account.Bytes(), key.Bytes()...), value)
    }
  }
  if err := itemRoot.Store(batch); err != nil { return err }
  return batch.Write()
}

type archiveLayer struct {
  store *archiveStore
  root  common.Hash
}

func (al *archiveLayer) Root() common.Hash {
  return al.root
}
func (al *archiveLayer) Account(hash common.Hash) (*Account, error) {
  data, err := al.AccountRLP(hash)
  if err != nil {
    return nil, err
  }
  if len(data) == 0 { // can be both nil and []byte{}
    return nil, nil
  }
  account := new(Account)
  if err := rlp.DecodeBytes(data, account); err != nil {
    panic(err)
  }
  return account, nil
}
func (al *archiveLayer) AccountRLP(hash common.Hash) ([]byte, error) {
  return getKey(al.store.diskdb, al.root, append(accountsPrefix, hash.Bytes()...))

}
func (al *archiveLayer) Storage(accountHash, storageHash common.Hash) ([]byte, error) {
  destructData, err := getKey(al.store.diskdb, al.root, append(destructsPrefix, accountHash.Bytes()...))
  if err != nil { return nil, err }
  destructHead := uint64(0)
  if len(destructData) != 0 {
    if err := rlp.DecodeBytes(destructData, &destructHead); err != nil { return nil, err }
  }
  root := getRoot(al.store.diskdb, al.root)
  rootStrand := getStrand(al.store.diskdb, root.Strand)
  return getKeyStrand(al.store.diskdb, rootStrand, storageHash.Bytes(), destructHead)
}
//   Snapshot
//   Parent() snapshot
//   Update(blockRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte) *diffLayer
//   Journal(buffer *bytes.Buffer) (common.Hash, error)
//   Stale() bool
//   AccountIterator(seek common.Hash) AccountIterator
//   StorageIterator(account common.Hash, seek common.Hash) (StorageIterator, bool)
