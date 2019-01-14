package replica

import (
  "github.com/Shopify/sarama"
  // "log"
  "fmt"
  "github.com/ethereum/go-ethereum/core/types"
  "encoding/json"
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
  txBytes, err := json.Marshal(tx)
  if err != nil {
    return err
  }
  // select {
    msg :=  &sarama.ProducerMessage{Topic: producer.topic, Value: sarama.ByteEncoder(txBytes)}
    partition, offset, err := producer.producer.SendMessage(msg)
    if err != nil {
      return err
    }
    fmt.Printf("Message is stored in topic(%s)/partition(%d)/offset(%d)\n", producer.topic, partition, offset)

  // }
  return nil
}

func (producer *KafkaTransactionProducer) String() string {
  return fmt.Sprintf("KafkaTransactionProducer Topic DEBUG: %v", producer.topic)
}

func NewKafkaTransactionProducerFromURLs(brokers []string, topic string) (TransactionProducer, error) {
  config := sarama.NewConfig()
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
