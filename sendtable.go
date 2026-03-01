package manta

import (
	"github.com/dotabuff/manta/dota"
	"github.com/golang/protobuf/proto"
	"fmt"
	"regexp"
	"strconv"
	"math"
	"strings"
)

// ------------------------------------------------------------------------- //
// sendtable
// ------------------------------------------------------------------------- //

var pointerTypes = map[string]bool{
	"PhysicsRagdollPose_t":       true,
	"CBodyComponent":             true,
	"CEntityIdentity":            true,
	"CPhysicsComponent":          true,
	"CRenderComponent":           true,
	"CDOTAGamerules":             true,
	"CDOTAGameManager":           true,
	"CDOTASpectatorGraphManager": true,
	"CPlayerLocalData":           true,
	"CPlayer_CameraServices":     true,
	"CDOTAGameRules":             true,
}

// Internal callback for OnCDemoSendTables.
func (p *Parser) onCDemoSendTables(m *dota.CDemoSendTables) error {
	r := newReader(m.GetData())
	buf := r.readBytes(r.readVarUint32())

	msg := &dota.CSVCMsg_FlattenedSerializer{}
	if err := proto.Unmarshal(buf, msg); err != nil {
		return err
	}

	patches := []fieldPatch{}
	for _, h := range fieldPatches {
		if h.shouldApply(p.GameBuild) {
			patches = append(patches, h)
		}
	}

	fields := map[int32]*field{}
	fieldTypes := map[string]*fieldType{}

	for _, s := range msg.GetSerializers() {
		serializer := &serializer{
			name:    msg.GetSymbols()[s.GetSerializerNameSym()],
			version: s.GetSerializerVersion(),
			fields:  []*field{},
		}

		for _, i := range s.GetFieldsIndex() {
			if _, ok := fields[i]; !ok {
				// create a new field
				field := newField(msg, msg.GetFields()[i])

				// patch parent name in builds <= 990
				if p.GameBuild <= 990 {
					field.parentName = serializer.name
				}

				// find or create a field type
				if _, ok := fieldTypes[field.varType]; !ok {
					fieldTypes[field.varType] = newFieldType(field.varType)
				}
				field.fieldType = fieldTypes[field.varType]

				// find associated serializer
				if field.serializerName != "" {
					field.serializer = p.serializers[field.serializerName]
				}

				// apply any build-specific patches to the field
				for _, h := range patches {
					h.patch(field)
				}

				// determine field model
				if field.serializer != nil {
					if field.fieldType.pointer || pointerTypes[field.fieldType.baseType] {
						field.setModel(fieldModelFixedTable)
					} else {
						field.setModel(fieldModelVariableTable)
					}
				} else if field.fieldType.count > 0 && field.fieldType.baseType != "char" {
					field.setModel(fieldModelFixedArray)
				} else if field.fieldType.baseType == "CUtlVector" || field.fieldType.baseType == "CNetworkUtlVectorBase" {
					field.setModel(fieldModelVariableArray)
				} else {
					field.setModel(fieldModelSimple)
				}

				// store the field
				fields[i] = field
			}

			// add the field to the serializer
			serializer.fields = append(serializer.fields, fields[i])
		}

		// store the serializer for field reference
		p.serializers[serializer.name] = serializer

		if _, ok := p.classesByName[serializer.name]; ok {
			p.classesByName[serializer.name].serializer = serializer
		}
	}

	return nil
}


// ------------------------------------------------------------------------- //
// field
// ------------------------------------------------------------------------- //

const (
	fieldModelSimple = iota
	fieldModelFixedArray
	fieldModelFixedTable
	fieldModelVariableArray
	fieldModelVariableTable
)

type field struct {
	parentName        string
	varName           string
	varType           string
	sendNode          string
	serializerName    string
	serializerVersion int32
	encoder           string
	encodeFlags       *int32
	bitCount          *int32
	lowValue          *float32
	highValue         *float32
	fieldType         *fieldType
	serializer        *serializer
	model             int

	decoder      fieldDecoder
	baseDecoder  fieldDecoder
	childDecoder fieldDecoder
}

func newField(ser *dota.CSVCMsg_FlattenedSerializer, f *dota.ProtoFlattenedSerializerFieldT) *field {
	resolve := func(p *int32) string {
		if p == nil {
			return ""
		}
		return ser.GetSymbols()[*p]
	}

	x := &field{
		varName:           resolve(f.VarNameSym),
		varType:           resolve(f.VarTypeSym),
		sendNode:          resolve(f.SendNodeSym),
		serializerName:    resolve(f.FieldSerializerNameSym),
		serializerVersion: f.GetFieldSerializerVersion(),
		encoder:           resolve(f.VarEncoderSym),
		encodeFlags:       f.EncodeFlags,
		bitCount:          f.BitCount,
		lowValue:          f.LowValue,
		highValue:         f.HighValue,
		model:             fieldModelSimple,
	}

	if x.sendNode == "(root)" {
		x.sendNode = ""
	}

	return x
}

func (f *field) setModel(model int) {
	f.model = model

	switch model {
	case fieldModelFixedArray:
		f.decoder = findDecoder(f)

	case fieldModelFixedTable:
		f.baseDecoder = booleanDecoder

	case fieldModelVariableArray:
		if f.fieldType.genericType == nil {
			_panicf("no generic type for variable array field %#v", f)
		}
		f.baseDecoder = unsignedDecoder
		f.childDecoder = findDecoderByBaseType(f.fieldType.genericType.baseType)

	case fieldModelVariableTable:
		f.baseDecoder = unsignedDecoder

	case fieldModelSimple:
		f.decoder = findDecoder(f)
	}
}

func (f *field) getNameForFieldPathField(fp *fieldPath, pos int) []string {
	x := []string{f.varName}

	switch f.model {
	case fieldModelFixedArray:
		if fp.last == pos {
			x = append(x, fmt.Sprintf("%04d", fp.path[pos]))
		}

	case fieldModelFixedTable:
		if fp.last >= pos {
			x = append(x, f.serializer.getNameForFieldPathSer(fp, pos)...)
		}

	case fieldModelVariableArray:
		if fp.last == pos {
			x = append(x, fmt.Sprintf("%04d", fp.path[pos]))
		}

	case fieldModelVariableTable:
		if fp.last != pos-1 {
			x = append(x, fmt.Sprintf("%04d", fp.path[pos]))
			if fp.last != pos {
				x = append(x, f.serializer.getNameForFieldPathSer(fp, pos+1)...)
			}
		}
	}

	return x
}

func (f *field) getDecoderForFieldPath(fp *fieldPath, pos int) fieldDecoder {
	switch f.model {
	case fieldModelFixedArray:
		return f.decoder

	case fieldModelFixedTable:
		if fp.last == pos-1 {
			return f.baseDecoder
		}
		return f.serializer.getDecoderForFieldPath(fp, pos)

	case fieldModelVariableArray:
		if fp.last == pos {
			return f.childDecoder
		}
		return f.baseDecoder

	case fieldModelVariableTable:
		if fp.last >= pos+1 {
			return f.serializer.getDecoderForFieldPath(fp, pos+1)
		}
		return f.baseDecoder
	}

	return f.decoder
}

func (f *field) getFieldPathForName(fp *fieldPath, name string) bool {
	switch f.model {
	case fieldModelFixedArray:
		assertLen(name, 4)
		fp.path[fp.last] = mustAtoi(name)
		return true

	case fieldModelFixedTable:
		return f.serializer.getFieldPathForName(fp, name)

	case fieldModelVariableArray:
		assertLen(name, 4)
		fp.path[fp.last] = mustAtoi(name)
		return true

	case fieldModelVariableTable:
		assertLenMin(name, 6)
		fp.path[fp.last] = mustAtoi(name[:4])
		fp.last++
		return f.serializer.getFieldPathForName(fp, name[5:])

	case fieldModelSimple:
		_panicf("not supported")
	}

	return false
}


func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		_panicf("assertion failed: '%s' not a number", s)
	}
	return n
}

func assertLen(s string, n int) {
	if len(s) != n {
		_panicf("assertion failed: '%s' is not %d long", s, n)
	}
}

func assertLenMin(s string, n int) {
	if len(s) < n {
		_panicf("assertion failed: '%s' is less than %d long", s, n)
	}
}

// ------------------------------------------------------------------------- //
// serializer
// ------------------------------------------------------------------------- //

type serializer struct {
	name    string
	version int32
	fields  []*field
}


func (s *serializer) getNameForFieldPathSer(fp *fieldPath, pos int) []string {
	return s.fields[fp.path[pos]].getNameForFieldPathField(fp, pos+1)
}

func (s *serializer) getDecoderForFieldPath(fp *fieldPath, pos int) fieldDecoder {
	index := fp.path[pos]
	if len(s.fields) <= index {
		_panicf("serializer %s: field path %s has no field (%d)", s.name, fp, index)
	}
	return s.fields[index].getDecoderForFieldPath(fp, pos+1)
}

func (s *serializer) getFieldPathForName(fp *fieldPath, name string) bool {
	for i, f := range s.fields {
		if name == f.varName {
			fp.path[fp.last] = i
			return true
		}

		if strings.HasPrefix(name, f.varName+".") {
			fp.path[fp.last] = i
			fp.last++
			return f.getFieldPathForName(fp, name[len(f.varName)+1:])
		}
	}

	return false
}


// ------------------------------------------------------------------------- //
// field_type
// ------------------------------------------------------------------------- //

var fieldTypeRe = regexp.MustCompile(`([^\<\[\*]+)(\<\s(.*)\s\>)?(\*)?(\[(.*)\])?`) // (\<\s.*?\s\>)?([.*?])?`)

type fieldType struct {
	baseType    string
	genericType *fieldType
	pointer     bool
	count       int
}

func newFieldType(name string) *fieldType {
	ss := fieldTypeRe.FindStringSubmatch(name)
	if len(ss) != 7 {
		panic(fmt.Sprintf("bad regexp: %s -> %#v", name, ss))
	}

	x := &fieldType{
		baseType: ss[1],
		pointer:  ss[4] == "*",
	}

	if ss[3] != "" {
		x.genericType = newFieldType(ss[3])
	}

	if n, ok := itemCounts[ss[6]]; ok {
		x.count = n
	} else if n, _ := strconv.Atoi(ss[6]); n > 0 {
		x.count = n
	} else if ss[6] != "" {
		x.count = 1024
	}

	return x
}

var itemCounts = map[string]int{
	"MAX_ITEM_STOCKS":             8,
	"MAX_ABILITY_DRAFT_ABILITIES": 48,
}

// ------------------------------------------------------------------------- //
// field_patch
// ------------------------------------------------------------------------- //

type fieldPatch struct {
	minBuild uint32
	maxBuild uint32
	patch    func(f *field)
}

var fieldPatches = []fieldPatch{
	fieldPatch{0, 990, func(f *field) {
		switch f.varName {
		case
			"angExtraLocalAngles",
			"angLocalAngles",
			"m_angInitialAngles",
			"m_angRotation",
			"m_ragAngles",
			"m_vLightDirection":
			if f.parentName == "CBodyComponentBaseAnimatingOverlay" {
				f.encoder = "qangle_pitch_yaw"
			} else {
				f.encoder = "QAngle"
			}

		case
			"dirPrimary",
			"localSound",
			"m_flElasticity",
			"m_location",
			"m_poolOrigin",
			"m_ragPos",
			"m_vecEndPos",
			"m_vecLadderDir",
			"m_vecPlayerMountPositionBottom",
			"m_vecPlayerMountPositionTop",
			"m_viewtarget",
			"m_WorldMaxs",
			"m_WorldMins",
			"origin",
			"vecLocalOrigin":
			f.encoder = "coord"

		case "m_vecLadderNormal":
			f.encoder = "normal"
		}
	}},
	fieldPatch{0, 954, func(f *field) {
		switch f.varName {
		case "m_flMana", "m_flMaxMana":
			f.lowValue = nil
			f.highValue = proto.Float32(8192.0)
		}
	}},
	fieldPatch{1016, 1027, func(f *field) {
		switch f.varName {
		case
			"m_bItemWhiteList",
			"m_bWorldTreeState",
			"m_iPlayerIDsInControl",
			"m_iPlayerSteamID",
			"m_ulTeamBannerLogo",
			"m_ulTeamBaseLogo",
			"m_ulTeamLogo":
			f.encoder = "fixed64"
		}
	}},
	fieldPatch{0, 0, func(f *field) {
		switch f.varName {
		case "m_flSimulationTime", "m_flAnimTime":
			f.encoder = "simtime"
		case "m_flRuneTime":
			f.encoder = "runetime"
		}
	}},
}

func (p *fieldPatch) shouldApply(build uint32) bool {
	if p.minBuild == 0 && p.maxBuild == 0 {
		return true
	}

	return build >= p.minBuild && build <= p.maxBuild
}

// ------------------------------------------------------------------------- //
// field_decoder
// ------------------------------------------------------------------------- //

type fieldDecoder func(*reader) interface{}
type fieldFactory func(*field) fieldDecoder

var fieldTypeFactories = map[string]fieldFactory{
	"float32":                  floatFactory,
	"CNetworkedQuantizedFloat": quantizedFactory,
	"Vector":                   vectorFactory(3),
	"Vector2D":                 vectorFactory(2),
	"Vector4D":                 vectorFactory(4),
	"VectorWS":                 vectorFactory(3),
	"uint64":                   unsigned64Factory,
	"QAngle":                   qangleFactory,
	"CHandle":                  unsignedFactory,
	"CStrongHandle":            unsigned64Factory,
	"CEntityHandle":            unsignedFactory,
}

var fieldNameDecoders = map[string]fieldDecoder{}

var fieldTypeDecoders = map[string]fieldDecoder{
	"bool":    booleanDecoder,
	"char":    stringDecoder,
	"color32": unsignedDecoder,
	"int16":   signedDecoder,
	"int32":   signedDecoder,
	"int64":   signedDecoder,
	"int8":    signedDecoder,
	"uint16":  unsignedDecoder,
	"uint32":  unsignedDecoder,
	"uint8":   unsignedDecoder,

	"GameTime_t":     noscaleDecoder,
	"HeroFacetKey_t": unsigned64Decoder,
	"BloodType":      unsignedDecoder,

	"CBodyComponent":       componentDecoder,
	"CGameSceneNodeHandle": unsignedDecoder,
	"Color":                unsignedDecoder,
	"CPhysicsComponent":    componentDecoder,
	"CRenderComponent":     componentDecoder,
	"CUtlString":           stringDecoder,
	"CUtlStringToken":      unsignedDecoder,
	"CUtlSymbolLarge":      stringDecoder,
}

func unsignedFactory(f *field) fieldDecoder {
	return unsignedDecoder
}

func unsigned64Factory(f *field) fieldDecoder {
	switch f.encoder {
	case "fixed64":
		return fixed64Decoder
	}
	return unsigned64Decoder
}

func floatFactory(f *field) fieldDecoder {
	switch f.encoder {
	case "coord":
		return floatCoordDecoder
	case "simtime":
		return simulationTimeDecoder
	case "runetime":
		return runeTimeDecoder
	}

	if f.bitCount == nil || (*f.bitCount <= 0 || *f.bitCount >= 32) {
		return noscaleDecoder
	}

	return quantizedFactory(f)
}

func quantizedFactory(f *field) fieldDecoder {
	qfd := newQuantizedFloatDecoder(f.bitCount, f.encodeFlags, f.lowValue, f.highValue)
	return func(r *reader) interface{} {
		return qfd.decode(r)
	}
}

func vectorFactory(n int) fieldFactory {
	return func(f *field) fieldDecoder {
		if n == 3 && f.encoder == "normal" {
			return vectorNormalDecoder
		}

		d := floatFactory(f)
		return func(r *reader) interface{} {
			x := make([]float32, n)
			for i := 0; i < n; i++ {
				x[i] = d(r).(float32)
			}
			return x
		}
	}
}

func vectorNormalDecoder(r *reader) interface{} {
	return r.read3BitNormal()
}

func fixed64Decoder(r *reader) interface{} {
	return r.readLeUint64()
}

func handleDecoder(r *reader) interface{} {
	return r.readVarUint32()
}

func booleanDecoder(r *reader) interface{} {
	return r.readBoolean()
}

func stringDecoder(r *reader) interface{} {
	return r.readString()
}

func defaultDecoder(r *reader) interface{} {
	return r.readVarUint32()
}

func signedDecoder(r *reader) interface{} {
	return r.readVarInt32()
}

func floatCoordDecoder(r *reader) interface{} {
	return r.readCoord()
}

func noscaleDecoder(r *reader) interface{} {
	return math.Float32frombits(r.readBits(32))
}

func runeTimeDecoder(r *reader) interface{} {
	return math.Float32frombits(r.readBits(4))
}

func simulationTimeDecoder(r *reader) interface{} {
	return float32(r.readVarUint32()) * (1.0 / 30)
}

func qangleFactory(f *field) fieldDecoder {
	if f.encoder == "qangle_pitch_yaw" {
		n := uint32(*f.bitCount)
		return func(r *reader) interface{} {
			return []float32{
				r.readAngle(n),
				r.readAngle(n),
				0.0,
			}
		}
	}

	if f.bitCount != nil && *f.bitCount != 0 {
		n := uint32(*f.bitCount)
		return func(r *reader) interface{} {
			return []float32{
				r.readAngle(n),
				r.readAngle(n),
				r.readAngle(n),
			}
		}
	}

	return func(r *reader) interface{} {
		ret := make([]float32, 3)
		rX := r.readBoolean()
		rY := r.readBoolean()
		rZ := r.readBoolean()
		if rX {
			ret[0] = r.readCoord()
		}
		if rY {
			ret[1] = r.readCoord()
		}
		if rZ {
			ret[2] = r.readCoord()
		}
		return ret
	}
}

func unsignedDecoder(r *reader) interface{} {
	return uint64(r.readVarUint32())
}

func unsigned64Decoder(r *reader) interface{} {
	return r.readVarUint64()
}

func componentDecoder(r *reader) interface{} {
	return r.readBits(1)
}

func findDecoder(f *field) fieldDecoder {
	if v, ok := fieldTypeFactories[f.fieldType.baseType]; ok {
		return v(f)
	}

	if v, ok := fieldNameDecoders[f.varName]; ok {
		return v
	}

	if v, ok := fieldTypeDecoders[f.fieldType.baseType]; ok {
		return v
	}

	return defaultDecoder
}

func findDecoderByBaseType(baseType string) fieldDecoder {
	if v, ok := fieldTypeDecoders[baseType]; ok {
		return v
	}

	return defaultDecoder
}

// ------------------------------------------------------------------------- //
// 
// ------------------------------------------------------------------------- //
