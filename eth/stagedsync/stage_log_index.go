package stagedsync

import (
	"context"
	"encoding/binary"
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/RoaringBitmap/gocroaring"
	"github.com/c2h5oh/datasize"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/ethdb/bitmapdb"
	"github.com/ledgerwatch/turbo-geth/log"
	"github.com/ledgerwatch/turbo-geth/rlp"
)

const (
	logIndicesMemLimit       = 512 * datasize.MB
	logIndicesCheckSizeEvery = 30 * time.Second
)

func SpawnLogIndex(s *StageState, db ethdb.Database, datadir string, quit <-chan struct{}) error {
	var tx ethdb.DbWithPendingMutations
	var useExternalTx bool
	if hasTx, ok := db.(ethdb.HasTx); ok && hasTx.Tx() != nil {
		tx = db.(ethdb.DbWithPendingMutations)
		useExternalTx = true
	} else {
		var err error
		tx, err = db.Begin(context.Background())
		if err != nil {
			return err
		}
		defer tx.Rollback()
	}

	endBlock, err := s.ExecutionAt(tx)
	if err != nil {
		return fmt.Errorf("logs index: getting last executed block: %w", err)
	}
	if endBlock == s.BlockNumber {
		s.Done()
		return nil
	}

	start := s.BlockNumber
	if start > 0 {
		start++
	}

	if err := promoteLogIndex(tx, start, quit); err != nil {
		return err
	}

	if err := s.DoneAndUpdate(tx, endBlock); err != nil {
		return err
	}
	if !useExternalTx {
		if _, err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func promoteLogIndex(tx ethdb.DbWithPendingMutations, start uint64, quit <-chan struct{}) error {
	logEvery := time.NewTicker(30 * time.Second)
	defer logEvery.Stop()

	topics := map[string]*gocroaring.Bitmap{}
	addresses := map[string]*gocroaring.Bitmap{}
	logTopicIndexCursor := tx.(ethdb.HasTx).Tx().Cursor(dbutils.LogTopicIndex)
	logAddrIndexCursor := tx.(ethdb.HasTx).Tx().Cursor(dbutils.LogAddressIndex)
	receipts := tx.(ethdb.HasTx).Tx().Cursor(dbutils.BlockReceiptsPrefix)
	checkFlushEvery := time.NewTicker(logIndicesCheckSizeEvery)
	defer checkFlushEvery.Stop()

	for k, v, err := receipts.Seek(dbutils.EncodeBlockNumber(start)); k != nil; k, v, err = receipts.Next() {
		if err != nil {
			return err
		}

		if err := common.Stopped(quit); err != nil {
			return err
		}
		blockNum := binary.BigEndian.Uint64(k[:8])

		select {
		default:
		case <-logEvery.C:
			sz, err := tx.(ethdb.HasTx).Tx().BucketSize(dbutils.LogTopicIndex)
			if err != nil {
				return err
			}
			sz2, err := tx.(ethdb.HasTx).Tx().BucketSize(dbutils.LogAddressIndex)
			if err != nil {
				return err
			}
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			log.Info("Progress", "blockNum", blockNum, dbutils.LogTopicIndex, common.StorageSize(sz), dbutils.LogAddressIndex, common.StorageSize(sz2), "alloc", common.StorageSize(m.Alloc), "sys", common.StorageSize(m.Sys))
		case <-checkFlushEvery.C:
			if err := flushBitmaps(logTopicIndexCursor, topics); err != nil {
				return err
			}

			topics = map[string]*gocroaring.Bitmap{}
			if err := flushBitmaps(logAddrIndexCursor, addresses); err != nil {
				return err
			}

			addresses = map[string]*gocroaring.Bitmap{}
		}

		// Convert the receipts from their storage form to their internal representation
		storageReceipts := []*types.ReceiptForStorage{}
		if err := rlp.DecodeBytes(v, &storageReceipts); err != nil {
			return fmt.Errorf("invalid receipt array RLP: %w, blocl=%d", err, blockNum)
		}

		for _, receipt := range storageReceipts {
			for _, log := range receipt.Logs {
				for _, topic := range log.Topics {
					topicStr := string(topic.Bytes())
					m, ok := topics[topicStr]
					if !ok {
						m = gocroaring.New()
						topics[topicStr] = m
					}
					m.Add(uint32(blockNum))
				}

				accStr := string(log.Address.Bytes())
				m, ok := addresses[accStr]
				if !ok {
					m = gocroaring.New()
					addresses[accStr] = m
				}
				m.Add(uint32(blockNum))
			}
		}
	}

	if err := flushBitmaps(logTopicIndexCursor, topics); err != nil {
		return err
	}
	if err := flushBitmaps(logAddrIndexCursor, addresses); err != nil {
		return err
	}
	return nil
}

func UnwindLogIndex(u *UnwindState, s *StageState, db ethdb.Database, quitCh <-chan struct{}) error {
	var tx ethdb.DbWithPendingMutations
	var useExternalTx bool
	if hasTx, ok := db.(ethdb.HasTx); ok && hasTx.Tx() != nil {
		tx = db.(ethdb.DbWithPendingMutations)
		useExternalTx = true
	} else {
		var err error
		tx, err = db.Begin(context.Background())
		if err != nil {
			return err
		}
		defer tx.Rollback()
	}

	if err := unwindLogIndex(tx, s.BlockNumber, u.UnwindPoint, quitCh); err != nil {
		return err
	}

	if err := u.Done(tx); err != nil {
		return fmt.Errorf("unwind AccountHistorytIndex: %w", err)
	}

	if !useExternalTx {
		if _, err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func unwindLogIndex(tx ethdb.Database, from, to uint64, quitCh <-chan struct{}) error {
	topics := map[string]bool{}
	addrs := map[string]bool{}
	addrIndex := tx.(ethdb.HasTx).Tx().Cursor(dbutils.LogAddressIndex)
	topicIndex := tx.(ethdb.HasTx).Tx().Cursor(dbutils.LogTopicIndex)

	receipts := tx.(ethdb.HasTx).Tx().Cursor(dbutils.BlockReceiptsPrefix)
	start := dbutils.EncodeBlockNumber(to + 1)
	for k, v, err := receipts.Seek(start); k != nil; k, v, err = receipts.Next() {
		if err != nil {
			return err
		}
		if err := common.Stopped(quitCh); err != nil {
			return err
		}
		// Convert the receipts from their storage form to their internal representation
		storageReceipts := []*types.ReceiptForStorage{}
		if err := rlp.DecodeBytes(v, &storageReceipts); err != nil {
			return fmt.Errorf("invalid receipt array RLP: %w, k=%x", err, k)
		}

		for _, storageReceipt := range storageReceipts {
			for _, log := range storageReceipt.Logs {
				for _, topic := range log.Topics {
					topics[string(topic.Bytes())] = true
				}
				addrs[string(log.Address.Bytes())] = true
			}
		}
	}

	if err := truncateBitmaps(topicIndex, topics, to+1, from+1); err != nil {
		return err
	}
	if err := truncateBitmaps(addrIndex, addrs, to+1, from+1); err != nil {
		return err
	}
	return nil
}

func needFlush(bitmaps map[string]*gocroaring.Bitmap, singleLimit datasize.ByteSize) bool {
	for _, m := range bitmaps {
		if m.SerializedSizeInBytes() > int(singleLimit) {
			return true
		}
	}
	return false
}

func flushBitmaps(c ethdb.Cursor, inMem map[string]*gocroaring.Bitmap) error {
	defer func(t time.Time) { fmt.Printf("dbutils.go:258: %s\n", time.Since(t)) }(time.Now())
	keys := make([]string, 0, len(inMem))
	for k := range inMem {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		b := inMem[k]
		if err := bitmapdb.AppendMergeByOr2(c, []byte(k), b); err != nil {
			return err
		}
	}

	return nil
}

func truncateBitmaps(c ethdb.Cursor, inMem map[string]bool, from, to uint64) error {
	keys := make([]string, 0, len(inMem))
	for k := range inMem {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := bitmapdb.TruncateRange2(c, []byte(k), from, to); err != nil {
			return nil
		}
	}

	return nil
}
