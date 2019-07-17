package cdc

import (
  "github.com/ethereum/go-ethereum/ethdb"
)

type Batch interface {
  ethdb.Batch
  BatchId() []byte
}

type LogProducer interface {
  Emit([]byte) error
  Close()
}

type LogConsumer interface {
  Messages() <-chan *Operation
  Ready() <-chan struct{}
  Close()
  TopicName() string
}
