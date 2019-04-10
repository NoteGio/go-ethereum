package replica

import (
  "errors"
  "encoding/binary"
  "github.com/ethereum/go-ethereum/p2p"
  "github.com/ethereum/go-ethereum/rpc"
  // "github.com/ethereum/go-ethereum/node"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/eth"
  "github.com/ethereum/go-ethereum/eth/filters"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/ethereum/go-ethereum/event"
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
)

type Replica struct {
  db ethdb.Database
  hc *core.HeaderChain
  chainConfig *params.ChainConfig
  bc *core.BlockChain
  transactionProducer TransactionProducer
  shutdownChan chan bool
  topic string
}

func (r *Replica) Protocols() []p2p.Protocol {
  return []p2p.Protocol{}
}
func (r *Replica) APIs() []rpc.API {
  backend := &ReplicaBackend{
    db: r.db,
    indexDb: ethdb.NewTable(r.db, string(rawdb.BloomBitsIndexPrefix)),
    hc: r.hc,
    chainConfig: r.chainConfig,
    bc: r.bc,
    transactionProducer: r.transactionProducer,
    eventMux: new(event.TypeMux),
    shutdownChan: r.shutdownChan,
  }
  return append(ethapi.GetAPIs(backend),
  rpc.API{
    Namespace: "eth",
    Version:   "1.0",
    Service:   filters.NewPublicFilterAPI(backend, false),
    Public:    true,
  },
  rpc.API{
    Namespace: "net",
    Version:   "1.0",
    Service:   NewReplicaNetAPI(backend),
    Public:    true,
  },
  rpc.API{
    Namespace: "eth",
    Version:   "1.0",
    Service:   NewPublicEthereumAPI(backend),
    Public:    true,
  },
  )
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
      log.Info("Replica Sync", "num", currentBlock.Number(), "hash", currentBlock.Hash(), "blockAge", common.PrettyAge(time.Unix(currentBlock.Time().Int64(), 0)), "offset", offset, "offsetAge", common.PrettyAge(time.Unix(offsetTimestamp, 0)))
    }
  }()
  return nil
}
func (r *Replica) Stop() error {
  return nil
}

func NewReplica(db ethdb.Database, config *eth.Config, ctx *node.ServiceContext, transactionProducer TransactionProducer, consumer cdc.LogConsumer) (*Replica, error) {
  go func() {
    for operation := range consumer.Messages() {
      operation.Apply(db)
    }
  }()
  <-consumer.Ready()
  log.Info("Replica up to date")
  chainConfig, _, _ := core.SetupGenesisBlock(db, config.Genesis)
  engine := eth.CreateConsensusEngine(ctx, chainConfig, &config.Ethash, []string{}, true, db)
  hc, err := core.NewHeaderChain(db, chainConfig, engine, func() bool { return false })
  if err != nil {
    return nil, err
  }
  bc, err := core.NewBlockChain(db, &core.CacheConfig{Disabled: true}, chainConfig, engine, vm.Config{}, nil)
  if err != nil {
    return nil, err
  }
  return &Replica{db, hc, chainConfig, bc, transactionProducer, make(chan bool), consumer.TopicName()}, nil
}

func NewKafkaReplica(db ethdb.Database, config *eth.Config, ctx *node.ServiceContext, kafkaSourceBroker []string, kafkaTopic, transactionTopic string) (*Replica, error) {
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
  consumer, err := cdc.NewKafkaLogConsumerFromURLs(
    kafkaSourceBroker,
    kafkaTopic,
    offset,
  )
  if err != nil { return nil, err }
  log.Info("Populating replica from topic", "topic", kafkaTopic, "offset", offset)
  transactionProducer, err := NewKafkaTransactionProducerFromURLs(
    kafkaSourceBroker,
    transactionTopic,
  )
  if err != nil {
    return nil, err
  }
  return NewReplica(db, config, ctx, transactionProducer, consumer)
}
