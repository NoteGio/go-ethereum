package replica

import (
	"fmt"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type ReplicaNetAPI struct {
	backend *ReplicaBackend
}

// NewReplicaNetAPI creates a new net API instance.
func NewReplicaNetAPI(backend *ReplicaBackend) *ReplicaNetAPI {
	return &ReplicaNetAPI{backend}
}

// Listening returns an indication if the node is listening for network connections.
func (s *ReplicaNetAPI) Listening() bool {
	return false // replicas are never listening
}

// PeerCount returns the number of connected peers
func (s *ReplicaNetAPI) PeerCount() hexutil.Uint {
	return hexutil.Uint(0)
}

// Version returns the current ethereum protocol version.
func (s *ReplicaNetAPI) Version() string {
	return fmt.Sprintf("%v", s.backend.ProtocolVersion())
}
