package stagedsync

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	"github.com/holiman/uint256"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/core/types"
	"github.com/petar/GoLLRB/llrb"
)

type Anchor struct {
	powDepth        int
	totalDifficulty uint256.Int
	tips            []common.Hash
	difficulty      uint256.Int
	timestamp       uint64
}

type Tip struct {
	anchorParent         common.Hash
	cumulativeDifficulty uint256.Int
	timestamp            uint64
	difficulty           uint256.Int
	blockHeight          uint64
	noPrepend            bool
}

// First item in ChainSegment is the anchor
// ChainSegment must be contiguous and must not include bad headers
type ChainSegment struct {
	headers []*types.Header
}

type PeerHandle int // This is int just for the PoC phase - will be replaced by more appropriate type to find a peer

type Penalty int

const (
	NoPenalty Penalty = iota
	BadBlockPenalty
	DuplicateHeaderPenalty
	WrongChildBlockHeightPenalty
	WrongChildDifficultyPenalty
)

type PeerPenalty struct {
	// This type may also contain the "severity" of penalty, if we find that it helps
	peerHandle PeerHandle
	penalty    Penalty
}

type RequestQueueItem struct {
	anchorParent common.Hash
	requestTime  time.Time
}

type RequestQueue []RequestQueueItem

type CalcDifficultyFunc func(childTimestamp uint64, parent *types.Header) *big.Int

type HeaderDownload struct {
	buffer                 []*types.Header
	filesDir               string
	currentFile            *os.File
	currentFileWriter      io.Writer
	badHeaders             map[common.Hash]struct{}
	anchors                map[common.Hash][]*Anchor // Mapping from parentHash to collection of anchors
	tips                   map[common.Hash]*Tip
	tipLimiter             *llrb.LLRB
	tipLimit               int
	highestTotalDifficulty uint256.Int
	requestQueue           *RequestQueue
	calcDifficultyFunc     CalcDifficultyFunc
}

func (a *Tip) Less(b llrb.Item) bool {
	bi := b.(*Tip)
	return a.cumulativeDifficulty.Lt(&bi.cumulativeDifficulty)
}

func (rq RequestQueue) Len() int {
	return len(rq)
}

func (rq RequestQueue) Less(i, j int) bool {
	return rq[i].requestTime.Before(rq[j].requestTime)
}

func (rq RequestQueue) Swap(i, j int) {
	rq[i], rq[j] = rq[j], rq[i]
}

func (rq *RequestQueue) Push(x interface{}) {
	// Push and Pop use pointer receivers because they modify the slice's length,
	// not just its contents.
	*rq = append(*rq, x.(RequestQueueItem))
}

func (rq *RequestQueue) Pop() interface{} {
	old := *rq
	n := len(old)
	x := old[n-1]
	*rq = old[0 : n-1]
	return x
}

func NewHeaderDownload(filesDir string, tipLimit int, calcDifficultyFunc CalcDifficultyFunc) *HeaderDownload {
	return &HeaderDownload{
		filesDir:           filesDir,
		badHeaders:         make(map[common.Hash]struct{}),
		anchors:            make(map[common.Hash][]*Anchor),
		tips:               make(map[common.Hash]*Tip),
		tipLimiter:         llrb.New(),
		tipLimit:           tipLimit,
		calcDifficultyFunc: calcDifficultyFunc,
	}
}

func (pp PeerPenalty) String() string {
	var penaltyStr string
	switch pp.penalty {
	case NoPenalty:
		penaltyStr = "None"
	case BadBlockPenalty:
		penaltyStr = "BadBlock"
	case DuplicateHeaderPenalty:
		penaltyStr = "DuplicateHeader"
	case WrongChildBlockHeightPenalty:
		penaltyStr = "WrongChildBlockHeight"
	case WrongChildDifficultyPenalty:
		penaltyStr = "WrongChildDifficulty"
	default:
		penaltyStr = fmt.Sprintf("Unknown(%d)", pp.penalty)
	}
	return fmt.Sprintf("peerPenalty{peer: %d, penalty: %s}", pp.peerHandle, penaltyStr)
}
