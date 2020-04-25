package replica

import (
  "context"
  "encoding/binary"
  "errors"
  "fmt"
  "math/big"
  "github.com/ethereum/go-ethereum/eth/downloader"
  "github.com/ethereum/go-ethereum/eth/gasprice"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/event"
  "github.com/ethereum/go-ethereum/accounts"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/common/bitutil"
  "github.com/ethereum/go-ethereum/common/math"
  "github.com/ethereum/go-ethereum/rpc"
  "github.com/ethereum/go-ethereum/core"
  "github.com/ethereum/go-ethereum/core/bloombits"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/core/vm"
  "github.com/ethereum/go-ethereum/core/state"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/params"
  "github.com/ethereum/go-ethereum/log"
  "github.com/ethereum/go-ethereum/trie"
  "runtime"
  "strings"
  "time"
)

type ReplicaBackend struct {
  db ethdb.Database
  indexDb ethdb.Database
  hc *core.HeaderChain
  chainConfig *params.ChainConfig
  bc *core.BlockChain
  transactionProducer TransactionProducer
  transactionConsumer TransactionConsumer
  eventMux *event.TypeMux
  dl *downloader.Downloader
  bloomRequests chan chan *bloombits.Retrieval
  shutdownChan chan bool
  accountManager *accounts.Manager
  gpo *gasprice.Oracle
  blockHeads <-chan []byte
  logsFeed event.Feed
  pendingLogsFeed event.Feed
  removedLogsFeed event.Feed
  chainFeed event.Feed
  chainHeadFeed event.Feed
  chainSideFeed event.Feed
  evmSemaphore chan struct{}
  txPool *core.TxPool
}

	// General Ethereum API
	// Block synchronization seems to happen at the downloader under normaly circumstances
func (backend *ReplicaBackend) Downloader() *downloader.Downloader {								// Seems to be used to get sync progress, cancel downloads {
  if backend.dl == nil {
                             // checkpoint uint64, stateDb ethdb.Database, stateBloom *trie.SyncBloom, mux *event.TypeMux, chain BlockChain, lightchain LightChain, dropPeer peerDropFn
    backend.dl = downloader.New(0, backend.db, nil, backend.eventMux, backend.bc, nil, func(id string){})
    backend.dl.Terminate()
  }
  return backend.dl
}
func (backend *ReplicaBackend) ProtocolVersion() int {
  return int(backend.chainConfig.ChainID.Int64())
}
func (backend *ReplicaBackend) SuggestPrice(ctx context.Context) (*big.Int, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  if backend.gpo == nil {
    backend.gpo = gasprice.NewOracle(backend, gasprice.Config{
      Blocks:     20,
      Percentile: 60,
      Default: new(big.Int),
    })
  }
  return backend.gpo.SuggestPrice(ctx)
}
func (backend *ReplicaBackend) ChainDb() ethdb.Database {
  return backend.db
}
func (backend *ReplicaBackend) EventMux() *event.TypeMux {
  return backend.eventMux
}
func (backend *ReplicaBackend) AccountManager() *accounts.Manager {
  if backend.accountManager == nil {
    backend.accountManager = accounts.NewManager(&accounts.Config{false})
  }
  return backend.accountManager
}

	// BlockChain API

// If we don't offer the private debug APIs, we don't need SetHead
func (backend *ReplicaBackend) SetHead(number uint64) {

}
func (backend *ReplicaBackend) HeaderByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*types.Header, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  if blockNr == rpc.LatestBlockNumber {
    latestHash := rawdb.ReadHeadHeaderHash(backend.db)
		return backend.hc.GetHeaderByHash(latestHash), nil
	}
	return backend.hc.GetHeaderByNumber(uint64(blockNr)), nil
}

func (backend *ReplicaBackend) HeaderByHash(ctx context.Context, blockHash common.Hash) (*types.Header, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  return backend.hc.GetHeaderByHash(blockHash), nil
}

func (backend *ReplicaBackend) BlockByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*types.Block, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  if blockNr == rpc.LatestBlockNumber || blockNr == rpc.PendingBlockNumber {
    latestHash := rawdb.ReadHeadBlockHash(backend.db)
		return backend.bc.GetBlockByHash(latestHash), nil
	}
	return backend.bc.GetBlockByNumber(uint64(blockNr)), nil
}
func (backend *ReplicaBackend) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  return backend.bc.GetBlockByHash(hash), nil
}

	// For StateAndHeaderByNumber, we'll need to construct a core.state object from
	// the state root for the specified block and the chaindb.
func (backend *ReplicaBackend) StateAndHeaderByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*state.StateDB, *types.Header, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, nil, err } }
  block, err := backend.BlockByNumber(ctx, blockNr)
  if block == nil || err != nil {
    return nil, nil, fmt.Errorf("stateAndHeaderByNumber: blockByNumber: %v", err)
  }
  stateDB, err := backend.bc.StateAt(block.Root())
  if err != nil {
    err = fmt.Errorf("statendHeaderByNumber: stateAt: %v", err)
  }
  return stateDB, block.Header(), err
}

	// This will need to rely on core.database_util.GetBlock instead of the core.blockchain version
func (backend *ReplicaBackend) GetBlock(ctx context.Context, blockHash common.Hash) (*types.Block, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  return backend.bc.GetBlockByHash(blockHash), nil
}
	// Proxy rawdb.ReadBlockReceipts
func (backend *ReplicaBackend) GetReceipts(ctx context.Context, blockHash common.Hash) (types.Receipts, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  return backend.bc.GetReceiptsByHash(blockHash), nil
}
	// This can probably lean on core.HeaderChain
func (backend *ReplicaBackend) GetTd(blockHash common.Hash) *big.Int {
  return backend.hc.GetTdByHash(blockHash)
}
	// Use core.NewEVMContext and vm.NewEVM - Will need custom ChainContext implementation
func (backend *ReplicaBackend) GetEVM(ctx context.Context, msg core.Message, state *state.StateDB, header *types.Header) (*vm.EVM, func() error, error) {
  state.SetBalance(msg.From(), math.MaxBig256)
  vmError := func() error {
    if backend.evmSemaphore != nil {
      <-backend.evmSemaphore
    }
    if err := state.Error(); err != nil {
      return err
    }
    return ctx.Err()
  }

  context := core.NewEVMContext(msg, header, backend.bc, nil)
  evm := vm.NewEVM(context, state, backend.chainConfig, *backend.bc.GetVMConfig())
  if backend.evmSemaphore != nil {
    backend.evmSemaphore <- struct{}{}
  }
  return evm, vmError, nil
}

func (backend *ReplicaBackend) GetLogs(ctx context.Context, blockHash common.Hash) ([][]*types.Log, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  receipts := backend.bc.GetReceiptsByHash(blockHash)
  if receipts == nil {
    return nil, nil
  }
  logs := make([][]*types.Log, len(receipts))
  for i, receipt := range receipts {
    logs[i] = receipt.Logs
  }
  return logs, nil
}

func (backend *ReplicaBackend) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
  return backend.logsFeed.Subscribe(ch)
}

func (backend *ReplicaBackend) SubscribePendingLogsEvent(ch chan<- []*types.Log) event.Subscription {
  return backend.pendingLogsFeed.Subscribe(ch)
}

func (backend *ReplicaBackend) SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription {
  return backend.removedLogsFeed.Subscribe(ch)
}

	// I Don't think these are really need for RPC calls. Maybe stub them out?
func (backend *ReplicaBackend) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
  return backend.chainFeed.Subscribe(ch)
}
func (backend *ReplicaBackend) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
  return backend.chainHeadFeed.Subscribe(ch)
}
func (backend *ReplicaBackend) SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription {
  return backend.chainSideFeed.Subscribe(ch)
}

	// TxPool API

func (backend *ReplicaBackend) SendTx(ctx context.Context, signedTx *types.Transaction) error {
  if ctx != nil { if err := ctx.Err(); err != nil { return err } }
  if backend.transactionProducer == nil {
    return errors.New("This api is not configured for accepting transactions")
  }
  currentState, header, err := backend.StateAndHeaderByNumber(ctx, rpc.LatestBlockNumber)
  if err != nil {
    return err
  }
  msg, err := signedTx.AsMessage(types.MakeSigner(backend.chainConfig, header.Number))
  if err != nil {
    return err
  }
  if n := currentState.GetNonce(msg.From()); n > signedTx.Nonce() {
    return core.ErrNonceTooLow
  }

  // Check the transaction doesn't exceed the current
  // block limit gas.
  if header.GasLimit < signedTx.Gas() {
    return core.ErrGasLimit
  }

  // Transactions can't be negative. This may never happen
  // using RLP decoded transactions but may occur if you create
  // a transaction using the RPC for example.
  if signedTx.Value().Sign() < 0 {
    return core.ErrNegativeValue
  }

  // Transactor should have enough funds to cover the costs
  // cost == V + GP * GL
  if b := currentState.GetBalance(msg.From()); b.Cmp(signedTx.Cost()) < 0 {
    return core.ErrInsufficientFunds
  }

  // Should supply enough intrinsic gas
  gas, err := core.IntrinsicGas(signedTx.Data(), signedTx.To() == nil, true, backend.chainConfig.IsIstanbul(header.Number))
  if err != nil {
    return err
  }
  if signedTx.Gas() < gas {
    return core.ErrIntrinsicGas
  }
  return backend.transactionProducer.Emit(signedTx)
}

func (backend *ReplicaBackend) BloomStatus() (uint64, uint64) {
  var sections uint64
  data, _ := backend.indexDb.Get([]byte("count"))
	if len(data) == 8 {
		sections = binary.BigEndian.Uint64(data)
	}
  return params.BloomBitsBlocks, sections
}

func (backend *ReplicaBackend) ServiceFilter(ctx context.Context, session *bloombits.MatcherSession) {
  bloomFilterThreads := 3
  bloomServiceThreads := 16
  bloomBatch := 16
  bloomBlockBits := uint64(4096)
  bloomWait := time.Duration(0)
  if backend.bloomRequests == nil {
    backend.bloomRequests = make(chan chan *bloombits.Retrieval)
  	for i := 0; i < bloomServiceThreads; i++ {
  		go func(sectionSize uint64) {
  			for {
  				select {
  				case <-backend.shutdownChan:
  					return

  				case request := <-backend.bloomRequests:
  					task := <-request
            if err := task.Context.Err(); err != nil {
              task.Error = err
              request <- task
              continue
            }
  					task.Bitsets = make([][]byte, len(task.Sections))
  					for i, section := range task.Sections {
  						head := rawdb.ReadCanonicalHash(backend.db, (section+1)*sectionSize-1)
  						if compVector, err := rawdb.ReadBloomBits(backend.db, task.Bit, section, head); err == nil {
  							if blob, err := bitutil.DecompressBytes(compVector, int(sectionSize/8)); err == nil {
  								task.Bitsets[i] = blob
  							} else {
  								task.Error = err
  							}
  						} else {
  							task.Error = err
  						}
  					}
  					request <- task
  				}
  			}
  		}(bloomBlockBits)
  	}
  }
  for i := 0; i < bloomFilterThreads; i++ {
		go session.Multiplex(bloomBatch, bloomWait, backend.bloomRequests)
	}
}

	// Read replicas won't have the p2p functionality, so these will be noops

	// GetPoolTransactions returns all pending tranactions in the pool
func (backend *ReplicaBackend) GetPoolTransactions() (types.Transactions, error) {
  pending, err := backend.txPool.Pending()
  if err != nil {
    return nil, err
  }
  var txs types.Transactions
  for _, batch := range pending {
    txs = append(txs, batch...)
  }
  return txs, nil
}

	// GetPoolTransaction returns the specified transaction if it's in the transaction pool
func (backend *ReplicaBackend) GetPoolTransaction(txHash common.Hash) *types.Transaction {
  return backend.txPool.Get(txHash)
}

	// Generate core.state.managed_state object from current state, and get nonce from that
	// It won't account for have pending transactions
func (backend *ReplicaBackend) GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return 0, err } }
  state, _, err := backend.StateAndHeaderByNumber(ctx, rpc.LatestBlockNumber)
  if err != nil {return 0, err}
  nonce := state.GetNonce(addr)
  return nonce, state.Error()
}

	// 0,0
func (backend *ReplicaBackend) Stats() (pending int, queued int) {
  return 0, 0
}

func (backend *ReplicaBackend) RPCGasCap() *big.Int {
  // TODO: Make configurable
  header, err := backend.HeaderByNumber(context.Background(), rpc.LatestBlockNumber)
  if err != nil {
    return big.NewInt(int64(math.MaxUint64 / 2))
  }
  return big.NewInt(int64(header.GasLimit * 1000))
}

	// Return empty maps
func (backend *ReplicaBackend) TxPoolContent() (map[common.Address]types.Transactions, map[common.Address]types.Transactions) {
  return backend.txPool.Content()
}

	// Not sure how to stub out subscriptions
func (backend *ReplicaBackend) SubscribeNewTxsEvent(ch chan<- core.NewTxsEvent) event.Subscription {
  return backend.txPool.SubscribeNewTxsEvent(ch)
}

func (backend *ReplicaBackend) ChainConfig() *params.ChainConfig {
  return backend.chainConfig
}

	// CurrentBlock needs to find the latest block number / hash from the DB, then
  // look that up using GetBlock() {

func (backend *ReplicaBackend) CurrentBlock() *types.Block {
  latestHash := rawdb.ReadHeadBlockHash(backend.db)
  return backend.bc.GetBlockByHash(latestHash)
}
func (backend *ReplicaBackend) ExtRPCEnabled() bool {
  // TODO: Pass in whether or not we're actually serving RPC
  return true
}
func (backend *ReplicaBackend) GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, common.Hash{}, 0, 0, err } }
	tx, blockHash, blockNumber, index := rawdb.ReadTransaction(backend.db, txHash)
	return tx, blockHash, blockNumber, index, nil
}

// warmAddresses makes sure that the state tries for the specified list of
// addresses has been walked. This ensures that the storage for these addresses
// are not stored on a cold snapshot, and depending on available trie cache it
// may put the storage into RAM
func (backend *ReplicaBackend) warmAddresses(addresses []common.Address) error {
  stateDB, _, err := backend.StateAndHeaderByNumber(context.Background(), rpc.LatestBlockNumber)
  if err != nil {
    return err
  }
  iterators := make(chan trie.NodeIterator)
  go func() {
    tries := []state.Trie{}
    for _, address := range addresses {
      tr := stateDB.StorageTrie(address)
      if tr == nil {
        continue
      }
      tries = append(tries, tr)
    }
    for i := 0; i < 256; i++ {
      for _, tr := range tries {
        iterators <- tr.NodeIterator([]byte{byte(i)})
      }
      log.Info("Dispatched iterator batch", "count", i, "limit", 256)
    }
    close(iterators)
  }()
  for i := 0; i < runtime.NumCPU(); i++ {
    go func () {
      for it := range iterators {
        for j := 0; j < 10; j++ {
          for it.Next(true) {
            if it.Leaf() {
              break
            }
          }
        }
      }
    }()
  }
  // addressChan := make(chan common.Address)
  // var wg sync.WaitGroup
  // for i := 0; i < runtime.NumCPU(); i++ {
  //   wg.Add(1)
  //   go func() {
  //     for address := range addressChan {
  //       stateDB, _, err := backend.StateAndHeaderByNumber(context.Background(), rpc.LatestBlockNumber)
  //       if err != nil {
  //         log.Warn("Error getting state. skipping", "err", err, "address", address)
  //         continue
  //       }
  //       trie := stateDB.StorageTrie(address)
  //       iterator := trie.NodeIterator(nil)
  //       for iterator.Next(true) {}
  //     }
  //     wg.Done()
  //   }()
  // }
  // for i, address := range addresses {
  //   addressChan <- address
  //   log.Info("Dispatched address for warming", "count", i)
  // }
  // close(addressChan)
  // wg.Wait()
  return nil
}

func (backend *ReplicaBackend) handleBlockUpdates() {
  var lastBlock *types.Block
  for head := range backend.blockHeads {
    headHash := common.BytesToHash(head)
    headBlock := backend.bc.GetBlockByHash(headHash)
    _, revertedBlocks, newBlocks, err := backend.findCommonAncestor(headBlock, lastBlock)
    if err != nil {
      log.Warn("Error finding common ancestor", "head", headHash, "old", lastBlock.Hash(), "err", err.Error())
    }
    for _, block := range revertedBlocks {
      logs, err := backend.GetLogs(context.Background(), block.Hash())
      if err != nil {
        log.Warn("Error getting reverted logs", "block", block.Hash(), "err", err.Error())
      }
      allLogs := []*types.Log{}
      for _, deletedLogs := range logs {
        allLogs = append(allLogs, deletedLogs...)
      }
      if len(allLogs) > 0 {
        backend.removedLogsFeed.Send(core.RemovedLogsEvent{allLogs})
      }
      backend.chainSideFeed.Send(core.ChainSideEvent{Block: block})
    }
    for _, block := range newBlocks {
      logs, err := backend.GetLogs(context.Background(), block.Hash())
      if err != nil {
        log.Warn("Error getting logs", "block", block.Hash(), "err", err.Error())
      }
      allLogs := []*types.Log{}
      for _, newLogs := range logs {
        allLogs = append(allLogs, newLogs...)
      }
      if len(allLogs) > 0 {
        backend.logsFeed.Send(allLogs)
      }
      backend.chainFeed.Send(core.ChainEvent{block, block.Hash(), allLogs})
      backend.chainHeadFeed.Send(core.ChainHeadEvent{block})
    }
  }
}

func (backend *ReplicaBackend) consumeTransactions(transactionConsumer TransactionConsumer) error {
  pool, err := core.NewReplicaTxPool(core.DefaultTxPoolConfig, backend.chainConfig, backend.bc, backend)
  backend.txPool = pool
  if err != nil {
    return err
  }
  if transactionConsumer != nil {
    go func() {
      for tx := range transactionConsumer.Messages() {
        if err := backend.txPool.AddRemote(tx); err != nil && !strings.HasPrefix(err.Error(), "known transaction") {
          log.Debug("Error adding tx to pool", "tx", tx.Hash(), "error", err)
        }
      }
      }()
  }
  return nil
}

func (backend *ReplicaBackend) findCommonAncestor(newHead, oldHead *types.Block) (*types.Block, types.Blocks, types.Blocks, error) {
  reverted := types.Blocks{}
  newBlocks := types.Blocks{newHead}
  if oldHead == nil {
    return nil, reverted, newBlocks, nil
  }
  for {
    for newHead.NumberU64() > oldHead.NumberU64() + 1 {
      parentHash := newHead.ParentHash()
      newHead = backend.bc.GetBlockByHash(parentHash)
      if newHead == nil {
        return newHead, reverted, newBlocks, fmt.Errorf("Block %#x missing from database", parentHash)
      }
      newBlocks = append(types.Blocks{newHead}, newBlocks...)
    }
    if(oldHead.Hash() == newHead.ParentHash())  {
      return oldHead, reverted, newBlocks, nil
    }
    reverted = append(types.Blocks{oldHead}, reverted...)
    oldHead = backend.bc.GetBlockByHash(oldHead.ParentHash())
    if oldHead.Hash() == backend.bc.Genesis().Hash() {
      return oldHead, reverted, newBlocks, fmt.Errorf("Reached genesis without finding common ancestor")
    }
  }
}

func (backend *ReplicaBackend) HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Header, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  if num, ok := blockNrOrHash.Number(); ok {
    return backend.HeaderByNumber(ctx, num)
  }
  if hash, ok := blockNrOrHash.Hash(); ok {
    return backend.HeaderByHash(ctx, hash)
  }
  return nil, fmt.Errorf("Invalid block number or hash")
}
func (backend *ReplicaBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, err } }
  if num, ok := blockNrOrHash.Number(); ok {
    return backend.BlockByNumber(ctx, num)
  }
  if hash, ok := blockNrOrHash.Hash(); ok {
    return backend.BlockByHash(ctx, hash)
  }
  return nil, fmt.Errorf("Invalid block number or hash")
}
func (backend *ReplicaBackend) StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
  if ctx != nil { if err := ctx.Err(); err != nil { return nil, nil, err } }
  if num, ok := blockNrOrHash.Number(); ok {
    return backend.StateAndHeaderByNumber(ctx, num)
  }
  if hash, ok := blockNrOrHash.Hash(); ok {
    header, err := backend.HeaderByHash(ctx, hash)
    if err != nil {
      return nil, nil, err
    }
    return backend.StateAndHeaderByNumber(ctx, rpc.BlockNumber(header.Number.Int64()))
  }
  return nil, nil, fmt.Errorf("Invalid block number or hash")
}

func NewTestReplicaBackend(db ethdb.Database, hc *core.HeaderChain, bc *core.BlockChain, tp TransactionProducer) (*ReplicaBackend) {
  backend := &ReplicaBackend{
    db: db,
    indexDb: rawdb.NewTable(db, string(rawdb.BloomBitsIndexPrefix)),
    hc: hc,
    chainConfig: params.AllEthashProtocolChanges,
    bc: bc,
    transactionProducer: tp,
    eventMux: new(event.TypeMux),
    shutdownChan: make(chan bool),
  }
  backend.txPool, _ = core.NewReplicaTxPool(core.DefaultTxPoolConfig, backend.chainConfig, backend.bc, backend)
  return backend
}
