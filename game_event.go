package manta

import (
	"github.com/dotabuff/manta/dota"
)

const (
	gameEventTypeString = iota + 1
	gameEventTypeFloat
	gameEventTypeLong
	gameEventTypeShort
	gameEventTypeByte
	gameEventTypeBool
	gameEventTypeUint64
)

var gameEventTypeNames = map[int32]string{
	gameEventTypeString: "string",
	gameEventTypeFloat:  "float",
	gameEventTypeLong:   "long",
	gameEventTypeShort:  "short",
	gameEventTypeByte:   "byte",
	gameEventTypeBool:   "bool",
	gameEventTypeUint64: "uint64",
}

// Represents a game event. Includes a type and the actual message.
type GameEvent struct {
	t *gameEventType
	m *dota.CMsgSource1LegacyGameEvent
}

// GameEventHandler is a function that can receive a game event
type GameEventHandler func(*GameEvent) error

// The type of a game event.
// Has an identifier, name, and ordered fields.
type gameEventType struct {
	eventId int32
	name    string
	fields  map[string]*gameEventField
}

// The type of a field in a game event.
// Has an index, name and type.
type gameEventField struct {
	i int
	n string
	t int32
}

// Internal handler for callback OnCMsgSource1LegacyGameEventList.
// Registers game event names and types with the parser for O(1) lookup later.
func (p *Parser) onCMsgSource1LegacyGameEventList(m *dota.CMsgSource1LegacyGameEventList) error {
	for _, d := range m.GetDescriptors() {
		t := &gameEventType{
			eventId: d.GetEventid(),
			name:    d.GetName(),
			fields:  make(map[string]*gameEventField),
		}
		for i, k := range d.GetKeys() {
			t.fields[k.GetName()] = &gameEventField{
				i: int(i),
				n: k.GetName(),
				t: k.GetType(),
			}
		}
		p.gameEventNames[d.GetEventid()] = d.GetName()
		p.gameEventTypes[d.GetName()] = t
	}

	return nil
}

// Internal handler for callback OnCMsgSource1LegacyGameEvent.
// Looks up the name and type of an event and offers it to registered handlers.
func (p *Parser) onCMsgSource1LegacyGameEvent(m *dota.CMsgSource1LegacyGameEvent) error {
	// Look up the handler name by event id.
	name, ok := p.gameEventNames[m.GetEventid()]
	if !ok {
		return _errorf("unknown event id: %d", m.GetEventid())
	}

	// Get the handlers for the event name. Return early if none.
	handlers := p.gameEventHandlers[name]
	if handlers == nil {
		return nil
	}

	// Get the type for the event.
	t, ok := p.gameEventTypes[name]
	if !ok {
		return _errorf("unknown event type: %s", name)
	}

	// Create a GameEvent, offer to all handlers.
	e := &GameEvent{t: t, m: m}
	for _, h := range handlers {
		if err := h(e); err != nil {
			return err
		}
	}

	return nil
}

// OnGameEvent registers an GameEventHandler that will be called when a
// named GameEvent occurs.
func (p *Parser) OnGameEvent(name string, fn GameEventHandler) {
	p.gameEventHandlers[name] = append(p.gameEventHandlers[name], fn)
}
