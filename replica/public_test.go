package replica

import (
  "bytes"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/common/hexutil"
  "testing"
)


func TestPublicConstants(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  publicAPI := NewPublicEthereumAPI(backend)
  emptyAddress := common.Address{}
  addr, err := publicAPI.Etherbase()
  if err != nil {
    t.Errorf(err.Error())
  }
  if !bytes.Equal(addr[:], emptyAddress[:]) {
    t.Errorf("Unexpected etherbase: %#x", addr)
  }
  cbaddr, err := publicAPI.Coinbase()
  if err != nil {
    t.Errorf(err.Error())
  }
  if !bytes.Equal(cbaddr[:], emptyAddress[:]) {
    t.Errorf("Unexpected coinbase: %#x", cbaddr)
  }
  if publicAPI.Hashrate() != hexutil.Uint64(0) {
    t.Errorf("Unexpected non-zero hashrate")
  }
  if publicAPI.Mining() {
    t.Errorf("Unexpectedly mining")
  }
}
func TestPublicChainID(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  publicAPI := NewPublicEthereumAPI(backend)
  if publicAPI.ChainId() != hexutil.Uint64(backend.ProtocolVersion()) {
    t.Errorf("Unexpected chainid %v", publicAPI.ChainId())
  }
}
