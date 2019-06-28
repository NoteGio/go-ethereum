package cdc

import (
  "github.com/Shopify/sarama"
  "github.com/ethereum/go-ethereum/log"
  "time"
)

type KafkaLogProducer struct {
  producer sarama.AsyncProducer
  topic string
  closed bool
}

func (producer *KafkaLogProducer) Close() {
  producer.closed = true
  producer.producer.Close()
}
func (producer *KafkaLogProducer) Start(duration time.Duration) {
  go func() {
    futureTimer := time.NewTicker(duration)
    heartbeatBytes := HeartbeatOperation().Bytes()
    for range futureTimer.C {
      if producer.closed {
        break
      }
      producer.Emit(heartbeatBytes)
    }
  }()
}

func (producer *KafkaLogProducer) Emit(data []byte) error {
  log.Debug("Emitting data", "topic", producer.topic, "bytes", len(data))
  select {
  case producer.producer.Input() <- &sarama.ProducerMessage{Topic: producer.topic, Value: sarama.ByteEncoder(data)}:
  case err := <-producer.producer.Errors():
    // TODO: If we get an error here, that indicates a problem with an earlier
    // write.
    log.Error("Error emitting: %v", "err", err.Error())
    return err
  }
  return nil
}

func CreateTopicIfDoesNotExist(brokerAddr, topic string) error {
  config := sarama.NewConfig()
	config.Version = sarama.V2_1_0_0
  client, err := sarama.NewClient([]string{brokerAddr}, config)
  if err != nil {
    return err
  }
  defer client.Close()
  brokers := client.Brokers()
  broker := brokers[0]
  log.Info("Getting metadata")
  broker.Open(config)
  defer broker.Close()
  response, err := broker.GetMetadata(&sarama.MetadataRequest{Topics: []string{topic}})
  // log.Info("Got here", "err", err, "topics", *response.Topics[0])
  if err != nil {
    log.Error("Error getting metadata", "err", err)
    return err
  }
  if len(response.Topics) == 0 || len(response.Topics[0].Partitions) == 0 {
    log.Info("Attempting to create topic")
    topicDetails := make(map[string]*sarama.TopicDetail)
    configEntries := make(map[string]*string)
    compressionType := "snappy"
    configEntries["compression.type"] = &compressionType
    topicDetails[topic] = &sarama.TopicDetail{
      ConfigEntries: configEntries,
      NumPartitions: 1,
      ReplicationFactor: int16(len(brokers)),
    }
    r, err := broker.CreateTopics(&sarama.CreateTopicsRequest{
      // Version: 2,
      Timeout: 5 * time.Second,
      TopicDetails: topicDetails,
    })
    if err != nil {
      log.Error("Error creating topic", "error", err, "response", r)
      return err
    }
    if err, _ := r.TopicErrors[topic]; err != nil && err.Err != sarama.ErrNoError {
      log.Error("topic error", "err", err)
      return err
    }
  }
  return nil
}

func NewKafkaLogProducerFromURLs(brokers []string, topic string) (LogProducer, error) {
  config := sarama.NewConfig()
  if err := CreateTopicIfDoesNotExist(brokers[0], topic); err != nil {
    return nil, err
  }
  producer, err := sarama.NewAsyncProducer(brokers, config)
  if err != nil {
    return nil, err
  }
  return NewKafkaLogProducer(producer, topic), nil
}

func NewKafkaLogProducer(producer sarama.AsyncProducer, topic string) (LogProducer) {
  logProducer := &KafkaLogProducer{producer, topic, false}
  // TODO: Make duration configurable?
  logProducer.Start(30 * time.Second)
  return logProducer
}

type KafkaLogConsumer struct {
  consumer sarama.PartitionConsumer
  topic string
  batchHandler *BatchHandler
  ready chan struct{}
  topicExists bool
}

func (consumer *KafkaLogConsumer) Messages() <-chan *Operation {
  if consumer.batchHandler != nil {
    return consumer.batchHandler.outputChannel
  }
  inputChannel := consumer.consumer.Messages()
  consumer.batchHandler = NewBatchHandler()
  if !consumer.topicExists {
    consumer.ready <- struct{}{}
    consumer.ready = nil
  }
  go func() {
    for input := range inputChannel {
      if consumer.ready != nil {
        if consumer.consumer.HighWaterMarkOffset() - input.Offset <= 1 {
          consumer.ready <- struct{}{}
          consumer.ready = nil
        }
      }
      if err := consumer.batchHandler.ProcessInput(input.Value, input.Topic, input.Offset); err != nil {
        log.Error(err.Error())
      }
    }
  }()
  return consumer.batchHandler.outputChannel
}

func (consumer *KafkaLogConsumer) Ready() <-chan struct{} {
  return consumer.ready
}

func (consumer *KafkaLogConsumer) Close() {
  consumer.consumer.Close()
}

func (consumer *KafkaLogConsumer) TopicName() string {
  return consumer.topic
}

func NewKafkaLogConsumer(consumer sarama.Consumer, topic string, offset int64, client sarama.Client) (LogConsumer, error) {
  partitionConsumer, err := consumer.ConsumePartition(topic, 0, offset)
  if err != nil {
    return nil, err
  }
  highOffset, _ := client.GetOffset(topic, 0, sarama.OffsetNewest)
  return &KafkaLogConsumer{partitionConsumer, topic, nil, make(chan struct{}), (highOffset > 0)}, nil
}

func NewKafkaLogConsumerFromURLs(brokers []string, topic string, offset int64) (LogConsumer, error) {
  config := sarama.NewConfig()
  if err := CreateTopicIfDoesNotExist(brokers[0], topic); err != nil {
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
  return NewKafkaLogConsumer(consumer, topic, offset, client)
}
