package replica

import (
  "testing"
  // "fmt"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/core/state"
  "github.com/ethereum/go-ethereum/trie"
  "golang.org/x/crypto/sha3"
)

var (
  emptyRoot = common.HexToHash("56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421")
)

func updateString(t *trie.Trie, k, v string) {
	t.Update([]byte(k), []byte(v))
}

func TestDelta(t *testing.T) {
  diskdb := rawdb.NewMemoryDatabase()
  statedb := state.NewDatabase(diskdb)
  s, err := state.New(emptyRoot, statedb, nil)
  if err != nil { t.Errorf(err.Error()) }
  s.SetState(common.Address{}, common.HexToHash("0x01"), common.HexToHash("0x01"))
  root, err := s.Commit(false)
  if err != nil { t.Fatalf(err.Error()) }
  if err := statedb.TrieDB().Commit(root, false); err != nil { t.Fatalf(err.Error()) }
  destructs, accounts, storage, err := DiffTries(diskdb, root, emptyRoot)
  if l := len(destructs); l > 0 { t.Errorf("Expected no destructs, got %v", l) }
  if l := len(accounts); l != 1 { t.Errorf("Expected 1 account, got %v", l) }
  hasher := sha3.NewLegacyKeccak256()
  hasher.Write(common.Address{}.Bytes())
  addrHash := common.BytesToHash(hasher.Sum(nil))
  hasher.Reset()
  hasher.Write(common.HexToHash("0x01").Bytes())
  keyHash := common.BytesToHash(hasher.Sum(nil))
  if common.BytesToHash(storage[addrHash][keyHash]) != common.HexToHash("0x01") {
    t.Errorf("Expected storage to equal 0x01, got %#x", storage[addrHash][keyHash])
  }
  // fmt.Printf("Diff:\nDestructs: %v\nAccounts: %v\nStorage: %v\nError: %v\nRoots: %#x - %#x", destructs, accounts, storage, err, root, emptyRoot)
  s.SetState(common.Address{}, common.HexToHash("0x01"), common.HexToHash("0x00"))
  s.SetState(common.Address{}, common.HexToHash("0x02"), common.HexToHash("0x02"))
  root, err = s.Commit(false)
  if err != nil { t.Fatalf(err.Error()) }
  if err := statedb.TrieDB().Commit(root, false); err != nil { t.Fatalf(err.Error()) }
  destructs, accounts, storage, err = DiffTries(diskdb, root, emptyRoot)
  if l := len(destructs); l > 0 { t.Errorf("Expected no destructs, got %v", l) }
  if l := len(accounts); l != 1 { t.Errorf("Expected 1 account, got %v", l) }
  if common.BytesToHash(storage[addrHash][keyHash]) != common.HexToHash("0x00") {
    t.Errorf("Expected storage to equal 0x00, got %#x", storage[addrHash][keyHash])
  }
  hasher.Reset()
  hasher.Write(common.HexToHash("0x02").Bytes())
  keyHash = common.BytesToHash(hasher.Sum(nil))
  if common.BytesToHash(storage[addrHash][keyHash]) != common.HexToHash("0x02") {
    t.Errorf("Expected storage to equal 0x02, got %#x", storage[addrHash][keyHash])
  }
}
