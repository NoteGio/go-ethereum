package replica

import (
  "github.com/Shopify/sarama"
  // "log"
  "fmt"
  "github.com/ethereum/go-ethereum/core"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/log"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  // "encoding/hex"
)

type KafkaTransactionProducer struct {
  producer sarama.SyncProducer
  // TODO;  sarama.SyncProducer
  topic string
}

func (producer *KafkaTransactionProducer) Close() {
  producer.producer.Close()
}

func (producer *KafkaTransactionProducer) Emit(tx *types.Transaction) error {
  txBytes, err := rlp.EncodeToBytes(tx)
  if err != nil {
    return err
  }
    msg :=  &sarama.ProducerMessage{Topic: producer.topic, Value: sarama.ByteEncoder(txBytes)}
    _, _, err = producer.producer.SendMessage(msg)
    if err != nil {
      return err
    }
  return nil
}

func (producer *KafkaTransactionProducer) String() string {
  return fmt.Sprintf("KafkaTransactionProducer Topic DEBUG: %v", producer.topic)
}

func (producer *KafkaTransactionProducer) RelayTransactions(txpool *core.TxPool) {
  // SubscribeNewTxsEvent(ch chan<- NewTxsEvent) event.Subscription
  txCh := make(chan core.NewTxsEvent, 100)
  subscription := txpool.SubscribeNewTxsEvent(txCh)
  go func() {
    for txEvents := range txCh {
      for _, tx := range txEvents.Txs {
        producer.Emit(tx)
      }
    }
    log.Warn("Transaction emitter shutting down")
    subscription.Unsubscribe()
  }()
}

func NewKafkaTransactionProducerFromURLs(brokerURL, topic string) (TransactionProducer, error) {
  configEntries := make(map[string]*string)
  configEntries["retention.ms"] = strPtr("3600000")
  brokers, config := cdc.ParseKafkaURL(brokerURL)
  if err := cdc.CreateTopicIfDoesNotExist(brokerURL, topic, 6, configEntries); err != nil {
    return nil, err
  }
  config.Producer.Return.Successes=true
  producer, err := sarama.NewSyncProducer(brokers, config)
  if err != nil {
    return nil, err
  }
  return NewKafkaTransactionProducer(producer, topic), nil
}

func NewKafkaTransactionProducer(producer sarama.SyncProducer, topic string) (TransactionProducer) {
  return &KafkaTransactionProducer{producer, topic}
}


type KafkaTransactionConsumer struct {
  txs chan *types.Transaction
  consumer sarama.Consumer
  topic string
}

func (consumer *KafkaTransactionConsumer) Messages() <-chan *types.Transaction {
  if consumer.txs == nil {
    partitions, err := consumer.consumer.Partitions(consumer.topic)
    if err != nil {
      log.Error("Failed to list partitions - Cannot consume transactions", "topic", consumer.topic, "error", err)
      return nil
    }
    consumer.txs = make(chan *types.Transaction, 100)
    for _, partition := range partitions {
      partitionConsumer, err := consumer.consumer.ConsumePartition(consumer.topic, partition, sarama.OffsetNewest)
      if err != nil {
        log.Error("Failed to consume partition", "topic", consumer.topic, "partition", partition, "error", err)
        consumer.txs = nil
        return nil
      }
      go func() {
        for msg := range partitionConsumer.Messages() {
          transaction := &types.Transaction{}
          if err := rlp.DecodeBytes(msg.Value, transaction); err != nil {
            fmt.Printf("Error decoding: %v\n", err.Error())
          }
          consumer.txs <- transaction
        }
      }()
    }
  }
  return consumer.txs
}

func (consumer *KafkaTransactionConsumer) Close() {
  consumer.consumer.Close()
}

func strPtr(x string) *string { return &x }

func NewKafkaTransactionConsumerFromURLs(brokerURL, topic string) (TransactionConsumer, error) {
  configEntries := make(map[string]*string)
  configEntries["retention.ms"] = strPtr("3600000")
  brokers, config := cdc.ParseKafkaURL(brokerURL)
  if err := cdc.CreateTopicIfDoesNotExist(brokerURL, topic, 6, configEntries); err != nil {
    return nil, err
  }
  client, err := sarama.NewClient(brokers, config)
  if err != nil {
    return nil, err
  }
  consumer, err := sarama.NewConsumerFromClient(client)
  if err != nil {
    return nil, err
  }
  return &KafkaTransactionConsumer{consumer: consumer, topic: topic}, nil
}
