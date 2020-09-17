package commands

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/RoaringBitmap/roaring"
	"math/big"
	"time"

	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/common/hexutil"
	"github.com/ledgerwatch/turbo-geth/core"
	"github.com/ledgerwatch/turbo-geth/core/bloombits"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/core/vm"
	"github.com/ledgerwatch/turbo-geth/eth/filters"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/params"
	"github.com/ledgerwatch/turbo-geth/rpc"
	"github.com/ledgerwatch/turbo-geth/turbo/adapter"
	"github.com/ledgerwatch/turbo-geth/turbo/rawdb"
	"github.com/ledgerwatch/turbo-geth/turbo/transactions"
)

func getReceipts(ctx context.Context, db rawdb.DatabaseReader, hash common.Hash) (types.Receipts, error) {
	number := rawdb.ReadHeaderNumber(db, hash)
	if number == nil {
		return nil, fmt.Errorf("block not found: %x", hash)
	}

	block := rawdb.ReadBlock(db, hash, *number)
	if cached := rawdb.ReadReceipts(db, block.Hash(), block.NumberU64()); cached != nil {
		return cached, nil
	}

	cc := adapter.NewChainContext(db)
	bc := adapter.NewBlockGetter(db)
	chainConfig := getChainConfig(db)
	_, _, ibs, dbstate, err := transactions.ComputeTxEnv(ctx, bc, chainConfig, cc, db.(ethdb.HasKV).KV(), hash, 0)
	if err != nil {
		return nil, err
	}

	var receipts types.Receipts
	gp := new(core.GasPool).AddGas(block.GasLimit())
	var usedGas = new(uint64)
	for i, tx := range block.Transactions() {
		ibs.Prepare(tx.Hash(), block.Hash(), i)

		header := rawdb.ReadHeader(db, hash, *number)
		receipt, err := core.ApplyTransaction(chainConfig, cc, nil, gp, ibs, dbstate, header, tx, usedGas, vm.Config{})
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}

	return receipts, nil
}

// GetLogsByHash non-standard RPC that returns all logs in a block
// TODO(tjayrush): Since this is non-standard we could rename it to GetLogsByBlockHash to be more consistent and avoid confusion
func (api *APIImpl) GetLogsByHash(ctx context.Context, hash common.Hash) ([][]*types.Log, error) {
	number := rawdb.ReadHeaderNumber(api.dbReader, hash)
	if number == nil {
		return nil, fmt.Errorf("block not found: %x", hash)
	}

	receipts, err := getReceipts(ctx, api.dbReader, hash)
	if err != nil {
		return nil, fmt.Errorf("getReceipts error: %v", err)
	}
	logs := make([][]*types.Log, len(receipts))
	for i, receipt := range receipts {
		logs[i] = receipt.Logs
	}
	return logs, nil
}

// Filter can be used to retrieve and filter logs.
type Filter struct {
	addresses []common.Address
	topics    [][]common.Hash

	block      common.Hash // Block hash if filtering a single block
	begin, end int64       // Range interval if filtering multiple blocks

	matcher *bloombits.Matcher
}

func NewBlockFilter(block common.Hash, addresses []common.Address, topics [][]common.Hash) *Filter {
	// Create a generic filter and convert it into a block filter
	filter := newFilter(addresses, topics)
	filter.block = block
	return filter
}

// newFilter creates a generic filter that can either filter based on a block hash,
// or based on range queries. The search criteria needs to be explicitly set.
func newFilter(addresses []common.Address, topics [][]common.Hash) *Filter {
	return &Filter{
		addresses: addresses,
		topics:    topics,
	}
}

// GetLogs returns logs matching the given argument that are stored within the state.
func (api *APIImpl) GetLogs2(ctx context.Context, crit filters.FilterCriteria) ([]*types.Log, error) {
	var filter *Filter
	if crit.BlockHash != nil {
		// Block filter requested, construct a single-shot filter
		filter = NewBlockFilter(*crit.BlockHash, crit.Addresses, crit.Topics)
	} else {
		// Convert the RPC block numbers into internal representations
		latest, err := getLatestBlockNumber(api.dbReader)
		if err != nil {
			return nil, err
		}

		begin := int64(latest)
		if crit.FromBlock != nil {
			begin = crit.FromBlock.Int64()
		}
		end := int64(latest)
		if crit.ToBlock != nil {
			end = crit.ToBlock.Int64()
		}

		filter = NewRangeFilter(begin, end, crit.Addresses, crit.Topics)
	}
	// Run the filter and return all the logs
	logs, err := filter.Logs(ctx, api)
	if err != nil {
		return nil, err
	}
	return returnLogs(logs), err
}

// GetLogs returns logs matching the given argument that are stored within the state.
func (api *APIImpl) GetLogs(ctx context.Context, crit filters.FilterCriteria) ([]*types.Log, error) {
	var begin, end uint32
	var logs []*types.Log

	tx, beginErr := api.dbReader.Begin(ctx)
	if beginErr != nil {
		return returnLogs(logs), beginErr
	}
	defer tx.Rollback()

	if crit.BlockHash != nil {
		number := rawdb.ReadHeaderNumber(tx, *crit.BlockHash)
		if number == nil {
			return nil, fmt.Errorf("block not found: %x", *crit.BlockHash)
		}
		begin = uint32(*number)
		end = uint32(*number)
	} else {
		// Convert the RPC block numbers into internal representations
		latest, err := getLatestBlockNumber(tx)
		if err != nil {
			return nil, err
		}

		begin = uint32(latest)
		if crit.FromBlock != nil {
			begin = uint32(crit.FromBlock.Uint64())
		}
		end = uint32(latest)
		if crit.ToBlock != nil {
			end = uint32(crit.ToBlock.Uint64())
		}
	}

	i := 0
	blockNBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(blockNBytes, begin)

	//if len(crit.Addresses) == 0 {
	//	c1 := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex4).Prefetch(10).(ethdb.CursorDupSort)
	//
	//	for k, v, err := c1.Seek(blockNBytes); k != nil; k, v, err = c1.Next() {
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		var (
	//			blockNumberBytes = k[:4]
	//			addr             = k[4:]
	//			txIndexBytes     = v[:4]
	//			logIndexBytes    = v[4:8]
	//			topicsBytes      = v[8:]
	//			blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
	//		)
	//
	//		i++
	//		if blockNumber > end {
	//			break
	//		}
	//
	//		var (
	//			txIndex  = binary.BigEndian.Uint32(txIndexBytes)
	//			logIndex = binary.BigEndian.Uint32(logIndexBytes)
	//		)
	//
	//		if !matchTopics(topicsBytes, crit.Topics) {
	//			continue
	//		}
	//
	//		topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
	//		for i := 0; i < len(topicsBytes); i += 32 {
	//			topicInLog := common.BytesToHash(topicsBytes[i : i+32])
	//			topicsInLog = append(topicsInLog, topicInLog)
	//		}
	//
	//		logData, err := rawdb.LogData(tx, blockNumber, txIndex, logIndex)
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		hash := rawdb.ReadCanonicalHash(tx, uint64(blockNumber))
	//		if hash == (common.Hash{}) {
	//			return returnLogs(logs), fmt.Errorf("block not found")
	//		}
	//
	//		txHash, err := rawdb.TransactionHash(tx, blockNumber, txIndex)
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		logs = append(logs, &types.Log{
	//			Address:     common.BytesToAddress(addr),
	//			BlockNumber: uint64(blockNumber),
	//			BlockHash:   hash,
	//			Data:        logData,
	//			Topics:      topicsInLog,
	//			TxIndex:     uint(txIndex),
	//			TxHash:      txHash,
	//			Index:       uint(logIndex),
	//		})
	//	}
	//
	//	return returnLogs(logs), err
	//}
	//
	//c1 := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex4).Prefetch(10).(ethdb.CursorDupSort)
	//
	//for _, addrToMatch := range crit.Addresses {
	//	//for blockN := begin; blockN <= end; blockN++ {
	//	//	binary.BigEndian.PutUint32(blockNBytes, blockN)
	//
	//	for k, v, err := c1.Seek(append(blockNBytes, addrToMatch[:]...)); k != nil; k, v, err = c1.Next() {
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		var (
	//			blockNumberBytes = k[:4]
	//			addr             = k[4:]
	//			txIndexBytes     = v[:4]
	//			logIndexBytes    = v[4:8]
	//			topicsBytes      = v[8:]
	//			blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
	//		)
	//
	//		i++
	//		if blockNumber > end {
	//			break
	//		}
	//		if !bytes.Equal(addrToMatch[:], addr) {
	//			continue
	//		}
	//
	//		var (
	//			txIndex  = binary.BigEndian.Uint32(txIndexBytes)
	//			logIndex = binary.BigEndian.Uint32(logIndexBytes)
	//		)
	//
	//		if !matchTopics(topicsBytes, crit.Topics) {
	//			continue
	//		}
	//
	//		topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
	//		for i := 0; i < len(topicsBytes); i += 32 {
	//			topicInLog := common.BytesToHash(topicsBytes[i : i+32])
	//			topicsInLog = append(topicsInLog, topicInLog)
	//		}
	//
	//		logData, err := rawdb.LogData(tx, blockNumber, txIndex, logIndex)
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		hash := rawdb.ReadCanonicalHash(tx, uint64(blockNumber))
	//		if hash == (common.Hash{}) {
	//			return returnLogs(logs), fmt.Errorf("block not found")
	//		}
	//
	//		txHash, err := rawdb.TransactionHash(tx, blockNumber, txIndex)
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		logs = append(logs, &types.Log{
	//			Address:     common.BytesToAddress(addr),
	//			BlockNumber: uint64(blockNumber),
	//			BlockHash:   hash,
	//			Data:        logData,
	//			Topics:      topicsInLog,
	//			TxIndex:     uint(txIndex),
	//			TxHash:      txHash,
	//			Index:       uint(logIndex),
	//		})
	//		//}
	//	}
	//}
	//
	//return returnLogs(logs), err
	tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex2)

	/*
		if len(crit.Addresses) == 0 {
			c1 := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex2).Prefetch(10).(ethdb.CursorDupSort)

			for k, v, err := c1.Seek(blockNBytes); k != nil; k, v, err = c1.Next() {
				if err != nil {
					return returnLogs(logs), err
				}

				var (
					blockNumberBytes = k
					addr             = v[:20]
					txIndexBytes     = v[20:24]
					logIndexBytes    = v[24:28]
					topicsBytes      = v[28:]
					blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
				)

				i++
				if blockNumber > end {
					break
				}

				var (
					txIndex  = binary.BigEndian.Uint32(txIndexBytes)
					logIndex = binary.BigEndian.Uint16(logIndexBytes)
				)

				if !matchTopics(topicsBytes, crit.Topics) {
					continue
				}

				topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
				for i := 0; i < len(topicsBytes); i += 32 {
					topicInLog := common.BytesToHash(topicsBytes[i : i+32])
					topicsInLog = append(topicsInLog, topicInLog)
				}

				logData, err := rawdb.LogData(tx, blockNumber, txIndex, logIndex)
				if err != nil {
					return returnLogs(logs), err
				}

				hash := rawdb.ReadCanonicalHash(tx, uint64(blockNumber))
				if hash == (common.Hash{}) {
					return returnLogs(logs), fmt.Errorf("block not found")
				}

				txHash, err := rawdb.TransactionHash(tx, blockNumber, txIndex)
				if err != nil {
					return returnLogs(logs), err
				}

				logs = append(logs, &types.Log{
					Address:     common.BytesToAddress(addr),
					BlockNumber: uint64(blockNumber),
					BlockHash:   hash,
					Data:        logData,
					Topics:      topicsInLog,
					TxIndex:     uint(txIndex),
					TxHash:      txHash,
					Index:       uint(logIndex),
				})
			}

			return returnLogs(logs), nil
		}

		for _, addrToMatch := range crit.Addresses {
			c1 := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex2).Prefetch(10).(ethdb.CursorDupSort)
			addrToMatchBytes := addrToMatch[:]

			for k, v, err := c1.SeekBothRange(blockNBytes, addrToMatchBytes); k != nil; k, v, err = c1.Next() {
				if err != nil {
					return returnLogs(logs), err
				}

				var (
					blockNumberBytes = k
					addr             = v[:20]
					txIndexBytes     = v[20:24]
					logIndexBytes    = v[24:28]
					topicsBytes      = v[28:]
					blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
				)

				i++
				if blockNumber > end {
					break
				}

				if !bytes.Equal(addr, addrToMatchBytes) {
					break
				}

				var (
					txIndex  = binary.BigEndian.Uint32(txIndexBytes)
					logIndex = binary.BigEndian.Uint16(logIndexBytes)
				)

				if !matchTopics(topicsBytes, crit.Topics) {
					continue
				}

				topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
				for i := 0; i < len(topicsBytes); i += 32 {
					topicInLog := common.BytesToHash(topicsBytes[i : i+32])
					topicsInLog = append(topicsInLog, topicInLog)
				}

				logData, err := rawdb.LogData(tx, blockNumber, txIndex, logIndex)
				if err != nil {
					return returnLogs(logs), err
				}

				hash := rawdb.ReadCanonicalHash(tx, uint64(blockNumber))
				if hash == (common.Hash{}) {
					return returnLogs(logs), fmt.Errorf("block not found")
				}

				txHash, err := rawdb.TransactionHash(tx, blockNumber, txIndex)
				if err != nil {
					return returnLogs(logs), err
				}

				logs = append(logs, &types.Log{
					Address:     common.BytesToAddress(addr),
					BlockNumber: uint64(blockNumber),
					BlockHash:   hash,
					Data:        logData,
					Topics:      topicsInLog,
					TxIndex:     uint(txIndex),
					TxHash:      txHash,
					Index:       uint(logIndex),
				})
			}

			return returnLogs(logs), nil
		}
	*/

	var allTopics []common.Hash
	for _, sub := range crit.Topics {
		for _, topic := range sub {
			allTopics = append(allTopics, topic)
		}
	}

	unfiltered := []*types.Log{}
	uniqueTracker := map[uint32]map[uint32]bool{} // allows add log to 'unfiltered' list only once - because bucket stores 1 row for each topic
	c := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex5).Prefetch(10).(ethdb.CursorDupSort)
	for _, addrToMatch := range crit.Addresses {
		for _, topicToMatch := range allTopics {
			key2 := make([]byte, 1+4)
			copy(key2, topicToMatch[32:])
			copy(key2[1:], blockNBytes)

			for k, v, err := c.SeekBothRange(addrToMatch[:], key2); k != nil; k, v, err = c.Next() {
				if err != nil {
					return returnLogs(logs), err
				}

				var (
					addr             = k[:20]
					topicsBytes      = v[:32]
					blockNumberBytes = v[32:36]
					txIndexBytes     = v[36:40]
					logIndexBytes    = v[40:44]
					blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
				)

				i++
				if blockNumber > end {
					break
				}

				if !bytes.Equal(addrToMatch[:], addr) {
					break
				}

				var (
					txIndex  = binary.BigEndian.Uint32(txIndexBytes)
					logIndex = binary.BigEndian.Uint32(logIndexBytes)
				)

				if byLogIndex, ok := uniqueTracker[blockNumber]; ok {
					if _, ok := uniqueTracker[logIndex]; !ok {
						byLogIndex[logIndex] = true

						topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
						for i := 0; i < len(topicsBytes); i += 32 {
							topicInLog := common.BytesToHash(topicsBytes[i : i+32])
							topicsInLog = append(topicsInLog, topicInLog)
						}

						unfiltered = append(unfiltered, &types.Log{
							Address:     addrToMatch,
							BlockNumber: uint64(blockNumber),
							//BlockHash:   hash,
							//Data:        logData,
							Topics:  topicsInLog,
							TxIndex: uint(txIndex),
							//TxHash:  txHash,
							Index: uint(logIndex),
						})
					} else {
						// nothing to do
					}
				} else {
					uniqueTracker[blockNumber] = map[uint32]bool{}
				}
			}
		}
	}

	filtered := []*types.Log{}
	for _, l := range unfiltered {
		if !matchTopics2(l.Topics, crit.Topics) {
			continue
		}

		logData, err := rawdb.LogData(tx, uint32(l.BlockNumber), uint32(l.TxIndex), uint32(l.Index))
		if err != nil {
			return returnLogs(logs), err
		}

		blockHash := rawdb.ReadCanonicalHash(tx, l.BlockNumber)
		if blockHash == (common.Hash{}) {
			return returnLogs(logs), fmt.Errorf("block not found %d", l.BlockNumber)
		}

		txHash, err := rawdb.TransactionHash(tx, uint32(l.BlockNumber), uint32(l.TxIndex))
		if err != nil {
			return returnLogs(logs), err
		}

		l.TxHash = txHash
		l.BlockHash = blockHash
		l.Data = logData
		filtered = append(filtered, l)
	}

	return returnLogs(logs), nil

	//c := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.ReceiptsIndex).Prefetch(10).(ethdb.CursorDupSort)
	//for _, address := range crit.Addresses {
	//	for k, v, err := c.SeekBothRange(address[:], blockNBytes); k != nil; k, v, err = c.Next() {
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		var (
	//			addr             = k
	//			blockNumberBytes = v[:4]
	//			txIndexBytes     = v[4:8]
	//			logIndexBytes    = v[8:12]
	//			topicsBytes      = v[12:]
	//			blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
	//		)
	//
	//		i++
	//		if blockNumber > end {
	//			break
	//		}
	//
	//		if !bytes.Equal(address[:], addr) {
	//			break
	//		}
	//
	//		var (
	//			txIndex  = binary.BigEndian.Uint32(txIndexBytes)
	//			logIndex = binary.BigEndian.Uint32(logIndexBytes)
	//		)
	//
	//		if !matchTopics(topicsBytes, crit.Topics) {
	//			continue
	//		}
	//
	//		topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
	//		for i := 0; i < len(topicsBytes); i += 32 {
	//			topicInLog := common.BytesToHash(topicsBytes[i : i+32])
	//			topicsInLog = append(topicsInLog, topicInLog)
	//		}
	//
	//		logData, err := rawdb.LogData(tx, blockNumber, txIndex, logIndex)
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		hash := rawdb.ReadCanonicalHash(tx, uint64(blockNumber))
	//		if hash == (common.Hash{}) {
	//			return returnLogs(logs), fmt.Errorf("block not found")
	//		}
	//
	//		txHash, err := rawdb.TransactionHash(tx, blockNumber, txIndex)
	//		if err != nil {
	//			return returnLogs(logs), err
	//		}
	//
	//		logs = append(logs, &types.Log{
	//			Address:     address,
	//			BlockNumber: uint64(blockNumber),
	//			BlockHash:   hash,
	//			Data:        logData,
	//			Topics:      topicsInLog,
	//			TxIndex:     uint(txIndex),
	//			TxHash:      txHash,
	//			Index:       uint(logIndex),
	//		})
	//	}
	//}

	return returnLogs(logs), nil
}

// GetLogs returns logs matching the given argument that are stored within the state.
func (api *APIImpl) GetLogs3(ctx context.Context, crit filters.FilterCriteria) ([]*types.Log, error) {
	var begin, end uint32
	var logs []*types.Log

	tx, beginErr := api.dbReader.Begin(ctx)
	if beginErr != nil {
		return returnLogs(logs), beginErr
	}
	defer tx.Rollback()

	if crit.BlockHash != nil {
		number := rawdb.ReadHeaderNumber(tx, *crit.BlockHash)
		if number == nil {
			return nil, fmt.Errorf("block not found: %x", *crit.BlockHash)
		}
		begin = uint32(*number)
		end = uint32(*number)
	} else {
		// Convert the RPC block numbers into internal representations
		latest, err := getLatestBlockNumber(tx)
		if err != nil {
			return nil, err
		}

		begin = uint32(latest)
		if crit.FromBlock != nil {
			begin = uint32(crit.FromBlock.Uint64())
		}
		end = uint32(latest)
		if crit.ToBlock != nil {
			end = uint32(crit.ToBlock.Uint64())
		}
	}

	t := time.Now()

	bitmapForANDing := roaring.New()
	bitmapForANDing.AddRange(uint64(begin), uint64(end))
	fmt.Printf("1: %s\n", time.Since(t))

	blockNBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(blockNBytes, begin)

	//
	//var bitmapForANDing *gocroaring.Bitmap
	//bitmapForANDing = gocroaring.New()
	//for i := begin; i <= end; i++ {
	//	bitmapForANDing.Add(i)
	//}
	//fmt.Printf("2: %s\n", time.Since(t))

	//fmt.Printf("beginning cardinality %d\n", bitmapForANDing.Cardinality())

	for _, sub := range crit.Topics {
		var bitmapForORing *roaring.Bitmap
		for _, topic := range sub {
			bitmapBytes, err := tx.Get(dbutils.Topics, topic[:])
			if err != nil {
				return nil, err
			}
			m := roaring.New()
			_, err = m.ReadFrom(bytes.NewReader(bitmapBytes))
			if err != nil {
				return nil, err
			}
			if bitmapForORing == nil {
				bitmapForORing = m
			} else {
				bitmapForORing.Or(m)
			}
		}

		if bitmapForANDing == nil {
			bitmapForANDing = bitmapForORing
		} else {
			bitmapForANDing.And(bitmapForORing)
		}
	}

	fmt.Printf("get_receipts.go:674: %s\n", time.Since(t))
	fmt.Printf("cardinality %d\n", bitmapForANDing.GetCardinality())

	//t := time.Now()
	//unfiltered := []*types.Log{}
	//uniqueTracker := map[uint32]map[uint32]bool{} // allows add log to 'unfiltered' list only once - because bucket stores 1 row for each topic
	//c := tx.(ethdb.HasTx).Tx().CursorDupSort(dbutils.).Prefetch(10).(ethdb.CursorDupSort)
	//for _, addrToMatch := range crit.Addresses {
	//	for _, topicToMatch := range allTopics {
	//		key2 := make([]byte, 1+4)
	//		copy(key2, topicToMatch[32:])
	//		copy(key2[1:], blockNBytes)
	//
	//		for k, v, err := c.SeekBothRange(addrToMatch[:], key2); k != nil; k, v, err = c.Next() {
	//			if err != nil {
	//				return returnLogs(logs), err
	//			}
	//
	//			var (
	//				addr             = k[:20]
	//				topicsBytes      = v[:32]
	//				blockNumberBytes = v[32:36]
	//				txIndexBytes     = v[36:40]
	//				logIndexBytes    = v[40:44]
	//				blockNumber      = binary.BigEndian.Uint32(blockNumberBytes)
	//			)
	//
	//			i++
	//			if blockNumber > end {
	//				break
	//			}
	//
	//			if !bytes.Equal(addrToMatch[:], addr) {
	//				break
	//			}
	//
	//			var (
	//				txIndex  = binary.BigEndian.Uint32(txIndexBytes)
	//				logIndex = binary.BigEndian.Uint32(logIndexBytes)
	//			)
	//
	//			if byLogIndex, ok := uniqueTracker[blockNumber]; ok {
	//				if _, ok := uniqueTracker[logIndex]; !ok {
	//					byLogIndex[logIndex] = true
	//
	//					topicsInLog := make([]common.Hash, 0, len(topicsBytes)/32)
	//					for i := 0; i < len(topicsBytes); i += 32 {
	//						topicInLog := common.BytesToHash(topicsBytes[i : i+32])
	//						topicsInLog = append(topicsInLog, topicInLog)
	//					}
	//
	//					unfiltered = append(unfiltered, &types.Log{
	//						Address:     addrToMatch,
	//						BlockNumber: uint64(blockNumber),
	//						//BlockHash:   hash,
	//						//Data:        logData,
	//						Topics:  topicsInLog,
	//						TxIndex: uint(txIndex),
	//						//TxHash:  txHash,
	//						Index: uint(logIndex),
	//					})
	//				} else {
	//					// nothing to do
	//				}
	//			} else {
	//				uniqueTracker[blockNumber] = map[uint32]bool{}
	//			}
	//		}
	//	}
	//}
	//
	//filtered := []*types.Log{}
	//for _, l := range unfiltered {
	//	if !matchTopics2(l.Topics, crit.Topics) {
	//		continue
	//	}
	//
	//	logData, err := rawdb.LogData(tx, uint32(l.BlockNumber), uint32(l.TxIndex), uint32(l.Index))
	//	if err != nil {
	//		return returnLogs(logs), err
	//	}
	//
	//	blockHash := rawdb.ReadCanonicalHash(tx, l.BlockNumber)
	//	if blockHash == (common.Hash{}) {
	//		return returnLogs(logs), fmt.Errorf("block not found %d", l.BlockNumber)
	//	}
	//
	//	txHash, err := rawdb.TransactionHash(tx, uint32(l.BlockNumber), uint32(l.TxIndex))
	//	if err != nil {
	//		return returnLogs(logs), err
	//	}
	//
	//	l.TxHash = txHash
	//	l.BlockHash = blockHash
	//	l.Data = logData
	//	filtered = append(filtered, l)
	//}

	return returnLogs(logs), nil
}

/*
Topics are order-dependent. A transaction with a log with topics [A, B] will be matched by the following topic filters:
[] “anything”
[A] “A in first position (and anything after)”
[null, B] “anything in first position AND B in second position (and anything after)”
[A, B] “A in first position AND B in second position (and anything after)”
[[A, B], [A, B]] “(A OR B) in first position AND (A OR B) in second position (and anything after)”

topicsBytes - slice which holds all topics without any separator
*/
func matchTopics(topicsBytes []byte, topicsToMatch [][]common.Hash) bool {
	if len(topicsToMatch) > len(topicsBytes)/32 {
		return false
	}

	if len(topicsToMatch) == 0 {
		return true
	}
	for i, sub := range topicsToMatch {
		match := len(sub) == 0 // empty rule set == wildcard
		for _, topic := range sub {
			topicInLog := common.BytesToHash(topicsBytes[i*32 : i*32+32])
			if topicInLog == topic {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	return true
}

func matchTopics2(topics []common.Hash, topicsToMatch [][]common.Hash) bool {
	if len(topicsToMatch) > len(topics) {
		return false
	}

	if len(topicsToMatch) == 0 {
		return true
	}
	for i, sub := range topicsToMatch {
		match := len(sub) == 0 // empty rule set == wildcard
		for _, topicToMatch := range sub {
			if topics[i] == topicToMatch {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	return true
}

// NewRangeFilter creates a new filter which uses a bloom filter on blocks to
// figure out whether a particular block is interesting or not.
func NewRangeFilter(begin, end int64, addresses []common.Address, topics [][]common.Hash) *Filter {
	// Flatten the address and topic filter clauses into a single bloombits filter
	// system. Since the bloombits are not positional, nil topics are permitted,
	// which get flattened into a nil byte slice.
	filters := make([][][]byte, 0, len(addresses))
	if len(addresses) > 0 {
		filter := make([][]byte, len(addresses))
		for i, address := range addresses {
			filter[i] = address.Bytes()
		}
		filters = append(filters, filter)
	}
	for _, topicList := range topics {
		filter := make([][]byte, len(topicList))
		for i, topic := range topicList {
			filter[i] = topic.Bytes()
		}
		filters = append(filters, filter)
	}

	// Create a generic filter and convert it into a range filter
	filter := newFilter(addresses, topics)

	filter.matcher = bloombits.NewMatcher(params.BloomBitsBlocks, filters)
	filter.begin = begin
	filter.end = end

	return filter
}

func (api *APIImpl) GetTransactionReceipt(ctx context.Context, hash common.Hash) (map[string]interface{}, error) {
	// Retrieve the transaction and assemble its EVM context
	tx, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(api.dbReader, hash)
	if tx == nil {
		return nil, fmt.Errorf("transaction %#x not found", hash)
	}

	receipts, err := getReceipts(ctx, api.dbReader, blockHash)
	if err != nil {
		return nil, fmt.Errorf("getReceipts error: %v", err)
	}
	if len(receipts) <= int(txIndex) {
		return nil, fmt.Errorf("block has less receipts than expected: %d <= %d, block: %d", len(receipts), int(txIndex), blockNumber)
	}
	receipt := receipts[txIndex]

	var signer types.Signer = types.FrontierSigner{}
	if tx.Protected() {
		signer = types.NewEIP155Signer(tx.ChainID().ToBig())
	}
	from, _ := types.Sender(signer, tx)

	// Fill in the derived information in the logs
	if receipt.Logs != nil {
		for i, log := range receipt.Logs {
			log.BlockNumber = blockNumber
			log.TxHash = hash
			log.TxIndex = uint(txIndex)
			log.BlockHash = blockHash
			log.Index = uint(i)
		}
	}

	// Now reconstruct the bloom filter
	fields := map[string]interface{}{
		"blockHash":         blockHash,
		"blockNumber":       hexutil.Uint64(blockNumber),
		"transactionHash":   hash,
		"transactionIndex":  hexutil.Uint64(txIndex),
		"from":              from,
		"to":                tx.To(),
		"gasUsed":           hexutil.Uint64(receipt.GasUsed),
		"cumulativeGasUsed": hexutil.Uint64(receipt.CumulativeGasUsed),
		"contractAddress":   nil,
		"logs":              receipt.Logs,
		"logsBloom":         receipt.Bloom,
	}

	// Assign receipt status or post state.
	if len(receipt.PostState) > 0 {
		fields["root"] = hexutil.Bytes(receipt.PostState)
	} else {
		fields["status"] = hexutil.Uint(receipt.Status)
	}
	if receipt.Logs == nil {
		fields["logs"] = [][]*types.Log{}
	}
	// If the ContractAddress is 20 0x0 bytes, assume it is not a contract creation
	if receipt.ContractAddress != (common.Address{}) {
		fields["contractAddress"] = receipt.ContractAddress
	}
	return fields, nil
}

// Logs searches the blockchain for matching log entries, returning all from the
// first block that contains matches, updating the start of the filter accordingly.
func (f *Filter) Logs(ctx context.Context, api *APIImpl) ([]*types.Log, error) {
	// If we're doing singleton block filtering, execute and return
	if f.block != (common.Hash{}) {
		header, err := api.GetHeaderByHash(ctx, f.block)
		if err != nil {
			return nil, err
		}
		if header == nil {
			return nil, errors.New("unknown block")
		}
		return f.blockLogs(ctx, header, api)
	}

	// Figure out the limits of the filter range
	latest, err := getLatestBlockNumber(api.dbReader)
	if err != nil {
		return nil, err
	}

	if f.begin == -1 {
		f.begin = int64(latest)
	}
	end := uint64(f.end)
	if f.end == -1 {
		end = latest
	}

	// Gather all indexed logs, and finish with non indexed ones
	var logs []*types.Log
	size, sections, _ := api.ethBackend.BloomStatus()

	if indexed := sections * size; indexed > uint64(f.begin) {
		if indexed > end {
			logs, err = f.indexedLogs(ctx, end, api)
		} else {
			logs, err = f.indexedLogs(ctx, indexed-1, api)
		}
		if err != nil {
			return logs, err
		}
	}
	rest, err := f.unindexedLogs(ctx, end, api)
	logs = append(logs, rest...)
	return logs, err
}

// indexedLogs returns the logs matching the filter criteria based on the bloom
// bits indexed available locally or via the network.
func (f *Filter) indexedLogs(ctx context.Context, end uint64, api *APIImpl) ([]*types.Log, error) {
	// Iterate over the matches until exhausted or context closed
	var logs []*types.Log

	for num := f.begin; num < int64(end)+1; num++ {
		// Retrieve the suggested block and pull any truly matching logs
		header, err := api.GetHeaderByNumber(ctx, rpc.BlockNumber(num))
		if header == nil || err != nil {
			return logs, err
		}
		found, err := f.checkMatches(ctx, header, api)
		if err != nil {
			return logs, err
		}
		logs = append(logs, found...)
	}

	return logs, nil
}

// unindexedLogs returns the logs matching the filter criteria based on raw block
// iteration and bloom matching.
func (f *Filter) unindexedLogs(ctx context.Context, end uint64, api *APIImpl) ([]*types.Log, error) {
	var logs []*types.Log

	for ; f.begin <= int64(end); f.begin++ {
		header, err := api.GetHeaderByNumber(ctx, rpc.BlockNumber(f.begin))
		if header == nil || err != nil {
			return logs, err
		}
		found, err := f.blockLogs(ctx, header, api)
		if err != nil {
			return logs, err
		}
		logs = append(logs, found...)
	}
	return logs, nil
}

// blockLogs returns the logs matching the filter criteria within a single block.
func (f *Filter) blockLogs(ctx context.Context, header *types.Header, api *APIImpl) (logs []*types.Log, err error) {
	if bloomFilter(header.Bloom, f.addresses, f.topics) {
		found, err := f.checkMatches(ctx, header, api)
		if err != nil {
			return logs, err
		}
		logs = append(logs, found...)
	}
	return logs, nil
}

// checkMatches checks if the receipts belonging to the given header contain any log events that
// match the filter criteria. This function is called when the bloom filter signals a potential match.
func (f *Filter) checkMatches(ctx context.Context, header *types.Header, api *APIImpl) (logs []*types.Log, err error) {
	// Get the logs of the block
	logsList, err := api.GetLogsByHash(ctx, header.Hash())
	if err != nil {
		return nil, err
	}
	unfiltered := make([]*types.Log, 0, len(logsList))
	for _, logs := range logsList {
		unfiltered = append(unfiltered, logs...)
	}
	logs = filterLogs(unfiltered, nil, nil, f.addresses, f.topics)
	if len(logs) > 0 {
		// We have matching logs, check if we need to resolve full logs via the light client
		if logs[0].TxHash == (common.Hash{}) {
			receipts := rawdb.ReadReceipts(api.dbReader, header.Hash(), header.Number.Uint64())
			unfiltered = unfiltered[:0]
			for _, receipt := range receipts {
				unfiltered = append(unfiltered, receipt.Logs...)
			}
			logs = filterLogs(unfiltered, nil, nil, f.addresses, f.topics)
		}
		return logs, nil
	}
	return nil, nil
}

func includes(addresses []common.Address, a common.Address) bool {
	for _, addr := range addresses {
		if addr == a {
			return true
		}
	}

	return false
}

// filterLogs creates a slice of logs matching the given criteria.
func filterLogs(logs []*types.Log, fromBlock, toBlock *big.Int, addresses []common.Address, topics [][]common.Hash) []*types.Log {
	var ret []*types.Log
Logs:
	for _, log := range logs {
		if fromBlock != nil && fromBlock.Int64() >= 0 && fromBlock.Uint64() > log.BlockNumber {
			continue
		}
		if toBlock != nil && toBlock.Int64() >= 0 && toBlock.Uint64() < log.BlockNumber {
			continue
		}

		if len(addresses) > 0 && !includes(addresses, log.Address) {
			continue
		}
		// If the to filtered topics is greater than the amount of topics in logs, skip.
		if len(topics) > len(log.Topics) {
			continue Logs
		}
		for i, sub := range topics {
			match := len(sub) == 0 // empty rule set == wildcard
			for _, topic := range sub {
				if log.Topics[i] == topic {
					match = true
					break
				}
			}
			if !match {
				continue Logs
			}
		}
		ret = append(ret, log)
	}
	return ret
}

func bloomFilter(bloom types.Bloom, addresses []common.Address, topics [][]common.Hash) bool {
	if len(addresses) > 0 {
		var included bool
		for _, addr := range addresses {
			if types.BloomLookup(bloom, addr) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}

	for _, sub := range topics {
		included := len(sub) == 0 // empty rule set == wildcard
		for _, topic := range sub {
			if types.BloomLookup(bloom, topic) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}
	return true
}

func returnLogs(logs []*types.Log) []*types.Log {
	if logs == nil {
		return []*types.Log{}
	}
	return logs
}
