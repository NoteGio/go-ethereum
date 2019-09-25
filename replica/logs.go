package replica

import (
  "bytes"
  "context"
  "database/sql"
  "fmt"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/core/types"
  "github.com/ethereum/go-ethereum/eth/filters"
  "github.com/ethereum/go-ethereum/log"
  _ "github.com/mattn/go-sqlite3"
  "sort"
  "strings"
)

type logApi struct {
  backend *ReplicaBackend
  db *sql.DB
}

func getTopicIndex(topics []common.Hash, idx int) []byte {
  if len(topics) > idx {
    return topics[idx].Bytes()
  }
  return []byte{}
}

type byBlockAndIndex []*types.Log

func (a byBlockAndIndex) Len() int           { return len(a) }
func (a byBlockAndIndex) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byBlockAndIndex) Less(i, j int) bool {
  if a[i].BlockNumber != a[j].BlockNumber {
    return a[i].BlockNumber < a[j].BlockNumber
  }
  return a[i].Index < a[j].Index
}

func trimPrefix(data []byte) ([]byte) {
  v := bytes.TrimLeft(data, string([]byte{0}))
  if len(v) == 0 {
    return []byte{0}
  }
  return v
}

func (api *logApi) GetLogs(ctx context.Context, crit filters.FilterCriteria) ([]*types.Log, error) {
  log.Info("Querying optimized GetLogs")
  latestBlock := api.backend.CurrentBlock()
  result := []*types.Log{}
  params := []interface{}{}
  whereClause := []string{}
  if crit.BlockHash != nil {
    whereClause = append(whereClause, "blockHash = ?")
    params = append(params, trimPrefix(crit.BlockHash.Bytes()))
  }
  whereClause = append(whereClause, "blockNumber >= ?")
  if crit.FromBlock == nil {
    params = append(params, latestBlock.Number().Int64())
  } else {
    params = append(params, crit.FromBlock.Int64())
  }
  whereClause = append(whereClause, "blockNumber <= ?")
  if crit.ToBlock == nil {
    params = append(params, latestBlock.Number().Int64())
  } else {
    params = append(params, crit.ToBlock.Int64())
  }
  addressClause := []string{}
  for _, address := range crit.Addresses {
    addressClause = append(addressClause, "address = ?")
    params = append(params, trimPrefix(address.Bytes()))
  }
  if len(addressClause) > 0 {
    whereClause = append(whereClause, fmt.Sprintf("(%v)", strings.Join(addressClause, " OR ")))
  }
  topicsClause := []string{}
  for i, topics := range crit.Topics {
    topicClause := []string{}
    for _, topic := range topics {
      topicClause = append(topicClause, fmt.Sprintf("topic%v = ?", i))
      params = append(params, trimPrefix(topic.Bytes()))
    }
    if len(topicClause) > 0 {
      topicsClause = append(topicsClause, fmt.Sprintf("(%v)", strings.Join(topicClause, " OR ")))
    } else {
      topicsClause = append(topicsClause, fmt.Sprintf("topic%v != zeroblob(0)"))
    }
  }
  if len(topicsClause) > 0 {
    whereClause = append(whereClause, fmt.Sprintf("(%v)", strings.Join(topicsClause, " AND ")))
  }
  query := fmt.Sprintf("SELECT blockHash, logIndex FROM event_logs WHERE %v;", strings.Join(whereClause, " AND "))
  rows, err := api.db.QueryContext(ctx, query, params...)
  if err != nil {
    return result, err
  }
  defer rows.Close()
  recordsByBlock := make(map[common.Hash][]int)
  for rows.Next() {
    blockHashBytes := []byte{}
    logIndex := 0
		err = rows.Scan(
      &blockHashBytes,
      &logIndex,
    )
		if err != nil {
			return result, err
		}
    blockHash := common.BytesToHash(blockHashBytes)
    records, ok := recordsByBlock[blockHash]
    if !ok {
      recordsByBlock[blockHash] = []int{logIndex}
    } else {
      recordsByBlock[blockHash] = append(records, logIndex)
    }
	}
  for blockHash, logRecords := range recordsByBlock {
    blockLogs, err := api.backend.GetLogs(ctx, blockHash)
    if err != nil {
      return result, err
    }
    flattenedLogs := []*types.Log{}
    for _, txLogs := range blockLogs {
      flattenedLogs = append(flattenedLogs, txLogs...)
    }
    for _, record := range logRecords {
      result = append(result, flattenedLogs[record])
    }
  }
  sort.Sort(byBlockAndIndex(result))
  return result, nil
}
