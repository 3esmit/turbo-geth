package headerdownload

import (
	"bytes"
	"container/heap"
	"fmt"
	"io"
	"math/big"
	"os"

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
	hash            common.Hash
	blockHeight     uint64
	timestamp       uint64
}

type Tip struct {
	anchor               *Anchor
	cumulativeDifficulty uint256.Int
	timestamp            uint64
	difficulty           uint256.Int
	blockHeight          uint64
	uncleHash            common.Hash
	noPrepend            bool
}

type TipItem struct {
	tipHash              common.Hash
	cumulativeDifficulty uint256.Int
}

// First item in ChainSegment is the anchor
// ChainSegment must be contigous and must not include bad headers
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
	InvalidSealPenalty
	TooFarFuturePenalty
	TooFarPastPenalty
)

type PeerPenalty struct {
	// This type may also contain the "severity" of penalty, if we find that it helps
	peerHandle PeerHandle
	penalty    Penalty
	err        error // Underlying error if available
}

type RequestQueueItem struct {
	anchorParent common.Hash
	waitUntil    uint64
}

type RequestQueue []RequestQueueItem

// Request for chain segment starting with hash and going to its parent, etc, with length headers in total
type HeaderRequest struct {
	hash   common.Hash
	length int
}

type VerifySealFunc func(header *types.Header) error
type CalcDifficultyFunc func(childTimestamp uint64, parentTime uint64, parentDifficulty, parentNumber *big.Int, parentHash, parentUncleHash common.Hash) *big.Int

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
	initPowDepth           int    // powDepth assigned to the newly inserted anchor
	newAnchorFutureLimit   uint64 // How far in the future (relative to current time) the new anchors are allowed to be
	newAnchorPastLimit     uint64 // How far in the past (relative to current time) the new anchors are allowed to be
	highestTotalDifficulty uint256.Int
	requestQueue           *RequestQueue
	calcDifficultyFunc     CalcDifficultyFunc
	verifySealFunc         VerifySealFunc
}

func (a *TipItem) Less(b llrb.Item) bool {
	bi := b.(*TipItem)
	if a.cumulativeDifficulty.Eq(&bi.cumulativeDifficulty) {
		// hash is unique and it breaks the ties
		return bytes.Compare(a.tipHash[:], bi.tipHash[:]) < 0
	}
	return a.cumulativeDifficulty.Lt(&bi.cumulativeDifficulty)
}

func (rq RequestQueue) Len() int {
	return len(rq)
}

func (rq RequestQueue) Less(i, j int) bool {
	return rq[i].waitUntil < rq[j].waitUntil
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

func NewHeaderDownload(filesDir string,
	tipLimit, initPowDepth int,
	calcDifficultyFunc CalcDifficultyFunc,
	verifySealFunc VerifySealFunc,
	newAnchorFutureLimit, newAnchorPastLimit uint64,
) *HeaderDownload {
	hd := &HeaderDownload{
		filesDir:             filesDir,
		badHeaders:           make(map[common.Hash]struct{}),
		anchors:              make(map[common.Hash][]*Anchor),
		tips:                 make(map[common.Hash]*Tip),
		tipLimiter:           llrb.New(),
		tipLimit:             tipLimit,
		initPowDepth:         initPowDepth,
		requestQueue:         &RequestQueue{},
		calcDifficultyFunc:   calcDifficultyFunc,
		verifySealFunc:       verifySealFunc,
		newAnchorFutureLimit: newAnchorFutureLimit,
		newAnchorPastLimit:   newAnchorPastLimit,
	}
	heap.Init(hd.requestQueue)
	return hd
}

func (p Penalty) String() string {
	switch p {
	case NoPenalty:
		return "None"
	case BadBlockPenalty:
		return "BadBlock"
	case DuplicateHeaderPenalty:
		return "DuplicateHeader"
	case WrongChildBlockHeightPenalty:
		return "WrongChildBlockHeight"
	case WrongChildDifficultyPenalty:
		return "WrongChildDifficulty"
	case InvalidSealPenalty:
		return "InvalidSeal"
	case TooFarFuturePenalty:
		return "TooFarFuture"
	case TooFarPastPenalty:
		return "TooFarPast"
	default:
		return fmt.Sprintf("Unknown(%d)", p)
	}
}

func (pp PeerPenalty) String() string {
	return fmt.Sprintf("peerPenalty{peer: %d, penalty: %s, err: %v}", pp.peerHandle, pp.penalty, pp.err)
}
