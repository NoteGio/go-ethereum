package cdc_test

import (
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/Shopify/sarama/mocks"
  "github.com/Shopify/sarama"
  "testing"
  "bytes"
)

func TestEmit(t *testing.T) {
  dataCollectorMock := mocks.NewAsyncProducer(t, nil)
  dataCollectorMock.ExpectInputAndSucceed()

  producer := cdc.NewKafkaLogProducer(dataCollectorMock, "test")
  defer producer.Close()
  op, err := cdc.HasOperation([]byte("Hello"))
  if err != nil {
    t.Fatalf(err.Error())
  }
  producer.Emit(op.Bytes())
}

func TestConsumer(t *testing.T) {
  consumer := mocks.NewConsumer(t, nil)
  consumerPartition := consumer.ExpectConsumePartition("test", 0, 0)
  logConsumer, err := cdc.NewKafkaLogConsumer(consumer, "test", 0, nil)
  if err != nil { t.Fatalf(err.Error() )}
  defer logConsumer.Close()
  if err != nil { t.Fatalf(err.Error() )}
  op, err := cdc.HasOperation([]byte("Hello"))
  if err != nil { t.Fatalf(err.Error() )}
  message := &sarama.ConsumerMessage{
    Topic: "test",
    Value: op.Bytes(),
  }
  consumerPartition.YieldMessage(message)
  go func() { <-logConsumer.Ready() }()
  receiveOp := <-logConsumer.Messages()
  if receiveOp.Op != cdc.OpHas {
    t.Errorf("Op has unexpected type %v", receiveOp.Op)
  }
  if bytes.Compare(receiveOp.Data, op.Data) != 0 {
    t.Errorf("Unexpected: %v != %v", receiveOp.Data, op.Data)
  }
}
