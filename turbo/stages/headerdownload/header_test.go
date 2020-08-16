package headerdownload

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/holiman/uint256"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/core/types"
)

func TestHandleHeadersMsg(t *testing.T) {
	hd := NewHeaderDownload("", 10, func(childTimestamp uint64, parentTime uint64, parentDifficulty, parentNumber *big.Int, parentHash, parentUncleHash common.Hash) *big.Int {
		// To get child difficulty, we just add 1000 to the parent difficulty
		return big.NewInt(0).Add(parentDifficulty, big.NewInt(1000))
	}, nil)
	peer := PeerHandle(1)

	// Empty message
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 0 {
			t.Errorf("expected no chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Single header
	var h types.Header
	h.Number = big.NewInt(5)
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 1 {
			t.Errorf("expected 1 chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Same header repeated twice
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h, &h}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != DuplicateHeaderPenalty {
			t.Errorf("expected DuplicateHeader penalty, got %s", peerPenalty)
		}
		if chainSegments != nil {
			t.Errorf("expected no chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Single header with a bad hash
	hd.badHeaders[h.Hash()] = struct{}{}
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != BadBlockPenalty {
			t.Errorf("expected BadBlock penalty, got %s", peerPenalty)
		}
		if chainSegments != nil {
			t.Errorf("expected no chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Two connected headers
	var h1, h2 types.Header
	h1.Number = big.NewInt(1)
	h1.Difficulty = big.NewInt(10)
	h2.Number = big.NewInt(2)
	h2.Difficulty = big.NewInt(1010)
	h2.ParentHash = h1.Hash()
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h1, &h2}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 1 {
			t.Errorf("expected 1 chainSegments, got %d", len(chainSegments))
		}
		if len(chainSegments[0].headers) != 2 {
			t.Errorf("expected chainSegment of the length 2, got %d", len(chainSegments[0].headers))
		}
		if chainSegments[0].headers[0] != &h1 {
			t.Errorf("expected h1 to be the root")
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Two connected headers with wrong numbers
	h2.Number = big.NewInt(3) // Child number 3, parent number 1
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h1, &h2}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != WrongChildBlockHeightPenalty {
			t.Errorf("expected WrongChildBlockHeight penalty, got %s", peerPenalty)
		}
		if chainSegments != nil {
			t.Errorf("expected no chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Two connected headers with wrong difficulty
	h2.Number = big.NewInt(2)        // Child number 2, parent number 1
	h2.Difficulty = big.NewInt(2000) // Expected difficulty 10 + 1000 = 1010
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h1, &h2}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != WrongChildDifficultyPenalty {
			t.Errorf("expected WrongChildDifficulty penalty, got %s", peerPenalty)
		}
		if chainSegments != nil {
			t.Errorf("expected no chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Two headers connected to the third header
	h2.Difficulty = big.NewInt(1010) // Fix difficulty of h2
	var h3 types.Header
	h3.Number = big.NewInt(2)
	h3.Difficulty = big.NewInt(1010)
	h3.ParentHash = h1.Hash()
	h3.Extra = []byte("I'm different") // To make sure the hash of h3 is different from the hash of h2
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h1, &h2, &h3}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 1 {
			t.Errorf("expected 1 chainSegments, got %d", len(chainSegments))
		}
		if len(chainSegments[0].headers) != 3 {
			t.Errorf("expected chainSegment of the length 3, got %d", len(chainSegments[0].headers))
		}
		if chainSegments[0].headers[0] != &h1 {
			t.Errorf("expected h1 to be the root")
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Same three headers, but in a reverse order
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h3, &h2, &h1}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 1 {
			t.Errorf("expected 1 chainSegments, got %d", len(chainSegments))
		}
		if len(chainSegments[0].headers) != 3 {
			t.Errorf("expected chainSegment of the length 3, got %d", len(chainSegments[0].headers))
		}
		if chainSegments[0].headers[0] != &h1 {
			t.Errorf("expected h1 to be the root")
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Two headers not connected to each other
	if chainSegments, peerPenalty, err := hd.HandleHeadersMsg([]*types.Header{&h3, &h2}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 2 {
			t.Errorf("expected 2 chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}
}

func TestHandleNewBlockMsg(t *testing.T) {
	hd := NewHeaderDownload("", 10, func(childTimestamp uint64, parentTime uint64, parentDifficulty, parentNumber *big.Int, parentHash, parentUncleHash common.Hash) *big.Int {
		// To get child difficulty, we just add 1000 to the parent difficulty
		return big.NewInt(0).Add(parentDifficulty, big.NewInt(1000))
	}, nil)
	peer := PeerHandle(1)
	var h types.Header
	h.Number = big.NewInt(5)
	if chainSegments, peerPenalty, err := hd.HandleNewBlockMsg(&h, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if len(chainSegments) != 1 {
			t.Errorf("expected 1 chainSegments, got %d", len(chainSegments))
		}
		if len(chainSegments[0].headers) != 1 {
			t.Errorf("expected chainSegment of the length 1, got %d", len(chainSegments[0].headers))
		}
		if chainSegments[0].headers[0] != &h {
			t.Errorf("expected h to be the root")
		}
	} else {
		t.Errorf("handle header msg: %v", err)
	}

	// Same header with a bad hash
	hd.badHeaders[h.Hash()] = struct{}{}
	if chainSegments, peerPenalty, err := hd.HandleNewBlockMsg(&h, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != BadBlockPenalty {
			t.Errorf("expected BadBlock penalty, got %s", peerPenalty)
		}
		if chainSegments != nil {
			t.Errorf("expected no chainSegments, got %d", len(chainSegments))
		}
	} else {
		t.Errorf("handle newBlock msg: %v", err)
	}
}

func TestPrepend(t *testing.T) {
	hd := NewHeaderDownload("", 10, func(childTimestamp uint64, parentTime uint64, parentDifficulty, parentNumber *big.Int, parentHash, parentUncleHash common.Hash) *big.Int {
		// To get child difficulty, we just add 1000 to the parent difficulty
		return big.NewInt(0).Add(parentDifficulty, big.NewInt(1000))
	}, func(header *types.Header) error {
		return nil
	},
	)
	peer := PeerHandle(1)
	// empty chain segment - returns error
	if _, _, err := hd.Prepend(&ChainSegment{}, peer); err == nil {
		t.Errorf("preprend for empty segment - expected error")
	}

	// single header in the chain segment
	var h types.Header
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if ok {
			t.Errorf("did not expect to prepend")
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// single header attaching to a single existing tip
	var h1, h2 types.Header
	h1.Number = big.NewInt(1)
	h1.Difficulty = big.NewInt(10)
	h2.Number = big.NewInt(2)
	h2.Difficulty = big.NewInt(1010)
	h2.ParentHash = h1.Hash()
	if err := hd.addHeaderAsTip(&h1, h1.ParentHash, new(uint256.Int).SetUint64(2000)); err != nil {
		t.Fatalf("setting up h1: %v", err)
	}
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h2}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if !ok {
			t.Errorf("expected to prepend")
		}
		if len(hd.tips) != 2 {
			t.Errorf("expected 2 tips, got %d", len(hd.tips))
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// two connected headers attaching to the the highest tip
	var h3, h4 types.Header
	h3.Number = big.NewInt(3)
	h3.Difficulty = big.NewInt(2010)
	h3.ParentHash = h2.Hash()
	h4.Number = big.NewInt(4)
	h4.Difficulty = big.NewInt(3010)
	h4.ParentHash = h3.Hash()
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h3, &h4}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if !ok {
			t.Errorf("expected to prepend")
		}
		if len(hd.tips) != 4 {
			t.Errorf("expected 4 tips, got %d", len(hd.tips))
		}
		if !hd.tips[h4.Hash()].cumulativeDifficulty.Eq(new(uint256.Int).SetUint64(2000 + 1010 + 2010 + 3010)) {
			t.Errorf("cumulative difficulty of h4 expected %d, got %d", 2000+1010+2010+3010, hd.tips[h4.Hash()].cumulativeDifficulty.ToBig())
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// one header attaching not to the highest tip
	var h41 types.Header
	h41.Number = big.NewInt(4)
	h41.Difficulty = big.NewInt(3010)
	h41.Extra = []byte("Extra")
	h41.ParentHash = h3.Hash()
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h41}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if !ok {
			t.Errorf("expected to prepend")
		}
		if len(hd.tips) != 5 {
			t.Errorf("expected 5 tips, got %d", len(hd.tips))
		}
		if !hd.tips[h41.Hash()].cumulativeDifficulty.Eq(new(uint256.Int).SetUint64(2000 + 1010 + 2010 + 3010)) {
			t.Errorf("cumulative difficulty of h41 expected %d, got %d", 2000+1010+2010+3010, hd.tips[h41.Hash()].cumulativeDifficulty.ToBig())
		}
		if hd.tips[h41.Hash()].anchorParent != h1.ParentHash {
			t.Errorf("Expected h41 anchorParent to be %x, got %x", h1.ParentHash, hd.tips[h41.Hash()].anchorParent)
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// trying to attach header with wrong block height
	var h5 types.Header
	h5.Number = big.NewInt(6) // Wrong (expected 5)
	h5.Difficulty = big.NewInt(4010)
	h5.ParentHash = h4.Hash()
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h5}}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != WrongChildBlockHeightPenalty {
			t.Errorf("expected WrongChildBlockHeight penalty, got %s", peerPenalty)
		}
		if ok {
			t.Errorf("did not expect to prepend")
		}
		if len(hd.tips) != 5 {
			t.Errorf("expected 5 tips, got %d", len(hd.tips))
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// trying to attach header with wrong difficulty
	h5.Number = big.NewInt(5)        // Now correct
	h5.Difficulty = big.NewInt(4020) // Wrong - expected 4010
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h5}}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != WrongChildDifficultyPenalty {
			t.Errorf("expected WrongChildDifficulty penalty, got %s", peerPenalty)
		}
		if ok {
			t.Errorf("did not expect to prepend")
		}
		if len(hd.tips) != 5 {
			t.Errorf("expected 5 tips, got %d", len(hd.tips))
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// trying to attach header with wrong PoW
	hd.verifySealFunc = func(header *types.Header) error {
		if header.Nonce.Uint64() > 0 {
			return fmt.Errorf("wrong nonce: %d", header.Nonce)
		}
		return nil
	}
	h5.Difficulty = big.NewInt(4010) // Now correct
	h5.Nonce = types.EncodeNonce(1)
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h5}}, peer); err == nil {
		if peerPenalty == nil || peerPenalty.peerHandle != peer || peerPenalty.penalty != InvalidSealPenalty {
			t.Errorf("expected InvalidSeal penalty, got %s", peerPenalty)
		}
		if ok {
			t.Errorf("did not expect to prepend")
		}
		if len(hd.tips) != 5 {
			t.Errorf("expected 5 tips, got %d", len(hd.tips))
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// trying to attach header not connected to any tips
	var h6 types.Header
	h6.Number = big.NewInt(6)
	h6.Difficulty = big.NewInt(5010)
	h6.ParentHash = h5.Hash()
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h6}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if ok {
			t.Errorf("did not expect to prepend")
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	// Introduce h51 as a tip and prepend h6
	if err := hd.addHeaderAsTip(&h5, h5.ParentHash, new(uint256.Int).SetUint64(2000)); err != nil {
		t.Fatalf("setting up h5: %v", err)
	}
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h6}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if !ok {
			t.Errorf("expected to prepend")
		}
		if len(hd.tips) != 7 {
			t.Errorf("expected 7 tips, got %d", len(hd.tips))
		}
		if !hd.tips[h6.Hash()].cumulativeDifficulty.Eq(new(uint256.Int).SetUint64(2000 + 5010)) {
			t.Errorf("cumulative difficulty of h6 expected %d, got %d", 2000+5010, hd.tips[h6.Hash()].cumulativeDifficulty.ToBig())
		}
		if hd.tips[h6.Hash()].anchorParent != h5.ParentHash {
			t.Errorf("Expected h6 anchorParent to be %x, got %x", h5.ParentHash, hd.tips[h6.Hash()].anchorParent)
		}
	} else {
		t.Errorf("prepend: %v", err)
	}

	var h7 types.Header
	h7.Number = big.NewInt(7)
	h7.Difficulty = big.NewInt(6010)
	h7.ParentHash = common.HexToHash("0x4354543543959438594359348990345893408")
	// Introduce hard-coded tip
	if err := hd.addHardCodedTip(10, 5555, h7.Hash(), h7.ParentHash, new(uint256.Int).SetUint64(2000)); err != nil {
		t.Fatalf("setting up h7: %v", err)
	}

	// Try to prepend to the hard-coded tip
	var h8 types.Header
	h8.Number = big.NewInt(8)
	h8.Difficulty = big.NewInt(7010)
	h8.ParentHash = h7.Hash()
	if ok, peerPenalty, err := hd.Prepend(&ChainSegment{headers: []*types.Header{&h8}}, peer); err == nil {
		if peerPenalty != nil {
			t.Errorf("unexpected penalty: %s", peerPenalty)
		}
		if ok {
			t.Errorf("did not expect to prepend")
		}
		if len(hd.tips) != 8 {
			t.Errorf("expected 8 tips, got %d", len(hd.tips))
		}
	} else {
		t.Errorf("prepend: %v", err)
	}
}
