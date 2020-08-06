// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bufio"
	"encoding/json"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/console/prompt"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/state/snapshot"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/trie"
	"gopkg.in/urfave/cli.v1"
)

var (
	initCommand = cli.Command{
		Action:    utils.MigrateFlags(initGenesis),
		Name:      "init",
		Usage:     "Bootstrap and initialize a new genesis block",
		ArgsUsage: "<genesisPath>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.KafkaLogTopicFlag,
			utils.KafkaLogBrokerFlag,
			utils.KafkaTransactionTopicFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The init command initializes a new genesis block and definition for the network.
This is a destructive action and changes the network in which you will be
participating.

It expects the genesis file as argument.`,
	}
	dumpGenesisCommand = cli.Command{
		Action:    utils.MigrateFlags(dumpGenesis),
		Name:      "dumpgenesis",
		Usage:     "Dumps genesis block JSON configuration to stdout",
		ArgsUsage: "",
		Flags: []cli.Flag{
			utils.DataDirFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The dumpgenesis command dumps the genesis block configuration in JSON format to stdout.`,
	}
	importCommand = cli.Command{
		Action:    utils.MigrateFlags(importChain),
		Name:      "import",
		Usage:     "Import a blockchain file",
		ArgsUsage: "<filename> (<filename 2> ... <filename N>) ",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.GCModeFlag,
			utils.SnapshotFlag,
			utils.CacheDatabaseFlag,
			utils.CacheGCFlag,
			utils.MetricsEnabledFlag,
			utils.MetricsEnabledExpensiveFlag,
			utils.MetricsHTTPFlag,
			utils.MetricsPortFlag,
			utils.MetricsEnableInfluxDBFlag,
			utils.MetricsInfluxDBEndpointFlag,
			utils.MetricsInfluxDBDatabaseFlag,
			utils.MetricsInfluxDBUsernameFlag,
			utils.MetricsInfluxDBPasswordFlag,
			utils.MetricsInfluxDBTagsFlag,
			utils.TxLookupLimitFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The import command imports blocks from an RLP-encoded form. The form can be one file
with several RLP-encoded blocks, or several files can be used.

If only one file is used, import error will result in failure. If several files are used,
processing will proceed even if an individual RLP-file import failure occurs.`,
	}
	exportCommand = cli.Command{
		Action:    utils.MigrateFlags(exportChain),
		Name:      "export",
		Usage:     "Export blockchain into file",
		ArgsUsage: "<filename> [<blockNumFirst> <blockNumLast>]",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
Requires a first argument of the file to write to.
Optional second and third arguments control the first and
last block to write. In this mode, the file will be appended
if already existing. If the file ends with .gz, the output will
be gzipped.`,
	}
	importPreimagesCommand = cli.Command{
		Action:    utils.MigrateFlags(importPreimages),
		Name:      "import-preimages",
		Usage:     "Import the preimage database from an RLP stream",
		ArgsUsage: "<datafile>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
	The import-preimages command imports hash preimages from an RLP encoded stream.`,
	}
	exportPreimagesCommand = cli.Command{
		Action:    utils.MigrateFlags(exportPreimages),
		Name:      "export-preimages",
		Usage:     "Export the preimage database into an RLP stream",
		ArgsUsage: "<dumpfile>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The export-preimages command export hash preimages to an RLP encoded stream`,
	}
	copydbCommand = cli.Command{
		Action:    utils.MigrateFlags(copyDb),
		Name:      "copydb",
		Usage:     "Create a local chain from a target chaindata folder",
		ArgsUsage: "<sourceChaindataDir>",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.FakePoWFlag,
			utils.RopstenFlag,
			utils.RinkebyFlag,
			utils.TxLookupLimitFlag,
			utils.GoerliFlag,
			utils.YoloV1Flag,
			utils.LegacyTestnetFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The first argument must be the directory containing the blockchain to download from`,
	}
	removedbCommand = cli.Command{
		Action:    utils.MigrateFlags(removeDB),
		Name:      "removedb",
		Usage:     "Remove blockchain and state databases",
		ArgsUsage: " ",
		Flags: []cli.Flag{
			utils.DataDirFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
Remove blockchain and state databases`,
	}
	dumpCommand = cli.Command{
		Action:    utils.MigrateFlags(dump),
		Name:      "dump",
		Usage:     "Dump a specific block from storage",
		ArgsUsage: "[<blockHash> | <blockNum>]...",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.IterativeOutputFlag,
			utils.ExcludeCodeFlag,
			utils.ExcludeStorageFlag,
			utils.IncludeIncompletesFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The arguments are interpreted as block numbers or hashes.
Use "ethereum dump 0" to dump the genesis block.`,
	}
	inspectCommand = cli.Command{
		Action:    utils.MigrateFlags(inspect),
		Name:      "inspect",
		Usage:     "Inspect the storage size for each type of data in the database",
		ArgsUsage: " ",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.AncientFlag,
			utils.CacheFlag,
			utils.RopstenFlag,
			utils.RinkebyFlag,
			utils.GoerliFlag,
			utils.YoloV1Flag,
			utils.LegacyTestnetFlag,
			utils.SyncModeFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
	}
	setHeadCommand = cli.Command{
		Action:    utils.MigrateFlags(setHead),
		Name:      "sethead",
		Usage:     "Sets the head block to a specific block",
		ArgsUsage: "[<blockHash> | <blockNum> | <-blockCount>]...",
		Flags: []cli.Flag{
			utils.DataDirFlag,
			utils.OverlayFlag,
			utils.AncientFlag,
			utils.CacheFlag,
			utils.SyncModeFlag,
			utils.KafkaLogBrokerFlag,
			utils.KafkaLogTopicFlag,
		},
		Category: "BLOCKCHAIN COMMANDS",
		Description: `
The arguments are interpreted as block numbers, hashes, or a number of blocks to be rolled back.
Use "ethereum sethead -2" to drop the two most recent blocks`,
	}
	verifyStateTrieCommand = cli.Command{
     Action:    utils.MigrateFlags(verifyStateTrie),
     Name:      "verifystatetrie",
     Usage:     "Verfies the state trie",
     Flags: []cli.Flag{
       utils.DataDirFlag,
			 utils.AncientFlag,
       utils.CacheFlag,
       utils.SyncModeFlag,
			 utils.OverlayFlag,
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Verify proofs of the latest block state trie. Exit 0 if correct, else exit 1`,
	}
	compactCommand = cli.Command{
     Action:    utils.MigrateFlags(compact),
     Name:      "compactdb",
     Usage:     "Compacts the database",
     Flags: []cli.Flag{
       utils.DataDirFlag,
			 utils.AncientFlag,
       utils.CacheFlag,
       utils.SyncModeFlag,
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Compacts the database`,
	}
	stateMigrateCommand = cli.Command{
     Action:    utils.MigrateFlags(migrateState),
     Name:      "migratestate",
     Usage:     "Migrates the latest state from a DB+Ancient to a new  DB+Ancient",
     Flags: []cli.Flag{
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Migrates state from one leveldb to another`,
	}
	repairMigrationCommand = cli.Command{
     Action:    utils.MigrateFlags(repairMigration),
     Name:      "repairmigration",
     Usage:     "Repairs earlier migrations",
     Flags: []cli.Flag{
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Repairs earlier migrations`,
	}
	repairFreezerIndexCommand = cli.Command{
     Action:    utils.MigrateFlags(repairFreezerIndex),
     Name:      "repairfreezerindex",
     Usage:     "Reindexes the freezer",
     Flags: []cli.Flag{
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Repairs a broken freezer index`,
	}
	freezerDumpCommand = cli.Command{
     Action:    utils.MigrateFlags(freezerDump),
     Name:      "freezerdump",
     Usage:     "Dump the freezer as jsonl",
     Flags: []cli.Flag{
       utils.DataDirFlag,
       utils.CacheFlag,
       utils.SyncModeFlag,
			 utils.AncientFlag,
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Dump the freezer as jsonl`,
	}
	freezerLoadCommand = cli.Command{
     Action:    utils.MigrateFlags(freezerLoad),
     Name:      "freezerload",
     Usage:     "Load jsonl from stdin to an ancients store",
     Flags: []cli.Flag{
       utils.DataDirFlag,
       utils.CacheFlag,
       utils.SyncModeFlag,
			 utils.AncientFlag,
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Load jsonl from stdin to ancients`,
	}
	diffBlocksCommand = cli.Command{
     Action:    utils.MigrateFlags(diffBlocks),
     Name:      "diffblocks",
     Usage:     "Compare two blocks by number, reporting the number of state changes between blocks",
     Flags: []cli.Flag{
       utils.DataDirFlag,
       utils.CacheFlag,
       utils.SyncModeFlag,
			 utils.AncientFlag,
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Compare blocks`,
	}
	resetToSnapshotCommand = cli.Command{
     Action:    utils.MigrateFlags(resetToSnapshot),
     Name:      "resettosnapshot",
     Usage:     "Reset leveldb to match the disk layer snapshot",
     Flags: []cli.Flag{
       utils.DataDirFlag,
       utils.CacheFlag,
       utils.SyncModeFlag,
			 utils.AncientFlag,
			 utils.OverlayFlag,
     },
     Category: "BLOCKCHAIN COMMANDS",
     Description: `
Reset leveldb to match the disk layer snapshot`,
	}
)

// initGenesis will initialise the given JSON format genesis file and writes it as
// the zero'd block (i.e. genesis) or will fail hard if it can't succeed.
func initGenesis(ctx *cli.Context) error {
	// Make sure we have a valid genesis JSON
	genesisPath := ctx.Args().First()
	if len(genesisPath) == 0 {
		utils.Fatalf("Must supply path to genesis JSON file")
	}
	file, err := os.Open(genesisPath)
	if err != nil {
		utils.Fatalf("Failed to read genesis file: %v", err)
	}
	defer file.Close()

	genesis := new(core.Genesis)
	if err := json.NewDecoder(file).Decode(genesis); err != nil {
		utils.Fatalf("invalid genesis file: %v", err)
	}
	// Open an initialise both full and light databases
	stack := makeFullNode(ctx)
	defer stack.Close()

	for _, name := range []string{"chaindata", "lightchaindata"} {
		chaindb, err := stack.OpenDatabase(name, 0, 0, "")
		if err != nil {
			utils.Fatalf("Failed to open database: %v", err)
		}
		_, hash, err := core.SetupGenesisBlock(chaindb, genesis)
		if err != nil {
			utils.Fatalf("Failed to write genesis block: %v", err)
		}
		chaindb.Close()
		log.Info("Successfully wrote genesis state", "database", name, "hash", hash)
	}
	return nil
}

func dumpGenesis(ctx *cli.Context) error {
	genesis := utils.MakeGenesis(ctx)
	if genesis == nil {
		genesis = core.DefaultGenesisBlock()
	}
	if err := json.NewEncoder(os.Stdout).Encode(genesis); err != nil {
		utils.Fatalf("could not encode genesis")
	}
	return nil
}

func importChain(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	// Start metrics export if enabled
	utils.SetupMetrics(ctx)
	// Start system runtime metrics collection
	go metrics.CollectProcessMetrics(3 * time.Second)
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, db := utils.MakeChain(ctx, stack, false)
	defer db.Close()

	// Start periodically gathering memory profiles
	var peakMemAlloc, peakMemSys uint64
	go func() {
		stats := new(runtime.MemStats)
		for {
			runtime.ReadMemStats(stats)
			if atomic.LoadUint64(&peakMemAlloc) < stats.Alloc {
				atomic.StoreUint64(&peakMemAlloc, stats.Alloc)
			}
			if atomic.LoadUint64(&peakMemSys) < stats.Sys {
				atomic.StoreUint64(&peakMemSys, stats.Sys)
			}
			time.Sleep(5 * time.Second)
		}
	}()
	// Import the chain
	start := time.Now()

	var importErr error

	if len(ctx.Args()) == 1 {
		if err := utils.ImportChain(chain, ctx.Args().First()); err != nil {
			importErr = err
			log.Error("Import error", "err", err)
		}
	} else {
		for _, arg := range ctx.Args() {
			if err := utils.ImportChain(chain, arg); err != nil {
				importErr = err
				log.Error("Import error", "file", arg, "err", err)
			}
		}
	}
	chain.Stop()
	fmt.Printf("Import done in %v.\n\n", time.Since(start))

	// Output pre-compaction stats mostly to see the import trashing
	stats, err := db.Stat("leveldb.stats")
	if err != nil {
		utils.Fatalf("Failed to read database stats: %v", err)
	}
	fmt.Println(stats)

	ioStats, err := db.Stat("leveldb.iostats")
	if err != nil {
		utils.Fatalf("Failed to read database iostats: %v", err)
	}
	fmt.Println(ioStats)

	// Print the memory statistics used by the importing
	mem := new(runtime.MemStats)
	runtime.ReadMemStats(mem)

	fmt.Printf("Object memory: %.3f MB current, %.3f MB peak\n", float64(mem.Alloc)/1024/1024, float64(atomic.LoadUint64(&peakMemAlloc))/1024/1024)
	fmt.Printf("System memory: %.3f MB current, %.3f MB peak\n", float64(mem.Sys)/1024/1024, float64(atomic.LoadUint64(&peakMemSys))/1024/1024)
	fmt.Printf("Allocations:   %.3f million\n", float64(mem.Mallocs)/1000000)
	fmt.Printf("GC pause:      %v\n\n", time.Duration(mem.PauseTotalNs))

	if ctx.GlobalBool(utils.NoCompactionFlag.Name) {
		return nil
	}

	// Compact the entire database to more accurately measure disk io and print the stats
	start = time.Now()
	fmt.Println("Compacting entire database...")
	if err = db.Compact(nil, nil); err != nil {
		utils.Fatalf("Compaction failed: %v", err)
	}
	fmt.Printf("Compaction done in %v.\n\n", time.Since(start))

	stats, err = db.Stat("leveldb.stats")
	if err != nil {
		utils.Fatalf("Failed to read database stats: %v", err)
	}
	fmt.Println(stats)

	ioStats, err = db.Stat("leveldb.iostats")
	if err != nil {
		utils.Fatalf("Failed to read database iostats: %v", err)
	}
	fmt.Println(ioStats)
	return importErr
}

func exportChain(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, _ := utils.MakeChain(ctx, stack, true)
	start := time.Now()

	var err error
	fp := ctx.Args().First()
	if len(ctx.Args()) < 3 {
		err = utils.ExportChain(chain, fp)
	} else {
		// This can be improved to allow for numbers larger than 9223372036854775807
		first, ferr := strconv.ParseInt(ctx.Args().Get(1), 10, 64)
		last, lerr := strconv.ParseInt(ctx.Args().Get(2), 10, 64)
		if ferr != nil || lerr != nil {
			utils.Fatalf("Export error in parsing parameters: block number not an integer\n")
		}
		if first < 0 || last < 0 {
			utils.Fatalf("Export error: block number must be greater than 0\n")
		}
		err = utils.ExportAppendChain(chain, fp, uint64(first), uint64(last))
	}

	if err != nil {
		utils.Fatalf("Export error: %v\n", err)
	}
	fmt.Printf("Export done in %v\n", time.Since(start))
	return nil
}

// importPreimages imports preimage data from the specified file.
func importPreimages(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack)
	start := time.Now()

	if err := utils.ImportPreimages(db, ctx.Args().First()); err != nil {
		utils.Fatalf("Import error: %v\n", err)
	}
	fmt.Printf("Import done in %v\n", time.Since(start))
	return nil
}

// exportPreimages dumps the preimage data to specified json file in streaming way.
func exportPreimages(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	defer stack.Close()

	db := utils.MakeChainDatabase(ctx, stack)
	start := time.Now()

	if err := utils.ExportPreimages(db, ctx.Args().First()); err != nil {
		utils.Fatalf("Export error: %v\n", err)
	}
	fmt.Printf("Export done in %v\n", time.Since(start))
	return nil
}

func copyDb(ctx *cli.Context) error {
	// Ensure we have a source chain directory to copy
	if len(ctx.Args()) < 1 {
		utils.Fatalf("Source chaindata directory path argument missing")
	}
	if len(ctx.Args()) < 2 {
		utils.Fatalf("Source ancient chain directory path argument missing")
	}
	// Initialize a new chain for the running node to sync into
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, chainDb := utils.MakeChain(ctx, stack, false)
	syncMode := *utils.GlobalTextMarshaler(ctx, utils.SyncModeFlag.Name).(*downloader.SyncMode)

	var syncBloom *trie.SyncBloom
	if syncMode == downloader.FastSync {
		syncBloom = trie.NewSyncBloom(uint64(ctx.GlobalInt(utils.CacheFlag.Name)/2), chainDb)
	}
	dl := downloader.New(0, chainDb, syncBloom, new(event.TypeMux), chain, nil, nil)

	// Create a source peer to satisfy downloader requests from
	db, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args().First(), ctx.GlobalInt(utils.CacheFlag.Name)/2, 256, ctx.Args().Get(1), "")
	if err != nil {
		return err
	}
	hc, err := core.NewHeaderChain(db, chain.Config(), chain.Engine(), func() bool { return false })
	if err != nil {
		return err
	}
	peer := downloader.NewFakePeer("local", db, hc, dl)
	if err = dl.RegisterPeer("local", 63, peer); err != nil {
		return err
	}
	// Synchronise with the simulated peer
	start := time.Now()

	currentHeader := hc.CurrentHeader()
	if err = dl.Synchronise("local", currentHeader.Hash(), hc.GetTd(currentHeader.Hash(), currentHeader.Number.Uint64()), syncMode); err != nil {
		return err
	}
	for dl.Synchronising() {
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("Database copy done in %v\n", time.Since(start))

	// Compact the entire database to remove any sync overhead
	start = time.Now()
	fmt.Println("Compacting entire database...")
	if err = db.Compact(nil, nil); err != nil {
		utils.Fatalf("Compaction failed: %v", err)
	}
	fmt.Printf("Compaction done in %v.\n\n", time.Since(start))
	return nil
}

func removeDB(ctx *cli.Context) error {
	stack, config := makeConfigNode(ctx)

	// Remove the full node state database
	path := stack.ResolvePath("chaindata")
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "full node state database")
	} else {
		log.Info("Full node state database missing", "path", path)
	}
	// Remove the full node ancient database
	path = config.Eth.DatabaseFreezer
	switch {
	case path == "":
		path = filepath.Join(stack.ResolvePath("chaindata"), "ancient")
	case !filepath.IsAbs(path):
		path = config.Node.ResolvePath(path)
	}
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "full node ancient database")
	} else {
		log.Info("Full node ancient database missing", "path", path)
	}
	// Remove the light node database
	path = stack.ResolvePath("lightchaindata")
	if common.FileExist(path) {
		confirmAndRemoveDB(path, "light node database")
	} else {
		log.Info("Light node database missing", "path", path)
	}
	return nil
}

// confirmAndRemoveDB prompts the user for a last confirmation and removes the
// folder if accepted.
func confirmAndRemoveDB(database string, kind string) {
	confirm, err := prompt.Stdin.PromptConfirm(fmt.Sprintf("Remove %s (%s)?", kind, database))
	switch {
	case err != nil:
		utils.Fatalf("%v", err)
	case !confirm:
		log.Info("Database deletion skipped", "path", database)
	default:
		start := time.Now()
		filepath.Walk(database, func(path string, info os.FileInfo, err error) error {
			// If we're at the top level folder, recurse into
			if path == database {
				return nil
			}
			// Delete all the files, but not subfolders
			if !info.IsDir() {
				os.Remove(path)
				return nil
			}
			return filepath.SkipDir
		})
		log.Info("Database successfully deleted", "path", database, "elapsed", common.PrettyDuration(time.Since(start)))
	}
}

func dump(ctx *cli.Context) error {
	stack := makeFullNode(ctx)
	defer stack.Close()

	chain, chainDb := utils.MakeChain(ctx, stack, true)
	defer chainDb.Close()
	for _, arg := range ctx.Args() {
		var block *types.Block
		if hashish(arg) {
			block = chain.GetBlockByHash(common.HexToHash(arg))
		} else {
			num, _ := strconv.Atoi(arg)
			block = chain.GetBlockByNumber(uint64(num))
		}
		if block == nil {
			fmt.Println("{}")
			utils.Fatalf("block not found")
		} else {
			state, err := state.New(block.Root(), state.NewDatabase(chainDb), nil)
			if err != nil {
				utils.Fatalf("could not create new state: %v", err)
			}
			excludeCode := ctx.Bool(utils.ExcludeCodeFlag.Name)
			excludeStorage := ctx.Bool(utils.ExcludeStorageFlag.Name)
			includeMissing := ctx.Bool(utils.IncludeIncompletesFlag.Name)
			if ctx.Bool(utils.IterativeOutputFlag.Name) {
				state.IterativeDump(excludeCode, excludeStorage, !includeMissing, json.NewEncoder(os.Stdout))
			} else {
				if includeMissing {
					fmt.Printf("If you want to include accounts with missing preimages, you need iterative output, since" +
						" otherwise the accounts will overwrite each other in the resulting mapping.")
				}
				fmt.Printf("%v %s\n", includeMissing, state.Dump(excludeCode, excludeStorage, false))
			}
		}
	}
	return nil
}

func setHead(ctx *cli.Context) error {
	if len(ctx.Args()) < 1 {
		utils.Fatalf("This command requires an argument.")
	}
	stack := makeFullNode(ctx)
	chain, db := utils.MakeChain(ctx, stack, false)
	arg := ctx.Args()[0]
	blockNumber, err := strconv.Atoi(arg)
	if err != nil {
		block := chain.GetBlockByHash(common.HexToHash(arg))
		blockNumber = int(block.Number().Int64())
	} else if blockNumber < 0 {
		latestHash := rawdb.ReadHeadBlockHash(db)
		block := chain.GetBlockByHash(latestHash)
		blockNumber = int(block.Number().Int64()) + blockNumber
	}
	if err := chain.SetHead(uint64(blockNumber)); err != nil {
		fmt.Printf("Failed to set head to %v", blockNumber)
		return err
	}
	chain.Stop()
	db.Close()
	fmt.Printf("Rolled back chain to block %v\n", blockNumber)
	return nil
}

func freezerDump(ctx *cli.Context) error {
	if len(ctx.Args()) < 2 {
		return fmt.Errorf("Usage: freezerDump [ancients] [leveldb] [?offset]")
	}
	startIndex := 0
	if len(ctx.Args()) == 3 {
		startIndex, _ = strconv.Atoi(ctx.Args()[2])
	}
	db, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args()[1], 16, 16, ctx.Args()[0], "new")
	if err != nil { return err }
	count, err := db.Ancients()
	if err != nil { return err }
	log.Info("Loading ancients", "count", count)
	for i := uint64(startIndex); i < count; i++ {
		data := make(map[string]string)
		data["index"] = fmt.Sprintf("%v", i)
		for _, table := range []string{"headers","hashes","bodies","receipts","diffs"} {
			raw, err := db.Ancient(table, i)
			if err != nil { return fmt.Errorf("Error retrieving %v # %v: %v", table, i, err.Error()) }
			data[table] = hex.EncodeToString(raw)
		}
		jsonData, err := json.Marshal(data)
		if err != nil { return fmt.Errorf("Error marshalling %v: %v", i, err.Error()) }
		os.Stdout.Write(jsonData)
		os.Stdout.Write([]byte("\n"))
	}
	return nil
}

func freezerLoad(ctx *cli.Context) error {
	if len(ctx.Args()) != 2 {
		return fmt.Errorf("Usage: freezerDump [ancients] [leveldb]")
	}
	var db ethdb.AncientStore
	var err error
	if strings.HasPrefix(ctx.Args()[0], "s3://") {
		db, err = rawdb.NewS3Freezer(ctx.Args()[0], 128)
	} else {
		db, err = rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args()[1], 16, 16, ctx.Args()[0], "new")
	}
	// db, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args()[1], 16, 16, ctx.Args()[0], "new")
	if err != nil { return err }
	count, err := db.Ancients()
	if err != nil { return err }
	log.Info("Starting load", "freezer size", count)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	for err == nil {
		if len(line) == 0 {
			line, err = reader.ReadBytes('\n')
			continue
		}
		data := make(map[string]string)
		if err := json.Unmarshal(line, &data); err != nil { return err }
		blockNumber, err := strconv.Atoi(data["index"])
		if err != nil { return err }
		if uint64(blockNumber) != count { return fmt.Errorf("Unexpected block: %d != %d", blockNumber, count) }
		hash, err := hex.DecodeString(data["hashes"])
		if err != nil { return err }
		header, err := hex.DecodeString(data["headers"])
		if err != nil { return err }
		body, err := hex.DecodeString(data["bodies"])
		if err != nil { return err }
		receipts, err := hex.DecodeString(data["receipts"])
		if err != nil { return err }
		td, err := hex.DecodeString(data["diffs"])
		if err != nil { return err }
		err = db.AppendAncient(uint64(blockNumber), hash, header, body, receipts, td)
		if err != nil { return err }
		count++
		line, err = reader.ReadBytes('\n')
	}
	db.Sync()
	if err != io.EOF { return err }
	log.Info("Ancient sync done. Indexing freezer")
	// return rawdb.InitDatabaseFromFreezer(db)
	return nil
}

func verifyStateTrie(ctx *cli.Context) error {
  stack := makeFullNode(ctx)
  bc, db := utils.MakeChain(ctx, stack, false)
  latestHash := rawdb.ReadHeadBlockHash(db)
  block := bc.GetBlockByHash(latestHash)

  tr, err := trie.New(block.Root(), trie.NewDatabase(db))
  if err != nil {
    log.Error(fmt.Sprintf("Unhandled trie error"))
    return err
  }
  nodesToCheck := 1000000
  if len(ctx.Args()) > 0 {
    arg := ctx.Args()[0]
    nodesToCheck, err = strconv.Atoi(arg)
    if err != nil { return err }
  }

  iterators := []trie.NodeIterator{}
  for i := 0; i < 256; i++ {
    iterators = append(iterators, tr.NodeIterator([]byte{byte(i)}))
  }
  for i := 0; i < nodesToCheck; i += len(iterators) {
    log.Info("Checking leaves", "checked", i, "limit", nodesToCheck)
    for _, it := range iterators {
      for it.Next(true) {
        if it.Leaf() {
          break
        }
      }
      if err := it.Error(); err != nil {
        return err
      }
    }
  }
  bc.Stop()
  db.Close()
  // fmt.Printf("Rolled back chain to block %v\n", blockNumber)
  return nil
}

type trieRequest struct {
	hash common.Hash
	i int
	data []byte
	err error
}

func syncState(root common.Hash, srcDb state.Database, newDb ethdb.Database) <-chan error {
	errCh := make(chan error)
	go func() {
		count := 10000
		sched := state.NewStateSync(root, newDb, trie.NewSyncBloom(1, newDb))
		log.Info("Syncing", "root", root)
		queue := append([]common.Hash{}, sched.Missing(count)...)
		total := 0
		for len(queue) > 0 {
			log.Info("Processing items", "completed", total, "known", sched.Pending())
			results := make([]trie.SyncResult, len(queue))
			var wg sync.WaitGroup
			ch := make(chan trieRequest, runtime.NumCPU())
			popCh := make(chan trieRequest, runtime.NumCPU())
			for i := 0; i < runtime.NumCPU(); i++ {
				wg.Add(1)
				go func(wg *sync.WaitGroup) {
					defer wg.Done()
					for r := range ch {
						r.data, r.err = srcDb.TrieDB().Node(r.hash)
						if r.err != nil {
							log.Warn("Error processing hash", "hash", r.hash, "err", r.err)
						}
						popCh <- r
					}
				}(&wg)
			}
			go func(wg *sync.WaitGroup) {
				wg.Wait()
				close(popCh)
			}(&wg)
			go func() {
				for i, hash := range queue {
					ch <- trieRequest{hash: hash, i: i}
				}
				close(ch)
			}()
			for r := range popCh {
				if r.err != nil {
					log.Crit("trie node error", "err", r.err)
					errCh <- r.err
					return
				}
				results[r.i] = trie.SyncResult{Hash: r.hash, Data: r.data}
			}
			if _, index, err := sched.Process(results); err != nil {
				log.Crit("trie processing error", "err", err)
				errCh <- fmt.Errorf("failed to process result #%d: %v", index, err)
				return
			}
			batch := newDb.NewBatch()
			if err := sched.Commit(batch); err != nil {
				log.Crit("commit error", "err", err)
				errCh <- fmt.Errorf("failed to commit data: %v", err)
				return
			}
			batch.Write()
			total += len(queue)
			queue = append(queue[:0], sched.Missing(count)...)
		}
		errCh <- nil
	}()

	return errCh
}

// repairMigration adds in Hash -> Number mappings and TxLookupEntries, which
// were inadvertently omitted from an earlier iteration of the state migration
// tool.
func repairMigration(ctx *cli.Context) error {
	newDb, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args()[1], 16, 16, ctx.Args()[0], "new")
	frozen, err := newDb.Ancients()
	if err != nil { return err }
	if frozen == 0 {
		return fmt.Errorf("Freezer is empty")
	}
	hash := rawdb.ReadCanonicalHash(newDb, frozen)
	block := rawdb.ReadBlock(newDb, hash, frozen)
	for {
		batch := newDb.NewBatch()
		rawdb.WriteHeaderNumber(batch, block.Hash(), block.NumberU64())
		rawdb.WriteTxLookupEntries(batch, block)
		batch.Write()
		nextHash := rawdb.ReadCanonicalHash(newDb, block.NumberU64() + 1)
		block = rawdb.ReadBlock(newDb, nextHash, block.NumberU64() + 1)
		if block == nil { return nil }
		if block.NumberU64() % 1000 == 0 {
			log.Info("Repairing block", "number", block.NumberU64(), "hash", block.Hash())
		}
	}
}

func repairFreezerIndex(ctx *cli.Context) error {
	newDb, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args()[1], 16, 16, ctx.Args()[0], "new")
	if err != nil { return err }
	hash := rawdb.ReadHeadFastBlockHash(newDb)
	rawdb.InitDatabaseFromFreezer(newDb)
	rawdb.WriteHeadHeaderHash(newDb, hash)
	rawdb.WriteHeadFastBlockHash(newDb, hash)
	return nil
}

func migrateState(ctx *cli.Context) error {
	if len(ctx.Args()) < 3 {
    return fmt.Errorf("Usage: migrateState [ancients] [oldLeveldb] [newLeveldb] [?kafkaTopic]")
  }
	newDb, err := rawdb.NewLevelDBDatabaseWithFreezer(ctx.Args()[2], 16, 16, ctx.Args()[0], "new")
	if err != nil { return err }
	frozen, err := newDb.Ancients()
	if err != nil { return err }
	if frozen == 0 {
		return fmt.Errorf("Freezer is empty")
	}
	oldDb, err := rawdb.NewLevelDBDatabase(ctx.Args()[1], 16, 16, "old")
	if err != nil { return err }
	if len(ctx.Args()) == 4 {
		key := fmt.Sprintf("cdc-log-%v-offset", ctx.Args()[3])
		offset, err := oldDb.Get([]byte(key))
		if err != nil { return err }
		if err := newDb.Put([]byte(key), offset); err != nil { return err }
		log.Info("Copied offset", "key", key)
	}
	start := time.Now()
	ancientErrCh := make(chan error, 1)
	if os.Getenv("SKIP_INIT_FREEZER") != "true" {
		go func() {
			it := oldDb.NewIterator([]byte("l"), nil)
			defer it.Release()
			batch := newDb.NewBatch()
			for it.Next() {
				if err := batch.Put(it.Key(), it.Value()); err != nil {
					ancientErrCh <- err
					return
				}
				if batch.ValueSize() > ethdb.IdealBatchSize {
					if err := batch.Write(); err != nil {
						ancientErrCh <- err
						return
					}
					batch.Reset()
				}
			}
			if err := it.Error(); err != nil {
				ancientErrCh <- err
				return
			}
			headerIt := oldDb.NewIterator([]byte("H"), nil)
			defer headerIt.Release()
			for headerIt.Next() {
				if err := batch.Put(headerIt.Key(), headerIt.Value()); err != nil {
					ancientErrCh <- err
					return
				}
				if batch.ValueSize() > ethdb.IdealBatchSize {
					if err := batch.Write(); err != nil {
						ancientErrCh <- err
						return
					}
					batch.Reset()
				}
			}
			if err := batch.Write(); err != nil {
				ancientErrCh <- err
				return
			}
			if err := headerIt.Error(); err != nil {
				ancientErrCh <- err
				return
			}
			blockNo, err := newDb.Ancients()
			if err != nil {
				ancientErrCh <- err
				return
			}
			hash := rawdb.ReadCanonicalHash(newDb, blockNo)

			rawdb.WriteHeadHeaderHash(newDb, hash)
			rawdb.WriteHeadFastBlockHash(newDb, hash)


			// rawdb.InitDatabaseFromFreezer(newDb)
			ancientErrCh <- nil
			log.Info("Initialized from freezer", "elapsed", time.Since(start))
		}()
	} else {
		ancientErrCh <- nil
	}
	srcDb := state.NewDatabase(oldDb)

	genesisHash := rawdb.ReadCanonicalHash(oldDb, 0)
	block := rawdb.ReadBlock(oldDb, genesisHash, 0)

	latestBlockHash := rawdb.ReadHeadBlockHash(oldDb) // Find the latest blockhash migrated to the new database
	if latestBlockHash == (common.Hash{}) {
		return fmt.Errorf("Source block hash empty")
	}
	latestHeaderNumber := rawdb.ReadHeaderNumber(oldDb, latestBlockHash)
	latestBlock := rawdb.ReadBlock(oldDb, latestBlockHash, *latestHeaderNumber)
	for _, err := srcDb.TrieDB().Node(latestBlock.Root()); err != nil ; {
		latestBlock = rawdb.ReadBlock(oldDb, latestBlock.ParentHash(), latestBlock.NumberU64())
	}


	log.Info("Syncing genesis block state", "hash", block.Hash(), "root", block.Root())
	genesisErrCh := syncState(block.Root(), srcDb, newDb)
	log.Info("Syncing latest block state", "hash", latestBlock.Hash(), "root", latestBlock.Root())
	latestErrCh := syncState(latestBlock.Root(), srcDb, newDb)

	chainConfig := rawdb.ReadChainConfig(oldDb, block.Hash())
	batch := newDb.NewBatch()
	rawdb.WriteTd(batch, block.Hash(), block.NumberU64(), rawdb.ReadTd(oldDb, block.Hash(), block.NumberU64()))
	rawdb.WriteBlock(batch, block)
	rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), nil)
	rawdb.WriteCanonicalHash(batch, block.Hash(), block.NumberU64())
	rawdb.WriteChainConfig(batch, block.Hash(), chainConfig)
	rawdb.WriteHeaderNumber(batch, block.Hash(), block.NumberU64())
	rawdb.WriteTxLookupEntries(batch, block)
	batch.Write()

	if err := <-ancientErrCh; err != nil { return err }
	srcBlockHash := rawdb.ReadHeadFastBlockHash(newDb) // Find the latest blockhash migrated to the new database
	if srcBlockHash == (common.Hash{}) {
		return fmt.Errorf("Source block hash empty")
	}
	srcHeaderNumber := rawdb.ReadHeaderNumber(newDb, srcBlockHash)
	block = rawdb.ReadBlock(newDb, srcBlockHash, *srcHeaderNumber)
	for {
		batch := newDb.NewBatch()
		rawdb.WriteTd(batch, block.Hash(), block.NumberU64(), rawdb.ReadTd(oldDb, block.Hash(), block.NumberU64()))
		rawdb.WriteBlock(batch, block)
		rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), rawdb.ReadReceipts(oldDb, block.Hash(), block.NumberU64(), chainConfig))
		rawdb.WriteCanonicalHash(batch, block.Hash(), block.NumberU64())
		rawdb.WriteHeaderNumber(batch, block.Hash(), block.NumberU64())
		rawdb.WriteTxLookupEntries(batch, block)
		batch.Write()
		nextHash := rawdb.ReadCanonicalHash(oldDb, block.NumberU64() + 1)
		if nextHash == (common.Hash{}) {
			// There's an edge case where early blocks may still be in the freezer
			// even though they didn't get migrated, and thus the olddb won't have the
			// canonical hash but the newdb will
			nextHash = rawdb.ReadCanonicalHash(newDb, block.NumberU64() + 1)
			if nextHash == (common.Hash{}) {
				log.Info("Migrated up to block", "number", block.NumberU64(), "hash", block.Hash(), "time", block.Time())
				break
			}
		}
		block = rawdb.ReadBlock(oldDb, nextHash, block.NumberU64() + 1)
		if block.NumberU64() % 1000 == 0 {
			log.Info("Migrating block", "number", block.NumberU64(), "hash", block.Hash())
		}
	}

	if err := <-genesisErrCh; err != nil {
		log.Crit("error syncing genesis")
		return err
	}
	if err := <-latestErrCh; err != nil {
		log.Crit("error syncing latest")
		return err
	}
	rawdb.WriteHeadBlockHash(newDb, block.Hash())
	rawdb.WriteHeadHeaderHash(newDb, block.Hash())
	rawdb.WriteHeadFastBlockHash(newDb, block.Hash())
	return nil
}

func compact(ctx *cli.Context) error {
  stack := makeFullNode(ctx)
  _, db := utils.MakeChain(ctx, stack, false)
	start := time.Now()
	err := db.Compact(nil, nil)
	log.Info("Done", "time", time.Since(start))
	return err
}

func diffBlocks(ctx *cli.Context) error {
	stack := makeFullNode(ctx)
	db := utils.MakeChainDatabase(ctx, stack)
	sourceBlockNumber, err := strconv.Atoi(ctx.Args()[0])
	if err != nil { return err }
	dstBlockNumber, err := strconv.Atoi(ctx.Args()[1])
	if err != nil { return err }
	sourceHash := rawdb.ReadCanonicalHash(db, uint64(sourceBlockNumber))
	dstHash := rawdb.ReadCanonicalHash(db, uint64(dstBlockNumber))
  sourceBlock := rawdb.ReadHeader(db, sourceHash, uint64(sourceBlockNumber))
  dstBlock := rawdb.ReadHeader(db, dstHash, uint64(dstBlockNumber))
	destructs, accounts, _, err := snapshot.DiffTries(db, sourceBlock.Root, dstBlock.Root)
	log.Info("Diff", "destructs", len(destructs), "accounts", len(accounts))
	return err
}

func resetToSnapshot(ctx *cli.Context) error {
	stack := makeFullNode(ctx)
	diskdb := utils.MakeChainDatabase(ctx, stack)
	baseRoot := rawdb.ReadSnapshotRoot(diskdb)
	if baseRoot == (common.Hash{}) {
		return fmt.Errorf("missing or corrupted snapshot")
	}
	headHash := rawdb.ReadHeadHeaderHash(diskdb)
	headNumber := *(rawdb.ReadHeaderNumber(diskdb, headHash))
	header := rawdb.ReadHeader(diskdb, headHash, headNumber)
	limit := 256
	counter := 0
	for header.Root != baseRoot {
		header = rawdb.ReadHeader(diskdb, header.ParentHash, header.Number.Uint64() - 1)
		counter++
		if counter > limit { return fmt.Errorf("No header matching root %#x within limit", baseRoot)}
	}
	snaps, err := snapshot.LoadDiskLayerSnapshot(diskdb, trie.NewDatabase(diskdb), 256)
	if err != nil { return err }
	snaps.Journal(baseRoot)
	rawdb.WriteHeadBlockHash(diskdb, header.Hash())
	rawdb.WriteHeadHeaderHash(diskdb, header.Hash())
	rawdb.WriteHeadFastBlockHash(diskdb, header.Hash())
	return nil
}

// func revertToSnapshot(ctx *cli.Context) error {
//   stack := makeFullNode(ctx)
//   db := utils.MakeChainDatabase(ctx, stack, false)
// 	root := rawdb.ReadSnapshotRoot(db)
// 	statedb := state.NewDatabase(db)
// 	_, err := statedb.OpenTrie(root)
// 	if err != nil { return err }
// 	log.Info("Root exists in database", "root", root)
// 	headHash := rawdb.ReadHeadHeaderHash(db)
// 	headNumber := *(rawdb.ReadHeaderNumber(db, headHash))
// 	header := rawdb.ReadHeader(db, headHash, headNumber)
// 	headers := []types.Header{header}
// 	limit := 256
// 	for header.Root != root {
// 		header = rawdb.ReadHeader(db, header.ParentHash, header.Number.Uint64() - 1)
// 		headers = append(headers, header)
// 		if len(headers) > limit { return fmt.Errorf("No header matching root %#x within limit", root)}
// 	}
// 	log.Info("Found matching block", "hash", header.Hash())
// 	return nil
// }


func inspect(ctx *cli.Context) error {
	node, _ := makeConfigNode(ctx)
	defer node.Close()

	_, chainDb := utils.MakeChain(ctx, node, true)
	defer chainDb.Close()

	return rawdb.InspectDatabase(chainDb)
}

// hashish returns true for strings that look like hashes.
func hashish(x string) bool {
	_, err := strconv.Atoi(x)
	return err != nil
}
