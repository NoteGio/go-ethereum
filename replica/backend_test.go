package replica

import (
  // "fmt"
  // "encoding/hex"
  "context"
  "math/big"
  "github.com/ethereum/go-ethereum/eth/filters"
  "github.com/ethereum/go-ethereum/consensus/ethash"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core"
  "github.com/ethereum/go-ethereum/core/bloombits"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/core/vm"
  "github.com/ethereum/go-ethereum/params"
  // "github.com/ethereum/go-ethereum/node"
  "github.com/ethereum/go-ethereum/rpc"
  "testing"
  "time"
  "log"
)


type MockTransactionProducer struct {
  transactions []types.Transaction
  closed bool
}

func (producer *MockTransactionProducer) Emit(tx *types.Transaction) error {
  producer.transactions = append(producer.transactions, *tx)
  return nil
}

func (producer *MockTransactionProducer) Close() {
  producer.closed = true
}

func (producer *MockTransactionProducer) RelayTransactions(*core.TxPool) {}

func testReplicaBackend() (*ReplicaBackend, *MockTransactionProducer, error) {
  var (
		db      = rawdb.NewMemoryDatabase()
    // funds   = big.NewInt(1000000000)
    gspec   = &core.Genesis{
			Config: params.TestChainConfig,
			Alloc:  core.GenesisAlloc{},
		}
	)
  _ = new(core.Genesis).MustCommit(db)
  hc, err := core.NewHeaderChain(db, gspec.Config, ethash.NewFaker(), func() (bool) { return true })
  if err != nil {
    return nil, nil, err
  }
  bc, err := core.NewBlockChain(db, nil, gspec.Config, ethash.NewFaker(), vm.Config{}, nil, nil)
  if err != nil {
    return nil, nil, err
  }
  txProducer := &MockTransactionProducer{}
  return NewTestReplicaBackend(db, hc, bc, txProducer), txProducer, nil
}

func TestProtocolVersion(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  if netId := backend.ProtocolVersion(); netId != 1337 {
    t.Errorf("Network ID, expected 1337 got %v", netId)
  }
}

func TestDownloader(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  downloader := backend.Downloader()
  if current := downloader.Progress().CurrentBlock; current != 0 {
    t.Errorf("Unexpected current block: %v", current)
  }
}

func TestSuggestPrice(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  price, err := backend.SuggestPrice(context.Background())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if price == nil {
    t.Fatalf("Price is nil")
  }
  if p := price.Int64(); p != 0 {
    t.Errorf("Unexpected price: %v", p)
  }
}

func TestAccountManager(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  if length := len(backend.AccountManager().Wallets()); length != 0 {
    t.Errorf("Expected 0 wallets, got %v", length)
  }
}

func TestChainDb(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  db := backend.ChainDb()
  db.Put([]byte("Hello"), []byte("world"))
  val, err := db.Get([]byte("Hello"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  if string(val) != "world" {
    t.Errorf("Unexpected value: %v", string(val))
  }
}

func TestHeaderByNumber(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  if header.Number.Int64() != 0 {
    t.Errorf("Unexpected block number: %v", header.Number)
  }
}

func TestHeaderByHash(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  if header.Number.Int64() != 0 {
    t.Errorf("Unexpected block number: %v", header.Number)
  }
  headerByHash, err := backend.HeaderByHash(context.Background(), header.Hash())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if header.Hash() != headerByHash.Hash() {
    t.Fatalf("Unexpected header: %#x != %#x", header.Hash(), headerByHash.Hash())
  }
}


func TestBlockByNumber(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  block, err := backend.BlockByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  if block.Number().Int64() != 0 {
    t.Errorf("Unexpected block number: %v", block.Number())
  }
}
func TestBlockByNumberLatest(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  block, err := backend.BlockByNumber(context.Background(), rpc.LatestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  if block.Number().Int64() != 0 {
    t.Errorf("Unexpected block number: %v", block.Number())
  }
}
func TestStateAndHeaderByNumber(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  state, header, err := backend.StateAndHeaderByNumber(context.Background(), rpc.LatestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  if header.Number.Int64() != 0 {
    t.Errorf("Unexpected header number: %v", header.Number)
  }
  if err := state.Reset(header.Root); err != nil {
    t.Errorf(err.Error())
  }
}

func TestStateAndHeaderByNumberErr(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  state, header, err := backend.StateAndHeaderByNumber(context.Background(), rpc.BlockNumber(1000))
  if state != nil {
    t.Errorf("Unexpected state: %v", state)
  }
  if header != nil {
    t.Errorf("Unexpected header: %v", header)
  }
}

func TestGetBlock(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  block, err := backend.GetBlock(context.Background(), header.Hash())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if header.Hash() != block.Hash() {
    t.Fatalf("Unexpected header: %#x != %#x", header.Hash(), block.Hash())
  }
}

func TestGetReceipts(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  receipts, err := backend.GetReceipts(context.Background(), header.Hash())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if length := len(receipts); length != 0 {
    t.Fatalf("Got %v receipts, expected 0", length)
  }
}

func TestGetTd(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  td := backend.GetTd(header.Hash())
  if td.Int64() != 0 {
    t.Fatalf("Got unexpected td: %v", td)
  }
}

func TestGetEVM(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  msg := types.NewMessage(common.Address{}, &common.Address{}, 0, new(big.Int), 0, new(big.Int), []byte{}, false)
  state, header, err := backend.StateAndHeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  ctx, _ := context.WithTimeout(context.Background(), 5 * time.Millisecond)
  evm, vmErr, err := backend.GetEVM(ctx, msg, state, header)
  if err != nil {
    t.Fatalf(err.Error())
  }
  evm.Cancel()
  <-ctx.Done()
  err = vmErr()
  if err == nil {
    t.Errorf("Expected timeout error")
  }
}

func TestGetLogs(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  logs, err := backend.GetLogs(context.Background(), header.Hash())
  if err != nil {
    t.Fatalf(err.Error())
  }
  if length := len(logs); length != 0 {
    t.Fatalf("Got %v logs, expected 0", length)
  }
}

func TestGetLogsBadHash(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  logs, err := backend.GetLogs(context.Background(), common.Hash{})
  if err != nil {
    t.Fatalf(err.Error())
  }
  if logs != nil {
    t.Fatalf("Expected logs to be nil")
  }
}

func TestSubscribeLogsEvent(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  ch := make(chan<- []*types.Log)
  subscription := backend.SubscribeLogsEvent(ch)
  subscription.Unsubscribe()
}
func TestSubscribeRemovedLogsEvent(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  ch := make(chan core.RemovedLogsEvent)
  subscription := backend.SubscribeRemovedLogsEvent(ch)
  subscription.Unsubscribe()
}
func TestSubscribeChainEvent(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  ch := make(chan core.ChainEvent)
  subscription := backend.SubscribeChainEvent(ch)
  subscription.Unsubscribe()
}
func TestSubscribeChainHeadEvent(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  ch := make(chan core.ChainHeadEvent)
  subscription := backend.SubscribeChainHeadEvent(ch)
  subscription.Unsubscribe()
}
func TestSubscribeChainSideEvent(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  ch := make(chan core.ChainSideEvent)
  subscription := backend.SubscribeChainSideEvent(ch)
  subscription.Unsubscribe()
}
// func TestSendTx(t *testing.T) {
//   backend, txProducer, err := testReplicaBackend()
//   if err != nil {
//     t.Fatalf(err.Error())
//   }
//   sig, _ := hex.DecodeString("015aa36ecbdcd5ee3f8557cfe4dd8bd34a1f4e11b4a6731f215d1e184eaa058e32210ba77921ce26bb03da4af4b81cc1e7a91b39362e8f7d5e64af7dccfa79eb1b")
//   tx, err := types.NewTransaction(0, common.Address{}, new(big.Int), 1, new(big.Int), []byte{}).WithSignature(types.HomesteadSigner{}, sig)
//   if err != nil { t.Errorf(err.Error())}
//   if err := backend.SendTx(context.Background(), tx); err != nil {
//     t.Errorf(err.Error())
//   }
//   if len(txProducer.transactions) != 1 {
//     t.Errorf("Expected 1 transaction")
//   }
// }
func TestBloomStatus(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  bloombits, sections := backend.BloomStatus()
  if bloombits != 4096 || sections != 0 {
    t.Errorf("Expected %v == 4096, %v == 0", bloombits, sections)
  }
}
func TestServiceFilter(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  matcher := bloombits.NewMatcher(8, [][][]byte{})
  ctx, _ := context.WithTimeout(context.Background(), 20 * time.Millisecond)
  results := make(chan uint64)
  session, err := matcher.Start(ctx, 0, 0, results)
  defer session.Close()
  if err != nil {
    t.Errorf(err.Error())
  }
  backend.ServiceFilter(ctx, session)
  select {
  case _ = <-ctx.Done():
      log.Printf("Timed tout")
  case result := <-results:
      log.Printf("Got result: %v", result)
  }
}

func TestPublicFilterAPI(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  filterAPI := filters.NewPublicFilterAPI(backend, false)
  header, err := backend.HeaderByNumber(context.Background(), rpc.EarliestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  blockHash := header.Hash()
  query := filters.FilterCriteria {
  	BlockHash: &blockHash,
  	FromBlock: header.Number,
  	ToBlock: header.Number,
  	Addresses: []common.Address{},
  	Topics: [][]common.Hash{},
  }
  logs, err := filterAPI.GetLogs(context.Background(), query)
  if err != nil {
    t.Errorf(err.Error())
  }
  if len(logs) != 0 {
    t.Errorf("Unexpected log count %v", len(logs))
  }
}

func TestNoOps(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  poolTxs, err := backend.GetPoolTransactions()
  if err != nil {
    t.Errorf(err.Error())
  }
  if poolTxs != nil {
    t.Errorf("Expected poolTxs to be nil")
  }
  poolTx := backend.GetPoolTransaction(common.Hash{})
  if poolTx != nil {
    t.Errorf("Expected poolTx to be nil")
  }
  nonce, err := backend.GetPoolNonce(context.Background(), common.Address{})
  if err != nil {
    t.Errorf(err.Error())
  }
  if nonce != 0 {
    t.Errorf("Expected nonce to be nil")
  }
  pending, queued := backend.Stats()
  if pending != 0 || queued != 0 {
    t.Errorf("Expected %v = 0, %v = 0", pending, queued)
  }
  pendingMap, queuedMap := backend.TxPoolContent()
  if len(pendingMap) != 0 || len(queuedMap) != 0 {
    t.Errorf("Expected %v = 0, %v = 0", len(pendingMap), len(queuedMap))
  }
}

func TestChainConfig(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  chainConfig := backend.ChainConfig()
  if chainConfig.ChainID.Int64() != 1337 {
    t.Errorf("Unexpected chainConfig value %v != 1337", chainConfig.ChainID)
  }
}

func TestCurrentBlock(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  header, err := backend.HeaderByNumber(context.Background(), rpc.LatestBlockNumber)
  if err != nil {
    t.Fatalf(err.Error())
  }
  block := backend.CurrentBlock()
  if header.Number.Int64() != block.Number().Int64() {
    t.Errorf("Unexpected block number: %v", block.Number())
  }
}
