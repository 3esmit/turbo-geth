// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package static

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ledgerwatch/turbo-geth/core/vm"
)

var errNotEnoughStack = errors.New("not enough stack")

type cell struct {
	v       *big.Int
	static  bool
	history []Operation
}

type Operation struct {
	Op        vm.OpCode
	Pc        uint64
	StaticRes bool
}

const debug = false

func (c *cell) AddHistory(op vm.OpCode, pc uint64, isStatic bool) *cell {
	if debug {
		c.history = append(c.history, Operation{op, pc, isStatic})
	}
	return c
}

func NewStaticCell() *cell {
	return &cell{nil, true, nil}
}

func NewNonStaticCell() *cell {
	return &cell{nil, false, nil}
}

func NewCell(isStatic bool) *cell {
	return &cell{nil, isStatic, nil}
}

func (c cell) IsStatic() bool {
	return c.static
}

func (c cell) IsValue() bool {
	return c.v != nil
}

func (c cell) Sign() *int {
	if c.static && c.IsValue() {
		res := c.v.Sign()
		return &res
	}
	return nil
}

func (c cell) Equals(n *big.Int) bool {
	if !c.IsValue() {
		return false
	}
	return c.v.Cmp(n) == 0
}

func (c *cell) SetValue(n *big.Int) {
	c.v = big.NewInt(0).Set(n)
}

func (c cell) History() []Operation {
	return c.history
}

func (c *cell) set(n int64, interpreter *vm.EVMInterpreter) *big.Int {
	if c.v == nil {
		c.v = interpreter.IntPool.GetZero()
	}
	c.v.SetInt64(n)
	return c.v
}

func (c *cell) unset(interpreter *vm.EVMInterpreter) {
	if c.v != nil {
		interpreter.IntPool.Put(c.v)
	}
	c.v = nil
}

type Stack struct {
	data []*cell
}

func newstack() *Stack {
	return &Stack{}
}

// Data returns the underlying big.Int array.
func (st *Stack) Data() []*cell {
	return st.data
}

func (st *Stack) push(d *cell) {
	// NOTE push limit (1024) is checked in baseCheck
	//stackItem := new(big.Int).Set(d)
	//st.data = append(st.data, stackItem)
	st.data = append(st.data, d)
}

func (st *Stack) pushN(ds ...*cell) {
	st.data = append(st.data, ds...)
}

func (st *Stack) pop() (ret *cell, err error) {
	if st.len() == 0 {
		err = errNotEnoughStack
		return
	}

	ret = st.data[len(st.data)-1]
	st.data = st.data[:len(st.data)-1]

	return
}

func (st *Stack) remove(n int, interpreter *vm.EVMInterpreter) error {
	var c *cell
	var err error
	for i := 0; i < n; i++ {
		c, err = st.pop()
		if err != nil {
			return err
		}
		interpreter.IntPool.Put(c.v)
	}

	return nil
}

func (st *Stack) len() int {
	return len(st.data)
}

func (st *Stack) Len() int {
	return len(st.data)
}

func (st *Stack) swap(n int, op vm.OpCode, pc uint64) error {
	if st.len() < n || st.len() == 0 {
		return errNotEnoughStack
	}

	st.data[st.len()-n], st.data[st.len()-1] = st.data[st.len()-1], st.data[st.len()-n]

	st.data[st.len()-n].AddHistory(op, pc, st.data[st.len()-n].static)
	st.data[st.len()-1].AddHistory(op, pc, st.data[st.len()-1].static)

	return nil
}

func (st *Stack) dup(n int, op vm.OpCode, pc uint64) error {
	if st.len() < n || st.len() == 0 {
		return errNotEnoughStack
	}

	v := st.data[st.len()-n]

	var vcopy *big.Int
	if v.v != nil {
		vcopy = big.NewInt(0).Set(v.v)
	}

	st.push(&cell{vcopy, v.static, append(v.history, Operation{op, pc, v.static})})

	return nil
}

func (st *Stack) peek() (*cell, error) {
	if st.len() == 0 {
		return nil, errNotEnoughStack
	}

	return st.data[st.len()-1], nil
}

// Back returns the n'th item in stack
func (st *Stack) Back(n int) (*cell, error) {
	if st.len() < n+1 || st.len() == 0 {
		return nil, errNotEnoughStack
	}

	return st.data[st.len()-n-1], nil
}

// Print dumps the content of the stack
func (st *Stack) Print() {
	fmt.Println("### stack ###")
	if len(st.data) > 0 {
		for i, val := range st.data {
			fmt.Printf("%-3d  %v\n", i, val)
		}
	} else {
		fmt.Println("-- empty --")
	}
	fmt.Println("#############")
}
