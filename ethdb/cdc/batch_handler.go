package cdc

import (
  "fmt"
  "github.com/ethereum/go-ethereum/rlp"
  "github.com/ethereum/go-ethereum/log"
)

type BatchHandler struct {
  outputChannel chan *Operation
  batches map[string][]BatchOperation
}

func (consumer *BatchHandler) ProcessInput(value []byte, topic string, offset int64) error {
  if value[0] == 255 {
    batchValue := make([]byte, len(value))
    copy(batchValue[:], value[:])
    bop, err := BatchOperationFromBytes(batchValue, topic, offset)
    if err != nil {
      return fmt.Errorf("Message(topic=%v, offset=%v) is not a valid operation: %v\n", topic, offset, err.Error())
    }
    batch, ok := consumer.batches[string(bop.Batch[:])]
    if !ok {
      batch = []BatchOperation{}
    }
    consumer.batches[string(bop.Batch[:])] = append(batch, bop)
  } else {
    op, err := OperationFromBytes(value, topic, offset)
    if op.Op == OpWrite {
      if batch, ok := consumer.batches[string(op.Data)]; ok {
        data, err := rlp.EncodeToBytes(batch)
        if err != nil {
          log.Error("Failed to encode batch operation: %v", err)
        }
        delete(consumer.batches, string(op.Data))
        op.Data = append(op.Data, data...)
      } else {
        return fmt.Errorf("Could not find matching batch: %#x, (%v known)", op.Data, len(consumer.batches))
      }
    }
    if err != nil {
      return fmt.Errorf("Message(topic=%v, offset=%v) is not a valid operation: %v\n", topic, offset, err.Error())
    }
    consumer.outputChannel <- op
  }
  return nil
}

func NewBatchHandler() (*BatchHandler) {
  return &BatchHandler{make(chan *Operation), make(map[string][]BatchOperation)}
}
