package replica_test

import (
  "fmt"
  "github.com/ethereum/go-ethereum/common/hexutil"
  "github.com/ethereum/go-ethereum/replica"
  "testing"
)


func TestNetConstants(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  netAPI := replica.NewReplicaNetAPI(backend)
  if netAPI.Listening() {
    t.Error("netAPI unexpectedly listening")
  }
  if netAPI.PeerCount() != hexutil.Uint(0) {
    t.Error("netAPI unexpectedly has peers")
  }
}
func TestNetVersion(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  netAPI := replica.NewReplicaNetAPI(backend)
  if netAPI.Version() == fmt.Sprintf("%s", backend.ProtocolVersion()) {
    t.Error("Unexpected protocol version %v", netAPI.Version())
  }
}
