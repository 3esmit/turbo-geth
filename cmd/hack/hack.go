package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RoaringBitmap/roaring"
	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/holiman/uint256"
	"github.com/ledgerwatch/lmdb-go/lmdb"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/changeset"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/common/etl"
	"github.com/ledgerwatch/turbo-geth/consensus/ethash"
	"github.com/ledgerwatch/turbo-geth/core"
	"github.com/ledgerwatch/turbo-geth/core/state"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/core/types/accounts"
	"github.com/ledgerwatch/turbo-geth/core/vm"
	"github.com/ledgerwatch/turbo-geth/crypto"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/ethdb/codecpool"
	"github.com/ledgerwatch/turbo-geth/log"
	"github.com/ledgerwatch/turbo-geth/node"
	"github.com/ledgerwatch/turbo-geth/params"
	"github.com/ledgerwatch/turbo-geth/rlp"
	"github.com/ledgerwatch/turbo-geth/turbo/rawdb"
	"github.com/ledgerwatch/turbo-geth/turbo/trie"
	"github.com/wcharczuk/go-chart"
	"github.com/wcharczuk/go-chart/util"
)

var emptyCodeHash = crypto.Keccak256(nil)

var verbosity = flag.Uint("verbosity", 3, "Logging verbosity: 0=silent, 1=error, 2=warn, 3=info, 4=debug, 5=detail (default 3)")
var action = flag.String("action", "", "action to execute")
var cpuprofile = flag.String("cpuprofile", "", "write cpu profile `file`")
var rewind = flag.Int("rewind", 1, "rewind to given number of blocks")
var block = flag.Int("block", 1, "specifies a block number for operation")
var account = flag.String("account", "0x", "specifies account to investigate")
var name = flag.String("name", "", "name to add to the file names")
var chaindata = flag.String("chaindata", "chaindata", "path to the chaindata database file")
var bucket = flag.String("bucket", "", "bucket in the database")
var hash = flag.String("hash", "0x00", "image for preimage or state root for testBlockHashes action")

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func parseFloat64(str string) float64 {
	v, _ := strconv.ParseFloat(str, 64)
	return v
}

func readData(filename string) (blocks []float64, hours []float64, dbsize []float64, trienodes []float64, heap []float64) {
	err := util.File.ReadByLines(filename, func(line string) error {
		parts := strings.Split(line, ",")
		blocks = append(blocks, parseFloat64(strings.Trim(parts[0], " ")))
		hours = append(hours, parseFloat64(strings.Trim(parts[1], " ")))
		dbsize = append(dbsize, parseFloat64(strings.Trim(parts[2], " ")))
		trienodes = append(trienodes, parseFloat64(strings.Trim(parts[3], " ")))
		heap = append(heap, parseFloat64(strings.Trim(parts[4], " ")))
		return nil
	})
	if err != nil {
		fmt.Println(err.Error())
	}
	return
}

func notables() []chart.GridLine {
	return []chart.GridLine{
		{Value: 1.0},
		{Value: 2.0},
		{Value: 3.0},
		{Value: 4.0},
		{Value: 5.0},
		{Value: 6.0},
	}
}

func days() []chart.GridLine {
	return []chart.GridLine{
		{Value: 24.0},
		{Value: 48.0},
		{Value: 72.0},
		{Value: 96.0},
		{Value: 120.0},
		{Value: 144.0},
		{Value: 168.0},
		{Value: 192.0},
		{Value: 216.0},
		{Value: 240.0},
		{Value: 264.0},
		{Value: 288.0},
	}
}

func mychart() {
	blocks, hours, dbsize, trienodes, heap := readData("bolt.csv")
	blocks0, hours0, dbsize0, _, _ := readData("badger.csv")
	mainSeries := &chart.ContinuousSeries{
		Name: "Cumulative sync time (bolt)",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorBlue,
			FillColor:   chart.ColorBlue.WithAlpha(100),
		},
		XValues: blocks,
		YValues: hours,
	}
	badgerSeries := &chart.ContinuousSeries{
		Name: "Cumulative sync time (badger)",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorRed,
			FillColor:   chart.ColorRed.WithAlpha(100),
		},
		XValues: blocks0,
		YValues: hours0,
	}
	dbsizeSeries := &chart.ContinuousSeries{
		Name: "Database size (bolt)",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorBlack,
		},
		YAxis:   chart.YAxisSecondary,
		XValues: blocks,
		YValues: dbsize,
	}
	dbsizeSeries0 := &chart.ContinuousSeries{
		Name: "Database size (badger)",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorOrange,
		},
		YAxis:   chart.YAxisSecondary,
		XValues: blocks,
		YValues: dbsize0,
	}

	graph1 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		YAxis: chart.YAxis{
			Name:      "Elapsed time",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%d h", int(v.(float64)))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorBlue,
				StrokeWidth: 1.0,
			},
			GridLines: days(),
		},
		YAxisSecondary: chart.YAxis{
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%d G", int(v.(float64)))
			},
		},
		XAxis: chart.XAxis{
			Name: "Blocks, million",
			Style: chart.Style{
				Show: true,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.3fm", v.(float64))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorAlternateGray,
				StrokeWidth: 1.0,
			},
			GridLines: notables(),
		},
		Series: []chart.Series{
			mainSeries,
			badgerSeries,
			dbsizeSeries,
			dbsizeSeries0,
		},
	}

	graph1.Elements = []chart.Renderable{chart.LegendThin(&graph1)}

	buffer := bytes.NewBuffer([]byte{})
	err := graph1.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart1.png", buffer.Bytes(), 0644)
	check(err)

	heapSeries := &chart.ContinuousSeries{
		Name: "Allocated heap",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorYellow,
			FillColor:   chart.ColorYellow.WithAlpha(100),
		},
		XValues: blocks,
		YValues: heap,
	}
	trienodesSeries := &chart.ContinuousSeries{
		Name: "Trie nodes",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorGreen,
		},
		YAxis:   chart.YAxisSecondary,
		XValues: blocks,
		YValues: trienodes,
	}
	graph2 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		YAxis: chart.YAxis{
			Name:      "Allocated heap",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.1f G", v.(float64))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorYellow,
				StrokeWidth: 1.0,
			},
			GridLines: days(),
		},
		YAxisSecondary: chart.YAxis{
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.1f m", v.(float64))
			},
		},
		XAxis: chart.XAxis{
			Name: "Blocks, million",
			Style: chart.Style{
				Show: true,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.3fm", v.(float64))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.ColorAlternateGray,
				StrokeWidth: 1.0,
			},
			GridLines: notables(),
		},
		Series: []chart.Series{
			heapSeries,
			trienodesSeries,
		},
	}

	graph2.Elements = []chart.Renderable{chart.LegendThin(&graph2)}
	buffer.Reset()
	err = graph2.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart2.png", buffer.Bytes(), 0644)
	check(err)
}

//nolint
func accountSavings(db ethdb.KV) (int, int) {
	emptyRoots := 0
	emptyCodes := 0
	check(db.View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.CurrentStateBucket)
		for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			if len(k) != 32 {
				continue
			}
			if bytes.Contains(v, trie.EmptyRoot.Bytes()) {
				emptyRoots++
			}
			if bytes.Contains(v, emptyCodeHash) {
				emptyCodes++
			}
		}
		return nil
	}))
	return emptyRoots, emptyCodes
}

func bucketStats(chaindata string) {
	t := ethdb.Lmdb
	bucketList := dbutils.Buckets

	switch t {
	case ethdb.Lmdb:
		env, err := lmdb.NewEnv()
		check(err)
		err = env.SetMaxDBs(100)
		check(err)
		err = env.Open(chaindata, lmdb.Readonly, 0664)
		check(err)

		fmt.Printf(",BranchPageN,LeafPageN,OverflowN,Entries\n")
		_ = env.View(func(tx *lmdb.Txn) error {
			for _, bucket := range bucketList {
				dbi, bucketErr := tx.OpenDBI(string(bucket), 0)
				if bucketErr != nil {
					fmt.Printf("opening bucket %s: %v\n", bucket, bucketErr)
					continue
				}
				bs, statErr := tx.Stat(dbi)
				check(statErr)
				fmt.Printf("%s,%d,%d,%d,%d\n", string(bucket),
					bs.BranchPages, bs.LeafPages, bs.OverflowPages, bs.Entries)
			}
			return nil
		})
	}
}

func readTrieLog() ([]float64, map[int][]float64, []float64) {
	data, err := ioutil.ReadFile("dust/hack.log")
	check(err)
	thresholds := []float64{}
	counts := map[int][]float64{}
	for i := 2; i <= 16; i++ {
		counts[i] = []float64{}
	}
	shorts := []float64{}
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("Threshold:")) {
			tokens := bytes.Split(line, []byte(" "))
			if len(tokens) == 23 {
				wei := parseFloat64(string(tokens[1]))
				thresholds = append(thresholds, wei)
				for i := 2; i <= 16; i++ {
					pair := bytes.Split(tokens[i+3], []byte(":"))
					counts[i] = append(counts[i], parseFloat64(string(pair[1])))
				}
				pair := bytes.Split(tokens[21], []byte(":"))
				shorts = append(shorts, parseFloat64(string(pair[1])))
			}
		}
	}
	return thresholds, counts, shorts
}

func ts() []chart.GridLine {
	return []chart.GridLine{
		{Value: 420.0},
	}
}

func trieChart() {
	thresholds, counts, shorts := readTrieLog()
	fmt.Printf("%d %d %d\n", len(thresholds), len(counts), len(shorts))
	shortsSeries := &chart.ContinuousSeries{
		Name: "Short nodes",
		Style: chart.Style{
			Show:        true,
			StrokeColor: chart.ColorBlue,
			FillColor:   chart.ColorBlue.WithAlpha(100),
		},
		XValues: thresholds,
		YValues: shorts,
	}
	countSeries := make(map[int]*chart.ContinuousSeries)
	for i := 2; i <= 16; i++ {
		countSeries[i] = &chart.ContinuousSeries{
			Name: fmt.Sprintf("%d-nodes", i),
			Style: chart.Style{
				Show:        true,
				StrokeColor: chart.GetAlternateColor(i),
			},
			XValues: thresholds,
			YValues: counts[i],
		}
	}
	xaxis := &chart.XAxis{
		Name: "Dust theshold",
		Style: chart.Style{
			Show: true,
		},
		ValueFormatter: func(v interface{}) string {
			return fmt.Sprintf("%d wei", int(v.(float64)))
		},
		GridMajorStyle: chart.Style{
			Show:        true,
			StrokeColor: chart.DefaultStrokeColor,
			StrokeWidth: 1.0,
		},
		Range: &chart.ContinuousRange{
			Min: thresholds[0],
			Max: thresholds[len(thresholds)-1],
		},
		Ticks: []chart.Tick{
			{Value: 0.0, Label: "0"},
			{Value: 1.0, Label: "wei"},
			{Value: 10.0, Label: "10"},
			{Value: 100.0, Label: "100"},
			{Value: 1e3, Label: "1e3"},
			{Value: 1e4, Label: "1e4"},
			{Value: 1e5, Label: "1e5"},
			{Value: 1e6, Label: "1e6"},
			{Value: 1e7, Label: "1e7"},
			{Value: 1e8, Label: "1e8"},
			{Value: 1e9, Label: "1e9"},
			{Value: 1e10, Label: "1e10"},
			//{1e15, "finney"},
			//{1e18, "ether"},
		},
	}

	graph3 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		XAxis: *xaxis,
		YAxis: chart.YAxis{
			Name:      "Node count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%dm", int(v.(float64)/1e6))
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.DefaultStrokeColor,
				StrokeWidth: 1.0,
			},
		},
		Series: []chart.Series{
			shortsSeries,
		},
	}
	graph3.Elements = []chart.Renderable{chart.LegendThin(&graph3)}
	buffer := bytes.NewBuffer([]byte{})
	err := graph3.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart3.png", buffer.Bytes(), 0644)
	check(err)
	graph4 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		XAxis: *xaxis,
		YAxis: chart.YAxis{
			Name:      "Node count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.2fm", v.(float64)/1e6)
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.DefaultStrokeColor,
				StrokeWidth: 1.0,
			},
		},
		Series: []chart.Series{
			countSeries[2],
			countSeries[3],
		},
	}
	graph4.Elements = []chart.Renderable{chart.LegendThin(&graph4)}
	buffer = bytes.NewBuffer([]byte{})
	err = graph4.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart4.png", buffer.Bytes(), 0644)
	check(err)
	graph5 := chart.Chart{
		Width:  1280,
		Height: 720,
		Background: chart.Style{
			Padding: chart.Box{
				Top: 50,
			},
		},
		XAxis: *xaxis,
		YAxis: chart.YAxis{
			Name:      "Node count",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
			TickStyle: chart.Style{
				TextRotationDegrees: 45.0,
			},
			ValueFormatter: func(v interface{}) string {
				return fmt.Sprintf("%.2fk", v.(float64)/1e3)
			},
			GridMajorStyle: chart.Style{
				Show:        true,
				StrokeColor: chart.DefaultStrokeColor,
				StrokeWidth: 1.0,
			},
		},
		Series: []chart.Series{
			countSeries[4],
			countSeries[5],
			countSeries[6],
			countSeries[7],
			countSeries[8],
			countSeries[9],
			countSeries[10],
			countSeries[11],
			countSeries[12],
			countSeries[13],
			countSeries[14],
			countSeries[15],
			countSeries[16],
		},
	}
	graph5.Elements = []chart.Renderable{chart.LegendThin(&graph5)}
	buffer = bytes.NewBuffer([]byte{})
	err = graph5.Render(chart.PNG, buffer)
	check(err)
	err = ioutil.WriteFile("chart5.png", buffer.Bytes(), 0644)
	check(err)
}

func extractTrie(block int) {
	stateDb := ethdb.MustOpen("statedb")
	defer stateDb.Close()
	txCacher := core.NewTxSenderCacher(runtime.NumCPU())
	bc, err := core.NewBlockChain(stateDb, nil, params.RopstenChainConfig, ethash.NewFaker(), vm.Config{}, nil, txCacher)
	check(err)
	defer bc.Stop()
	baseBlock := bc.GetBlockByNumber(uint64(block))
	tds := state.NewTrieDbState(baseBlock.Root(), stateDb, baseBlock.NumberU64())
	rebuiltRoot := tds.LastRoot()
	fmt.Printf("Rebuit root hash: %x\n", rebuiltRoot)
	filename := fmt.Sprintf("right_%d.txt", baseBlock.NumberU64())
	fmt.Printf("Generating deep snapshot of the right tries... %s\n", filename)
	f, err := os.Create(filename)
	if err == nil {
		defer f.Close()
		tds.PrintTrie(f)
	}
}

func testRewind(chaindata string, block, rewind int) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	txCacher := core.NewTxSenderCacher(runtime.NumCPU())
	bc, err := core.NewBlockChain(ethDb, nil, params.MainnetChainConfig, ethash.NewFaker(), vm.Config{}, nil, txCacher)
	check(err)
	defer bc.Stop()
	currentBlock := bc.CurrentBlock()
	currentBlockNr := currentBlock.NumberU64()
	if block == 1 {
		block = int(currentBlockNr)
	}
	baseBlock := bc.GetBlockByNumber(uint64(block))
	baseBlockNr := baseBlock.NumberU64()
	fmt.Printf("Base block number: %d\n", baseBlockNr)
	fmt.Printf("Base block root hash: %x\n", baseBlock.Root())
	db := ethDb.NewBatch()
	defer db.Rollback()
	tds := state.NewTrieDbState(baseBlock.Root(), db, baseBlockNr)
	tds.SetHistorical(baseBlockNr != currentBlockNr)
	rebuiltRoot := tds.LastRoot()
	fmt.Printf("Rebuit root hash: %x\n", rebuiltRoot)
	startTime := time.Now()
	rewindLen := uint64(rewind)

	err = tds.UnwindTo(baseBlockNr - rewindLen)
	fmt.Printf("Unwind done in %v\n", time.Since(startTime))
	check(err)
	rewoundBlock1 := bc.GetBlockByNumber(baseBlockNr - rewindLen + 1)
	fmt.Printf("Rewound+1 block number: %d\n", rewoundBlock1.NumberU64())
	fmt.Printf("Rewound+1 block hash: %x\n", rewoundBlock1.Hash())
	fmt.Printf("Rewound+1 block root hash: %x\n", rewoundBlock1.Root())
	fmt.Printf("Rewound+1 block parent hash: %x\n", rewoundBlock1.ParentHash())

	rewoundBlock := bc.GetBlockByNumber(baseBlockNr - rewindLen)
	fmt.Printf("Rewound block number: %d\n", rewoundBlock.NumberU64())
	fmt.Printf("Rewound block hash: %x\n", rewoundBlock.Hash())
	fmt.Printf("Rewound block root hash: %x\n", rewoundBlock.Root())
	fmt.Printf("Rewound block parent hash: %x\n", rewoundBlock.ParentHash())
	rewoundRoot := tds.LastRoot()
	fmt.Printf("Calculated rewound root hash: %x\n", rewoundRoot)
	/*
		filename := fmt.Sprintf("root_%d.txt", rewoundBlock.NumberU64())
		fmt.Printf("Generating deep snapshot of the wront tries... %s\n", filename)
		f, err := os.Create(filename)
		if err == nil {
			defer f.Close()
			tds.PrintTrie(f)
		}

		{
			tds, err = state.NewTrieDbState(rewoundBlock.Root(), db, rewoundBlock.NumberU64())
			tds.SetHistorical(true)
			check(err)
			rebuiltRoot, err := tds.TrieRoot()
			fmt.Printf("Rebuilt root: %x\n", rebuiltRoot)
			check(err)
		}
	*/
}

func testStartup() {
	startTime := time.Now()
	//ethDb := ethdb.MustOpen(node.DefaultDataDir() + "/geth/chaindata")
	ethDb := ethdb.MustOpen("/home/akhounov/.ethereum/geth/chaindata")
	defer ethDb.Close()
	txCacher := core.NewTxSenderCacher(runtime.NumCPU())
	bc, err := core.NewBlockChain(ethDb, nil, params.MainnetChainConfig, ethash.NewFaker(), vm.Config{}, nil, txCacher)
	check(err)
	defer bc.Stop()
	currentBlock := bc.CurrentBlock()
	currentBlockNr := currentBlock.NumberU64()
	fmt.Printf("Current block number: %d\n", currentBlockNr)
	fmt.Printf("Current block root hash: %x\n", currentBlock.Root())
	l := trie.NewSubTrieLoader(currentBlockNr)
	rl := trie.NewRetainList(0)
	subTries, err1 := l.LoadSubTries(ethDb, currentBlockNr, rl, nil /* HashCollector */, [][]byte{nil}, []int{0}, false)
	if err1 != nil {
		fmt.Printf("%v\n", err1)
	}
	if subTries.Hashes[0] != currentBlock.Root() {
		fmt.Printf("Hash mismatch, got %x, expected %x\n", subTries.Hashes[0], currentBlock.Root())
	}
	fmt.Printf("Took %v\n", time.Since(startTime))
}

func dbSlice(chaindata string, bucket string, prefix []byte) {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(bucket)
		for k, v, err := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v, err = c.Next() {
			if err != nil {
				return err
			}
			fmt.Printf("db.Put([]byte(\"%s\"), common.FromHex(\"%x\"), common.FromHex(\"%x\"))\n", bucket, k, v)
		}
		return nil
	}); err != nil {
		panic(err)
	}
}

func testResolve(chaindata string) {
	startTime := time.Now()
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	//bc, err := core.NewBlockChain(ethDb, nil, params.MainnetChainConfig, ethash.NewFaker(), vm.Config{}, nil, nil)
	//check(err)
	/*
		currentBlock := bc.CurrentBlock()
		currentBlockNr := currentBlock.NumberU64()
		fmt.Printf("Current block number: %d\n", currentBlockNr)
		fmt.Printf("Current block root hash: %x\n", currentBlock.Root())
		prevBlock := bc.GetBlockByNumber(currentBlockNr - 2)
		fmt.Printf("Prev block root hash: %x\n", prevBlock.Root())
	*/
	currentBlockNr := uint64(286798)
	//var contract []byte
	//contract = common.FromHex("8416044c93d8fdf2d06a5bddbea65234695a3d4d278d5c824776c8b31702505dfffffffffffffffe")
	resolveHash := common.HexToHash("321131c74d582ebe29075d573023accd809234e4dbdee29e814bacedd3467279")
	l := trie.NewSubTrieLoader(currentBlockNr)
	var key []byte
	key = common.FromHex("0a080d05070c0604040302030508050100020105040e05080c0a0f030d0d050f08070a050b0c08090b02040e0e0200030f0c0b0f0704060a0d0703050009010f")
	rl := trie.NewRetainList(0)
	rl.AddHex(key[:3])
	subTries, err1 := l.LoadSubTries(ethDb, currentBlockNr, rl, nil /* HashCollector */, [][]byte{{0xa8, 0xd0}}, []int{12}, true)
	if err1 != nil {
		fmt.Printf("Resolve error: %v\n", err1)
	}
	if subTries.Hashes[0] != resolveHash {
		fmt.Printf("Has mismatch, got %x, expected %x\n", subTries.Hashes[0], resolveHash)
	}
	/*
		var filename string
		if err == nil {
			filename = fmt.Sprintf("right_%d.txt", currentBlockNr)
		} else {
			filename = fmt.Sprintf("root_%d.txt", currentBlockNr)
		}
		fmt.Printf("Generating deep snapshot of the tries... %s\n", filename)
		f, err := os.Create(filename)
		if err == nil {
			defer f.Close()
			t.Print(f)
		}
		if err != nil {
			fmt.Printf("%v\n", err)
		}
	*/
	fmt.Printf("Took %v\n", time.Since(startTime))
}

func hashFile() {
	f, err := os.Open("/Users/alexeyakhunov/mygit/go-ethereum/geth.log")
	check(err)
	defer f.Close()
	w, err := os.Create("/Users/alexeyakhunov/mygit/go-ethereum/geth_read.log")
	check(err)
	defer w.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ResolveWithDb") || strings.HasPrefix(line, "Error") ||
			strings.HasPrefix(line, "0000000000000000000000000000000000000000000000000000000000000000") ||
			strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "tc{") {
			fmt.Printf("%d %s\n", count, line)
			count++
		} else if count == 66 {
			w.WriteString(line)
			w.WriteString("\n")
		}
	}
	fmt.Printf("%d lines scanned\n", count)
}

func rlpIndices() {
	keybuf := new(bytes.Buffer)
	for i := 0; i < 512; i++ {
		keybuf.Reset()
		rlp.Encode(keybuf, uint(i))
		fmt.Printf("Encoding of %d is %x\n", i, keybuf.Bytes())
	}
}

func printFullNodeRLPs() {
	trie.FullNode1()
	trie.FullNode2()
	trie.FullNode3()
	trie.FullNode4()
	trie.ShortNode1()
	trie.ShortNode2()
	trie.Hash1()
	trie.Hash2()
	trie.Hash3()
	trie.Hash4()
	trie.Hash5()
	trie.Hash6()
	trie.Hash7()
}

func testDifficulty() {
	genesisBlock, _, _, err := core.DefaultGenesisBlock().ToBlock(nil, false /* history */)
	check(err)
	genesisHeader := genesisBlock.Header()
	d1 := ethash.CalcDifficulty(params.MainnetChainConfig, 100000, genesisHeader.Time, genesisHeader.Difficulty, genesisHeader.Number, genesisHeader.UncleHash)
	fmt.Printf("Block 1 difficulty: %d\n", d1)
}

// Searches 1000 blocks from the given one to try to find the one with the given state root hash
func testBlockHashes(chaindata string, block int, stateRoot common.Hash) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	blocksToSearch := 10000000
	for i := uint64(block); i < uint64(block+blocksToSearch); i++ {
		hash := rawdb.ReadCanonicalHash(ethDb, i)
		header := rawdb.ReadHeader(ethDb, hash, i)
		if header.Root == stateRoot || stateRoot == (common.Hash{}) {
			fmt.Printf("\n===============\nCanonical hash for %d: %x\n", i, hash)
			fmt.Printf("Header.Root: %x\n", header.Root)
			fmt.Printf("Header.TxHash: %x\n", header.TxHash)
			fmt.Printf("Header.UncleHash: %x\n", header.UncleHash)
		}
	}
}

func printCurrentBlockNumber(chaindata string) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	hash := rawdb.ReadHeadBlockHash(ethDb)
	number := rawdb.ReadHeaderNumber(ethDb, hash)
	fmt.Printf("Block number: %d\n", *number)
}

func printTxHashes() {
	ethDb := ethdb.MustOpen(node.DefaultDataDir() + "/geth/chaindata")
	defer ethDb.Close()
	for b := uint64(0); b < uint64(100000); b++ {
		hash := rawdb.ReadCanonicalHash(ethDb, b)
		block := rawdb.ReadBlock(ethDb, hash, b)
		if block == nil {
			break
		}
		for _, tx := range block.Transactions() {
			fmt.Printf("%x\n", tx.Hash())
		}
	}
}

func relayoutKeys() {
	//db := ethdb.MustOpen("/home/akhounov/.ethereum/geth/chaindata")
	db := ethdb.MustOpen(node.DefaultDataDir() + "/geth/chaindata")
	defer db.Close()
	var accountChangeSetCount, storageChangeSetCount int
	err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.AccountChangeSetBucket)
		for k, _, _ := c.First(); k != nil; k, _, _ = c.Next() {
			accountChangeSetCount++
		}
		return nil
	})
	check(err)
	err = db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.StorageChangeSetBucket)
		for k, _, _ := c.First(); k != nil; k, _, _ = c.Next() {
			storageChangeSetCount++
		}
		return nil
	})
	check(err)
	fmt.Printf("Account changeset: %d\n", accountChangeSetCount)
	fmt.Printf("Storage changeset: %d\n", storageChangeSetCount)
	fmt.Printf("Total: %d\n", accountChangeSetCount+storageChangeSetCount)
}

func readTrie(filename string) *trie.Trie {
	f, err := os.Open(filename)
	check(err)
	defer f.Close()
	t, err := trie.Load(f)
	check(err)
	return t
}

func invTree(wrong, right, diff string, name string) {
	fmt.Printf("Reading trie...\n")
	t1 := readTrie(fmt.Sprintf("%s_%s.txt", wrong, name))
	fmt.Printf("Root hash: %x\n", t1.Hash())
	fmt.Printf("Reading trie 2...\n")
	t2 := readTrie(fmt.Sprintf("%s_%s.txt", right, name))
	fmt.Printf("Root hash: %x\n", t2.Hash())
	c, err := os.Create(fmt.Sprintf("%s_%s.txt", diff, name))
	check(err)
	defer c.Close()
	t1.PrintDiff(t2, c)
}

func preimage(chaindata string, image common.Hash) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	p, err := ethDb.Get(dbutils.PreimagePrefix, image[:])
	check(err)
	fmt.Printf("%x\n", p)
}

func printBranches(block uint64) {
	//ethDb := ethdb.MustOpen("/home/akhounov/.ethereum/geth/chaindata")
	ethDb := ethdb.MustOpen(node.DefaultDataDir() + "/testnet/geth/chaindata")
	defer ethDb.Close()
	fmt.Printf("All headers at the same height %d\n", block)
	{
		var hashes []common.Hash
		numberEnc := make([]byte, 8)
		binary.BigEndian.PutUint64(numberEnc, block)
		if err := ethDb.Walk("h", numberEnc, 8*8, func(k, v []byte) (bool, error) {
			if len(k) == 8+32 {
				hashes = append(hashes, common.BytesToHash(k[8:]))
			}
			return true, nil
		}); err != nil {
			panic(err)
		}
		for _, hash := range hashes {
			h := rawdb.ReadHeader(ethDb, hash, block)
			fmt.Printf("block hash: %x, root hash: %x\n", h.Hash(), h.Root)
		}
	}
}

func readPlainAccount(chaindata string, address common.Address) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	var acc accounts.Account
	enc, err := ethDb.Get(dbutils.PlainStateBucket, address[:])
	if err != nil {
		panic(err)
	} else if enc == nil {
		panic("acc not found")
	}
	if err = acc.DecodeForStorage(enc); err != nil {
		panic(err)
	}
	fmt.Printf("%x\n%x\n%x\n%d\n", address, acc.Root, acc.CodeHash, acc.Incarnation)
}

func readAccount(chaindata string, account common.Address, block uint64, rewind uint64) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	secKey := crypto.Keccak256(account[:])
	var a accounts.Account
	ok, err := rawdb.ReadAccount(ethDb, common.BytesToHash(secKey), &a)
	if err != nil {
		panic(err)
	} else if !ok {
		panic("acc not found")
	}
	fmt.Printf("%x\n%x\n%x\n%d\n", secKey, a.Root, a.CodeHash, a.Incarnation)
	//var addrHash common.Hash
	//copy(addrHash[:], secKey)
	//codeHash, err := ethDb.Get(dbutils.ContractCodeBucket, dbutils.GenerateStoragePrefix(addrHash, a.Incarnation))
	//check(err)
	//fmt.Printf("codeHash: %x\n", codeHash)
	timestamp := block
	for i := uint64(0); i < rewind; i++ {
		var printed bool
		encodedTS := dbutils.EncodeTimestamp(timestamp)
		var v []byte
		v, _ = ethDb.Get(dbutils.StorageChangeSetBucket, encodedTS)
		if v != nil {
			err = changeset.StorageChangeSetBytes(v).Walk(func(key, value []byte) error {
				if bytes.HasPrefix(key, secKey) {
					incarnation := binary.BigEndian.Uint64(key[common.HashLength : common.HashLength+common.IncarnationLength])
					if !printed {
						fmt.Printf("Changes for block %d\n", timestamp)
						printed = true
					}
					fmt.Printf("%d %x %x\n", incarnation, key[common.HashLength+common.IncarnationLength:], value)
				}
				return nil
			})
			check(err)
		}
		timestamp--
	}
}

func fixAccount(chaindata string, addrHash common.Hash, storageRoot common.Hash) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	var a accounts.Account
	if ok, err := rawdb.ReadAccount(ethDb, addrHash, &a); err != nil {
		panic(err)
	} else if !ok {
		panic("acc not found")
	}
	a.Root = storageRoot
	if err := rawdb.WriteAccount(ethDb, addrHash, a); err != nil {
		panic(err)
	}
}

func nextIncarnation(chaindata string, addrHash common.Hash) {
	ethDb := ethdb.MustOpen(chaindata)
	defer ethDb.Close()
	var found bool
	var incarnationBytes [common.IncarnationLength]byte
	startkey := make([]byte, common.HashLength+common.IncarnationLength+common.HashLength)
	var fixedbits = 8 * common.HashLength
	copy(startkey, addrHash[:])
	if err := ethDb.Walk(dbutils.CurrentStateBucket, startkey, fixedbits, func(k, v []byte) (bool, error) {
		copy(incarnationBytes[:], k[common.HashLength:])
		found = true
		return false, nil
	}); err != nil {
		fmt.Printf("Incarnation(z): %d\n", 0)
		return
	}
	if found {
		fmt.Printf("Incarnation: %d\n", (binary.BigEndian.Uint64(incarnationBytes[:]))+1)
		return
	}
	fmt.Printf("Incarnation(f): %d\n", state.FirstContractIncarnation)
}

func repairCurrent() {
	historyDb := ethdb.MustOpen("/Volumes/tb4/turbo-geth/ropsten/geth/chaindata")
	defer historyDb.Close()
	currentDb := ethdb.MustOpen("statedb")
	defer currentDb.Close()
	check(historyDb.ClearBuckets(dbutils.CurrentStateBucket))
	check(historyDb.KV().Update(context.Background(), func(tx ethdb.Tx) error {
		newB := tx.Cursor(dbutils.CurrentStateBucket)
		count := 0
		if err := currentDb.KV().View(context.Background(), func(ctx ethdb.Tx) error {
			c := ctx.Cursor(dbutils.CurrentStateBucket)
			for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
				if err != nil {
					return err
				}
				if len(k) == 32 {
					continue
				}
				check(newB.Put(k, v))
				count++
				if count == 10000 {
					fmt.Printf("Copied %d storage items\n", count)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		return nil
	}))
}

func dumpStorage() {
	db := ethdb.MustOpen(node.DefaultDataDir() + "/geth/chaindata")
	defer db.Close()
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.StorageHistoryBucket)
		return ethdb.ForEach(c, func(k, v []byte) (bool, error) {
			fmt.Printf("%x %x\n", k, v)
			return true, nil
		})
	}); err != nil {
		panic(err)
	}
}

func printBucket(chaindata string) {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	f, err := os.Create("bucket.txt")
	check(err)
	defer f.Close()
	fb := bufio.NewWriter(f)
	defer fb.Flush()
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.StorageHistoryBucket)
		for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			fmt.Fprintf(fb, "%x %x\n", k, v)
		}
		return nil
	}); err != nil {
		panic(err)
	}
}

func ValidateTxLookups2(chaindata string) {
	startTime := time.Now()
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	//nolint: errcheck
	startTime = time.Now()
	sigs := make(chan os.Signal, 1)
	interruptCh := make(chan bool, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		interruptCh <- true
	}()
	var blockNum uint64 = 1
	validateTxLookups2(db, blockNum, interruptCh)
	log.Info("All done", "duration", time.Since(startTime))
}

func validateTxLookups2(db rawdb.DatabaseReader, startBlock uint64, interruptCh chan bool) {
	blockNum := startBlock
	iterations := 0
	var interrupt bool
	// Validation Process
	blockBytes := big.NewInt(0)
	for !interrupt {
		blockHash := rawdb.ReadCanonicalHash(db, blockNum)
		body := rawdb.ReadBody(db, blockHash, blockNum)

		if body == nil {
			break
		}

		select {
		case interrupt = <-interruptCh:
			log.Info("interrupted, please wait for cleanup...")
		default:
		}
		blockBytes.SetUint64(blockNum)
		bn := blockBytes.Bytes()

		for _, tx := range body.Transactions {
			val, err := db.Get(dbutils.TxLookupPrefix, tx.Hash().Bytes())
			iterations++
			if iterations%100000 == 0 {
				log.Info("Validated", "entries", iterations, "number", blockNum)
			}
			if bytes.Compare(val, bn) != 0 {
				check(err)
				panic(fmt.Sprintf("Validation process failed(%d). Expected %b, got %b", iterations, bn, val))
			}
		}
		blockNum++
	}
}

func getModifiedAccounts(chaindata string) {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	addrs, err := ethdb.GetModifiedAccounts(db, 49300, 49400)
	check(err)
	fmt.Printf("Len(addrs)=%d\n", len(addrs))
}

type Receiver struct {
	defaultReceiver *trie.DefaultReceiver
	accountMap      map[string]*accounts.Account
	storageMap      map[string][]byte
	unfurlList      []string
	currentIdx      int
}

func (r *Receiver) Root() common.Hash { panic("don't call me") }
func (r *Receiver) Receive(
	itemType trie.StreamItem,
	accountKey []byte,
	storageKey []byte,
	accountValue *accounts.Account,
	storageValue []byte,
	hash []byte,
	cutoff int,
) error {
	for r.currentIdx < len(r.unfurlList) {
		ks := r.unfurlList[r.currentIdx]
		k := []byte(ks)
		var c int
		switch itemType {
		case trie.StorageStreamItem, trie.SHashStreamItem:
			c = bytes.Compare(k, storageKey)
		case trie.AccountStreamItem, trie.AHashStreamItem:
			c = bytes.Compare(k, accountKey)
		case trie.CutoffStreamItem:
			c = -1
		}
		if c > 0 {
			return r.defaultReceiver.Receive(itemType, accountKey, storageKey, accountValue, storageValue, hash, cutoff)
		}
		if len(k) > common.HashLength {
			v := r.storageMap[ks]
			if len(v) > 0 {
				if err := r.defaultReceiver.Receive(trie.StorageStreamItem, nil, k, nil, v, nil, 0); err != nil {
					return err
				}
			}
		} else {
			v := r.accountMap[ks]
			if v != nil {
				if err := r.defaultReceiver.Receive(trie.AccountStreamItem, k, nil, v, nil, nil, 0); err != nil {
					return err
				}
			}
		}
		r.currentIdx++
		if c == 0 {
			return nil
		}
	}
	// We ran out of modifications, simply pass through
	return r.defaultReceiver.Receive(itemType, accountKey, storageKey, accountValue, storageValue, hash, cutoff)
}

func (r *Receiver) Result() trie.SubTries {
	return r.defaultReceiver.Result()
}

func regenerate(chaindata string) error {
	var m runtime.MemStats
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	check(db.ClearBuckets(
		dbutils.IntermediateTrieHashBucket,
	))
	headHash := rawdb.ReadHeadBlockHash(db)
	headNumber := rawdb.ReadHeaderNumber(db, headHash)
	headHeader := rawdb.ReadHeader(db, headHash, *headNumber)
	log.Info("Regeneration started")
	collector := etl.NewCollector(".", etl.NewSortableBuffer(etl.BufferOptimalSize))
	hashCollector := func(keyHex []byte, hash []byte) error {
		if len(keyHex)%2 != 0 || len(keyHex) == 0 {
			return nil
		}
		var k []byte
		trie.CompressNibbles(keyHex, &k)
		if hash == nil {
			return collector.Collect(k, nil)
		}
		return collector.Collect(k, common.CopyBytes(hash))
	}
	loader := trie.NewFlatDbSubTrieLoader()
	if err := loader.Reset(db, trie.NewRetainList(0), trie.NewRetainList(0), hashCollector /* HashCollector */, [][]byte{nil}, []int{0}, false); err != nil {
		return err
	}
	if subTries, err := loader.LoadSubTries(); err == nil {
		runtime.ReadMemStats(&m)
		log.Info("Loaded initial trie", "root", fmt.Sprintf("%x", subTries.Hashes[0]),
			"expected root", fmt.Sprintf("%x", headHeader.Root),
			"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))
	} else {
		return err
	}
	/*
		quitCh := make(chan struct{})
		if err := collector.Load(db, dbutils.IntermediateTrieHashBucket, etl.IdentityLoadFunc, etl.TransformArgs{Quit: quitCh}); err != nil {
			return err
		}
	*/
	log.Info("Regeneration ended")
	return nil
}

func testGetProof(chaindata string, address common.Address, rewind int, regen bool) error {
	if regen {
		if err := regenerate(chaindata); err != nil {
			return err
		}
	}
	storageKeys := []string{}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	headHash := rawdb.ReadHeadBlockHash(db)
	headNumber := rawdb.ReadHeaderNumber(db, headHash)
	block := *headNumber - uint64(rewind)
	log.Info("GetProof", "address", address, "storage keys", len(storageKeys), "head", *headNumber, "block", block,
		"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))

	ts := dbutils.EncodeTimestamp(block + 1)
	accountMap := make(map[string]*accounts.Account)
	if err := db.Walk(dbutils.AccountChangeSetBucket, ts, 0, func(k, v []byte) (bool, error) {
		timestamp, _ := dbutils.DecodeTimestamp(k)
		if timestamp > *headNumber {
			return false, nil
		}
		if changeset.Len(v) > 0 {
			walker := func(kk, vv []byte) error {
				if _, ok := accountMap[string(kk)]; !ok {
					if len(vv) > 0 {
						var a accounts.Account
						if innerErr := a.DecodeForStorage(vv); innerErr != nil {
							return innerErr
						}
						accountMap[string(kk)] = &a
					} else {
						accountMap[string(kk)] = nil
					}
				}
				return nil
			}
			v = common.CopyBytes(v) // Making copy because otherwise it will be invalid after the transaction
			if innerErr := changeset.AccountChangeSetBytes(v).Walk(walker); innerErr != nil {
				return false, innerErr
			}
		}
		return true, nil
	}); err != nil {
		return err
	}
	runtime.ReadMemStats(&m)
	log.Info("Constructed account map", "size", len(accountMap),
		"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))
	storageMap := make(map[string][]byte)
	if err := db.Walk(dbutils.StorageChangeSetBucket, ts, 0, func(k, v []byte) (bool, error) {
		timestamp, _ := dbutils.DecodeTimestamp(k)
		if timestamp > *headNumber {
			return false, nil
		}
		if changeset.Len(v) > 0 {
			walker := func(kk, vv []byte) error {
				if _, ok := storageMap[string(kk)]; !ok {
					storageMap[string(kk)] = vv
				}
				return nil
			}
			v = common.CopyBytes(v) // Making copy because otherwise it will be invalid after the transaction
			if innerErr := changeset.StorageChangeSetBytes(v).Walk(walker); innerErr != nil {
				return false, innerErr
			}
		}
		return true, nil
	}); err != nil {
		return err
	}
	runtime.ReadMemStats(&m)
	log.Info("Constructed storage map", "size", len(storageMap),
		"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))
	var unfurlList = make([]string, len(accountMap)+len(storageMap))
	unfurl := trie.NewRetainList(0)
	i := 0
	for ks, acc := range accountMap {
		unfurlList[i] = ks
		i++
		unfurl.AddKey([]byte(ks))
		if acc != nil {
			// Fill the code hashes
			if acc.Incarnation > 0 && acc.IsEmptyCodeHash() {
				if codeHash, err1 := db.Get(dbutils.ContractCodeBucket, dbutils.GenerateStoragePrefix([]byte(ks), acc.Incarnation)); err1 == nil {
					copy(acc.CodeHash[:], codeHash)
				} else {
					return err1
				}
			}
		}
	}
	for ks := range storageMap {
		unfurlList[i] = ks
		i++
		unfurl.AddKey([]byte(ks))
	}
	rl := trie.NewRetainList(0)
	addrHash, err := common.HashData(address[:])
	if err != nil {
		return err
	}
	rl.AddKey(addrHash[:])
	unfurl.AddKey(addrHash[:])
	for _, key := range storageKeys {
		keyAsHash := common.HexToHash(key)
		if keyHash, err1 := common.HashData(keyAsHash[:]); err1 == nil {
			//TODO Add incarnation in the middle of this
			trieKey := append(addrHash[:], keyHash[:]...)
			rl.AddKey(trieKey)
			unfurl.AddKey(trieKey)
		} else {
			return err1
		}
	}
	sort.Strings(unfurlList)
	runtime.ReadMemStats(&m)
	log.Info("Constructed account unfurl lists",
		"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))
	loader := trie.NewFlatDbSubTrieLoader()
	if err = loader.Reset(db, unfurl, trie.NewRetainList(0), nil /* HashCollector */, [][]byte{nil}, []int{0}, false); err != nil {
		return err
	}
	r := &Receiver{defaultReceiver: trie.NewDefaultReceiver(), unfurlList: unfurlList, accountMap: accountMap, storageMap: storageMap}
	r.defaultReceiver.Reset(rl, nil /* HashCollector */, false)
	loader.SetStreamReceiver(r)
	subTries, err1 := loader.LoadSubTries()
	if err1 != nil {
		return err1
	}
	runtime.ReadMemStats(&m)
	log.Info("Loaded subtries",
		"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))
	hash := rawdb.ReadCanonicalHash(db, block)
	header := rawdb.ReadHeader(db, hash, block)
	tr := trie.New(common.Hash{})
	if err = tr.HookSubTries(subTries, [][]byte{nil}); err != nil {
		fmt.Printf("Error hooking: %v\n", err)
	}
	runtime.ReadMemStats(&m)
	log.Info("Constructed trie", "nodes", tr.NumberOfAccounts(),
		"alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys), "numGC", int(m.NumGC))
	fmt.Printf("Resulting root: %x (subTrie %x), expected root: %x\n", tr.Hash(), subTries.Hashes[0], header.Root)
	return nil
}

func changeSetStats(chaindata string, block1, block2 uint64) error {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	fmt.Printf("State stats\n")
	stAccounts := 0
	stStorage := 0
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainStateBucket)
		k, _, e := c.First()
		for ; k != nil && e == nil; k, _, e = c.Next() {
			if len(k) > 28 {
				stStorage++
			} else {
				stAccounts++
			}
			if (stStorage+stAccounts)%100000 == 0 {
				fmt.Printf("State records: %d\n", stStorage+stAccounts)
			}
		}
		return e
	}); err != nil {
		return err
	}
	fmt.Printf("stAccounts = %d, stStorage = %d\n", stAccounts, stStorage)
	fmt.Printf("Changeset stats from %d to %d\n", block1, block2)
	accounts := make(map[string]struct{})
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainAccountChangeSetBucket)
		start := dbutils.EncodeTimestamp(block1)
		for k, v, err := c.Seek(start); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			timestamp, _ := dbutils.DecodeTimestamp(k)
			if timestamp >= block2 {
				break
			}
			if (timestamp-block1)%100000 == 0 {
				fmt.Printf("at the block %d for accounts, booster size: %d\n", timestamp, len(accounts))
			}
			if err1 := changeset.AccountChangeSetPlainBytes(v).Walk(func(kk, _ []byte) error {
				accounts[string(common.CopyBytes(kk))] = struct{}{}
				return nil
			}); err1 != nil {
				return err1
			}
		}
		return nil
	}); err != nil {
		return err
	}
	storage := make(map[string]struct{})
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainStorageChangeSetBucket)
		start := dbutils.EncodeTimestamp(block1)
		for k, v, err := c.Seek(start); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			timestamp, _ := dbutils.DecodeTimestamp(k)
			if timestamp >= block2 {
				break
			}
			if (timestamp-block1)%100000 == 0 {
				fmt.Printf("at the block %d for storage, booster size: %d\n", timestamp, len(storage))
			}
			if err1 := changeset.StorageChangeSetPlainBytes(v).Walk(func(kk, _ []byte) error {
				storage[string(common.CopyBytes(kk))] = struct{}{}
				return nil
			}); err1 != nil {
				return err1
			}
		}
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("accounts changed: %d, storage changed: %d\n", len(accounts), len(storage))
	return nil
}

func searchChangeSet(chaindata string, key []byte, block uint64) error {
	fmt.Printf("Searching changesets\n")
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainAccountChangeSetBucket)
		start := dbutils.EncodeTimestamp(block)
		for k, v, err := c.Seek(start); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			timestamp, _ := dbutils.DecodeTimestamp(k)
			//fmt.Printf("timestamp: %d\n", timestamp)
			if err1 := changeset.AccountChangeSetPlainBytes(v).Walk(func(kk, vv []byte) error {
				if bytes.Equal(kk, key) {
					fmt.Printf("Found in block %d with value %x\n", timestamp, vv)
				}
				return nil
			}); err1 != nil {
				return err1
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func searchStorageChangeSet(chaindata string, key []byte, block uint64) error {
	fmt.Printf("Searching storage changesets\n")
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainStorageChangeSetBucket)
		start := dbutils.EncodeTimestamp(block)
		for k, v, err := c.Seek(start); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			timestamp, _ := dbutils.DecodeTimestamp(k)
			if err1 := changeset.StorageChangeSetPlainBytes(v).Walk(func(kk, vv []byte) error {
				if bytes.Equal(kk, key) {
					fmt.Printf("Found in block %d with value %x\n", timestamp, vv)
				}
				return nil
			}); err1 != nil {
				return err1
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func supply(chaindata string) error {
	startTime := time.Now()
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	count := 0
	supply := uint256.NewInt()
	var a accounts.Account
	if err := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainStateBucket)
		for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			if len(k) != 20 {
				continue
			}
			if err1 := a.DecodeForStorage(v); err1 != nil {
				return err1
			}
			count++
			supply.Add(supply, &a.Balance)
			if count%100000 == 0 {
				fmt.Printf("Processed %dK account records\n", count/1000)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("Total accounts: %d, supply: %d, took: %s\n", count, supply, time.Since(startTime))
	return nil
}

func extractCode(chaindata string) error {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	destDb := ethdb.MustOpen("codes")
	defer destDb.Close()
	return destDb.KV().Update(context.Background(), func(tx1 ethdb.Tx) error {
		c1 := tx1.Cursor(dbutils.PlainContractCodeBucket)
		return db.KV().View(context.Background(), func(tx ethdb.Tx) error {
			c := tx.Cursor(dbutils.PlainContractCodeBucket)
			for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
				if err != nil {
					return err
				}
				if err = c1.Append(k, v); err != nil {
					return err
				}
			}
			c1 = tx1.Cursor(dbutils.CodeBucket)
			c = tx.Cursor(dbutils.CodeBucket)
			for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
				if err != nil {
					return err
				}
				if err = c1.Append(k, v); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

func logIndex(chaindata string) error {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	datadir := ""
	tx, err := db.Begin(context.Background())
	check(err)
	defer tx.Rollback()

	//var NoTopic = common.HexToHash("0000000000000000000000000000000000000000000000000000000000000000")
	//var NoTopics = []common.Hash{NoTopic}
	blockNumBytes := make([]byte, 4)
	txIndex := make([]byte, 4)
	logIndex := make([]byte, 4)

	logEvery := time.NewTicker(30 * time.Second)
	defer logEvery.Stop()

	//total := 0
	//max := 0
	//count := 0
	//if err := tx.Walk(dbutils.Topics5, nil, 0, func(k, v []byte) (bool, error) {
	//	count++
	//	total += len(v)
	//	if max < len(v) {
	//		max = len(v)
	//	}
	//	select {
	//	default:
	//	case <-logEvery.C:
	//		fmt.Printf("avg: %.2f\n", float64(total)/float64(count))
	//		fmt.Printf("Max: %.2f\n", float64(max))
	//	}
	//	return true, nil
	//}); err != nil {
	//	panic(err)
	//}
	//fmt.Printf("avg: %.2f\n", float64(total)/float64(count))
	//fmt.Printf("Max: %.2f\n", float64(max))
	//return nil

	var buf bytes.Buffer
	encoder := codecpool.Encoder(&buf)
	defer codecpool.Return(encoder)

	receipts := make(types.Receipts, 0, 256)
	logs := make([][][]byte, 0, 256)

	topicsBitmap := map[string][]uint32{}  // topic ->
	topicsBitmap3 := map[string][]uint32{} // addr ->

	topicsBitmap4 := map[string][]uint64{} // topic ->
	topicsBitmap5 := map[string][]uint64{} // addr ->

	lowSelectivityTopics := map[common.Hash]bool{
		//common.HexToHash("0000000000000000000000000000000000000000000000000000000000000000"):  true,
		//common.HexToHash("ea0f544916910bb1ff33390cbe54a3f5d36d298328578399311cde3c9a750686"):  true,
		//common.HexToHash("009f837f1feddc3de305fab200310a83d2871686078dab617c02b44360c9e236"):  true,
		//common.HexToHash("00000000000000000000000048175da4c20313bcb6b62d74937d3ff985885701"):  true,
		//common.HexToHash("ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"):  true,
		//common.HexToHash("0000000000000000000000000000000000000000000000000000000000000058"):  true,
		//common.HexToHash("90890809c654f11d6e72a28fa60149770a0d11ec6c92319d6ceb2bb0a4ea1a15"):  true,
		//common.HexToHash("cc494284735b76f0235b8a507abc67ce930b369dac12b8a45e49510ccee0abe5"):  true,
		//common.HexToHash("f10cb5dcb691bb26c2685b3fd72f4ca4008c33eafd1ee88c27210ef1db722459"):  true,
		//common.HexToHash("e1fffcc4923d04b559f4d29a8bfc6cda04eb5b0d3c460751c2402c5c5cc9109c"):  true,
		//common.HexToHash("5b59a4139d8c317549b49f57962d4733012f0e76915ab0828c22548892d71782"):  true,
		//common.HexToHash("0000000000000000000000007fb3b877f2d85d92d4172764ea5cd68982cbe53e"):  true,
		//common.HexToHash("000000000000000000000000490c0dd13bfea5865ca985297cf2bed3f77beb5d"):  true,
		//common.HexToHash("000000000000000000000000b3089884fa970922e6c099e818a8164bd0d402d2"):  true,
		//common.HexToHash("0000000000000000000000002a65aca4d5fc5b5c859090a6c34d164135398226"):  true,
		//common.HexToHash("000000000000000000000000b5606469f317018d21f504b6e1518e54b23fa761"):  true,
		//common.HexToHash("000000000000000000000000939292f2b41b74ccb7261a452de556ba2c45db86"):  true,
		//common.HexToHash("0000000000000000000000009c4ea8d25d6150a8ed2848fc745158aad926bf8d"):  true,
		//common.HexToHash("dbccb92686efceafb9bb7e0394df7f58f71b954061b81afb57109bf247d3d75a"):  true,
		//common.HexToHash("95c567a11896e793a41e067198ab5c4a4bdc7b3cf1182571fe911ec7e1426853"):  true,
		//common.HexToHash("23919512b2162ddc59b67a65e3b03c419d4105366f7d4a632f5d3c3bee9b1cff"):  true,
	}

	//check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.BlockReceiptsPrefix2))
	//check(tx.CommitAndBegin(context.Background()))
	//check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.Logs, dbutils.Logs2))
	//check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.Topics))
	//check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.Topics, dbutils.Topics3))
	//check(tx.CommitAndBegin(context.Background()))
	//check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.ReceiptsIndex, dbutils.ReceiptsIndex2, dbutils.ReceiptsIndex3, dbutils.ReceiptsIndex4, dbutils.ReceiptsIndex5))
	//check(tx.CommitAndBegin(context.Background()))

	topicsCursor := tx.(ethdb.HasTx).Tx().Cursor(dbutils.Topics)
	topicsCursor3 := tx.(ethdb.HasTx).Tx().Cursor(dbutils.Topics3)
	topicsCursor4 := tx.(ethdb.HasTx).Tx().Cursor(dbutils.Topics4)
	topicsCursor5 := tx.(ethdb.HasTx).Tx().Cursor(dbutils.Topics5)

	check(tx.Walk(dbutils.BlockReceiptsPrefix, nil, 0, func(k, v []byte) (bool, error) {
		blockHashBytes := k[len(k)-32:]
		blockNum64Bytes := k[:len(k)-32]
		//blockHash := common.BytesToHash(blockHashBytes)
		blockNum := binary.BigEndian.Uint64(blockNum64Bytes)
		canonicalHash := rawdb.ReadCanonicalHash(tx, blockNum)
		if !bytes.Equal(blockHashBytes, canonicalHash[:]) {
			return true, nil
		}

		select {
		default:
		case <-logEvery.C:
			log.Info("progress1", "blockNum", blockNum)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.BlockReceiptsPrefix2)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Logs)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Logs2)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Logs3)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.ReceiptsIndex)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.ReceiptsIndex2)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.ReceiptsIndex3)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.ReceiptsIndex4)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.ReceiptsIndex5)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Topics)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Topics2)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Topics3)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Topics4)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.Topics5)
		}

		binary.BigEndian.PutUint32(blockNumBytes, uint32(blockNum))

		// Convert the receipts from their storage form to their internal representation
		storageReceipts := []*types.ReceiptForStorage{}
		if err := rlp.DecodeBytes(v, &storageReceipts); err != nil {
			return false, fmt.Errorf("invalid receipt array RLP: %w, hash=%x", err, hash)
		}

		receipts = receipts[:0]
		for _, storageReceipt := range storageReceipts {
			receipts = append(receipts, (*types.Receipt)(storageReceipt))
		}

		// Index Index2 Index4
		logIdx := uint32(0) // logIdx - indexed IN THE BLOCK and starting from 0.
		for txIdx, storageReceipt := range receipts {
			binary.BigEndian.PutUint32(txIndex, uint32(txIdx))
			for _, log := range storageReceipt.Logs {
				binary.BigEndian.PutUint32(logIndex, logIdx)

				var topicsToStore = make([]byte, 0, 32*len(log.Topics))
				for _, topic := range log.Topics {
					t := topic[:]
					topicsToStore = append(topicsToStore, t...)
					if _, ok := lowSelectivityTopics[topic]; !ok {
						if m, ok := topicsBitmap[string(topic.Bytes())]; ok {
							topicsBitmap[string(topic.Bytes())] = append(m, uint32(blockNum))
						} else {
							topicsBitmap[string(topic.Bytes())] = make([]uint32, 0, 1024)
						}
					}

					if _, ok := lowSelectivityTopics[topic]; !ok {
						if m, ok := topicsBitmap4[string(topic.Bytes())]; ok {
							topicsBitmap4[string(topic.Bytes())] = append(m, uint64(logIdx)<<32|blockNum)
						} else {
							topicsBitmap4[string(topic.Bytes())] = make([]uint64, 0, 1024)
						}
					}

					//if _, ok := lowSelectivityTopics[topic]; !ok {
					//	if m, ok := topicsBitmap2[log.Address]; !ok {
					//		topicsBitmap2[log.Address] = map[common.Hash]*roaring.Bitmap{}
					//	} else {
					//		if mm, ok := m[topic]; !ok {
					//			m[topic] = roaring.New()
					//		} else {
					//			mm.Add(uint32(blockNum))
					//		}
					//	}
					//}

					if m, ok := topicsBitmap5[string(log.Address.Bytes())]; ok {
						topicsBitmap5[string(topic.Bytes())] = append(m, uint64(logIdx)<<32|blockNum)
					} else {
						topicsBitmap5[string(log.Address.Bytes())] = make([]uint64, 0, 1024)
					}
				}

				if m, ok := topicsBitmap3[string(log.Address.Bytes())]; !ok {
					topicsBitmap3[string(log.Address.Bytes())] = make([]uint32, 0, 1024)
				} else {
					topicsBitmap3[string(log.Address.Bytes())] = append(m, uint32(blockNum))
				}

				//{ // dbutils.Logs
				//	newK := append(common.CopyBytes(blockNumBytes), logIndex...)
				//	leadingZeros := uint8(0)
				//	for i := 0; i < len(log.Data); i++ {
				//		if log.Data[i] != 0 || leadingZeros == 255 {
				//			break
				//		}
				//		leadingZeros++
				//	}
				//	var logData []byte
				//	if leadingZeros > 0 {
				//		logData = common.CopyBytes(log.Data)
				//		logData[leadingZeros-1] = leadingZeros
				//		logData = logData[leadingZeros-1:]
				//	} else {
				//		logData = append([]byte{0}, log.Data...)
				//	}
				//
				//	if err := tx.Put(dbutils.Logs, newK, logData); err != nil {
				//		return false, err
				//	}
				//}

				//{ // dbutils.Logs3
				//	newK := append(common.CopyBytes(blockNumBytes), logIndex...)
				//
				//	leadingZeros := uint8(0)
				//	for i := 0; i < len(log.Data); i++ {
				//		if log.Data[i] != 0 || leadingZeros == 255 {
				//			break
				//		}
				//		leadingZeros++
				//	}
				//	var logData []byte
				//	if leadingZeros > 0 {
				//		logData = common.CopyBytes(log.Data)
				//		logData[leadingZeros-1] = leadingZeros
				//		logData = logData[leadingZeros-1:]
				//	} else {
				//		logData = append([]byte{0}, log.Data...)
				//	}
				//
				//	if err := tx.Put(dbutils.Logs3, newK, logData); err != nil {
				//		return false, err
				//	}
				//}

				////dbutils.ReceiptsIndex
				//newK := common.CopyBytes(log.Address[:])
				//
				//newV := make([]byte, 0, 4+4+4+len(topicsToStore))
				//newV = append(newV, blockNumBytes...)
				//newV = append(newV, txIndex...)
				//newV = append(newV, logIndex...)
				//newV = append(newV, topicsToStore...)
				//if err := tx.Put(dbutils.ReceiptsIndex, newK, newV); err != nil {
				//	return false, err
				//}
				//
				////dbutils.ReceiptsIndex2
				//newK2 := common.CopyBytes(blockNumBytes)
				//
				//newV2 := make([]byte, 0, 20+4+4+len(topicsToStore))
				//newV2 = append(newV2, log.Address[:]...)
				//newV2 = append(newV2, txIndex...)
				//newV2 = append(newV2, logIndex...)
				//newV2 = append(newV2, topicsToStore...)
				//if err := tx.Put(dbutils.ReceiptsIndex2, newK2, newV2); err != nil {
				//	return false, err
				//}

				{
					// dbutils.ReceiptsIndex3
					newK2 := common.CopyBytes(blockNumBytes)

					newV2 := make([]byte, 0, 4+4+20+len(topicsToStore))
					newV2 = append(newV2, logIndex...)
					newV2 = append(newV2, log.Address[:]...)
					newV2 = append(newV2, topicsToStore...)
					if err := tx.Put(dbutils.ReceiptsIndex3, newK2, newV2); err != nil {
						return false, err
					}
				}

				{
					// dbutils.ReceiptsIndex4
					newK2 := common.CopyBytes(logIndex)

					newV2 := make([]byte, 0, 4+20+len(topicsToStore))
					newV2 = append(newV2, blockNumBytes...)
					newV2 = append(newV2, log.Address[:]...)
					newV2 = append(newV2, topicsToStore...)
					if err := tx.Put(dbutils.ReceiptsIndex4, newK2, newV2); err != nil {
						return false, err
					}
				}

				logIdx++
			}
		}

		// dbutils.BlockReceiptsPrefix2
		//for i := range storageReceipts {
		//	storageReceipts[i].Logs = nil
		//}
		//
		//var bytes []byte
		//if bytes, err = rlp.EncodeToBytes(storageReceipts); err != nil {
		//	return false, fmt.Errorf("encode block receipts for block %w", err)
		//}
		//
		//if err := tx.Put(dbutils.BlockReceiptsPrefix2, common.CopyBytes(k[4:8]), bytes); err != nil {
		//	return false, err
		//}

		if len(topicsBitmap) > 1_000_000 {
			flushBitmaps(topicsCursor, topicsBitmap)
			topicsBitmap = map[string][]uint32{}
		}

		if len(topicsBitmap3) > 1_000_000 {
			flushBitmaps(topicsCursor3, topicsBitmap3)
			topicsBitmap3 = map[string][]uint32{}
		}

		if len(topicsBitmap4) > 1_000_000 {
			flushBitmaps64(topicsCursor4, topicsBitmap4)
			topicsBitmap4 = map[string][]uint64{}
		}

		if len(topicsBitmap5) > 1_000_000 {
			flushBitmaps64(topicsCursor5, topicsBitmap5)
			topicsBitmap5 = map[string][]uint64{}
		}

		return true, nil
	}))

	flushBitmaps(topicsCursor, topicsBitmap)
	topicsBitmap = map[string][]uint32{}

	flushBitmaps(topicsCursor3, topicsBitmap3)
	topicsBitmap3 = map[string][]uint32{}

	flushBitmaps64(topicsCursor4, topicsBitmap4)
	topicsBitmap4 = map[string][]uint64{}

	flushBitmaps64(topicsCursor5, topicsBitmap5)
	topicsBitmap5 = map[string][]uint64{}

	check(tx.CommitAndBegin(context.Background()))

	check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.TxHash))
	check(tx.CommitAndBegin(context.Background()))

	check(tx.Walk(dbutils.BlockBodyPrefix, nil, 0, func(k, v []byte) (bool, error) {
		blockHashBytes := k[len(k)-32:]
		blockNum64Bytes := k[:len(k)-32]
		//blockHash := common.BytesToHash(blockHashBytes)
		blockNum := binary.BigEndian.Uint64(blockNum64Bytes)
		binary.BigEndian.PutUint32(blockNumBytes, uint32(blockNum))
		canonicalHash := rawdb.ReadCanonicalHash(tx, blockNum)
		if !bytes.Equal(blockHashBytes, canonicalHash[:]) {
			return true, nil
		}

		select {
		default:
		case <-logEvery.C:
			log.Info("progress", "blockNum", blockNum)
			printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.TxHash)
		}

		bodyRlp, err := rawdb.DecompressBlockBody(v)
		if err != nil {
			return false, err
		}
		body := new(types.Body)
		if err := rlp.Decode(bytes.NewReader(bodyRlp), body); err != nil {
			return false, fmt.Errorf("invalid receipt array RLP: %w, hash=%x", err, hash)
		}

		for txIdx, txn := range body.Transactions {
			txHash := txn.Hash()

			binary.BigEndian.PutUint32(txIndex, uint32(txIdx))
			if err := tx.Put(dbutils.TxHash, common.CopyBytes(blockNumBytes), append(common.CopyBytes(txIndex), txHash[:]...)); err != nil {
				return false, err
			}
		}

		return true, nil
	}))

	check(tx.CommitAndBegin(context.Background()))

	//check(tx.(ethdb.BucketsMigrator).ClearBuckets(dbutils.Senders2))
	//check(tx.CommitAndBegin(context.Background()))
	//
	//senders2Cursor := tx.(ethdb.HasTx).Tx().CursorDupFixed(dbutils.Senders2)
	//
	//check(tx.Walk(dbutils.Senders, nil, 0, func(k, v []byte) (bool, error) {
	//	blockNum64Bytes := k[:len(k)-32]
	//	//blockHash := common.BytesToHash(blockHashBytes)
	//	blockNum := binary.BigEndian.Uint64(blockNum64Bytes)
	//
	//	count := len(v) / common.AddressLength
	//	const stride = common.AddressLength + 4
	//	page := make([]byte, stride*count)
	//
	//	for i := 0; i < count; i++ {
	//		i0 := i * stride
	//		j0 := (i) * common.AddressLength
	//		j1 := (i + 1) * common.AddressLength
	//
	//		binary.BigEndian.PutUint32(page[i0:], uint32(i))
	//		copy(page[i0+4:], v[j0:j1])
	//	}
	//
	//	if err := senders2Cursor.PutMulti(k, page, stride); err != nil {
	//		return false, err
	//	}
	//
	//	select {
	//	default:
	//	case <-logEvery.C:
	//		log.Info("progress", "blockNum", blockNum)
	//		printBucketSize(tx.(ethdb.HasTx).Tx(), dbutils.TxHash)
	//	}
	//
	//	return true, nil
	//}))

	check(tx.CommitAndBegin(context.Background()))

	_ = logIndex
	_ = buf
	_ = receipts
	_ = logs
	_ = datadir
	_ = topicsBitmap
	_ = topicsBitmap3
	_ = topicsBitmap4
	_ = topicsBitmap5
	_ = lowSelectivityTopics
	_ = blockNumBytes

	return nil
}

func flushBitmaps64(c ethdb.Cursor, inMem map[string][]uint64) {
	defer func(t time.Time) { fmt.Printf("flushBitmaps64: %s\n", time.Since(t)) }(time.Now())
	for k, b := range inMem {
		v, err := c.SeekExact([]byte(k))
		if err != nil {
			panic(err)
		}

		var appended = roaring64.BitmapOf(b...)
		if len(v) > 0 {
			exisintg := roaring64.New()
			_, err = exisintg.ReadFrom(bytes.NewReader(v))
			if err != nil {
				panic(err)
			}

			appended.Or(exisintg)
		}

		bufBytes := make([]byte, 0, appended.GetSizeInBytes())

		buf := bytes.NewBuffer(bufBytes[:0])
		_, err = appended.WriteTo(buf)
		if err != nil {
			panic(err)
		}
		err = c.Put([]byte(k), buf.Bytes())
		if err != nil {
			panic(err)
		}
		//bufBytes, err := c.Reserve([]byte(k), int(b.GetSizeInBytes()))
		//if err != nil {
		//	panic(err)
		//}
		//
		//buf := bytes.NewBuffer(bufBytes[:0])
		//_, err = b.WriteTo(buf)
		//if err != nil {
		//	panic(err)
		//}
	}
}

func flushBitmaps(c ethdb.Cursor, inMem map[string][]uint32) {
	defer func(t time.Time) { fmt.Printf("flushBitmaps: %s\n", time.Since(t)) }(time.Now())
	for k, b := range inMem {
		v, err := c.SeekExact([]byte(k))
		if err != nil {
			panic(err)
		}

		var appended = roaring.BitmapOf(b...)
		if len(v) > 0 {
			exisintg := roaring.New()
			_, err = exisintg.ReadFrom(bytes.NewReader(v))
			if err != nil {
				panic(err)
			}

			appended.Or(exisintg)
		}

		bufBytes := make([]byte, 0, appended.GetSizeInBytes())

		buf := bytes.NewBuffer(bufBytes[:0])
		_, err = appended.WriteTo(buf)
		if err != nil {
			panic(err)
		}
		err = c.Put([]byte(k), buf.Bytes())
		if err != nil {
			panic(err)
		}
	}
}

func printBucketSize(tx ethdb.Tx, bucket string) {
	sz, _ := tx.BucketSize(bucket)
	log.Info("size", bucket, common.StorageSize(sz))
}

func logIndexBitmap(chaindata string) error {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	tx, err := db.Begin(context.Background())
	check(err)
	defer tx.Rollback()

	c := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex)

	defer func(t time.Time) { fmt.Printf("hack.go:1802: %s\n", time.Since(t)) }(time.Now())
	x := common.FromHex("6090a6e47849629b7245dfa1ca21d94cd15878ef")
	for k, _, err := c.SeekBothRange(x, common.FromHex("00000000003cf04c")); k != nil; k, _, err = c.Next() {
		check(err)
		if !bytes.Equal(x, k) {
			break
		}
	}

	return nil
}

func iterateOverCode(chaindata string) error {
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	var contractKeyTotalLength int
	var contractValTotalLength int
	var codeHashTotalLength int
	var codeTotalLength int // Total length of all byte code (just to illustrate iterating)
	if err1 := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.PlainContractCodeBucket)
		// This is a mapping of contractAddress + incarnation => CodeHash
		for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			contractKeyTotalLength += len(k)
			contractValTotalLength += len(v)
		}
		c = tx.Cursor(dbutils.CodeBucket)
		// This is a mapping of CodeHash => Byte code
		for k, v, err := c.First(); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			codeHashTotalLength += len(k)
			codeTotalLength += len(v)
		}
		return nil
	}); err1 != nil {
		return err1
	}
	fmt.Printf("contractKeyTotalLength: %d, contractValTotalLength: %d, codeHashTotalLength: %d, codeTotalLength: %d\n", contractKeyTotalLength, contractValTotalLength, codeHashTotalLength, codeTotalLength)
	return nil
}

func mint(chaindata string, block uint64) error {
	f, err := os.Create("mint.csv")
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	db := ethdb.MustOpen(chaindata)
	defer db.Close()
	//chiTokenAddr = common.HexToAddress("0x0000000000004946c0e9F43F4Dee607b0eF1fA1c")
	//mintFuncPrefix = common.FromHex("0xa0712d68")
	var gwei uint256.Int
	gwei.SetUint64(1000000000)
	blockEncoded := dbutils.EncodeBlockNumber(block)
	canonical := make(map[common.Hash]struct{})
	if err1 := db.KV().View(context.Background(), func(tx ethdb.Tx) error {
		c := tx.Cursor(dbutils.HeaderPrefix)
		// This is a mapping of contractAddress + incarnation => CodeHash
		for k, v, err := c.Seek(blockEncoded); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			// Skip non relevant records
			if !dbutils.CheckCanonicalKey(k) {
				continue
			}
			canonical[common.BytesToHash(v)] = struct{}{}
		}
		c = tx.Cursor(dbutils.BlockBodyPrefix)
		var prevBlock uint64
		var burntGas uint64
		for k, v, err := c.Seek(blockEncoded); k != nil; k, v, err = c.Next() {
			if err != nil {
				return err
			}
			blockNumber := binary.BigEndian.Uint64(k[:8])
			blockHash := common.BytesToHash(k[8:])
			if _, isCanonical := canonical[blockHash]; !isCanonical {
				continue
			}
			if blockNumber != prevBlock && blockNumber != prevBlock+1 {
				fmt.Printf("Gap [%d-%d]\n", prevBlock, blockNumber-1)
			}
			prevBlock = blockNumber
			bodyRlp, err := rawdb.DecompressBlockBody(v)
			if err != nil {
				return err
			}
			body := new(types.Body)
			if err := rlp.Decode(bytes.NewReader(bodyRlp), body); err != nil {
				return fmt.Errorf("invalid block body RLP: %w", err)
			}
			header := rawdb.ReadHeader(db, blockHash, blockNumber)
			senders := rawdb.ReadSenders(db, blockHash, blockNumber)
			var ethSpent uint256.Int
			var ethSpentTotal uint256.Int
			var totalGas uint256.Int
			count := 0
			for i, tx := range body.Transactions {
				ethSpent.SetUint64(tx.Gas())
				totalGas.Add(&totalGas, &ethSpent)
				if senders[i] == header.Coinbase {
					continue // Mining pool sending payout potentially with abnormally low fee, skip
				}
				ethSpent.Mul(&ethSpent, tx.GasPrice())
				ethSpentTotal.Add(&ethSpentTotal, &ethSpent)
				count++
			}
			if count > 0 {
				ethSpentTotal.Div(&ethSpentTotal, &totalGas)
				ethSpentTotal.Div(&ethSpentTotal, &gwei)
				gasPrice := ethSpentTotal.Uint64()
				burntGas += header.GasUsed
				fmt.Fprintf(w, "%d, %d\n", burntGas, gasPrice)
			}
		}
		return nil
	}); err1 != nil {
		return err1
	}
	return nil
}

func main() {
	flag.Parse()

	log.SetupDefaultTerminalLogger(log.Lvl(*verbosity), "", "")

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Error("could not create CPU profile", "error", err)
			return
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Error("could not start CPU profile", "error", err)
			return
		}
		defer pprof.StopCPUProfile()
	}
	//db := ethdb.MustOpen("/home/akhounov/.ethereum/geth/chaindata")
	//db := ethdb.MustOpen(node.DefaultDataDir() + "/geth/chaindata")
	//check(err)
	//defer db.Close()
	if *action == "cfg" {
		testGenCfg()
	}
	if *action == "bucketStats" {
		bucketStats(*chaindata)
	}
	if *action == "syncChart" {
		mychart()
	}
	//testRebuild()
	if *action == "testRewind" {
		testRewind(*chaindata, *block, *rewind)
	}
	//hashFile()
	//buildHashFromFile()
	if *action == "testResolve" {
		testResolve(*chaindata)
	}
	//rlpIndices()
	//printFullNodeRLPs()
	//testStartup()
	//testDifficulty()
	//testRewindTests()
	//if *reset != -1 {
	//	testReset(uint64(*reset))
	//}
	if *action == "testBlockHashes" {
		testBlockHashes(*chaindata, *block, common.HexToHash(*hash))
	}
	//printBuckets(db)
	//printTxHashes()
	//relayoutKeys()
	//upgradeBlocks()
	//compareTries()
	if *action == "invTree" {
		invTree("root", "right", "diff", *name)
	}
	//invTree("iw", "ir", "id", *block, true)
	//loadAccount()
	//printBranches(uint64(*block))
	//extractTrie(*block)
	//repair()
	if *action == "readAccount" {
		readAccount(*chaindata, common.HexToAddress(*account), uint64(*block), uint64(*rewind))
	}
	if *action == "readPlainAccount" {
		readPlainAccount(*chaindata, common.HexToAddress(*account))
	}
	if *action == "fixAccount" {
		fixAccount(*chaindata, common.HexToHash(*account), common.HexToHash(*hash))
	}
	if *action == "nextIncarnation" {
		nextIncarnation(*chaindata, common.HexToHash(*account))
	}
	//repairCurrent()
	//fmt.Printf("\u00b3\n")
	if *action == "dumpStorage" {
		dumpStorage()
	}
	if *action == "current" {
		printCurrentBlockNumber(*chaindata)
	}
	if *action == "bucket" {
		printBucket(*chaindata)
	}

	if *action == "val-tx-lookup-2" {
		ValidateTxLookups2(*chaindata)
	}
	if *action == "modiAccounts" {
		getModifiedAccounts(*chaindata)
	}
	if *action == "slice" {
		dbSlice(*chaindata, *bucket, common.FromHex(*hash))
	}
	if *action == "getProof" {
		if err := testGetProof(*chaindata, common.HexToAddress(*account), *rewind, false); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "regenerateIH" {
		if err := regenerate(*chaindata); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "searchChangeSet" {
		if err := searchChangeSet(*chaindata, common.FromHex(*hash), uint64(*block)); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "searchStorageChangeSet" {
		if err := searchStorageChangeSet(*chaindata, common.FromHex(*hash), uint64(*block)); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "changeSetStats" {
		if err := changeSetStats(*chaindata, uint64(*block), uint64(*block)+uint64(*rewind)); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "supply" {
		if err := supply(*chaindata); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "extractCode" {
		if err := extractCode(*chaindata); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "iterateOverCode" {
		if err := iterateOverCode(*chaindata); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "logIndex" {
		if err := logIndex(*chaindata); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "logIndexBitmap" {
		if err := logIndexBitmap(*chaindata); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
	if *action == "mint" {
		if err := mint(*chaindata, uint64(*block)); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}
}
