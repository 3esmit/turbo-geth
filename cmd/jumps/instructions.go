package static

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/davecgh/go-spew/spew"

	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/core/vm"
)

var (
	ErrInvalidJump   = errors.New("evm: invalid jump destination")
	ErrNonStatic     = errors.New("non static jump")
	ErrNoValueStatic = errors.New("no value")
	ErrReturn        = errors.New("op.RETURN")
	ErrRevert        = errors.New("op.REVERT")
	ErrSelfDestruct  = errors.New("op.SELFDESTRUCT")
	ErrStop          = errors.New("op.STOP")

	ErrTimeout = errors.New("execution timeout")
)

func NotStaticIfOneNotStatic(cmds ...*cell) bool {
	for _, cmd := range cmds {
		if !cmd.static {
			return false
		}
	}
	return true
}

func opAdd(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = NotStaticIfOneNotStatic(x, y)
	y.AddHistory(vm.ADD, *pc, y.static)

	return nil, nil
}

func opSub(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = NotStaticIfOneNotStatic(x, y)
	y.AddHistory(vm.SUB, *pc, y.static)

	return nil, nil
}

func opMul(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y.static = NotStaticIfOneNotStatic(x, y)
	y.AddHistory(vm.MUL, *pc, y.static)

	return nil, nil
}

func opDiv(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	if y.static && y.IsValue() {
		if y.v.Sign() == 0 {
			y.static = true
		}
	} else {
		y.static = NotStaticIfOneNotStatic(x, y)
	}

	y.AddHistory(vm.DIV, *pc, y.static)

	return nil, nil
}

func opSdiv(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}

	var res *cell
	if y.Sign() || x.Sign() {
		res = NewStaticCell()
	} else {
		res = NewCell(NotStaticIfOneNotStatic(x, y))
	}

	res.AddHistory(vm.SDIV, *pc, res.static)
	stack.push(res)

	return nil, nil
}

func opMod(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}

	if y.Sign() {
		x.static = true
	} else {
		x.static = NotStaticIfOneNotStatic(x, y)
	}

	x.AddHistory(vm.MOD, *pc, x.static)
	stack.push(x)

	return nil, nil
}

func opSmod(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}

	var res *cell
	if y.Sign() {
		res = NewStaticCell()
	} else {
		res = NewCell(NotStaticIfOneNotStatic(x, y))
	}

	res.AddHistory(vm.SMOD, *pc, res.static)
	stack.push(res)

	return nil, nil
}

func opExp(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	base, err := stack.pop()
	if err != nil {
		return nil, err
	}
	exponent, err := stack.pop()
	if err != nil {
		return nil, err
	}

	var res *cell
	if base.Sign() {
		res = NewStaticCell()
	} else {
		res = NewCell(NotStaticIfOneNotStatic(base, exponent))
	}

	/*
		//fixme skipped until we dont calculate exact values on stack
		// some shortcuts
		cmpToOne := exponent.Cmp(big1)
		if cmpToOne < 0 { // Exponent is zero
			// x ^ 0 == 1
			stack.push(base.SetUint64(1))
		} else if cmpToOne == 0 { // Exponent is one
			// x ^ 1 == x
			stack.push(base)
		} else {
			stack.push(math.Exp(base, exponent))
		}
	*/

	res.AddHistory(vm.EXP, *pc, res.static)
	stack.push(res)

	return nil, nil
}

// fixme: isStatic depends on code and data
func opSignExtend(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	/*
		back := stack.pop()
		if back.Cmp(big.NewInt(31)) < 0 {
			bit := uint(back.Uint64()*8 + 7)
			num := stack.pop()
			mask := back.Lsh(common.Big1, bit)
			mask.Sub(mask, common.Big1)
			if num.Bit(int(bit)) > 0 {
				num.Or(num, mask.Not(mask))
			} else {
				num.And(num, mask)
			}

			stack.push(math.U256(num))
		}
	*/

	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func opNot(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, _ *Stack) ([]byte, error) {
	return nil, nil
}

func opLt(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}
	y.static = true
	y.AddHistory(vm.LT, *pc, y.static)

	return nil, nil
}

func opGt(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}
	y.static = true
	y.AddHistory(vm.LT, *pc, y.static)

	return nil, nil
}

func opSlt(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = true
	y.AddHistory(vm.SLT, *pc, y.static)

	return nil, nil
}

func opSgt(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = true
	y.AddHistory(vm.SGT, *pc, y.static)

	return nil, nil
}

func opEq(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = true
	y.AddHistory(vm.EQ, *pc, y.static)

	return nil, nil
}

func opIszero(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.peek()
	if err != nil {
		return nil, err
	}

	x.static = true
	x.AddHistory(vm.ISZERO, *pc, x.static)

	return nil, nil
}

func opAnd(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}

	x.static = NotStaticIfOneNotStatic(x, y)
	x.AddHistory(vm.AND, *pc, x.static)
	stack.push(x)

	return nil, nil
}

func opOr(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = NotStaticIfOneNotStatic(x, y)
	y.AddHistory(vm.OR, *pc, y.static)
	stack.push(y)

	return nil, nil
}

func opXor(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}

	y, err := stack.peek()
	if err != nil {
		return nil, err
	}

	y.static = NotStaticIfOneNotStatic(x, y)
	y.AddHistory(vm.XOR, *pc, y.static)
	stack.push(y)

	return nil, nil
}

func opByte(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	th, err := stack.pop()
	if err != nil {
		return nil, err
	}
	val, err := stack.peek()
	if err != nil {
		return nil, err
	}

	val.static = NotStaticIfOneNotStatic(th, val) // it could be TRUE, but let's decide a bit more strict
	val.AddHistory(vm.BYTE, *pc, val.static)

	/*
		if th.Cmp(common.Big32) < 0 {
			b := math.Byte(val, 32, int(th.Int64()))
			val.SetUint64(uint64(b))
		} else {
			val.SetUint64(0)
		}
	*/

	return nil, nil
}

func opAddmod(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}
	z, err := stack.pop()
	if err != nil {
		return nil, err
	}

	x.static = NotStaticIfOneNotStatic(x, y, z) // it could be (x, z), but let's decide a bit more strict
	x.AddHistory(vm.ADDMOD, *pc, x.static)
	stack.push(x)

	return nil, nil
}

func opMulmod(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	x, err := stack.pop()
	if err != nil {
		return nil, err
	}
	y, err := stack.pop()
	if err != nil {
		return nil, err
	}
	z, err := stack.pop()
	if err != nil {
		return nil, err
	}

	x.static = NotStaticIfOneNotStatic(x, y, z) // it could be (x, z), but let's decide a bit more strict
	x.AddHistory(vm.MULMOD, *pc, x.static)
	stack.push(x)

	return nil, nil
}

// opSHL implements Shift Left
// The SHL instruction (shift left) pops 2 values from the stack, first arg1 and then arg2,
// and pushes on the stack arg2 shifted to the left by arg1 number of bits.
func opSHL(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Note, second operand is left in the stack; accumulate result into it, and no need to push it afterwards
	shift, err := stack.pop()
	if err != nil {
		return nil, err
	}
	value, err := stack.peek()
	if err != nil {
		return nil, err
	}

	value.static = NotStaticIfOneNotStatic(shift, value) // it could be SAME, but let's decide a bit more strict
	value.AddHistory(vm.SHL, *pc, value.static)

	return nil, nil
}

// opSHR implements Logical Shift Right
// The SHR instruction (logical shift right) pops 2 values from the stack, first arg1 and then arg2,
// and pushes on the stack arg2 shifted to the right by arg1 number of bits with zero fill.
func opSHR(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Note, second operand is left in the stack; accumulate result into it, and no need to push it afterwards
	shift, err := stack.pop()
	if err != nil {
		return nil, err
	}
	value, err := stack.peek()
	if err != nil {
		return nil, err
	}

	value.static = NotStaticIfOneNotStatic(shift, value) // it could be SAME, but let's decide a bit more strict
	value.AddHistory(vm.SHR, *pc, value.static)

	return nil, nil
}

// opSAR implements Arithmetic Shift Right
// The SAR instruction (arithmetic shift right) pops 2 values from the stack, first arg1 and then arg2,
// and pushes on the stack arg2 shifted to the right by arg1 number of bits with sign extension.
func opSAR(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Note, S256 returns (potentially) a new bigint, so we're popping, not peeking this one
	shift, err := stack.pop()
	if err != nil {
		return nil, err
	}
	value, err := stack.pop()
	if err != nil {
		return nil, err
	}

	value.static = NotStaticIfOneNotStatic(shift, value) // it could be SAME, but let's decide a bit more strict
	value.AddHistory(vm.SAR, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opSha3(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell() // fixme it's stricter than it could be
	value.AddHistory(vm.SHA3, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opAddress(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewStaticCell()
	value.AddHistory(vm.ADDRESS, *pc, value.static)

	stack.push(value)
	return nil, nil
}

func opBalance(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	slot, err := stack.peek()
	if err != nil {
		return nil, err
	}
	slot.static = false
	slot.AddHistory(vm.BALANCE, *pc, slot.static)

	return nil, nil
}

func opOrigin(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.ORIGIN, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opCaller(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.CALLER, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opCallValue(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.CALLVALUE, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opCallDataLoad(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.CALLDATALOAD, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opCallDataSize(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.CALLDATASIZE, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opCallDataCopy(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func opReturnDataSize(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.RETURNDATASIZE, *pc, value.static)
	stack.push(NewNonStaticCell()) // fixme: stricter than it could be

	return nil, nil
}

func opReturnDataCopy(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func opExtCodeSize(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	slot, err := stack.peek()
	if err != nil {
		return nil, err
	}

	if slot.IsStatic() && slot.IsValue() {
		slot.static = true
	} else {
		slot.static = false
	}
	slot.AddHistory(vm.EXTCODECOPY, *pc, slot.static)

	return nil, nil
}

func opCodeSize(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewStaticCell()
	value.AddHistory(vm.CODESIZE, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opCodeCopy(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func opExtCodeCopy(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// opExtCodeHash returns the code hash of a specified account.
// There are several cases when the function is called, while we can relay everything
// to `state.GetCodeHash` function to ensure the correctness.
//   (1) Caller tries to get the code hash of a normal contract account, state
// should return the relative code hash and set it as the result.
//
//   (2) Caller tries to get the code hash of a non-existent account, state should
// return common.Hash{} and zero will be set as the result.
//
//   (3) Caller tries to get the code hash for an account without contract code,
// state should return emptyCodeHash(0xc5d246...) as the result.
//
//   (4) Caller tries to get the code hash of a precompiled account, the result
// should be zero or emptyCodeHash.
//
// It is worth noting that in order to avoid unnecessary create and clean,
// all precompile accounts on mainnet have been transferred 1 wei, so the return
// here should be emptyCodeHash.
// If the precompile account is not transferred any amount on a private or
// customized chain, the return value will be zero.
//
//   (5) Caller tries to get the code hash for an account which is marked as suicided
// in the current transaction, the code hash of this account should be returned.
//
//   (6) Caller tries to get the code hash for an account which is marked as deleted,
// this account should be regarded as a non-existent account and zero should be returned.
func opExtCodeHash(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	slot, err := stack.peek()
	if err != nil {
		return nil, err
	}

	if slot.IsStatic() && slot.IsValue() {
		slot.static = true
	} else {
		slot.static = false
	}
	slot.AddHistory(vm.EXTCODEHASH, *pc, slot.static)

	return nil, nil
}

func opGasprice(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.GASPRICE, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opBlockhash(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	slot, err := stack.pop()
	if err != nil {
		return nil, err
	}

	if slot.IsStatic() && slot.IsValue() {
		slot.static = true
	} else {
		slot.static = false
	}
	slot.AddHistory(vm.BLOCKHASH, *pc, slot.static)
	stack.push(slot)

	return nil, nil
}

func opCoinbase(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.COINBASE, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opTimestamp(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.TIMESTAMP, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opNumber(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.NUMBER, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opDifficulty(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.DIFFICULTY, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opGasLimit(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.GASLIMIT, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opPop(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func opMload(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	v, err := stack.peek()
	if err != nil {
		return nil, err
	}

	v.static = false // fixme: not true if we introduce a memory fake
	v.AddHistory(vm.MLOAD, *pc, v.static)

	return nil, nil
}

func opMstore(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func opMstore8(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func opSload(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	loc, err := stack.peek()
	if err != nil {
		return nil, err
	}

	loc.static = false // fixme: not true if we introduce memory type
	loc.AddHistory(vm.SLOAD, *pc, loc.static)

	return nil, nil
}

func opSstore(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func opJump(pc *uint64, _ *vm.EVMInterpreter, contract *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	pos, err := stack.pop()
	if err != nil {
		return nil, err
	}

	/*
	if pos.static {
		fmt.Printf("jumpiT: on %x to %v\nValue history %v\n", *pc, pos.v, pos.History())
	}
	*/

	if !pos.static || !pos.IsValue() {
		return nil, fmt.Errorf("opJump: %w on %v\nValue history %v\n", ErrNonStatic, spew.Sdump(pc), pos.History())
	}
	if !contract.validJumpdest(pos.v) {
		return nil, fmt.Errorf("%w on %v\nValue history %v\n", ErrInvalidJump, spew.Sdump(pc), pos.History())
	}

	*pc = pos.v.Uint64()

	return nil, nil
}

func opJumpi(pc *uint64, _ *vm.EVMInterpreter, contract *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	pos, err := stack.pop()
	if err != nil {
		return nil, err
	}
	cond, err := stack.pop()
	if err != nil {
		return nil, err
	}

	/*
	if pos.static {
		fmt.Printf("jumpiT: on %x to %v\nValue history %v\n", *pc, pos.v, pos.History())
	}
	*/

	if cond.v.Sign() != 0 {
		if !pos.static {
			return nil, fmt.Errorf("opJumpi: %w on %v\nValue history %v\n", ErrNonStatic, spew.Sdump(pc), pos.History())
		}
		if pos.static && !pos.IsValue() {
			return nil, fmt.Errorf("jumpi: %w on %v\nValue history %v\n", ErrNoValueStatic, spew.Sdump(pc), pos.History())
		}

		if !contract.validJumpdest(pos.v) {
			return nil, fmt.Errorf("jumpi: %w on %v\nValue history %v\n", ErrInvalidJump, spew.Sdump(pc), pos.History())
		}
		*pc = pos.v.Uint64()
	} else {
		*pc++
	}

	return nil, nil
}

func opJumpiJUMP(pc *uint64, _ *vm.EVMInterpreter, contract *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	pos, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	/*
	if pos.static {
		fmt.Printf("jumpiT: on %x to %v\nValue history %v\n", *pc, pos.v, pos.History())
	}
	*/

	if !pos.static {
		return nil, fmt.Errorf("opJumpiJUMP: %w on %v\nValue history %v\n", ErrNonStatic, spew.Sdump(pc), pos.History())
	}
	if pos.static && !pos.IsValue() {
		return nil, fmt.Errorf("jumpi: %w on %v. jump to %x\nValue history %v\n", ErrNoValueStatic, spew.Sdump(pc), pos.v.Uint64(), pos.History())
	}

	if !contract.validJumpdest(pos.v) {
		return nil, fmt.Errorf("jumpi: %w on %v\nValue history %v\n", ErrInvalidJump, spew.Sdump(pc), pos.History())
	}

	*pc = pos.v.Uint64()

	return nil, nil
}

func opJumpiNotJUMP(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	*pc = *pc + 1

	return nil, nil
}

func opJumpdest(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	return nil, nil
}

func opPc(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewStaticCell()
	value.AddHistory(vm.PC, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opMsize(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.MSIZE, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opGas(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.GAS, *pc, value.static)
	stack.push(value)
	return nil, nil
}

func opCreate(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.CREATE, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opCreate2(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.CREATE2, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opCall(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Pop gas. The actual gas in interpreter.evm.callGasTemp.
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.CALL, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opCallCode(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.CALLCODE, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opDelegateCall(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.DELEGATECALL, *pc, value.static)
	stack.push(value)

	return nil, nil
}

func opStaticCall(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	// Pop gas. The actual gas is in interpreter.evm.callGasTemp.
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}

	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	value := NewNonStaticCell()
	value.AddHistory(vm.STATICCALL, *pc, value.static) // fixme stricter than it could be
	stack.push(value)

	return nil, nil
}

func opReturn(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, ErrReturn
}

func opRevert(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	_, err = stack.pop()
	if err != nil {
		return nil, err
	}

	return nil, ErrRevert
}

func opStop(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	return nil, ErrStop
}

func opSuicide(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	_, err := stack.pop()
	if err != nil {
		return nil, err
	}
	return nil, ErrSelfDestruct
}

// following functions are used by the instruction jump  table
type executionFunc func(pc *uint64, interpreter *vm.EVMInterpreter, contract *Contract, memory *vm.Memory, stack *Stack) ([]byte, error)

// make log instruction function
func makeLog(size int) executionFunc {
	return func(_ *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
		_, err := stack.pop()
		if err != nil {
			return nil, err
		}
		_, err = stack.pop()
		if err != nil {
			return nil, err
		}
		for i := 0; i < size; i++ {
			_, err = stack.pop()
			if err != nil {
				return nil, err
			}
		}

		return nil, nil
	}
}

// var PushDest = make(map[uint64]struct{})

// opPush1 is a specialized version of pushN
func opPush1(pc *uint64, _ *vm.EVMInterpreter, contract *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	var (
		codeLen = uint64(len(contract.Code))
		integer = big.NewInt(0)
	)
	*pc += 1

	c := NewStaticCell()
	c.AddHistory(vm.PUSH1, *pc, c.static)

	if *pc < codeLen {
		c.SetValue(integer.SetUint64(uint64(contract.Code[*pc])))
	} else {
		c.SetValue(integer.SetUint64(0))
	}

	stack.push(c)

	return nil, nil
}

// make push instruction function
func makePush(size uint64, pushByteSize int) executionFunc {
	return func(pc *uint64, _ *vm.EVMInterpreter, contract *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
		codeLen := len(contract.Code)

		startMin := codeLen
		if int(*pc+1) < startMin {
			startMin = int(*pc + 1)
		}

		endMin := codeLen
		if startMin+pushByteSize < endMin {
			endMin = startMin + pushByteSize
		}

		integer := big.NewInt(0)
		integer.SetBytes(common.RightPadBytes(contract.Code[startMin:endMin], pushByteSize))


		c := NewStaticCell()
		c.SetValue(integer)
		c.AddHistory(vm.PUSH, *pc, c.static)
		stack.push(c)

		*pc += size
		return nil, nil
	}
}

// make dup instruction function
func makeDup(size int64) executionFunc {
	return func(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
		err := stack.dup(int(size), vm.DUP, *pc)
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
}

// make swap instruction function
func makeSwap(size int64) executionFunc {
	// switch n + 1 otherwise n would be swapped with n
	size++
	return func(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
		err := stack.swap(int(size), vm.SWAP, *pc)
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func opSelfBalance(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewNonStaticCell()
	value.AddHistory(vm.SELFBALANCE, *pc, value.static)
	stack.push(value)
	return nil, nil
}

// opChainID implements CHAINID opcode
func opChainID(pc *uint64, _ *vm.EVMInterpreter, _ *Contract, _ *vm.Memory, stack *Stack) ([]byte, error) {
	value := NewStaticCell()
	value.AddHistory(vm.CHAINID, *pc, value.static)
	stack.push(value)
	return nil, nil
}
