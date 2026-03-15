package manta

import (
	"sync"
	"container/heap"
	"fmt"

	"github.com/dotabuff/manta/dota"
)

// ------------------------------------------------------------------------- //
//  * entity
// ------------------------------------------------------------------------- //

// EntityOp is a bitmask representing the type of operation performed on an Entity
type EntityOp int

const (
	EntityOpNone           EntityOp = 0x00
	EntityOpCreated        EntityOp = 0x01
	EntityOpUpdated        EntityOp = 0x02
	EntityOpDeleted        EntityOp = 0x04
	EntityOpEntered        EntityOp = 0x08
	EntityOpLeft           EntityOp = 0x10
	EntityOpCreatedEntered EntityOp = EntityOpCreated | EntityOpEntered
	EntityOpUpdatedEntered EntityOp = EntityOpUpdated | EntityOpEntered
	EntityOpDeletedLeft    EntityOp = EntityOpDeleted | EntityOpLeft
)

// Entity represents a single game entity in the replay
type Entity struct {
	index   int32
	serial  int32
	class   *class
	active  bool
	state   *fieldState
}

// newEntity returns a new entity for the given index, serial and class
func newEntity(index, serial int32, class *class) *Entity {
	return &Entity{
		index:   index,
		serial:  serial,
		class:   class,
		active:  true,
		state:   newFieldState(),
	}
}

// Internal Callback for OnCSVCMsg_PacketEntities.
func (p *Parser) onCSVCMsg_PacketEntities(m *dota.CSVCMsg_PacketEntities) error {
	r := newReader(m.GetEntityData())

	var index = int32(-1)
	var updates = int(m.GetUpdatedEntries())
	var cmd uint32
	var classId int32
	var serial int32
	var e *Entity
	var op EntityOp

	if !m.GetLegacyIsDelta() {
		if p.entityFullPackets > 0 {
			return nil
		}
		p.entityFullPackets++
	}

	type tuple struct {
		e  *Entity
		op EntityOp
	}
	tuples := make([]tuple, 0, updates)

	for ; updates > 0; updates-- {
		index += int32(r.readUBitVar()) + 1
		op = EntityOpNone

		cmd = r.readBits(2)
		if cmd&0x01 == 0 {
			if cmd&0x02 != 0 {
				classId = int32(r.readBits(p.classIdSize))
				serial = int32(r.readBits(17))
				r.readVarUint32()

				class := p.classesById[classId]
				if class == nil {
					panic(fmt.Errorf("unable to find new class %d", classId))
				}

				baseline := p.classBaselines[classId]
				if baseline == nil {
					panic(fmt.Errorf("unable to find new baseline %d", classId))
				}

				e = newEntity(index, serial, class)
				p.entities[index] = e
				readFields(newReader(baseline), class.serializer, e.state)
				readFields(r, class.serializer, e.state)
				op = EntityOpCreated | EntityOpEntered

			} else {
				if e = p.entities[index]; e == nil {
					panic(fmt.Errorf("unable to find existing entity %d", index))
				}

				op = EntityOpUpdated
				if !e.active {
					e.active = true
					op |= EntityOpEntered
				}

				readFields(r, e.class.serializer, e.state)
			}

		} else {
			if e = p.entities[index]; e == nil {
				panic(fmt.Errorf("unable to find existing entity %d", index))
			}

			if !e.active {
				panic(fmt.Errorf("entity %d (%s) ordered to leave, already inactive", e.class.classId, e.class.name))
			}

			op = EntityOpLeft
			if cmd&0x02 != 0 {
				op |= EntityOpDeleted
				p.entities[index] = nil
			}
		}

		tuples = append(tuples, tuple{e, op})
	}

/*
	for _, h := range p.entityHandlers {
		for _, t := range tuples {
			if err := h(t.e, t.op); err != nil {
				return err
			}
		}
	}
*/

	return nil
}


// ------------------------------------------------------------------------- //
// field_state
// ------------------------------------------------------------------------- //

type fieldState struct {
	state []interface{}
}

func newFieldState() *fieldState {
	return &fieldState{
		state: make([]interface{}, 8),
	}
}

func (s *fieldState) set(fp *fieldPath, v interface{}) {
	x := s
	z := 0
	for i := 0; i <= fp.last; i++ {
		z = fp.path[i]
		if y := len(x.state); y < z+2 {
			z := make([]interface{}, max(z+2, y*2))
			copy(z, x.state)
			x.state = z
		}
		if i == fp.last {
			if _, ok := x.state[z].(*fieldState); !ok {
				x.state[z] = v
			}
			return
		}
		if _, ok := x.state[z].(*fieldState); !ok {
			x.state[z] = newFieldState()
		}
		x = x.state[z].(*fieldState)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ------------------------------------------------------------------------- //
// field_reader
// ------------------------------------------------------------------------- //

func readFields(r *reader, s *serializer, state *fieldState) {
	fps := readFieldPaths(r)

	for _, fp := range fps {
		decoder := s.getDecoderForFieldPathSer(fp, 0)

		val := decoder(r)
		state.set(fp, val)

		fp.release()
	}
}


// ------------------------------------------------------------------------- //
// field_path
// ------------------------------------------------------------------------- //

var huffTree = newHuffmanTree()

// readFieldPaths reads a new slice of fieldPath values from the given reader
func readFieldPaths(r *reader) []*fieldPath {
	fp := newFieldPath()

	node, next := huffTree, huffTree

	paths := []*fieldPath{}

	for !fp.done {
		if r.readBits(1) == 1 {
			next = node.Right()
		} else {
			next = node.Left()
		}

		if next.IsLeaf() {
			node = huffTree
			fieldPathTable[next.Value()].fn(r, fp)
			if !fp.done {
				paths = append(paths, fp.copy())
			}
		} else {
			node = next
		}
	}

	fp.release()

	return paths
}

// newHuffmanTree creates a new huffmanTree from the field path table
func newHuffmanTree() huffmanTree {
	freqs := make([]int, len(fieldPathTable))
	for i, op := range fieldPathTable {
		freqs[i] = op.weight
	}
	return buildHuffmanTree(freqs)
}

type fieldPathOp struct {
	name   string
	weight int
	fn     func(r *reader, fp *fieldPath)
}

var fieldPathTable = []fieldPathOp{
	fieldPathOp{"PlusOne", 36271, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
	}},
	fieldPathOp{"PlusTwo", 10334, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 2
	}},
	fieldPathOp{"PlusThree", 1375, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 3
	}},
	fieldPathOp{"PlusFour", 646, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 4
	}},
	fieldPathOp{"PlusN", 4128, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += r.readUBitVarFieldPath() + 5
	}},
	fieldPathOp{"PushOneLeftDeltaZeroRightZero", 35, func(r *reader, fp *fieldPath) {
		fp.last++
		fp.path[fp.last] = 0
	}},
	fieldPathOp{"PushOneLeftDeltaZeroRightNonZero", 3, func(r *reader, fp *fieldPath) {
		fp.last++
		fp.path[fp.last] = r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushOneLeftDeltaOneRightZero", 521, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
		fp.last++
		fp.path[fp.last] = 0
	}},
	fieldPathOp{"PushOneLeftDeltaOneRightNonZero", 2942, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
		fp.last++
		fp.path[fp.last] = r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushOneLeftDeltaNRightZero", 560, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] = 0
	}},
	fieldPathOp{"PushOneLeftDeltaNRightNonZero", 471, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += r.readUBitVarFieldPath() + 2
		fp.last++
		fp.path[fp.last] = r.readUBitVarFieldPath() + 1
	}},
	fieldPathOp{"PushOneLeftDeltaNRightNonZeroPack6Bits", 10530, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += int(r.readBits(3)) + 2
		fp.last++
		fp.path[fp.last] = int(r.readBits(3)) + 1
	}},
	fieldPathOp{"PushOneLeftDeltaNRightNonZeroPack8Bits", 251, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += int(r.readBits(4)) + 2
		fp.last++
		fp.path[fp.last] = int(r.readBits(4)) + 1
	}},
	fieldPathOp{"PushTwoLeftDeltaZero", 0, func(r *reader, fp *fieldPath) {
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushTwoPack5LeftDeltaZero", 0, func(r *reader, fp *fieldPath) {
		fp.last++
		fp.path[fp.last] = int(r.readBits(5))
		fp.last++
		fp.path[fp.last] = int(r.readBits(5))
	}},
	fieldPathOp{"PushThreeLeftDeltaZero", 0, func(r *reader, fp *fieldPath) {
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushThreePack5LeftDeltaZero", 0, func(r *reader, fp *fieldPath) {
		fp.last++
		fp.path[fp.last] = int(r.readBits(5))
		fp.last++
		fp.path[fp.last] = int(r.readBits(5))
		fp.last++
		fp.path[fp.last] = int(r.readBits(5))
	}},
	fieldPathOp{"PushTwoLeftDeltaOne", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushTwoPack5LeftDeltaOne", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
	}},
	fieldPathOp{"PushThreeLeftDeltaOne", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushThreePack5LeftDeltaOne", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += 1
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
	}},
	fieldPathOp{"PushTwoLeftDeltaN", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += int(r.readUBitVar()) + 2
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushTwoPack5LeftDeltaN", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += int(r.readUBitVar()) + 2
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
	}},
	fieldPathOp{"PushThreeLeftDeltaN", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += int(r.readUBitVar()) + 2
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
		fp.last++
		fp.path[fp.last] += r.readUBitVarFieldPath()
	}},
	fieldPathOp{"PushThreePack5LeftDeltaN", 0, func(r *reader, fp *fieldPath) {
		fp.path[fp.last] += int(r.readUBitVar()) + 2
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
		fp.last++
		fp.path[fp.last] += int(r.readBits(5))
	}},
	fieldPathOp{"PushN", 0, func(r *reader, fp *fieldPath) {
		n := int(r.readUBitVar())
		fp.path[fp.last] += int(r.readUBitVar())
		for i := 0; i < n; i++ {
			fp.last++
			fp.path[fp.last] += r.readUBitVarFieldPath()
		}
	}},
	fieldPathOp{"PushNAndNonTopological", 310, func(r *reader, fp *fieldPath) {
		for i := 0; i <= fp.last; i++ {
			if r.readBoolean() {
				fp.path[i] += int(r.readVarInt32()) + 1
			}
		}
		count := int(r.readUBitVar())
		for i := 0; i < count; i++ {
			fp.last++
			fp.path[fp.last] = r.readUBitVarFieldPath()
		}
	}},
	fieldPathOp{"PopOnePlusOne", 2, func(r *reader, fp *fieldPath) {
		fp.pop(1)
		fp.path[fp.last] += 1
	}},
	fieldPathOp{"PopOnePlusN", 0, func(r *reader, fp *fieldPath) {
		fp.pop(1)
		fp.path[fp.last] += r.readUBitVarFieldPath() + 1
	}},
	fieldPathOp{"PopAllButOnePlusOne", 1837, func(r *reader, fp *fieldPath) {
		fp.pop(fp.last)
		fp.path[0] += 1
	}},
	fieldPathOp{"PopAllButOnePlusN", 149, func(r *reader, fp *fieldPath) {
		fp.pop(fp.last)
		fp.path[0] += r.readUBitVarFieldPath() + 1
	}},
	fieldPathOp{"PopAllButOnePlusNPack3Bits", 300, func(r *reader, fp *fieldPath) {
		fp.pop(fp.last)
		fp.path[0] += int(r.readBits(3)) + 1
	}},
	fieldPathOp{"PopAllButOnePlusNPack6Bits", 634, func(r *reader, fp *fieldPath) {
		fp.pop(fp.last)
		fp.path[0] += int(r.readBits(6)) + 1
	}},
	fieldPathOp{"PopNPlusOne", 0, func(r *reader, fp *fieldPath) {
		fp.pop(r.readUBitVarFieldPath())
		fp.path[fp.last] += 1
	}},
	fieldPathOp{"PopNPlusN", 0, func(r *reader, fp *fieldPath) {
		fp.pop(r.readUBitVarFieldPath())
		fp.path[fp.last] += int(r.readVarInt32())
	}},
	fieldPathOp{"PopNAndNonTopographical", 1, func(r *reader, fp *fieldPath) {
		fp.pop(r.readUBitVarFieldPath())
		for i := 0; i <= fp.last; i++ {
			if r.readBoolean() {
				fp.path[i] += int(r.readVarInt32())
			}
		}
	}},
	fieldPathOp{"NonTopoComplex", 76, func(r *reader, fp *fieldPath) {
		for i := 0; i <= fp.last; i++ {
			if r.readBoolean() {
				fp.path[i] += int(r.readVarInt32())
			}
		}
	}},
	fieldPathOp{"NonTopoPenultimatePlusOne", 271, func(r *reader, fp *fieldPath) {
		fp.path[fp.last-1] += 1
	}},
	fieldPathOp{"NonTopoComplexPack4Bits", 99, func(r *reader, fp *fieldPath) {
		for i := 0; i <= fp.last; i++ {
			if r.readBoolean() {
				fp.path[i] += int(r.readBits(4)) - 7
			}
		}
	}},
	fieldPathOp{"FieldPathEncodeFinish", 25474, func(r *reader, fp *fieldPath) {
		fp.done = true
	}},
}

// pop reduces the last element by n, zeroing values in the popped path
func (fp *fieldPath) pop(n int) {
	for i := 0; i < n; i++ {
		fp.path[fp.last] = 0
		fp.last--
	}
}

// copy returns a copy of the fieldPath
func (fp *fieldPath) copy() *fieldPath {
	x := fpPool.Get().(*fieldPath)
	copy(x.path, fp.path)
	x.last = fp.last
	x.done = fp.done
	return x
}

// newFieldPath returns a new fieldPath ready for use
func newFieldPath() *fieldPath {
	fp := fpPool.Get().(*fieldPath)
	fp.reset()
	return fp
}

var fpPool = &sync.Pool{
	New: func() interface{} {
		return &fieldPath{
			path: make([]int, 7),
			last: 0,
			done: false,
		}
	},
}

var fpReset = []int{-1, 0, 0, 0, 0, 0, 0}

// reset resets the fieldPath to the empty value
func (fp *fieldPath) reset() {
	copy(fp.path, fpReset)
	fp.last = 0
	fp.done = false
}

// release returns the fieldPath to the pool for re-use
func (fp *fieldPath) release() {
	fpPool.Put(fp)
}


// ------------------------------------------------------------------------- //
// huffman
// ------------------------------------------------------------------------- //

// Interface for the tree, only implements Weight
type huffmanTree interface {
	Weight() int
	IsLeaf() bool
	Value() int
	Left() huffmanTree
	Right() huffmanTree
}

// A leaf, contains encoded value
type huffmanLeaf struct {
	weight int
	value  int
}

// A node with potential left / right nodes or leafs
type huffmanNode struct {
	weight int
	value  int
	left   huffmanTree
	right  huffmanTree
}

// Return weight for leaf
func (self huffmanLeaf) Weight() int {
	return self.weight
}

// Return leaf state
func (self huffmanLeaf) IsLeaf() bool {
	return true
}

// Return value for leaf
func (self huffmanLeaf) Value() int {
	return self.value
}

func (self huffmanLeaf) Right() huffmanTree {
	panic(fmt.Errorf("huffmanLeaf doesn't have right node"))
	return nil
}

func (self huffmanLeaf) Left() huffmanTree {
	panic(fmt.Errorf("huffmanLeaf doesn't have left node"))
	return nil
}

// Return weight for node
func (self huffmanNode) Weight() int {
	return self.weight
}

// Return leaf state
func (self huffmanNode) IsLeaf() bool {
	return false
}

// Return value for node
func (self huffmanNode) Value() int {
	return self.value
}

func (self huffmanNode) Left() huffmanTree {
	return huffmanTree(self.left)
}

func (self huffmanNode) Right() huffmanTree {
	return huffmanTree(self.right)
}

type treeHeap []huffmanTree

// Returns the amount of nodes in the tree
func (th treeHeap) Len() int {
	return len(th)
}

// Weight compare function
func (th treeHeap) Less(i int, j int) bool {
	if th[i].Weight() == th[j].Weight() {
		return th[i].Value() >= th[j].Value()
	} else {
		return th[i].Weight() < th[j].Weight()
	}
}

// Append item, required for heap
func (th *treeHeap) Push(ele interface{}) {
	*th = append(*th, ele.(huffmanTree))
}

// Remove item, required for heap
func (th *treeHeap) Pop() (popped interface{}) {
	popped = (*th)[len(*th)-1]
	*th = (*th)[:len(*th)-1]
	return
}

// Swap two items, required for heap
func (th treeHeap) Swap(i, j int) {
	th[i], th[j] = th[j], th[i]
}

// Construct a tree from a map of weight -> item
func buildHuffmanTree(symFreqs []int) huffmanTree {
	var trees treeHeap
	for v, w := range symFreqs {
		if w == 0 {
			w = 1
		}

		trees = append(trees, &huffmanLeaf{w, v})
	}

	n := 40

	heap.Init(&trees)
	for trees.Len() > 1 {
		a := heap.Pop(&trees).(huffmanTree)
		b := heap.Pop(&trees).(huffmanTree)

		heap.Push(&trees, &huffmanNode{a.Weight() + b.Weight(), n, a, b})
		n++
	}

	return heap.Pop(&trees).(huffmanTree)
}


// ------------------------------------------------------------------------- //
// class
// ------------------------------------------------------------------------- //

type class struct {
	classId    int32
	name       string
	serializer *serializer
}


// ------------------------------------------------------------------------- //
// 
// ------------------------------------------------------------------------- //
