package manta

import (
	"strconv"
	"strings"
	"sync"
	"container/heap"
	"math"
)

var huffTree = newHuffmanTree()

type fieldPath struct {
	path []int
	last int
	done bool
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

// String returns a string representing the fieldPath
func (fp *fieldPath) String() string {
	ss := make([]string, fp.last+1)
	for i := 0; i <= fp.last; i++ {
		ss[i] = strconv.Itoa(fp.path[i])
	}
	return strings.Join(ss, "/")
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

// ------------------------------------------------------------------------- //
// Huffman tree
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
	_panicf("huffmanLeaf doesn't have right node")
	return nil
}

func (self huffmanLeaf) Left() huffmanTree {
	_panicf("huffmanLeaf doesn't have left node")
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
// Quantized float
// ------------------------------------------------------------------------- //


// Quantized float flags
const qff_rounddown uint32 = (1 << 0)
const qff_roundup uint32 = (1 << 1)
const qff_encode_zero uint32 = (1 << 2)
const qff_encode_integers uint32 = (1 << 3)

// Quantized-decoder struct containing the computed properties
type quantizedFloatDecoder struct {
	Low        float32 // Gets recomputed for round up / down
	High       float32
	HighLowMul float32
	DecMul     float32
	Offset     float32
	Bitcount   uint32 // Gets recomputed for qff_encode_int
	Flags      uint32
	NoScale    bool // Whether to decodes this as a noscale
}

// Validates / recomputes decoder flags
func (qfd *quantizedFloatDecoder) validateFlags() {
	// Check that we have some flags set
	if qfd.Flags == 0 {
		return
	}

	// Discard zero flag when encoding min / max set to 0
	if (qfd.Low == 0.0 && (qfd.Flags&qff_rounddown) != 0) || (qfd.High == 0.0 && (qfd.Flags&qff_roundup) != 0) {
		qfd.Flags &= ^qff_encode_zero
	}

	// If min / max is zero when encoding zero, switch to round up / round down instead
	if qfd.Low == 0.0 && (qfd.Flags&qff_encode_zero) != 0 {
		qfd.Flags |= qff_rounddown
		qfd.Flags &= ^qff_encode_zero
	}

	if qfd.High == 0.0 && (qfd.Flags&qff_encode_zero) != 0 {
		qfd.Flags |= qff_roundup
		qfd.Flags &= ^qff_encode_zero
	}

	// Check if the range spans zero
	if qfd.Low > 0.0 || qfd.High < 0.0 {
		qfd.Flags &= ^qff_encode_zero
	}

	// If we are left with encode zero, only leave integer flag
	if (qfd.Flags & qff_encode_integers) != 0 {
		qfd.Flags &= ^(qff_roundup | qff_rounddown | qff_encode_zero)
	}

	// Verify that we don;t have roundup / rounddown set
	if qfd.Flags&(qff_rounddown|qff_roundup) == (qff_rounddown | qff_roundup) {
		_panicf("Roundup / Rounddown are mutually exclusive")
	}
}

// Assign multipliers
func (qfd *quantizedFloatDecoder) assignMultipliers(steps uint32) {
	qfd.HighLowMul = 0.0
	Range := qfd.High - qfd.Low

	High := uint32(0)
	if qfd.Bitcount == 32 {
		High = 0xFFFFFFFE
	} else {
		High = (1 << qfd.Bitcount) - 1
	}

	HighMul := float32(0.0)
	if math.Abs(float64(Range)) <= 0.0 {
		HighMul = float32(High)
	} else {
		HighMul = float32(High) / Range
	}

	// Adjust precision
	if (HighMul*Range > float32(High)) || (float64(HighMul*Range) > float64(High)) {
		multipliers := []float32{0.9999, 0.99, 0.9, 0.8, 0.7}

		for _, mult := range multipliers {
			HighMul = float32(High) / Range * mult

			if (HighMul*Range > float32(High)) || (float64(HighMul*Range) > float64(High)) {
				continue
			}

			break
		}
	}

	qfd.HighLowMul = HighMul
	qfd.DecMul = 1.0 / float32(steps-1)

	if qfd.HighLowMul == 0.0 {
		_panicf("Error computing high / low multiplier")
	}
}

// Quantize a float
func (qfd *quantizedFloatDecoder) quantize(val float32) float32 {
	if val < qfd.Low {
		if (qfd.Flags & qff_roundup) == 0 {
			_panicf("Field tried to quantize an out of range value")
		}

		return qfd.Low
	} else if val > qfd.High {
		if (qfd.Flags & qff_rounddown) == 0 {
			_panicf("Field tried to quantize an out of range value")
		}

		return qfd.High
	}

	i := uint32((val - qfd.Low) * qfd.HighLowMul)
	return qfd.Low + (qfd.High-qfd.Low)*(float32(i)*qfd.DecMul)
}

// Actual float decoding
func (qfd *quantizedFloatDecoder) decode(r *reader) float32 {
	if (qfd.Flags&qff_rounddown) != 0 && r.readBoolean() {
		return qfd.Low
	}

	if (qfd.Flags&qff_roundup) != 0 && r.readBoolean() {
		return qfd.High
	}

	if (qfd.Flags&qff_encode_zero) != 0 && r.readBoolean() {
		return 0.0
	}

	return qfd.Low + (qfd.High-qfd.Low)*float32(r.readBits(qfd.Bitcount))*qfd.DecMul
}

// Creates a new quantized float decoder based on given field
func newQuantizedFloatDecoder(bitCount, flags *int32, lowValue, highValue *float32) *quantizedFloatDecoder {
	qfd := &quantizedFloatDecoder{}

	// Set common properties
	if *bitCount == 0 || *bitCount >= 32 {
		qfd.NoScale = true
		qfd.Bitcount = 32
		return qfd
	} else {
		qfd.NoScale = false
		qfd.Bitcount = uint32(*bitCount)
		qfd.Offset = 0.0

		if lowValue != nil {
			qfd.Low = *lowValue
		} else {
			qfd.Low = 0.0
		}

		if highValue != nil {
			qfd.High = *highValue
		} else {
			qfd.High = 1.0
		}
	}
	if flags != nil {
		qfd.Flags = uint32(*flags)
	} else {
		qfd.Flags = 0
	}

	// Validate flags
	qfd.validateFlags()

	// Handle Round Up, Round Down
	steps := (1 << uint(qfd.Bitcount))

	Range := float32(0)
	if (qfd.Flags & qff_rounddown) != 0 {
		Range = qfd.High - qfd.Low
		qfd.Offset = (Range / float32(steps))
		qfd.High -= qfd.Offset
	} else if (qfd.Flags & qff_roundup) != 0 {
		Range = qfd.High - qfd.Low
		qfd.Offset = (Range / float32(steps))
		qfd.Low += qfd.Offset
	}

	// Handle integer encoding flag
	if (qfd.Flags & qff_encode_integers) != 0 {
		delta := qfd.High - qfd.Low

		if delta < 1 {
			delta = 1
		}

		deltaLog2 := math.Ceil(math.Log2(float64(delta)))
		Range2 := (1 << uint(deltaLog2))
		bc := qfd.Bitcount

		for 1 == 1 {
			if (1 << uint(bc)) > Range2 {
				break
			} else {
				bc++
			}
		}

		if bc > qfd.Bitcount {
			qfd.Bitcount = bc
			steps = (1 << uint(qfd.Bitcount))
		}

		qfd.Offset = float32(Range2) / float32(steps)
		qfd.High = qfd.Low + float32(Range2) - qfd.Offset
	}

	// Assign multipliers
	qfd.assignMultipliers(uint32(steps))

	// Remove unessecary flags
	if (qfd.Flags & qff_rounddown) != 0 {
		if qfd.quantize(qfd.Low) == qfd.Low {
			qfd.Flags &= ^qff_rounddown
		}
	}

	if (qfd.Flags & qff_roundup) != 0 {
		if qfd.quantize(qfd.High) == qfd.High {
			qfd.Flags &= ^qff_roundup
		}
	}

	if (qfd.Flags & qff_encode_zero) != 0 {
		if qfd.quantize(0.0) == 0.0 {
			qfd.Flags &= ^qff_encode_zero
		}
	}

	return qfd
}

// ------------------------------------------------------------------------- //
// 
// ------------------------------------------------------------------------- //


