package replica

import (
  "errors"
  "encoding/binary"
  "github.com/ethereum/go-ethereum/p2p"
  "github.com/ethereum/go-ethereum/rpc"
  // "github.com/ethereum/go-ethereum/node"
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
  "github.com/Shopify/sarama"
  "fmt"
  "time"
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
  fmt.Println("Replica.start()")
  return nil
}
func (r *Replica) Stop() error {
  fmt.Println("Replica.stop()")
  return nil
}

// TODO ADD THE CONFIGURATION HERE FOR TRIGGERING POSTBACK
func NewReplica(db ethdb.Database, config *eth.Config, ctx *node.ServiceContext, kafkaSourceBroker []string, kafkaTopic, transactionTopic string) (*Replica, error) {
  topicParts := strings.Split(kafkaTopic, ":")
  kafkaTopic = topicParts[0]
  var offset int64
  if len(topicParts) > 1 {
    offsetInt, err := strconv.Atoi(topicParts[1])
    if err != nil {
      return nil, fmt.Errorf("Error parsing '%v' as integer: %v", topicParts[1], err.Error())
    }
    offset = int64(offsetInt)
  } else {
    offsetBytes, err := db.Get([]byte(fmt.Sprintf("cdc-log-%v-offset", kafkaTopic)))
    var bytesRead int
    if err != nil || len(offsetBytes) == 0 {
      offset = sarama.OffsetOldest
    } else {
      offset, bytesRead = binary.Varint(offsetBytes)
      if bytesRead <= 0 { return nil, errors.New("Offset buffer too small") }
    }
  }
  consumer, err := cdc.NewKafkaLogConsumerFromURLs(
    kafkaSourceBroker,
    kafkaTopic,
    offset,
  )
  if err != nil { return nil, err }
  fmt.Printf("Pre: %v\n", time.Now())
  // TODO : CREATE THE PRODUCER HERE with if conditionals
  transactionProducer, err := NewKafkaTransactionProducerFromURLs(
    kafkaSourceBroker,
    transactionTopic,
  )
  if err != nil {
    return nil, err
  }
  go func() {
    for operation := range consumer.Messages() {
      operation.Apply(db)
    }
  }()
  <-consumer.Ready()
  fmt.Printf("Post: %v\n", time.Now())
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
  return &Replica{db, hc, chainConfig, bc, transactionProducer, make(chan bool)}, nil
}
