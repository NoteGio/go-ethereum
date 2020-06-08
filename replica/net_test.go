package replica

import (
  "fmt"
  "github.com/ethereum/go-ethereum/common/hexutil"
  "testing"
)

func TestNetConstants(t *testing.T) {
  backend, _, err := testReplicaBackend()
  if err != nil {
    t.Fatalf(err.Error())
  }
  netAPI := NewReplicaNetAPI(backend)
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
  netAPI := NewReplicaNetAPI(backend)
  if netAPI.Version() != fmt.Sprintf("%v", backend.ProtocolVersion()) {
    t.Errorf("Unexpected protocol version %v, wanted %v", netAPI.Version(), backend.ProtocolVersion())
  }
}
