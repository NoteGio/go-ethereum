  package replica

import (
  "github.com/ethereum/go-ethereum/consensus/ethash"
  "github.com/ethereum/go-ethereum/eth/ethconfig"
  "github.com/ethereum/go-ethereum/core/rawdb"
  "github.com/ethereum/go-ethereum/ethdb/cdc"
  "github.com/ethereum/go-ethereum/rpc"
  "testing"
)


func TestReplicaConstants(t *testing.T) {
  _, consumer := cdc.MockLogPair()
  transactionProducer := &MockTransactionProducer{}
  db := rawdb.NewMemoryDatabase()
  config := ethconfig.Defaults
  config.Ethash.PowMode = ethash.ModeFake
  replicaNode, err := NewReplica(db, &config, nil, transactionProducer, consumer, nil, false, 0, 0, 0, rpc.HTTPTimeouts{}, 0, "", true, -1)
  if err != nil {
    t.Errorf(err.Error())
  }
  if length := len(replicaNode.Protocols()); length != 0 {
    t.Errorf("Expected no protocol support, got %v", length)
  }
  if err := replicaNode.Start(); err != nil {
    t.Errorf(err.Error())
  }
  if err := replicaNode.Stop(); err != nil {
    t.Errorf(err.Error())
  }
}

func TestReplicaAPIs(t *testing.T) {
  _, consumer := cdc.MockLogPair()
  transactionProducer := &MockTransactionProducer{}
  db := rawdb.NewMemoryDatabase()
  config := ethconfig.Defaults
  config.Ethash.PowMode = ethash.ModeFake
  replicaNode, err := NewReplica(db, &config, nil, transactionProducer, consumer, nil, false, 0, 0, 0, rpc.HTTPTimeouts{}, 0, "", true, -1)
  if err != nil {
    t.Errorf(err.Error())
  }
  apis := replicaNode.APIs()
  if length := len(apis); length < 4 {
    t.Errorf("Fewer APIs than expected, got %v", apis)
  }
}
