package manta

import (
	"github.com/dotabuff/manta/dota"
	"github.com/golang/protobuf/proto"
	"fmt"
	"regexp"
	"strconv"
)

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

var itemCounts = map[string]int{
	"MAX_ITEM_STOCKS":             8,
	"MAX_ABILITY_DRAFT_ABILITIES": 48,
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
// fieldType
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

func (t *fieldType) String() string {
	x := t.baseType
	if t.genericType != nil {
		x += "<" + t.genericType.String() + ">"
	}
	if t.pointer {
		x += "*"
	}
	if t.count > 0 {
		x += "[" + strconv.Itoa(t.count) + "]"
	}
	return x
}

// ------------------------------------------------------------------------- //
// 
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
// 
// ------------------------------------------------------------------------- //
