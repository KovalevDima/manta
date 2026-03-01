package manta

import (
	"strings"

	"github.com/dotabuff/manta/dota"
)

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

var entityOpNames = map[EntityOp]string{
	EntityOpNone:           "None",
	EntityOpCreated:        "Created",
	EntityOpUpdated:        "Updated",
	EntityOpDeleted:        "Deleted",
	EntityOpEntered:        "Entered",
	EntityOpLeft:           "Left",
	EntityOpCreatedEntered: "Created+Entered",
	EntityOpUpdatedEntered: "Updated+Entered",
	EntityOpDeletedLeft:    "Deleted+Left",
}

// Flag determines whether an EntityOp includes another. This is primarily
// offered to prevent bit flag errors in downstream clients.
func (o EntityOp) Flag(p EntityOp) bool {
	return o&p != 0
}


// EntityHandler is a function that receives Entity updates
type EntityHandler func(*Entity, EntityOp) error

// Entity represents a single game entity in the replay
type Entity struct {
	index   int32
	serial  int32
	class   *class
	active  bool
	state   *fieldState
	fpCache map[string]*fieldPath
	fpNoop  map[string]bool
}

// newEntity returns a new entity for the given index, serial and class
func newEntity(index, serial int32, class *class) *Entity {
	return &Entity{
		index:   index,
		serial:  serial,
		class:   class,
		active:  true,
		state:   newFieldState(),
		fpCache: make(map[string]*fieldPath),
		fpNoop:  make(map[string]bool),
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
					_panicf("unable to find new class %d", classId)
				}

				baseline := p.classBaselines[classId]
				if baseline == nil {
					_panicf("unable to find new baseline %d", classId)
				}

				e = newEntity(index, serial, class)
				p.entities[index] = e
				readFields(newReader(baseline), class.serializer, e.state)
				readFields(r, class.serializer, e.state)
				op = EntityOpCreated | EntityOpEntered

			} else {
				if e = p.entities[index]; e == nil {
					_panicf("unable to find existing entity %d", index)
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
				_panicf("unable to find existing entity %d", index)
			}

			if !e.active {
				_panicf("entity %d (%s) ordered to leave, already inactive", e.class.classId, e.class.name)
			}

			op = EntityOpLeft
			if cmd&0x02 != 0 {
				op |= EntityOpDeleted
				p.entities[index] = nil
			}
		}

		tuples = append(tuples, tuple{e, op})
	}

	for _, h := range p.entityHandlers {
		for _, t := range tuples {
			if err := h(t.e, t.op); err != nil {
				return err
			}
		}
	}

	return nil
}

// OnEntity registers an EntityHandler that will be called when an entity
// is created, updated, deleted, etc.
func (p *Parser) OnEntity(h EntityHandler) {
	p.entityHandlers = append(p.entityHandlers, h)
}

// ------------------------------------------------------------------------- //
// Field state
// ------------------------------------------------------------------------- //

type fieldState struct {
	state []interface{}
}

func newFieldState() *fieldState {
	return &fieldState{
		state: make([]interface{}, 8),
	}
}

func (s *fieldState) get(fp *fieldPath) interface{} {
	x := s
	z := 0
	for i := 0; i <= fp.last; i++ {
		z = fp.path[i]
		if len(x.state) < z+2 {
			return nil
		}
		if i == fp.last {
			return x.state[z]
		}
		if _, ok := x.state[z].(*fieldState); !ok {
			return nil
		}
		x = x.state[z].(*fieldState)
	}
	return nil
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
// Fields reader
// ------------------------------------------------------------------------- //

func readFields(r *reader, s *serializer, state *fieldState) {
	fps := readFieldPaths(r)

	for _, fp := range fps {
		decoder := s.getDecoderForFieldPathSer(fp, 0)

		val := decoder(r)
		state.set(fp, val)

		if v(6) {
			name := strings.Join(s.getNameForFieldPathSer(fp, 0), ".")
			fp2 := newFieldPath()
			b := s.getFieldPathForNameSer(fp2, name)

			if !b {
				_panicf("GOT NO FP: name=%s fp2=%#vv", name, fp2)
			}

			if fp2.String() != fp.String() {
				_panicf("GOT FP MISMATCH: fp=%s fp2=%s", fp, fp2)
			}

			fp2.release()

			_debugf(" => %#v", val)
		}

		fp.release()
	}
}

// ------------------------------------------------------------------------- //
// 
// ------------------------------------------------------------------------- //
