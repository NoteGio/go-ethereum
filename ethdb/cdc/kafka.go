package cdc

import (
  "fmt"
  "math/rand"
  "github.com/Shopify/sarama"
  "github.com/ethereum/go-ethereum/log"
  "net/url"
  "strings"
  "strconv"
  "time"
)


func ParseKafkaURL(brokerURL string) ([]string, *sarama.Config) {
  parsedURL, _ := url.Parse("kafka://" + brokerURL)
  config := sarama.NewConfig()
  config.Version = sarama.V2_1_0_0

  if parsedURL.Query().Get("tls") == "1" {
    config.Net.TLS.Enable = true
  }
  if val := parsedURL.Query().Get("fetch.default"); val != "" {
    fetchDefault, err := strconv.Atoi(val)
    if err != nil {
      log.Warn("fetch.default set, but not number", "fetch.default", val)
    } else {
      config.Consumer.Fetch.Default = int32(fetchDefault)
    }
  }
  if val := parsedURL.Query().Get("max.waittime"); val != "" {
    maxWaittime, err := strconv.Atoi(val)
    if err != nil {
      log.Warn("max.waittime set, but not number", "max.waittime", val)
    } else {
      config.Consumer.MaxWaitTime = time.Duration(maxWaittime) * time.Millisecond
    }
  }
  switch parsedURL.Query().Get("compression.codec") {
  case "gzip":
    config.Producer.Compression = sarama.CompressionGZIP
  case "none":
    config.Producer.Compression = sarama.CompressionNone
  case "lz4":
    config.Producer.Compression = sarama.CompressionLZ4
  case "zstd":
    config.Producer.Compression = sarama.CompressionZSTD
  case "snappy":
    config.Producer.Compression = sarama.CompressionSnappy
  default:
    log.Warn("compression.codec not set or not recognized. Defaulting to snappy")
    config.Producer.Compression = sarama.CompressionSnappy
  }

  if val, err := strconv.Atoi(parsedURL.Query().Get("message.send.max.retries")); err == nil {
    config.Producer.Retry.Max = val
  } else {
    config.Producer.Retry.Max = 10000000
  }

  if val, err := strconv.Atoi(parsedURL.Query().Get("retry.backoff.ms")); err == nil {
    config.Producer.Retry.Backoff = time.Duration(val) * time.Millisecond
  }
  if val, err := strconv.Atoi(parsedURL.Query().Get("net.maxopenrequests")); err == nil {
    config.Net.MaxOpenRequests = val
  }
  if parsedURL.Query().Get("idempotent") == "1" {
    config.Producer.Idempotent = true
  }
  if parsedURL.Query().Get("avoid_leader") == "1" {
    config.Consumer.ReplicaSelector = func(topic string, partition int32, client sarama.Client) (*sarama.Broker, error) {
      leader, err := client.Leader(topic, partition)
      if err != nil { return nil, err }
      replicas, err := client.InSyncReplicas(topic, partition)
      if err != nil { return nil, err }
      nonleaders := []int32{}
      for _, id := range replicas {
        if id != leader.ID() { nonleaders = append(nonleaders, id) }
      }
      if len(nonleaders) == 0 { return leader, nil }
      n := rand.Int31n(int32(len(nonleaders)))
      return client.Broker(nonleaders[n])

    }
  }

  if parsedURL.User != nil {
    config.Net.SASL.Enable = true
    config.Net.SASL.User = parsedURL.User.Username()
    config.Net.SASL.Password, _ = parsedURL.User.Password()
  }
  return strings.Split(parsedURL.Host, ","), config
}

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

func CreateTopicIfDoesNotExist(brokerAddr, topic string, numPartitions int32, configEntries map[string]*string) error {
  if topic == "" {
    return fmt.Errorf("Unspecified topic")
  }
  if configEntries == nil {
    configEntries = make(map[string]*string)
  }
  brokerList, config := ParseKafkaURL(brokerAddr)
  client, err := sarama.NewClient(brokerList, config)
  if err != nil {
    return err
  }
  defer client.Close()
  broker, err := client.Controller()
  if err != nil {
    return err
  }
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

    maxBytes := "5000012"
    configEntries["message.max.bytes"] = &maxBytes
    replicationFactor := int16(len(client.Brokers()))
    if replicationFactor > 3 {
      // If we have more than 3 brokers, only replicate to 3
      replicationFactor = 3
    }
    topicDetails[topic] = &sarama.TopicDetail{
      ConfigEntries: configEntries,
      NumPartitions: numPartitions,
      ReplicationFactor: replicationFactor,
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
    log.Info("Topic created without errors")
  }
  return nil
}

func NewKafkaLogProducerFromURL(brokerURL, topic string) (LogProducer, error) {
  brokers, config := ParseKafkaURL(brokerURL)
  if err := CreateTopicIfDoesNotExist(brokerURL, topic, 1, nil); err != nil {
    return nil, err
  }
  config.Producer.MaxMessageBytes = 5000012
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
      if err := consumer.batchHandler.ProcessInput(input.Value, input.Topic, input.Offset, input.Timestamp); err != nil {
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
  var highOffset, lowOffset int64
  if client != nil {
    highOffset, _ = client.GetOffset(topic, 0, sarama.OffsetNewest)
    lowOffset, _ = client.GetOffset(topic, 0, sarama.OffsetOldest)
  }
  return &KafkaLogConsumer{partitionConsumer, topic, nil, make(chan struct{}), (highOffset > lowOffset)}, nil
}

func NewKafkaLogConsumerFromURL(brokerURL, topic string, offset int64) (LogConsumer, error) {
  brokers, config := ParseKafkaURL(brokerURL)
  if err := CreateTopicIfDoesNotExist(brokerURL, topic, 1, nil); err != nil {
    return nil, err
  }
  config.Version = sarama.V2_1_0_0
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
