package replica


import (
  "bytes"
  // "fmt"
  "math/big"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core/state/snapshot"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/trie"
)

func accountData(value []byte) ([]byte, common.Hash, error) {
  var acc struct {
    Nonce    uint64
    Balance  *big.Int
    Root     common.Hash
    CodeHash []byte
  }
  if err := rlp.DecodeBytes(value, &acc); err != nil {
    return nil, common.Hash{}, err
  }
  return snapshot.SlimAccountRLP(acc.Nonce, acc.Balance, acc.Root, acc.CodeHash), acc.Root, nil
}

type compareRoots struct {
  old common.Hash
  new common.Hash
  account common.Hash
}

// Update(blockRoot common.Hash, parentRoot common.Hash, destructs map[common.Hash]struct{}, accounts map[common.Hash][]byte, storage map[common.Hash]map[common.Hash][]byte)

// DiffTries returns the destructs, accounts, and storage maps to transition the
// trie at blockRoot into the trie at parentRoot.
func DiffTries(db ethdb.KeyValueStore, blockRoot, parentRoot common.Hash) (map[common.Hash]struct{}, map[common.Hash][]byte, map[common.Hash]map[common.Hash][]byte, error) {
  tdb := trie.NewDatabase(db)
  blockTrie, err := trie.New(blockRoot, tdb)
  if err != nil { return nil, nil, nil, err}
  parentTrie, err := trie.New(parentRoot, tdb)
  if err != nil { return nil, nil, nil, err}
  destructs := make(map[common.Hash]struct{})
  accounts := make(map[common.Hash][]byte)
  accountRoots := []compareRoots{}
  blockIterator := blockTrie.NodeIterator([]byte{})
  parentIterator := parentTrie.NodeIterator([]byte{})
  blockNext := blockIterator.Next(true)
  parentNext := parentIterator.Next(true)
  for blockNext && parentNext {
    for blockNext && parentNext && bytes.Compare(blockIterator.Path(), parentIterator.Path()) < 0 {
      // blockIterator is behind parentIterator. Any leaves we find before
      // catching up are new accounts
  		if blockIterator.Leaf() {
        var root common.Hash
        accounts[common.BytesToHash(blockIterator.LeafKey())], root, err = accountData(blockIterator.LeafBlob())
        if err != nil { return nil, nil, nil, err }
        accountRoots = append(accountRoots, compareRoots{new: root, account: common.BytesToHash(blockIterator.LeafKey())})
      }
      blockNext = blockIterator.Next(true)
  	}
    for blockNext && parentNext && bytes.Compare(blockIterator.Path(), parentIterator.Path()) == 0 {
      if bytes.Compare(blockIterator.Hash().Bytes(), parentIterator.Hash().Bytes()) == 0 {
        // Hashes match, no difference between subtrees, no need to compare
        // further
        blockNext = blockIterator.Next(false)
        parentNext = parentIterator.Next(false)
      } else {
        // Paths match but hashes don't, continue exploring the subtree
        if blockIterator.Leaf(){
          var oldRoot, newRoot common.Hash
          _, oldRoot, err = accountData(parentIterator.LeafBlob())
          if err != nil { return nil, nil, nil, err }
          accounts[common.BytesToHash(blockIterator.LeafKey())], newRoot, err = accountData(blockIterator.LeafBlob())
          if err != nil { return nil, nil, nil, err }
          accountRoots = append(accountRoots, compareRoots{old: oldRoot, new: newRoot, account: common.BytesToHash(blockIterator.LeafKey())})
        }
        blockNext = blockIterator.Next(true)
        parentNext = parentIterator.Next(true)
      }
    }
    for parentNext && blockNext && bytes.Compare(blockIterator.Path(), parentIterator.Path()) > 0 {
      // parentIterator is behind blockIterator. Any leaves we find before
      // catching up are destructed accounts
      if parentIterator.Leaf() {
        destructs[common.BytesToHash(parentIterator.LeafKey())] = struct{}{}
      }
      parentNext = parentIterator.Next(true)
    }
  }
  for ; blockNext; blockNext = blockIterator.Next(true) {
    // The parent iterator is done, but we still have more in the block. The
    // rest are new accounts.
    if blockIterator.Leaf() {
      var root common.Hash
      accounts[common.BytesToHash(blockIterator.LeafKey())], root, err = accountData(blockIterator.LeafBlob())
      if err != nil { return nil, nil, nil, err }
      accountRoots = append(accountRoots, compareRoots{new: root, account: common.BytesToHash(blockIterator.LeafKey())})
    }
  }
  for ; parentNext; parentNext = parentIterator.Next(true) {
    // The block iterator is done, but we still have more in the parent. The
    // rest are destructed accounts.
    if parentIterator.Leaf() {
      destructs[common.BytesToHash(parentIterator.LeafKey())] = struct{}{}
    }
  }
  storage := make(map[common.Hash]map[common.Hash][]byte)
  for _, roots := range accountRoots {
    if roots.old == (common.Hash{}) {
      accountTrie, err  := trie.New(roots.new, tdb)
      if err != nil { return nil, nil, nil, err}
      accountIter := accountTrie.NodeIterator([]byte{})
      // Iterate over accountTrie, adding every leaf to storage
      storage[roots.account] = make(map[common.Hash][]byte)
      for accountIter.Next(true) {
        if accountIter.Leaf() {
          storage[roots.account][common.BytesToHash(accountIter.LeafKey())] = accountIter.LeafBlob()
        }
      }
      if err := accountIter.Error(); err != nil { return nil, nil, nil, err}
    } else {
      newTrie, err := trie.New(roots.new, tdb)
      if err != nil { return nil, nil, nil, err}
      oldTrie, err := trie.New(roots.old, tdb)
      if err != nil { return nil, nil, nil, err}
      newIter := newTrie.NodeIterator([]byte{})
      oldIter := oldTrie.NodeIterator([]byte{})
      // Compare new to old, adding any leaves to storage (values from newTrie, empty if present in oldTrie and not in newTrie)
      nextNew := newIter.Next(true)
      nextOld := oldIter.Next(true)
      for nextNew && nextOld {
        for nextNew && nextOld && bytes.Compare(newIter.Path(), oldIter.Path()) < 0 {
          if newIter.Leaf() {
            storage[roots.account][common.BytesToHash(newIter.LeafKey())] = newIter.LeafBlob()
          }
          nextNew = newIter.Next(true)
        }
        for nextNew && nextOld && bytes.Compare(newIter.Path(), oldIter.Path()) == 0 {
          if bytes.Compare(newIter.Hash().Bytes(), oldIter.Hash().Bytes()) == 0 {
            // Hashes match, no difference between subtrees, no need to compare
            // further
            nextNew = newIter.Next(false)
            nextOld = oldIter.Next(false)
          } else {
            // Paths match but hashes don't, continue exploring the subtree
            if newIter.Leaf(){
              storage[roots.account][common.BytesToHash(newIter.LeafKey())] = newIter.LeafBlob()
            }
            nextNew = newIter.Next(false)
            nextOld = oldIter.Next(false)
          }
        }
        for nextNew && nextOld && bytes.Compare(newIter.Path(), oldIter.Path()) > 0 {
          if oldIter.Leaf() {
            // It exists in old but not new, so it's been zeroed out
            storage[roots.account][common.BytesToHash(oldIter.LeafKey())] = []byte{}
          }
          nextOld = oldIter.Next(true)
        }
      }
      for ; nextNew ; nextNew = newIter.Next(true) {
        if newIter.Leaf() {
          storage[roots.account][common.BytesToHash(newIter.LeafKey())] = newIter.LeafBlob()
        }
      }
      for ; nextOld ; nextOld = oldIter.Next(true) {
        if oldIter.Leaf() {
          storage[roots.account][common.BytesToHash(oldIter.LeafKey())] = []byte{}
        }
      }
    }
  }
  return destructs, accounts, storage, nil
}
