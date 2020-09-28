package commands

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/RoaringBitmap/gocroaring"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/common/hexutil"
	"github.com/ledgerwatch/turbo-geth/core"
	"github.com/ledgerwatch/turbo-geth/core/bloombits"
	"github.com/ledgerwatch/turbo-geth/core/rawdb"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/core/vm"
	"github.com/ledgerwatch/turbo-geth/eth/filters"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/ethdb/bitmapdb"
	"github.com/ledgerwatch/turbo-geth/params"
	"github.com/ledgerwatch/turbo-geth/turbo/adapter"
	"github.com/ledgerwatch/turbo-geth/turbo/transactions"
)

func getReceipts(ctx context.Context, tx rawdb.DatabaseReader, kv ethdb.KV, number uint64, hash common.Hash) (types.Receipts, error) {
	if cached := rawdb.ReadReceipts(tx, number); cached != nil {
		return cached, nil
	}

	block := rawdb.ReadBlock(tx, hash, number)

	cc := adapter.NewChainContext(tx)
	bc := adapter.NewBlockGetter(tx)
	chainConfig := getChainConfig(tx)
	_, _, ibs, dbstate, err := transactions.ComputeTxEnv(ctx, bc, chainConfig, cc, kv, hash, 0)
	if err != nil {
		return nil, err
	}

	var receipts types.Receipts
	gp := new(core.GasPool).AddGas(block.GasLimit())
	var usedGas = new(uint64)
	for i, txn := range block.Transactions() {
		ibs.Prepare(txn.Hash(), block.Hash(), i)

		header := rawdb.ReadHeader(tx, hash, number)
		receipt, err := core.ApplyTransaction(chainConfig, cc, nil, gp, ibs, dbstate, header, txn, usedGas, vm.Config{})
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
	tx, err := api.dbReader.Begin(context.Background())
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	number := rawdb.ReadHeaderNumber(tx, hash)
	if number == nil {
		return nil, fmt.Errorf("block not found: %x", hash)
	}

	receipts, err := getReceipts(ctx, api.dbReader, api.db, *number, hash)
	if err != nil {
		return nil, fmt.Errorf("getReceipts error: %v", err)
	}
	logs := make([][]*types.Log, len(receipts))
	for i, receipt := range receipts {
		for _, l := range receipt.Logs {
			l.Topics, err = rawdb.ReadTopics(tx, l.TopicIds)
			if err != nil {
				return nil, err
			}
		}

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
func (api *APIImpl) GetLogs(ctx context.Context, crit filters.FilterCriteria) ([]*types.Log, error) {
	var begin, end uint32
	var logs []*types.Log //nolint:prealloc

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

	blockNumbers := gocroaring.New()
	blockNumbers.AddRange(uint64(begin), uint64(end+1)) // [min,max)

	critTopicIds, err := topicsToIds(tx, crit.Topics)
	if err != nil {
		return returnLogs(logs), err
	}

	topicsBitmap, err := getTopicsBitmap(tx.(ethdb.HasTx).Tx().Cursor(dbutils.LogTopicIndex), critTopicIds)
	if err != nil {
		return nil, err
	}
	if topicsBitmap != nil {
		if blockNumbers == nil {
			blockNumbers = topicsBitmap
		} else {
			blockNumbers.And(topicsBitmap)
		}
	}

	logAddrIndex := tx.(ethdb.HasTx).Tx().Cursor(dbutils.LogAddressIndex)
	var addrBitmap *gocroaring.Bitmap
	for _, addr := range crit.Addresses {
		m, errGet := bitmapdb.Get(logAddrIndex, addr[:])
		if errGet != nil {
			return nil, errGet
		}
		if addrBitmap == nil {
			addrBitmap = m
		} else {
			addrBitmap = gocroaring.Or(addrBitmap, m)
		}
	}

	if addrBitmap != nil {
		if blockNumbers == nil {
			blockNumbers = addrBitmap
		} else {
			blockNumbers.And(addrBitmap)
		}
	}

	blockNToMatchBytes := make([]byte, 4)

	if blockNumbers.Cardinality() == 0 {
		return returnLogs(logs), nil
	}

	for _, blockNToMatch := range blockNumbers.ToArray() {
		binary.BigEndian.PutUint32(blockNToMatchBytes, blockNToMatch)

		blockHash := rawdb.ReadCanonicalHash(tx, uint64(blockNToMatch))
		if blockHash == (common.Hash{}) {
			return returnLogs(logs), fmt.Errorf("block not found %d", uint64(blockNToMatch))
		}

		receipts, errGet := getReceipts(ctx, tx, api.db, uint64(blockNToMatch), blockHash)
		if errGet != nil {
			return returnLogs(logs), errGet
		}

		unfiltered := make([]*types.Log, 0, len(receipts))
		for _, receipt := range receipts {
			unfiltered = append(unfiltered, receipt.Logs...)
		}
		unfiltered = filterLogs(unfiltered, nil, nil, crit.Addresses, critTopicIds)
		logs = append(logs, unfiltered...)
	}

	for _, l := range logs {
		l.Topics, err = rawdb.ReadTopics(tx, l.TopicIds)
		if err != nil {
			return returnLogs(logs), err
		}
	}

	return returnLogs(logs), nil
}

// The Topic list restricts matches to particular event topics. Each event has a list
// of topics. Topics matches a prefix of that list. An empty element slice matches any
// topic. Non-empty elements represent an alternative that matches any of the
// contained topics.
//
// Examples:
// {} or nil          matches any topic list
// {{A}}              matches topic A in first position
// {{}, {B}}          matches any topic in first position AND B in second position
// {{A}, {B}}         matches topic A in first position AND B in second position
// {{A, B}, {C, D}}   matches topic (A OR B) in first position AND (C OR D) in second position
func getTopicsBitmap(c ethdb.Cursor, topics [][]uint32) (*gocroaring.Bitmap, error) {
	idBytes := make([]byte, 4)
	var result *gocroaring.Bitmap
	for _, sub := range topics {
		var bitmapForORing *gocroaring.Bitmap
		for _, topic := range sub {
			binary.BigEndian.PutUint32(idBytes, topic)
			m, err := bitmapdb.Get(c, idBytes)
			if err != nil {
				return nil, err
			}
			if bitmapForORing == nil {
				bitmapForORing = m
			} else {
				bitmapForORing = gocroaring.FastOr(bitmapForORing, m)
			}
		}

		if bitmapForORing != nil {
			if result == nil {
				result = bitmapForORing
			} else {
				result = gocroaring.And(bitmapForORing, result)
			}
		}
	}
	return result, nil
}

func topicsToIds(db rawdb.DatabaseReader, topics [][]common.Hash) ([][]uint32, error) {
	ids := make([][]uint32, len(topics))
	for i, sub := range topics {
		ids[i] = make([]uint32, len(sub))
		for j, topic := range sub {
			var err error
			ids[i][j], err = rawdb.ReadTopicId(db, topic)
			if err != nil {
				return nil, err
			}
		}
	}
	return ids, nil
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
	tx, err := api.dbReader.Begin(context.Background())
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Retrieve the transaction and assemble its EVM context
	txn, blockHash, blockNumber, txIndex := rawdb.ReadTransaction(tx, hash)
	if tx == nil {
		return nil, fmt.Errorf("transaction %#x not found", hash)
	}

	receipts, err := getReceipts(ctx, tx, api.db, blockNumber, blockHash)
	if err != nil {
		return nil, fmt.Errorf("getReceipts error: %v", err)
	}
	if len(receipts) <= int(txIndex) {
		return nil, fmt.Errorf("block has less receipts than expected: %d <= %d, block: %d", len(receipts), int(txIndex), blockNumber)
	}
	receipt := receipts[txIndex]

	var signer types.Signer = types.FrontierSigner{}
	if txn.Protected() {
		signer = types.NewEIP155Signer(txn.ChainID().ToBig())
	}
	from, _ := types.Sender(signer, txn)

	// Fill in the derived information in the logs
	if receipt.Logs != nil {
		for _, log := range receipt.Logs {
			log.Topics, err = rawdb.ReadTopics(tx, log.TopicIds)
			if err != nil {
				return nil, err
			}
			log.BlockNumber = blockNumber
			log.TxHash = hash
			log.TxIndex = uint(txIndex)
			log.BlockHash = blockHash
		}
	}

	// Now reconstruct the bloom filter
	fields := map[string]interface{}{
		"blockHash":         blockHash,
		"blockNumber":       hexutil.Uint64(blockNumber),
		"transactionHash":   hash,
		"transactionIndex":  hexutil.Uint64(txIndex),
		"from":              from,
		"to":                txn.To(),
		"gasUsed":           hexutil.Uint64(receipt.GasUsed),
		"cumulativeGasUsed": hexutil.Uint64(receipt.CumulativeGasUsed),
		"contractAddress":   nil,
		"logs":              receipt.Logs,
		"logsBloom":         types.CreateBloom(types.Receipts{receipt}),
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

func includes(addresses []common.Address, a common.Address) bool {
	for _, addr := range addresses {
		if addr == a {
			return true
		}
	}

	return false
}

// filterLogs creates a slice of logs matching the given criteria.
func filterLogs(logs []*types.Log, fromBlock, toBlock *big.Int, addresses []common.Address, topics [][]uint32) []*types.Log {
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
		if len(topics) > len(log.TopicIds) {
			continue Logs
		}
		for i, sub := range topics {
			match := len(sub) == 0 // empty rule set == wildcard
			for _, topic := range sub {
				if log.TopicIds[i] == topic {
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

func returnLogs(logs []*types.Log) []*types.Log {
	if logs == nil {
		return []*types.Log{}
	}
	return logs
}
