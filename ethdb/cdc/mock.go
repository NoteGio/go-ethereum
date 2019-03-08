package cdc

import (
  "log"
)

type MockLogProducer struct {
  channel chan []byte
}

func (producer *MockLogProducer) Emit(data []byte) error {
  producer.channel <- data
  return nil
}

func (producer *MockLogProducer) Close() {}

type MockLogConsumer struct {
  channel <-chan []byte
  handler *BatchHandler
}

func (consumer *MockLogConsumer) Messages() (<-chan *Operation) {
  if consumer.handler == nil {
    consumer.handler = NewBatchHandler()
    go func() {
      counter := int64(0)
      for value := range consumer.channel {
        if err := consumer.handler.ProcessInput(value, "mock", counter); err != nil {
          log.Printf(err.Error())
        }
        counter++
      }
    }()
  }
  return consumer.handler.outputChannel
}

func (consumer *MockLogConsumer) Ready() (<-chan struct{}) {
  channel := make(chan struct{})
  go func () { channel <- struct{}{} }()
  return channel
}

func (consumer *MockLogConsumer) Close() {}

func MockLogPair() (LogProducer, LogConsumer) {
  channel := make(chan []byte, 2)
  return &MockLogProducer{channel}, &MockLogConsumer{channel: channel}
}
