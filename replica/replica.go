package replica

import (
  "errors"
  "encoding/binary"
  "encoding/json"
  "github.com/ethereum/go-ethereum/p2p"
  "github.com/ethereum/go-ethereum/rpc"
  // "github.com/ethereum/go-ethereum/node"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/eth"
  "github.com/ethereum/go-ethereum/eth/filters"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/ethereum/go-ethereum/event"
  "github.com/ethereum/go-ethereum/graphql"
  "github.com/ethereum/go-ethereum/node"
  "github.com/ethereum/go-ethereum/core"
  "github.com/ethereum/go-ethereum/core/vm"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/internal/ethapi"
  "github.com/ethereum/go-ethereum/params"
  "github.com/ethereum/go-ethereum/log"
  "github.com/Shopify/sarama"
  "time"
  "fmt"
  "strings"
  "strconv"
  "os"
  "io/ioutil"
)

type Replica struct {
  db ethdb.Database
  hc *core.HeaderChain
  chainConfig *params.ChainConfig
  bc *core.BlockChain
  transactionProducer TransactionProducer
  transactionConsumer TransactionConsumer
  shutdownChan chan bool
  topic string
  maxOffsetAge int64
  maxBlockAge  int64
  headChan chan []byte
  backend *ReplicaBackend
  graphql *graphql.Service
  evmConcurrency int
  warmAddressFile string
  quit chan struct{}
}

func (r *Replica) Protocols() []p2p.Protocol {
  return []p2p.Protocol{}
}

func (r *Replica) GetBackend() *ReplicaBackend {
  if r.backend == nil {
    var evmSemaphore chan struct{}
    if r.evmConcurrency > 0 {
      evmSemaphore = make(chan struct{}, r.evmConcurrency)
    }
    r.backend = &ReplicaBackend{
      db: r.db,
      indexDb: rawdb.NewTable(r.db, string(rawdb.BloomBitsIndexPrefix)),
      hc: r.hc,
      chainConfig: r.chainConfig,
      bc: r.bc,
      transactionProducer: r.transactionProducer,
      eventMux: new(event.TypeMux),
      shutdownChan: r.shutdownChan,
      blockHeads: r.headChan,
      evmSemaphore: evmSemaphore,
    }
    if err := r.backend.consumeTransactions(r.transactionConsumer); err != nil {
      log.Warn("Error consuming transactions")
    }

    go r.backend.handleBlockUpdates()
    if r.warmAddressFile != "" {
      jsonFile, err := os.Open(r.warmAddressFile)
      if err != nil {
        log.Warn("Error warming addresses: %v", "error", err)
      } else {
        addressJSONBytes, _ := ioutil.ReadAll(jsonFile)
        addresses := []common.Address{}
        if err := json.Unmarshal(addressJSONBytes, &addresses); err != nil {
          log.Warn("Error warming addresses: %v", "error", err)
        }
        log.Info("Warming addresses", "count", len(addresses))
        if err := r.backend.warmAddresses(addresses); err != nil {
          log.Warn("Error warming addresses: %v", "error", err)
        }
      }
    } else {
      log.Info("No address file. Not warming")
    }
  }
  return r.backend
}

func (r *Replica) APIs() []rpc.API {
  apiBackend := r.GetBackend()
  nonceLock := new(ethapi.AddrLocker)
	return []rpc.API{
		{
			Namespace: "eth",
			Version:   "1.0",
			Service:   ethapi.NewPublicEthereumAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   ethapi.NewPublicBlockChainAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "eth",
			Version:   "1.0",
			Service:   ethapi.NewPublicTransactionPoolAPI(apiBackend, nonceLock),
			Public:    true,
		}, {
			Namespace: "ethercattle",
			Version:   "1.0",
			Service:   ethapi.NewEtherCattleBlockChainAPI(apiBackend),
			Public:    true,
		}, {
			Namespace: "debug",
			Version:   "1.0",
			Service:   ethapi.NewPublicDebugAPI(apiBackend),
			Public:    true,
		}, {
      Namespace: "eth",
      Version:   "1.0",
      Service:   filters.NewPublicFilterAPI(r.GetBackend(), false),
      Public:    true,
    }, {
      Namespace: "net",
      Version:   "1.0",
      Service:   NewReplicaNetAPI(r.GetBackend()),
      Public:    true,
    }, {
      Namespace: "eth",
      Version:   "1.0",
      Service:   NewPublicEthereumAPI(r.GetBackend()),
      Public:    true,
    },
	}
}
func (r *Replica) Start(server *p2p.Server) error {
  go func() {
    for _ = range time.NewTicker(time.Second * 30).C { // TODO: Make interval configurable?
      latestHash := rawdb.ReadHeadBlockHash(r.db)
      currentBlock := r.bc.GetBlockByHash(latestHash)
      offsetBytes, err := r.db.Get([]byte(fmt.Sprintf("cdc-log-%v-offset", r.topic)))
      if err != nil {
        log.Error(err.Error())
        continue
      }
      var bytesRead int
      var offset, offsetTimestamp int64
      if err != nil || len(offsetBytes) == 0 {
        offset = 0
      } else {
        offset, bytesRead = binary.Varint(offsetBytes[:binary.MaxVarintLen64])
        if bytesRead <= 0 {
          log.Error("Offset buffer too small")
        }
        offsetTimestamp, bytesRead = binary.Varint(offsetBytes[binary.MaxVarintLen64:])
        if bytesRead <= 0 {
          log.Error("Offset buffer too small")
        }
      }
      now := time.Now().Unix()
      if r.maxBlockAge > 0 && now - int64(currentBlock.Time()) > r.maxBlockAge {
        log.Error("Max block age exceeded.", "maxAgeSec", r.maxBlockAge, "realAge", common.PrettyAge(time.Unix(int64(currentBlock.Time()), 0)))
        r.Stop()
        os.Exit(1)
      }
      if r.maxOffsetAge > 0 && now - offsetTimestamp > r.maxOffsetAge {
        log.Error("Max offset age exceeded.", "maxAgeSec", r.maxBlockAge, "realAge", common.PrettyAge(time.Unix(offsetTimestamp, 0)))
        r.Stop()
        os.Exit(1)
      }
      log.Info("Replica Sync", "num", currentBlock.Number(), "hash", currentBlock.Hash(), "blockAge", common.PrettyAge(time.Unix(int64(currentBlock.Time()), 0)), "offset", offset, "offsetAge", common.PrettyAge(time.Unix(offsetTimestamp, 0)))
    }
  }()
  if r.graphql != nil {
    return r.graphql.Start(server)
  }
  return nil
}
func (r *Replica) Stop() error {
  r.quit <- struct{}{}
  r.db.Close()
  if r.graphql != nil {
    r.graphql.Stop()
  }
  if r.transactionConsumer != nil {
    r.transactionConsumer.Close()
  }
  return nil
}

func NewReplica(db ethdb.Database, config *eth.Config, ctx *node.ServiceContext, transactionProducer TransactionProducer, consumer cdc.LogConsumer, transactionConsumer TransactionConsumer, syncShutdown bool, startupAge, maxOffsetAge, maxBlockAge int64, graphqlEnabled bool, graphqlEndpoint string, graphqlCors []string, graphqlVirtualHosts []string, timeout rpc.HTTPTimeouts, evmConcurrency int, warmAddressFile string) (*Replica, error) {
  var headChan chan []byte
  quit := make(chan struct{})
  go func() {
    for {
      select {
      case operation := <-consumer.Messages():
        head, err := operation.Apply(db)
        if err != nil {
          log.Warn("Error applying operation", "err", err.Error())
        }
        if head != nil && headChan != nil {
          headChan <- head
        }
      case <-quit:
        log.Warn("Operation consumer shutting down")
        return
      }
    }
  }()
  if ready := consumer.Ready(); ready != nil {
    <-ready
  }
  log.Info("Replica up to date with master")
  if syncShutdown {
    log.Info("Replica shutdown after sync flag was set, shutting down")
    quit <- struct{}{}
    db.Close()
    os.Exit(0)
  }
  chainConfig, _, _ := core.SetupGenesisBlockWithOverride(db, config.Genesis, config.OverrideIstanbul, config.OverrideMuirGlacier)
  engine := eth.CreateConsensusEngine(ctx, chainConfig, &config.Ethash, []string{}, true, db)
  hc, err := core.NewHeaderChain(db, chainConfig, engine, func() bool { return false })
  if err != nil {
    return nil, err
  }
  bc, err := core.NewBlockChain(db, &core.CacheConfig{}, chainConfig, engine, vm.Config{}, nil)
  if err != nil {
    return nil, err
  }
  if startupAge > 0 {
    log.Info("Waiting for current block time")
    for time.Now().Unix() - startupAge > int64(bc.GetBlockByHash(rawdb.ReadHeadBlockHash(db)).Time()) {
      time.Sleep(100 * time.Millisecond)
    }
    log.Info("Block time is current. Starting replica.")
  }
  headChan = make(chan []byte, 10)
  replica := &Replica{db, hc, chainConfig, bc, transactionProducer, transactionConsumer, make(chan bool), consumer.TopicName(), maxOffsetAge, maxBlockAge, headChan, nil, nil, evmConcurrency, warmAddressFile, quit}
  // endpoint string, cors, vhosts []string, timeouts rpc.HTTPTimeouts
  if graphqlEnabled {
    replica.graphql, err = graphql.New(replica.GetBackend(), graphqlEndpoint, graphqlCors, graphqlVirtualHosts, timeout)
  }
  return replica, err
}

func NewKafkaReplica(db ethdb.Database, config *eth.Config, ctx *node.ServiceContext, kafkaSourceBroker, kafkaTopic, transactionTopic, txPoolTopic string, syncShutdown bool, startupAge, offsetAge, blockAge int64, graphqlEnabled bool, graphqlEndpoint string, graphqlCors []string, graphqlVirtualHosts []string, timeout rpc.HTTPTimeouts, evmConcurrency int, warmAddressFile string) (*Replica, error) {
  topicParts := strings.Split(kafkaTopic, ":")
  kafkaTopic = topicParts[0]
  var offset int64
  if len(topicParts) > 1 {
    if topicParts[1] == "" {
      offset = sarama.OffsetOldest
    } else {
      offsetInt, err := strconv.Atoi(topicParts[1])
      if err != nil {
        return nil, fmt.Errorf("Error parsing '%v' as integer: %v", topicParts[1], err.Error())
      }
      offset = int64(offsetInt)
    }
  } else {
    offsetBytes, err := db.Get([]byte(fmt.Sprintf("cdc-log-%v-offset", kafkaTopic)))
    var bytesRead int
    if err != nil || len(offsetBytes) == 0 {
      offset = sarama.OffsetOldest
    } else {
      offset, bytesRead = binary.Varint(offsetBytes[:binary.MaxVarintLen64])
      if bytesRead <= 0 { return nil, errors.New("Offset buffer too small") }
    }
  }
  consumer, err := cdc.NewKafkaLogConsumerFromURL(
    kafkaSourceBroker,
    kafkaTopic,
    offset,
  )
  if err != nil { return nil, err }
  var transactionConsumer TransactionConsumer
  if txPoolTopic != "" {
    transactionConsumer, err = NewKafkaTransactionConsumerFromURLs(kafkaSourceBroker, txPoolTopic)
    if err != nil { return nil, err }
  }
  log.Info("Populating replica from topic", "topic", kafkaTopic, "offset", offset)
  var transactionProducer TransactionProducer
  if transactionTopic != "" {
    transactionProducer, err = NewKafkaTransactionProducerFromURLs(
      kafkaSourceBroker,
      transactionTopic,
    )
    if err != nil {
      return nil, err
    }
  } else {
    log.Warn("No transaction topic specified. Replica will not have mempool data.")
  }
  return NewReplica(db, config, ctx, transactionProducer, consumer, transactionConsumer, syncShutdown, startupAge, offsetAge, blockAge, graphqlEnabled, graphqlEndpoint, graphqlCors, graphqlVirtualHosts, timeout, evmConcurrency, warmAddressFile)
}
