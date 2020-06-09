package rawdb

import (
  "encoding/json"
  "fmt"
  "bytes"
  "runtime"
  "strings"
  "strconv"
  "sync"
  lru "github.com/hashicorp/golang-lru"
  "github.com/aws/aws-sdk-go/aws"
  "github.com/aws/aws-sdk-go/aws/session"
  s3 "github.com/aws/aws-sdk-go/service/s3"
	// "github.com/aws/aws-sdk-go/service/s3/s3manager"
  "github.com/golang/snappy"
  "github.com/ethereum/go-ethereum/common"
  "github.com/ethereum/go-ethereum/ethdb"
  "github.com/ethereum/go-ethereum/log"
  "github.com/ethereum/go-ethereum/params"
  "io/ioutil"
  "path"
  "time"
)

type freezeInterface interface{
  ethdb.AncientStore
  freeze(db ethdb.KeyValueStore)
}

type s3freezer struct {
  sess *session.Session
  cache *lru.Cache
  bucket string
  root string
  count uint64
  uploadCh chan s3record
  concurrency int
  wg sync.WaitGroup
}

type s3record struct {
  Hash []byte `json:"x"`
  Header []byte `json:"h"`
  Body []byte `json:"b"`
  Receipt []byte `json:"r"`
  Td []byte `json:"d"`
  number uint64
}

func numToPath(number uint64) string {
  h := fmt.Sprintf("%0.7x", number)
  x := len(h)
  return path.Join(h[:x-4], h[x-4:x-2], h[x-2:])
}

func NewS3Freezer(path string, cacheSize int) (ethdb.AncientStore, error) {
  path = strings.TrimPrefix(path, "s3://")
  parts := strings.SplitN(path, "/", 2)
  cache, err := lru.New(cacheSize)
  if err != nil { return nil, err }
  freezer := &s3freezer{
    cache: cache,
    sess: session.Must(session.NewSession()),
    bucket: parts[0],
    root: strings.TrimSuffix(parts[1], "/") + "/",
    concurrency: runtime.NumCPU(),
    uploadCh: make(chan s3record),
  }
  svc := s3.New(freezer.sess)
  output, err := svc.ListObjectsV2(&s3.ListObjectsV2Input{
    Bucket: &freezer.bucket,
    Delimiter: aws.String("/"),
    Prefix: &freezer.root,
  })
  log.Info("Getting ancient count:", "bucket", freezer.bucket, "prefix", freezer.root)
  if len(output.CommonPrefixes) == 0 {
    // Fresh freezer
    freezer.count = 0
    freezer.uploader()
    return freezer, nil
  }
  for output.NextContinuationToken != nil {
    if err != nil { return nil, err }
    output, err = svc.ListObjectsV2(&s3.ListObjectsV2Input{
      Bucket: &freezer.bucket,
      Delimiter: aws.String("/"),
      Prefix: &freezer.root,
      ContinuationToken: output.NextContinuationToken,
    })
  }
  l1prefix := output.CommonPrefixes[len(output.CommonPrefixes) - 1].Prefix
  output, err = svc.ListObjectsV2(&s3.ListObjectsV2Input{
    Bucket: &freezer.bucket,
    Delimiter: aws.String("/"),
    Prefix: l1prefix,
  })
  l2prefix := output.CommonPrefixes[len(output.CommonPrefixes)-1].Prefix
  output, err = svc.ListObjectsV2(&s3.ListObjectsV2Input{
    Bucket: &freezer.bucket,
    Delimiter: aws.String("/"),
    Prefix: l2prefix,
  })
  highest := output.Contents[len(output.Contents) - 1].Key
  count, err := strconv.ParseInt(strings.Join(strings.Split(strings.TrimPrefix(*highest, freezer.root), "/"), ""), 16, 64)
  if err != nil { return nil, err }
  freezer.count = uint64(count) + 1
  freezer.uploader()
  return freezer, nil
}

// HasAncient returns an indicator whether the specified data exists in the
// ancient store.
func (f *s3freezer) HasAncient(kind string, number uint64) (bool, error) {
  if number > f.count + uint64(f.concurrency) {
    // If the requested number is too high, we definitely won't have it
    return false, fmt.Errorf("Requested %v (%v) exceeds max (%v)", kind, number, f.count)
  }
  key := numToPath(number)
  svc := s3.New(f.sess)
  if _, ok := f.cache.Get(key); ok { return true, nil }
  _, err := svc.HeadObject(&s3.HeadObjectInput{
    Bucket: &f.bucket,
    Key: aws.String(path.Join(f.root, key)),
  })
  return err == nil, nil
}

// Ancient retrieves an ancient binary blob from the append-only immutable files.
func (f *s3freezer) Ancient(kind string, number uint64) ([]byte, error) {
  if number > f.count + uint64(f.concurrency) {
    // If the requested number is too high, we definitely won't have it.
    return []byte{}, fmt.Errorf("Requested %v (%v) exceeds max (%v)", kind, number, f.count)
  }
  key := numToPath(number)
  var content []byte
  svc := s3.New(f.sess)
  cacheContent, ok := f.cache.Get(key)
  if ok {
    content = cacheContent.([]byte)
  } else {
    value, err := svc.GetObject(&s3.GetObjectInput{
      Bucket: &f.bucket,
      Key: aws.String(path.Join(f.root, key)),
    })
    if err != nil {
      return nil, err
    }
    content, err = ioutil.ReadAll(snappy.NewReader(value.Body))
    if err != nil {
      return nil, err
    }
    f.cache.Add(key, content)
  }
  record := &s3record{}
  err := json.Unmarshal(content, &record)
  if err != nil {
    return []byte{}, fmt.Errorf("Error parsing '%v' - %v", string(content), err.Error())
  }
  switch kind {
  case freezerHeaderTable:
    return record.Header, err
  case freezerHashTable:
    return record.Hash, err
  case freezerBodiesTable:
    return record.Body, err
  case freezerReceiptTable:
    return record.Receipt, err
  case freezerDifficultyTable:
    return record.Td, err
  default:
    return nil, fmt.Errorf("Unknown kind '%v'", kind)
  }
}

// Ancients returns the ancient item numbers in the ancient store.
func (f *s3freezer) Ancients() (uint64, error) {
  return f.count, nil
}

// AncientSize returns the ancient size of the specified category.
func (f *s3freezer) AncientSize(kind string) (uint64, error) {
  return f.count, nil
}
// AppendAncient injects all binary blobs belong to block at the end of the
// append-only immutable table files.
func (f *s3freezer) AppendAncient(number uint64, hash, header, body, receipt, td []byte) error {
  log.Debug("Appending")
  f.uploadCh <- s3record{
    Hash: hash,
    Header: header,
    Body: body,
    Receipt: receipt,
    Td: td,
    number: number,
  }
  log.Debug("Appended")
  f.count++
  return nil
}

func (f *s3freezer) uploader() {
  for i := 0; i < f.concurrency; i++ {
    go func(i int) {
      log.Info("Starting uploader thread", "id", i)
      defer log.Info("Stopping uploader thread", "id", i)

      for record := range f.uploadCh {
        f.wg.Add(1)
        for i := 0; true; i++ {
          time.Sleep(100 * time.Duration(i) * time.Millisecond) // Backoff on failure
          log.Debug("Processing record")
          if exists, _ := f.HasAncient("bodies", record.number); !exists {
            data, err := json.Marshal(record)
            if err != nil {
              log.Error("Error marshalling record", "number", record.number)
            }
            buff := &bytes.Buffer{}
            writer := snappy.NewWriter(buff)
            writer.Write(data)
            writer.Flush()
            svc := s3.New(f.sess)
            key := numToPath(record.number)
            _, err = svc.PutObject(&s3.PutObjectInput{
              Bucket: &f.bucket,
              Key: aws.String(path.Join(f.root, key)),
              Body: bytes.NewReader(buff.Bytes()),
            })
            if err != nil {
              log.Error("Error recording block", "number", record.number, "bucket", f.bucket, "key", path.Join(f.root, key), "err", err)
              continue
            }
          }
          break
        }
        f.wg.Done()
      }
    }(i)
  }
}

// TruncateAncients discards all but the first n ancient data from the ancient store.
func (f *s3freezer) TruncateAncients(n uint64) error {
  if n < f.count {
    f.count = n
  }
  return nil
}

// Sync is a no-op, as we will write content as AppendAncient is called
func (f *s3freezer) Sync() error {
  f.wg.Wait()
  return nil
}

func (f *s3freezer) Close() error {
  close(f.uploadCh)
  return nil
}

func (f *s3freezer) freeze(db ethdb.KeyValueStore) {
	nfdb := &nofreezedb{KeyValueStore: db}

	for {
		// Retrieve the freezing threshold.
		hash := ReadHeadBlockHash(nfdb)
		if hash == (common.Hash{}) {
			log.Debug("Current full block hash unavailable") // new chain, empty database
			time.Sleep(freezerRecheckInterval)
			continue
		}
		number := ReadHeaderNumber(nfdb, hash)
		switch {
		case number == nil:
			log.Error("Current full block number unavailable", "hash", hash)
			time.Sleep(freezerRecheckInterval)
			continue

		case *number < params.ImmutabilityThreshold:
			log.Debug("Current full block not old enough", "number", *number, "hash", hash, "delay", params.ImmutabilityThreshold)
			time.Sleep(freezerRecheckInterval)
			continue

		case *number-params.ImmutabilityThreshold <= f.count:
			log.Debug("Ancient blocks frozen already", "number", *number, "hash", hash, "frozen", f.count)
			time.Sleep(freezerRecheckInterval)
			continue
		}
		head := ReadHeader(nfdb, hash, *number)
		if head == nil {
			log.Error("Current full block unavailable", "number", *number, "hash", hash)
			time.Sleep(freezerRecheckInterval)
			continue
		}
		// Seems we have data ready to be frozen, process in usable batches
		limit := *number - params.ImmutabilityThreshold
		if limit-f.count > freezerBatchLimit {
			limit = f.count + freezerBatchLimit
		}
		var (
			start    = time.Now()
			first    = f.count
			ancients = make([]common.Hash, 0, limit)
		)
		for f.count < limit {
			// Retrieves all the components of the canonical block
			hash := ReadCanonicalHash(nfdb, f.count)
			if hash == (common.Hash{}) {
				log.Error("Canonical hash missing, can't freeze", "number", f.count)
				break
			}
			header := ReadHeaderRLP(nfdb, hash, f.count)
			if len(header) == 0 {
				log.Error("Block header missing, can't freeze", "number", f.count, "hash", hash)
				break
			}
			body := ReadBodyRLP(nfdb, hash, f.count)
			if len(body) == 0 {
				log.Error("Block body missing, can't freeze", "number", f.count, "hash", hash)
				break
			}
			receipts := ReadReceiptsRLP(nfdb, hash, f.count)
			if len(receipts) == 0 {
				log.Error("Block receipts missing, can't freeze", "number", f.count, "hash", hash)
				break
			}
			td := ReadTdRLP(nfdb, hash, f.count)
			if len(td) == 0 {
				log.Error("Total difficulty missing, can't freeze", "number", f.count, "hash", hash)
				break
			}
			log.Trace("Deep froze ancient block", "number", f.count, "hash", hash)
			// Inject all the components into the relevant data tables
			if err := f.AppendAncient(f.count, hash[:], header, body, receipts, td); err != nil {
				break
			}
			ancients = append(ancients, hash)
		}
		// Batch of blocks have been frozen, flush them before wiping from leveldb
		if err := f.Sync(); err != nil {
			log.Crit("Failed to flush frozen tables", "err", err)
		}
		// Wipe out all data from the active database
		batch := db.NewBatch()
		for i := 0; i < len(ancients); i++ {
			// Always keep the genesis block in active database
			if first+uint64(i) != 0 {
				DeleteBlockWithoutNumber(batch, ancients[i], first+uint64(i))
				DeleteCanonicalHash(batch, first+uint64(i))
			}
		}
		if err := batch.Write(); err != nil {
			log.Crit("Failed to delete frozen canonical blocks", "err", err)
		}
		batch.Reset()
		// Wipe out side chain also.
		for number := first; number < f.count; number++ {
			// Always keep the genesis block in active database
			if number != 0 {
				for _, hash := range ReadAllHashes(db, number) {
					DeleteBlock(batch, hash, number)
				}
			}
		}
		if err := batch.Write(); err != nil {
			log.Crit("Failed to delete frozen side blocks", "err", err)
		}
		// Log something friendly for the user
		context := []interface{}{
			"blocks", f.count - first, "elapsed", common.PrettyDuration(time.Since(start)), "number", f.count - 1,
		}
		if n := len(ancients); n > 0 {
			context = append(context, []interface{}{"hash", ancients[n-1]}...)
		}
		log.Info("Deep froze chain segment", context...)

		// Avoid database thrashing with tiny writes
		if f.count-first < freezerBatchLimit {
			time.Sleep(freezerRecheckInterval)
		}
	}
}
