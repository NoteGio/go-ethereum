package replica

import (
  "github.com/ethereum/go-ethereum/core"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/event"
)

type EventProducer interface {
  Emit(chainEvent core.ChainEvent) error
  RelayEvents(bc ChainEventSubscriber)
  Close()
}

type EventConsumer interface {
  SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription
  SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription
  SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription
  SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription
  SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription
  SubscribeOffsets(ch chan<- int64) event.Subscription
  Start()
}

type TransactionProducer interface {
  Emit(marshall(*types.Transaction)) error
  RelayTransactions(*core.TxPool)
  Close()
}

type TransactionConsumer interface {
  Messages() <-chan *types.Transaction
  Close()
}
