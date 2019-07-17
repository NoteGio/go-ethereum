  package replica_test

import (
  "github.com/ethereum/go-ethereum/consensus/ethash"
  "github.com/ethereum/go-ethereum/eth"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/ethereum/go-ethereum/replica"
  "testing"
)


func TestReplicaConstants(t *testing.T) {
  _, consumer := cdc.MockLogPair()
  transactionProducer := &MockTransactionProducer{}
  db := ethdb.NewMemDatabase()
  config := eth.DefaultConfig
  config.Ethash.PowMode = ethash.ModeFake
  replicaNode, err := replica.NewReplica(db, &config, nil, transactionProducer, consumer, false, 0, 0, 0)
  if err != nil {
    t.Errorf(err.Error())
  }
  if length := len(replicaNode.Protocols()); length != 0 {
    t.Errorf("Expected no protocol support, got %v", length)
  }
  if err := replicaNode.Start(nil); err != nil {
    t.Errorf(err.Error())
  }
  if err := replicaNode.Stop(); err != nil {
    t.Errorf(err.Error())
  }
}

func TestReplicaAPIs(t *testing.T) {
  _, consumer := cdc.MockLogPair()
  transactionProducer := &MockTransactionProducer{}
  db := ethdb.NewMemDatabase()
  config := eth.DefaultConfig
  config.Ethash.PowMode = ethash.ModeFake
  replicaNode, err := replica.NewReplica(db, &config, nil, transactionProducer, consumer, false, 0, 0, 0)
  if err != nil {
    t.Errorf(err.Error())
  }
  apis := replicaNode.APIs()
  if length := len(apis); length < 4 {
    t.Errorf("Fewer APIs than expected, got %v", apis)
  }
}
