package replica

import (
  "github.com/ethereum/go-ethereum/core/types"
)

type TransactionProducer interface {
  Emit(marshall(*types.Transaction)) error
  Close()
}
