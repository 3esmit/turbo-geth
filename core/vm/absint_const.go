package vm

import (
	"fmt"
	"github.com/holiman/uint256"
)

//////////////////////////////////////////////////
const (
	absStackLen = 10
)

type AbsElmType int

const (
	//bot AbsElmType = iota
	//top
	value = iota
)

type state struct {
	kind AbsElmType
	stack []AbsConst
}

func botSt() state {
	st := state{value, make([]AbsConst, absStackLen)}
	for i := range st.stack {
		st.stack[i] = ConstBot()
	}
	return st
}

func startSt() state {
	return botSt()
}

type stmt struct {
	opcode OpCode
	operation operation
	value uint256.Int
	numBytes int
}

type edge struct {
	pc0 int
	stmt stmt
	pc1 int
}

func (e edge) String() string {
	return fmt.Sprintf("%v %v %v", e.pc0, e.pc1, e.stmt.opcode)
}

func resolve(prog *Contract, pc0 int, st0 state, stmt stmt) []edge {
	//fmt.Printf("RESOLVE: %v %v %v %v\n", pc0, stmt.opcode, stmt.operation.halts, stmt.operation.valid)

	if !stmt.operation.valid || stmt.operation.halts {
		return nil
	}

	codeLen := len(prog.Code)
	var edges []edge
	if stmt.opcode == JUMP || stmt.opcode == JUMPI {
		jumpDest := st0.stack[0]
		if jumpDest.kind == Value {
			if jumpDest.value.IsUint64() {
				pc1 := int(jumpDest.value.Uint64())
				edges = append(edges, edge{pc0, stmt, pc1})
			} else {
				panic("Invalid program counter. Cannot resolve jump.")

			}
		} else if jumpDest.kind == Top {
			panic("Imprecise jump found. Cannot resolve jump.")
		}
	}

	if stmt.opcode != JUMP {
		if pc0 < codeLen-stmt.numBytes {
			edges = append(edges, edge{pc0, stmt, pc0 + stmt.numBytes})
		}
	}

	return edges
}

func post(st0 state, stmt stmt) state {
	st1 := st0

	if stmt.opcode.IsPush() {
		st1.stack = append(st1.stack, AbsConst{value,stmt.value})
	} else {
		switch stmt.opcode {
		case JUMP, JUMPI:
			st1.stack = st1.stack[1:]
		}
	}

	return st1
}

func leq(st0 state, st1 state) bool {
	for i := 0; i < absStackLen; i++ {
		if !ConstLeq(st0.stack[i], st1.stack[i]) {
			return false
		}
	}
	return true
}

func lub(st0 state, st1 state) state {
	res := state{value, []AbsConst{}}

	for i := 0; i < absStackLen; i++ {
		lub := ConstLub(st0.stack[i], st1.stack[i])
		res.stack = append(res.stack, lub)
	}

	return res
}

func getStmts(prog *Contract) []stmt {
	jt := newIstanbulInstructionSet()

	codeLen := len(prog.Code)
	var stmts []stmt
	for pc := 0; pc < codeLen; pc++ {
		stmt := stmt{}

		op := prog.GetOp(uint64(pc))
		stmt.opcode = op
		stmt.operation = jt[op]
		//fmt.Printf("%v %v %v", pc, stmt.opcode, stmt.operation.valid)

		if op.IsPush() {
			pushByteSize := GetPushBytes(op)
			startMin := pc + 1
			if startMin >= codeLen {
				startMin = codeLen
			}
			endMin := startMin + pushByteSize
			if startMin+pushByteSize >= codeLen {
				endMin = codeLen
			}
			integer := new(uint256.Int)
			integer.SetBytes(prog.Code[startMin:endMin])
			stmt.value = *integer
			stmt.numBytes = pushByteSize + 1
		} else {
			stmt.numBytes = 1
		}

		stmts = append(stmts, stmt)
		if pc != len(stmts) - 1 {
			panic("Invalid length")
		}
	}
	return stmts
}

func printEdges(edges []edge) {
	for _, edge := range edges {
		fmt.Printf("%v\n", edge)
	}
}

func AbsIntCfgHarness(prog *Contract) error {
	startPC := 0
	codeLen := len(prog.Code)
	D := make(map[int]state)
	for pc := 0; pc < codeLen; pc++ {
		D[pc] = botSt()
	}
	D[startPC] = startSt()

	stmts := getStmts(prog)

	workList := resolve(prog, startPC, D[startPC], stmts[startPC])
	for len(workList) > 0 {
		printEdges(workList)

		var e edge
		e, workList = workList[0], workList[1:]
		post1 := post(D[e.pc0], e.stmt)
		if !leq(post1, D[e.pc1]) {
			D[e.pc1] = lub(post1, D[e.pc1])
			workList = append(workList, resolve(prog, e.pc1, D[e.pc1], stmts[e.pc1])...)
		}
	}

	var edges []edge
	for pc := 0; pc < codeLen; pc++ {
		edges = append(edges, resolve(prog, pc, D[pc], stmts[pc])...)
	}

	fmt.Printf("\n# of edges: %v\n", len(edges))
	printEdges(edges)

	println("done")

	return nil
}
