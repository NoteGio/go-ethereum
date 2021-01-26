package snapshot

import (
  "fmt"
  "math/big"
  "testing"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/ethdb/memorydb"
)

func getStore() *archiveStore {
  return &archiveStore{diskdb: memorydb.New()}
}

func TestUpdateLayer(t *testing.T) {
  as := getStore()

  acctData := SlimAccountRLP(0, new(big.Int), emptyRoot, emptyCode.Bytes())

  root1 := common.HexToHash("0x01")
  if err := as.Update(
    root1,
    emptyRoot,
    map[common.Hash]struct{}{},
    map[common.Hash][]byte{common.HexToHash("0x0001"): acctData},
    map[common.Hash]map[common.Hash][]byte{common.HexToHash("0x0001"): map[common.Hash][]byte{common.HexToHash("0x000001"): []byte("Hello world")}},
  ); err != nil { t.Errorf(err.Error()) }

  al := &archiveLayer{store: as, root: root1}
  acct, err := al.Account(common.HexToHash("0x0001"))
  if err != nil { t.Fatalf(err.Error()) }
  if acct.Nonce != 0 { t.Errorf("Unexpected nonce: %v", acct.Nonce) }
  if acct.Balance.Cmp(new(big.Int)) != 0 { t.Errorf("Unexpected balance: %v", acct.Balance)}
  data, err := al.Storage(common.HexToHash("0x0001"), common.HexToHash("0x000001"))
  if err != nil { t.Fatalf(err.Error()) }
  if string(data) != "Hello world" { t.Errorf("Unexpected data '%v'", string(data))}

  root2 := common.HexToHash("0x02")
  if err := as.Update(
    root2,
    root1,
    map[common.Hash]struct{}{},
    map[common.Hash][]byte{common.HexToHash("0x0002"): acctData},
    map[common.Hash]map[common.Hash][]byte{common.HexToHash("0x0002"): map[common.Hash][]byte{common.HexToHash("0x000001"): []byte("Hello world")}},
  ); err != nil { t.Errorf(err.Error()) }


  al = &archiveLayer{store: as, root: root2}
  acct, err = al.Account(common.HexToHash("0x0001"))
  if err != nil { t.Fatalf(err.Error()) }
  if acct.Nonce != 0 { t.Errorf("Unexpected nonce: %v", acct.Nonce) }
  if acct.Balance.Cmp(new(big.Int)) != 0 { t.Errorf("Unexpected balance: %v", acct.Balance)}
  data, err = al.Storage(common.HexToHash("0x0001"), common.HexToHash("0x000001"))
  if err != nil { t.Fatalf(err.Error()) }
  if string(data) != "Hello world" { t.Errorf("Unexpected data %v", string(data))}
  acct, err = al.Account(common.HexToHash("0x0002"))
  if err != nil { t.Fatalf(err.Error()) }
  if acct.Nonce != 0 { t.Errorf("Unexpected nonce: %v", acct.Nonce) }
  if acct.Balance.Cmp(new(big.Int)) != 0 { t.Errorf("Unexpected balance: %v", acct.Balance)}
  data, err = al.Storage(common.HexToHash("0x0002"), common.HexToHash("0x000001"))
  if err != nil { t.Fatalf(err.Error()) }
  if string(data) != "Hello world" { t.Errorf("Unexpected data %v", string(data))}


  fmt.Println("---")
  root3 := common.HexToHash("0x03")
  if err := as.Update(
    root3,
    root1,
    map[common.Hash]struct{}{},
    map[common.Hash][]byte{common.HexToHash("0x0002"): acctData},
    map[common.Hash]map[common.Hash][]byte{common.HexToHash("0x0002"): map[common.Hash][]byte{common.HexToHash("0x000001"): []byte("Goodbye world")}},
  ); err != nil { t.Errorf(err.Error()) }

  al = &archiveLayer{store: as, root: root3}

  // alr3 := getRoot(as.diskdb, root3)
  // strand := getStrand(as.diskdb, alr3.Strand)
  // fmt.Printf("R: %#x P: %#x", alr3.Strand, strand.ParentStrand)

  acct, err = al.Account(common.HexToHash("0x0001"))
  if err != nil { t.Fatal(err.Error()) }
  if acct.Nonce != 0 { t.Errorf("Unexpected nonce: %v", acct.Nonce) }
  if acct.Balance.Cmp(new(big.Int)) != 0 { t.Errorf("Unexpected balance: %v", acct.Balance)}
  data, err = al.Storage(common.HexToHash("0x0001"), common.HexToHash("0x000001"))
  if err != nil { t.Fatalf(err.Error()) }
  if string(data) != "Hello world" { t.Errorf("Unexpected data %v", string(data))}
  acct, err = al.Account(common.HexToHash("0x0002"))
  if err != nil { t.Fatalf(err.Error()) }
  if acct.Nonce != 0 { t.Errorf("Unexpected nonce: %v", acct.Nonce) }
  if acct.Balance.Cmp(new(big.Int)) != 0 { t.Errorf("Unexpected balance: %v", acct.Balance)}
  data, err = al.Storage(common.HexToHash("0x0002"), common.HexToHash("0x000001"))
  if err != nil { t.Fatalf(err.Error()) }
  if string(data) != "Goodbye world" { t.Errorf("Unexpected data %v", string(data))}
}

// TODO: Test fork, destructs, and more
