package migrations

import (
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/common/dbutils"
	"github.com/ledgerwatch/turbo-geth/common/etl"
	"github.com/ledgerwatch/turbo-geth/eth/stagedsync/stages"
	"github.com/ledgerwatch/turbo-geth/ethdb"
	"github.com/ledgerwatch/turbo-geth/log"
)

// migrations apply sequentially in order of this array, skips applied migrations
// it allows - don't worry about merge conflicts and use switch branches
// see also dbutils.Migrations - it stores context in which each transaction was exectured - useful for bug-reports
//
// Idempotency is expected
// Best practices to achieve Idempotency:
// - in dbutils/bucket.go add suffix for existing bucket variable, create new bucket with same variable name.
//		Example:
//			- SyncStageProgress = []byte("SSP1")
//			+ SyncStageProgressOld1 = []byte("SSP1")
//			+ SyncStageProgress = []byte("SSP2")
// - clear new bucket in the begining of transaction, drop old bucket in the end (not defer!).
//		Example:
//			Up: func(db ethdb.Database, datadir string, OnLoadCommit etl.LoadCommitHandler) error {
//				if err := db.(ethdb.NonTransactional).ClearBuckets(dbutils.SyncStageProgress); err != nil { // clear new bucket
//					return err
//				}
//
//				extractFunc := func(k []byte, v []byte, next etl.ExtractNextFunc) error {
//					... // migration logic
//				}
//              if err := etl.Transform(...); err != nil {
//					return err
//				}
//
//				if err := db.(ethdb.NonTransactional).DropBuckets(dbutils.SyncStageProgressOld1); err != nil {  // clear old bucket
//					return err
//				}
//			},
// - if you need migrate multiple buckets - create separate migration for each bucket
// - write test for new transaction
var migrations = []Migration{
	stagesToUseNamedKeys,
	unwindStagesToUseNamedKeys,
}

type Migration struct {
	Name string
	Up   func(db ethdb.Database, dataDir string, OnLoadCommit etl.LoadCommitHandler) error
}

func NewMigrator() *Migrator {
	return &Migrator{
		Migrations: migrations,
	}
}

type Migrator struct {
	Migrations []Migration
}

func (m *Migrator) Apply(db ethdb.Database, datadir string) error {
	if len(m.Migrations) == 0 {
		return nil
	}

	applied := map[string]bool{}
	db.Walk(dbutils.Migrations, nil, 0, func(k []byte, _ []byte) (bool, error) {
		applied[string(common.CopyBytes(k))] = true
		return true, nil
	})

	for _, v := range m.Migrations {
		if _, ok := applied[v.Name]; ok {
			continue
		}
		log.Info("Apply migration", "name", v.Name)
		if err := v.Up(db, datadir, func(putter ethdb.Putter, key []byte, isDone bool) error {
			if !isDone {
				return nil // don't save partial progress
			}
			stagesProgress, err := stages.MarshalAllStages(db)
			if err != nil {
				return err
			}
			err = db.Put(dbutils.Migrations, []byte(v.Name), stagesProgress)
			if err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}

		log.Info("Applied migration", "name", v.Name)
	}
	return nil
}
