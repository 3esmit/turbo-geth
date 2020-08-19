package vm

import (
	"fmt"
	"github.com/holiman/uint256"
	"github.com/logrusorgru/aurora"
	"strconv"
	"strings"
)

//////////////////////////////////////////////////
type AbsValueKind int

const (
	BotValue AbsValueKind = iota
	TopValue
	ConcreteValue
)

func (d AbsValueKind) String() string {
	return [...]string{"⊥", "⊤", "AbsValue"}[d]
}
//////////////////////////////////////////////////

type AbsValue struct {
	kind AbsValueKind
	value uint256.Int 	//only when kind=ConcreteValue
	pc int 				//only when kind=TopValue
}

func (c0 AbsValue) String(showpc bool) string {
	if c0.kind == BotValue || c0.kind == TopValue {
		if showpc {
			return fmt.Sprintf("%v%v", c0.kind.String(), c0.pc)
		} else {
			return c0.kind.String()
		}
	} else {
		if c0.value.IsUint64() {
			return strconv.FormatUint(c0.value.Uint64(), 10)
		} else {
			return "256bit"
		}
	}
}

//////////////////////////////////////////////////

type ValueSet struct {
	values map[AbsValue]bool
	isTop bool
}

func (set ValueSet) Copy() ValueSet {
	if set.isTop {
		return ValueSet{isTop: true}
	} else {
		newSet := ValueSet{values: make(map[AbsValue]bool), isTop: false}
		for k, v := range set.values {
			newSet.values[k] = v
		}
		return newSet
	}
}

func (set ValueSet) String() string {
	if set.isTop {
		return "⊤"
	}

	var strs []string
	for v, in := range set.values {
		if in {
			strs = append(strs, v.String(false))
		}
	}

	res := strings.Join(strs, " ")
	if len(strs) == 0 {
		return "⊥"
	} else if len(strs) == 1 {
		return fmt.Sprintf("%v", res)
	} else {
		return fmt.Sprintf("{%v}", res)
	}
}

func ValueSetBot() ValueSet {
	return ValueSet{ values: make(map[AbsValue]bool), isTop: false }
}

func ValueSetTop() ValueSet {
	return ValueSet{ isTop: true }
}

func ValueSetLub(c0 ValueSet, c1 ValueSet) ValueSet {
	if c0.isTop || c1.isTop {
		return ValueSetTop()
	}

	res := ValueSet{ values: make(map[AbsValue]bool) }

	for k, v := range c0.values {
		res.values[k] = v
	}

	for k, v := range c1.values {
		res.values[k] = v
	}

	return res
}

func ValueSetLeq(c0 ValueSet, c1 ValueSet) bool {
	if c0.isTop && c1.isTop {
		return true
	} else if !c0.isTop && c1.isTop {
		return true
	} else if c0.isTop && !c1.isTop {
		return false
	} else if !c0.isTop && !c1.isTop {
		for k, v := range c0.values {
			if v && !c1.values[k] {
				return false
			}
		}
	} else {
		panic("unreachable")
	}
	return true
}

func ValueSetSingle(value uint256.Int, pc int) ValueSet {
	valueSet := ValueSet{values: make(map[AbsValue]bool)}
	valueSet.values[AbsValue{ConcreteValue, value, pc}] = true
	return valueSet
}

//////////////////////////////////////////////////

type state2 struct {
	stack       []ValueSet
	anlyCounter int
	worklistLen int
}

func EmptyState2() state2 {
	return state2{stack: nil, anlyCounter: -1, worklistLen: -1}
}

func (state *state2) Copy() state2 {
	newState := EmptyState2()
	for _, valueSet := range state.stack {
		newState.stack = append(newState.stack, valueSet.Copy())
	}
	return newState
}

func (state *state2) Push(value ValueSet) {
	rest := state.stack[0: absStackLen-1]
	state.stack = []ValueSet{value}
	state.stack = append(state.stack, rest...)
}

func (state *state2) Pop() ValueSet {
	res := state.stack[0]
	state.stack = append(state.stack[1:], ValueSetTop())
	return res
}

func botState() state2 {
	st := EmptyState2()
	st.stack = make([]ValueSet, absStackLen)

	for i := range st.stack {
		st.stack[i] = ValueSetBot()
	}
	return st
}

func (state state2) String() string {
	var strs []string
	for _, c := range state.stack {
		strs = append(strs, c.String())
	}
	return strings.Join(strs, " ")
}

//////////////////////////////////////////////////

func resolve2(prog *Contract, pc0 int, st0 state2, stmt stmt) ResolveResult {
	if !stmt.operation.valid || stmt.ends {
		return ResolveResult{resolved: true}
	}

	codeLen := len(prog.Code)
	var edges []edge

	if stmt.opcode == JUMP || stmt.opcode == JUMPI {
		jumpDestSet := st0.stack[0]
		for jumpDest, _ := range jumpDestSet.values {
			if jumpDest.kind == ConcreteValue {
				if jumpDest.value.IsUint64() {
					pc1 := int(jumpDest.value.Uint64())
					edges = append(edges, edge{pc0, stmt, pc1})
				} else {
					return ResolveResult{resolved: false, badJump: &stmt}
				}
			} else if jumpDest.kind == TopValue {
				return ResolveResult{resolved: false, badJump: &stmt}
			}
		}
	}

	if stmt.opcode != JUMP {
		if pc0 < codeLen-stmt.numBytes {
			edges = append(edges, edge{pc0, stmt, pc0 + stmt.numBytes})
		}
	}

	if DEBUG {
		fmt.Printf("\nResolve: %v %v\n", pc0, stmt)
		printEdges(edges)
	}

	return ResolveResult{edges: edges, resolved: true}
}

func post2(st0 state2, stmt stmt) state2 {
	st1 := st0.Copy()

	if stmt.opcode.IsPush() {
		valueSet := ValueSet{values: make(map[AbsValue]bool)}
		valueSet.values[AbsValue{kind: ConcreteValue, value: stmt.value, pc: stmt.pc}] = true
		st1.Push(valueSet)
	} else if stmt.operation.isDup {
		valueSet := st1.stack[stmt.operation.opNum-1]
		st1.Push(valueSet)
	} else if stmt.operation.isSwap {
		opNum := stmt.operation.opNum
		a := st1.stack[0]
		b := st1.stack[opNum]
		st1.stack[0] = b
		st1.stack[opNum] = a
	} else {
		for i := 0; i < stmt.operation.numPop; i++ {
			st1.Pop()
		}
		for i := 0; i < stmt.operation.numPush; i++ {
			valueSet := ValueSet{values: make(map[AbsValue]bool)}
			valueSet.values[AbsValue{kind: TopValue, pc: stmt.pc}] = true
			st1.Push(valueSet)
		}
	}

	return st1
}


func leq2(st0 state2, st1 state2) bool {
	if len(st0.stack) != len(st1.stack) || absStackLen != len(st0.stack) {
		panic("mismatched stack len")
	}

	for i := 0; i < absStackLen; i++ {
		if !ValueSetLeq(st0.stack[i], st1.stack[i]) {
			return false
		}
	}
	return true
}

func lub2(st0 state2, st1 state2) state2 {
	newState := EmptyState2()

	for i := 0; i < absStackLen; i++ {
		lub := ValueSetLub(st0.stack[i], st1.stack[i])
		newState.stack = append(newState.stack, lub)
	}

	return newState
}

func printAnlyState2(stmts []stmt, prevEdgeMap map[int]map[int]bool, D map[int]state2, badJumps map[int]bool) {
//	es := make([]edge, len(edges))
//	copy(es, edges)
//	sortEdges(es)

	var badJumpList []string
	for pc, stmt := range stmts {
		if stmt.inferredAsData {
			//fmt.Printf("data: %v\n", stmt.inferredAsData)
			continue
		}

		if stmt.opcode == JUMPDEST {
			fmt.Println()
		}

		var valueStr string
		if stmt.opcode.IsPush(){
			valueStr = fmt.Sprintf("%v %v", stmt.opcode, stmt.value.Hex())
		} else {
			valueStr = fmt.Sprintf("%v", stmt.opcode)
		}

		pc0s := make([]string, 0)
		showPC0s := false
		for pc0, _ := range prevEdgeMap[pc] {
			pc0s = append(pc0s, strconv.Itoa(pc0))
			stmt0 := stmts[pc0]
			if pc0 + stmt0.numBytes != pc {
				showPC0s = true
			}
		}
		if !showPC0s && false {
			pc0s = nil
		}

		if badJumps[pc] {
			out := fmt.Sprintf("[%5v] (w:%2v) %3v\t %-25v %-10v %v\n", 	aurora.Blue(D[pc].anlyCounter), aurora.Cyan(D[pc].worklistLen), aurora.Yellow(pc), aurora.Red(valueStr), aurora.Magenta(strings.Join(pc0s, ",")), D[pc])
			fmt.Print(out)
			badJumpList = append(badJumpList, out)
		} else if prevEdgeMap[pc] != nil {
			fmt.Printf("[%5v] (w:%2v) %3v\t %-25v %-10v %v\n", 			aurora.Blue(D[pc].anlyCounter), aurora.Cyan(D[pc].worklistLen), aurora.Yellow(pc), aurora.Green(valueStr), aurora.Magenta(strings.Join(pc0s, ",")), D[pc])
		} else {
			fmt.Printf("[%5v] (w:%2v) %3v\t %-25v\n", 					aurora.Blue(D[pc].anlyCounter), aurora.Cyan(D[pc].worklistLen), aurora.Yellow(pc), valueStr)
		}
	}

	print("\nFull list of bad jumps:\n")
	for _, badJump := range badJumpList {
		fmt.Println(badJump)
	}
}

func AbsIntCfgHarness2(prog *Contract) error {

	stmts := getStmts(prog)
	if DEBUG {
		printStmts(stmts)
	}

	startPC := 0
	codeLen := len(prog.Code)
	D := make(map[int]state2)
	for pc := 0; pc < codeLen; pc++ {
		D[pc] = botState()
	}
	D[startPC] = botState()

	prevEdgeMap := make(map[int]map[int]bool)

	var workList []edge
	{
		resolution := resolve2(prog, startPC, D[startPC], stmts[startPC])
		if !resolution.resolved {
			fmt.Printf("Unable to resolve at pc=%x\n", startPC)
			return nil
		} else {
			for _, e := range resolution.edges {
				if prevEdgeMap[e.pc1] == nil {
					prevEdgeMap[e.pc1] = make(map[int]bool)
				}
				prevEdgeMap[e.pc1][e.pc0] = true
			}
		}
		workList = resolution.edges
	}

	check(stmts, prevEdgeMap)

	anlyCounter := 0
	badJumps := make(map[int]bool)
	for len(workList) > 0 {
		//sortEdges(workList)
		var e edge
		e, workList = workList[0], workList[1:]

		//fmt.Printf("%v\n", e.pc0)
		if e.pc0 == -1 {
			fmt.Printf("---------------------------------------\n")
			fmt.Printf("Verbose debugging for pc=%v\n", e.pc0)
			DEBUG = true
		}

		if DEBUG {
			fmt.Printf("pre pc=%v\t%v\n", e.pc0, D[e.pc0])
		}
		preDpc0 := D[e.pc0]
		preDpc1 := D[e.pc1]
		post1 := post2(preDpc0, e.stmt)
		if DEBUG {
			fmt.Printf("post\t\t%v\n", post1);
			fmt.Printf("Dprev\t\t%v\n", preDpc1)
		}

		if !leq2(post1, preDpc1) {
			postDpc1 := lub2(post1, preDpc1)
			if false {

				fmt.Printf("\nedge %v %v\n", e.pc0, e.pc1)
				//fmt.Printf("pre D[pc0]\t\t%v\n", preDpc0);
				fmt.Printf("pre D[pc1]\t\t%v\n", preDpc1)
				fmt.Printf("post\t\t\t%v\n", post1)

				/*
				for i := 0; i < absStackLen; i++ {
					c0 := post1.stack[i]
					c1 := preDpc1.stack[i]
					if !ValueSetLeq(c0, c1) {
						fmt.Printf("diff: \t\t\t%v %v %v %v %v\n", i, c0, c1, c0.kind, c1.kind)
						if c0.kind == ConstValueKind && c1.kind == ConstValueKind {
							fmt.Printf("\t\t\t\t\tEQ=%v\n", c0.value.Eq(&c1.value))
						}
					}
				}*/
				//fmt.Printf("lub\t\t\t%v\n", postDpc1)
				printAnlyState2(stmts, prevEdgeMap, D, nil)
			}
			D[e.pc1] = postDpc1

			resolution := resolve2(prog, e.pc1, D[e.pc1], stmts[e.pc1])

			if !resolution.resolved {
				badJumps[resolution.badJump.pc] = true
				fmt.Printf("FAILURE: Unable to resolve: anlyCounter=%v pc=%x\n", aurora.Red(anlyCounter), aurora.Red(e.pc1))
				if failOnBadJump {
					badJumps := make(map[int]bool)
					badJumps[resolution.badJump.pc] = true
					printAnlyState2(stmts, prevEdgeMap, D, badJumps)
					return nil
				}
			} else {
				for _, e := range resolution.edges {
					inWorkList := false
					for _, w := range workList {
						if w.pc0 == e.pc0 && w.pc1 == e.pc1 {
							inWorkList = true
						}
					}
					if !inWorkList {
						head := []edge{e}
						workList = append(head, workList...)
					}
				}

				if prevEdgeMap[e.pc1] == nil {
					prevEdgeMap[e.pc1] = make(map[int]bool)
				}
				prevEdgeMap[e.pc1][e.pc0] = true
			}
		}
		DEBUG = false

		decp1Copy := D[e.pc1]
		decp1Copy.anlyCounter = anlyCounter
		decp1Copy.worklistLen = len(workList)
		D[e.pc1] = decp1Copy
		anlyCounter++

		check(stmts, prevEdgeMap)
	}

	print("\nFinal resolve....")
	var finalEdges []edge
	for pc := 0; pc < codeLen; pc++ {
		resolution := resolve2(prog, pc, D[pc], stmts[pc])
		if !resolution.resolved {
			badJumps[resolution.badJump.pc] = true
			fmt.Println("Bad jump found during final resolve.")
		}
		finalEdges = append(finalEdges, resolution.edges...)
	}
	//need to run a DFS from the entry point to pick only reachable stmts

	reachableEdges := getEntryReachableEdges(0, finalEdges)

	fmt.Printf("\n# of unreachable edges: %v\n", len(finalEdges) - len(reachableEdges))
	fmt.Printf("\n# of total edges: %v\n", len(finalEdges))
	//printEdges(edges)

	printAnlyState2(stmts, prevEdgeMap, D, nil)
	println("done valueset")

	if len(badJumps) > 0 {
		printAnlyState2(stmts, prevEdgeMap, D, badJumps)
	}

	return nil
}