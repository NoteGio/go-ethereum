package core

import (
	"context"
	// "errors"
	// "fmt"
	// "math"
	"math/big"
	// "sort"
	// "sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	// "github.com/ethereum/go-ethereum/common/prque"
	// "github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	// "github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/params"
)

type Backend interface {
	HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
	SubscribeChainHeadEvent(ch chan<- ChainHeadEvent) event.Subscription
}

// NewTxPool creates a new transaction pool to gather, sort and filter inbound
// transactions from the network.
func NewReplicaTxPool(config TxPoolConfig, chainconfig *params.ChainConfig, chain blockChain, backend Backend) (*TxPool, error) {
	// Sanitize the input to ensure no vulnerable gas prices are set
	config = (&config).sanitize()

	// Create the transaction pool with its initial settings
	pool := &TxPool{
		config:          config,
		chainconfig:     chainconfig,
		chain:           chain,
		signer:          types.NewEIP155Signer(chainconfig.ChainID),
		pending:         make(map[common.Address]*txList),
		queue:           make(map[common.Address]*txList),
		beats:           make(map[common.Address]time.Time),
		all:             newTxLookup(),
		chainHeadCh:     make(chan ChainHeadEvent, chainHeadChanSize),
		reqResetCh:      make(chan *txpoolResetRequest),
		reqPromoteCh:    make(chan *accountSet),
		queueTxEventCh:  make(chan *types.Transaction),
		reorgDoneCh:     make(chan chan struct{}),
		reorgShutdownCh: make(chan struct{}),
		gasPrice:        new(big.Int).SetUint64(config.PriceLimit),
	}
	pool.locals = newAccountSet(pool.signer)
	for _, addr := range config.Locals {
		log.Info("Setting new local account", "address", addr)
		pool.locals.add(addr)
	}
	pool.priced = newTxPricedList(pool.all)
	header, err := backend.HeaderByNumber(context.Background(), rpc.LatestBlockNumber)
	if err != nil {
		return nil, err
	}
	pool.reset(nil, header)

	// Start the reorg loop early so it can handle requests generated during journal loading.
	pool.wg.Add(1)
	go pool.scheduleReorgLoop()

	// If local transactions and journaling is enabled, load from disk
	if !config.NoLocals && config.Journal != "" {
		pool.journal = newTxJournal(config.Journal)

		if err := pool.journal.load(pool.AddLocals); err != nil {
			log.Warn("Failed to load transaction journal", "err", err)
		}
		if err := pool.journal.rotate(pool.local()); err != nil {
			log.Warn("Failed to rotate transaction journal", "err", err)
		}
	}

	// Subscribe events from blockchain and start the main event loop.
	pool.chainHeadSub = backend.SubscribeChainHeadEvent(pool.chainHeadCh)
	pool.wg.Add(1)
	go pool.loop()

	return pool, nil
}
