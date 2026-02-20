package manta

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dotabuff/manta/dota"
	"github.com/golang/snappy"
)

// The first 8 bytes of a replay for Source 1 and Source 2
var magicSource1 = []byte{'P', 'U', 'F', 'D', 'E', 'M', 'S', '\000'}
var magicSource2 = []byte{'P', 'B', 'D', 'E', 'M', 'S', '2', '\000'}

// Parser is an instance of the replay parser
type Parser struct {
	// Callbacks provide a mechanism for receiving notification
	// when a specific message has been received and decoded.
	Callbacks *Callbacks

	// Contains the game tick associated with the last message processed.
	Tick uint32

	// Contains the net tick associated with the last net message processed.
	NetTick uint32

	// Stores the game build.
	GameBuild uint32

	// AfterStopCallback is a function to be called when the parser stops.
	AfterStopCallback func()

	classBaselines             map[int32][]byte
	classesById                map[int32]*class
	classesByName              map[string]*class
	classIdSize                uint32
	classInfo                  bool
	entities                   map[int32]*Entity
	entityFullPackets          int
	entityHandlers             []EntityHandler
	gameEventHandlers          map[string][]GameEventHandler
	gameEventNames             map[int32]string
	gameEventTypes             map[string]*gameEventType
	isStopping                 bool
	modifierTableEntryHandlers []ModifierTableEntryHandler
	serializers                map[string]*serializer
	stream                     *stream
	stringTables               *stringTables
	stopAtTick                 uint32
}

// Create a new parser from a byte slice.
func NewParser(buf []byte) (*Parser, error) {
	r := bytes.NewReader(buf)
	return NewStreamParser(r)
}

// Create a new Parser from an io.Reader
func NewStreamParser(r io.Reader) (*Parser, error) {
	// Create a new parser with an internal reader for the given buffer.
	parser := &Parser{
		Callbacks: newCallbacks(),
		Tick:      0,
		NetTick:   0,
		GameBuild: 0,

		classBaselines:    make(map[int32][]byte),
		classesById:       make(map[int32]*class),
		classesByName:     make(map[string]*class),
		entities:          make(map[int32]*Entity),
		entityHandlers:    make([]EntityHandler, 0),
		gameEventHandlers: make(map[string][]GameEventHandler),
		gameEventNames:    make(map[int32]string),
		gameEventTypes:    make(map[string]*gameEventType),
		isStopping:        false,
		serializers:       make(map[string]*serializer),
		stream:            newStream(r),
		stringTables:      newStringTables(),
	}

	// Parse out the header, ensuring that it's valid.
	magic, err := parser.stream.readBytes(8)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(magic, magicSource2) {
		return nil, _errorf("unexpected magic: expected %s, got %s", magicSource2, magic)
	}

	// Skip the next 8 bytes, which appear to be two int32s related to the size
	// of the demo file. We may need them in the future, but not so far.
	parser.stream.readBytes(8)

	// Internal handlers
	parser.Callbacks.OnCDemoPacket(parser.onCDemoPacket)
	parser.Callbacks.OnCDemoSignonPacket(parser.onCDemoPacket)
	parser.Callbacks.OnCDemoFullPacket(parser.onCDemoFullPacket)
	parser.Callbacks.OnCSVCMsg_CreateStringTable(parser.onCSVCMsg_CreateStringTable)
	parser.Callbacks.OnCSVCMsg_UpdateStringTable(parser.onCSVCMsg_UpdateStringTable)
	parser.Callbacks.OnCSVCMsg_ServerInfo(parser.onCSVCMsg_ServerInfo)
	parser.Callbacks.OnCMsgSource1LegacyGameEventList(parser.onCMsgSource1LegacyGameEventList)
	parser.Callbacks.OnCMsgSource1LegacyGameEvent(parser.onCMsgSource1LegacyGameEvent)

	parser.Callbacks.OnCDemoClassInfo(parser.onCDemoClassInfo)
	parser.Callbacks.OnCDemoSendTables(parser.onCDemoSendTables)
	parser.Callbacks.OnCSVCMsg_PacketEntities(parser.onCSVCMsg_PacketEntities)

	// Maintains the value of parser.Tick
	parser.Callbacks.OnCNETMsg_Tick(func(m *dota.CNETMsg_Tick) error {
		parser.NetTick = m.GetTick()
		return nil
	})

	return parser, nil
}

// Start parsing the replay. Will stop processing new events after Stop() is called.
func (p *Parser) Start() (err error) {
	var msg *outerMessage

	defer p.afterStop()

	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", p)
			}
		}
	}()

	// Loop through all outer messages until we're signaled to stop. Stopping
	// happens when either the OnCDemoStop message is encountered or
	// parser.Stop() is called programatically.
	for !p.isStopping {
		if p.stopAtTick > 0 && p.Tick > p.stopAtTick {
			return
		}

		msg, err = p.readOuterMessage()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return
		}

		p.Tick = msg.tick

		if err = p.Callbacks.callByDemoType(msg.typeId, msg.data); err != nil {
			return
		}
	}

	return
}

// Stop parsing the replay, causing the parser to stop processing new events.
func (p *Parser) Stop() {
	p.isStopping = true
}

func (p *Parser) afterStop() {
	if p.AfterStopCallback != nil {
		p.AfterStopCallback()
	}
}

// Describes a demo message parsed from the replay.
type outerMessage struct {
	tick   uint32
	typeId int32
	data   []byte
}

// Read the next outer message from the buffer.
func (p *Parser) readOuterMessage() (*outerMessage, error) {
	// Read a command header, which includes both the message type
	// well as a flag to determine whether or not whether or not the
	// message is compressed with snappy.
	command, err := p.stream.readCommand()
	if err != nil {
		return nil, err
	}

	// Extract the type and compressed flag out of the command
	msgType := int32(command & ^dota.EDemoCommands_DEM_IsCompressed)
	msgCompressed := (command & dota.EDemoCommands_DEM_IsCompressed) == dota.EDemoCommands_DEM_IsCompressed

	// Read the tick that the message corresponds with.
	tick, err := p.stream.readVarUint32()
	if err != nil {
		return nil, err
	}

	// This appears to actually be an int32, where a -1 means pre-game.
	if tick == 4294967295 {
		tick = 0
	}

	// Read the size and following buffer.
	size, err := p.stream.readVarUint32()
	if err != nil {
		return nil, err
	}

	buf, err := p.stream.readBytes(size)
	if err != nil {
		return nil, err
	}

	// If the buffer is compressed, decompress it with snappy.
	if msgCompressed {
		var err error
		if buf, err = snappy.Decode(nil, buf); err != nil {
			return nil, err
		}
	}

	// Return the message
	msg := &outerMessage{
		tick:   tick,
		typeId: msgType,
		data:   buf,
	}
	return msg, nil
}

// parseToTick configures this Parser to stop once it has parsed the given tick.
func (p *Parser) parseToTick(n uint32) {
	p.stopAtTick = n
}

// ------------------------------------------------------------------------- //
// CDemoPacket
// ------------------------------------------------------------------------- //

// A message that has been read from an outerMessage but not yet processed.
type pendingMessage struct {
	tick uint32
	t    int32
	buf  []byte
}

// Calculates the priority of the message. Lower is more important.
func (m *pendingMessage) priority() int {
	switch m.t {
	case
		// These messages provide context needed for the rest of the tick
		// and should have the highest priority.
		int32(dota.NET_Messages_net_Tick),
		int32(dota.SVC_Messages_svc_CreateStringTable),
		int32(dota.SVC_Messages_svc_UpdateStringTable),
		int32(dota.NET_Messages_net_SpawnGroup_Load):
		return -10

	case
		// These messages benefit from having context but may also need to
		// provide context in terms of delta updates.
		int32(dota.SVC_Messages_svc_PacketEntities):
		return 5

	case
		// These messages benefit from having as much context as possible and
		// should have the lowest priority.
		int32(dota.EBaseGameEvents_GE_Source1LegacyGameEvent):
		return 10
	}

	return 0
}

// Provides a sortable structure for storing messages in the same packet.
type pendingMessages []*pendingMessage

func (ms pendingMessages) Len() int      { return len(ms) }
func (ms pendingMessages) Swap(i, j int) { ms[i], ms[j] = ms[j], ms[i] }
func (ms pendingMessages) Less(i, j int) bool {
	if ms[i].tick > ms[j].tick {
		return false
	}
	if ms[i].tick < ms[j].tick {
		return true
	}
	return ms[i].priority() < ms[j].priority()
}

// Internal parser for callback OnCDemoPacket, responsible for extracting
// multiple inner packets from a single CDemoPacket. This is the main structure
// that contains all other data types in the demo file.
func (p *Parser) onCDemoPacket(m *dota.CDemoPacket) error {
	// Create a slice to store pending mesages. Messages are read first as
	// pending messages then sorted before dispatch.
	ms := make(pendingMessages, 0, 2)

	// Read all messages from the buffer. Messages are packed serially as
	// {type, size, data}. We keep reading until until less than a byte remains.
	r := newReader(m.GetData())
	for r.remBytes() > 0 {
		t := int32(r.readUBitVar())
		size := r.readVarUint32()
		buf := r.readBytes(size)
		ms = append(ms, &pendingMessage{p.Tick, t, buf})
	}

	// Sort messages to ensure dependencies are met. For example, we need to
	// process string tables before game events that may reference them.
	sort.Sort(ms)

	// Dispatch messages in order, returning on handler error.
	for _, m := range ms {
		if err := p.Callbacks.callByPacketType(m.t, m.buf); err != nil {
			return err
		}
	}

	return nil
}

// Internal parser for callback OnCDemoFullPacket.
func (p *Parser) onCDemoFullPacket(m *dota.CDemoFullPacket) error {
	// Per Valve docs, parse the CDemoStringTables first.
	if m.StringTable != nil {
		if err := p.onCDemoStringTables(m.GetStringTable()); err != nil {
			return err
		}
	}

	// Then the CDemoPacket.
	if m.Packet != nil {
		if err := p.onCDemoPacket(m.GetPacket()); err != nil {
			return err
		}
	}

	return nil
}


// ------------------------------------------------------------------------- //
// 
// ------------------------------------------------------------------------- //

var gameBuildRegexp = regexp.MustCompile(`/dota_v(\d+)/`)

type class struct {
	classId    int32
	name       string
	serializer *serializer
}

func (c *class) getNameForFieldPath(fp *fieldPath) string {
	return strings.Join(c.serializer.getNameForFieldPath(fp, 0), ".")
}

func (c *class) getFieldPathForName(fp *fieldPath, name string) bool {
	return c.serializer.getFieldPathForName(fp, name)
}

func (c *class) getFieldPaths(fp *fieldPath, state *fieldState) []*fieldPath {
	return c.serializer.getFieldPaths(fp, state)
}

// Internal callback for OnCSVCMsg_ServerInfo.
func (p *Parser) onCSVCMsg_ServerInfo(m *dota.CSVCMsg_ServerInfo) error {
	// This may be needed to parse PacketEntities.
	p.classIdSize = uint32(math.Log(float64(m.GetMaxClasses()))/math.Log(2)) + 1

	// Extract the build from the game dir.
	matches := gameBuildRegexp.FindStringSubmatch(m.GetGameDir())
	if len(matches) < 2 {
		return fmt.Errorf("unable to determine game build from '%s'", m.GetGameDir())
	}
	build, err := strconv.ParseUint(matches[1], 10, 32)
	if err != nil {
		return err
	}
	p.GameBuild = uint32(build)

	return nil
}

// Internal callback for OnCDemoClassInfo.
func (p *Parser) onCDemoClassInfo(m *dota.CDemoClassInfo) error {
	for _, c := range m.GetClasses() {
		classId := c.GetClassId()
		networkName := c.GetNetworkName()

		class := &class{
			classId:    classId,
			name:       networkName,
			serializer: p.serializers[networkName],
		}
		p.classesById[class.classId] = class
		p.classesByName[class.name] = class
	}

	p.classInfo = true

	p.updateInstanceBaseline()

	return nil
}

func (p *Parser) updateInstanceBaseline() {
	// We can't update the instancebaseline until we have class info.
	if !p.classInfo {
		return
	}

	stringTable, ok := p.stringTables.GetTableByName("instancebaseline")
	if !ok {
		if v(1) {
			_debugf("skipping updateInstanceBaseline: no instancebaseline string table")
		}
		return
	}

	// Iterate through instancebaseline table items
	for _, item := range stringTable.Items {
		classId, err := atoi32(item.Key)
		if err != nil {
			_panicf("invalid instancebaseline key '%s': %s", item.Key, err)
		}
		p.classBaselines[classId] = item.Value
	}
}

// Convert a string to an int32
func atoi32(s string) (int32, error) {
	n, err := strconv.ParseInt(s, 0, 32)
	if err != nil {
		return 0, err
	}
	return int32(n), nil
}

// ------------------------------------------------------------------------- //
// 
// ------------------------------------------------------------------------- //
