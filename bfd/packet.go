package bfd

import (
	"bytes"
	"encoding/binary"
	"errors"
)

type (
	Diagnostic uint8
	State      uint8
)

const (
	StateAdminDown         State      = 0 // AdminDown
	StateDown              State      = 1 // Down
	StateInit              State      = 2 // Init
	StateUp                State      = 3 // Up
	DiagNone               Diagnostic = 0 // No Diagnostic
	DiagTimeExpired        Diagnostic = 1 // Control Detection Time Expired
	DiagEchoFailed         Diagnostic = 2 // Echo Function Failed
	DiagNeighborSignalDown Diagnostic = 3 // Neighbor Signaled Session Down
	DiagForwardPlaneReset  Diagnostic = 4 // Forwarding Plane Reset
	DiagPathDown           Diagnostic = 5 // Path Down
	DiagConcatPathDown     Diagnostic = 6 // Concatenated Path Down
	DiagAdminDown          Diagnostic = 7 // Administratively Down
	DiagRevConcatPathDown  Diagnostic = 8 // Reverse Concatenated Path Down
)

func (s State) String() string {
	switch s {
	case StateAdminDown:
		return "AdminDown"
	case StateDown:
		return "Down"
	case StateInit:
		return "Init"
	case StateUp:
		return "Up"
	default:
		return "Unknown"
	}
}

type ControlPacket struct {
	Version                   uint8
	Diagnostic                Diagnostic
	State                     State
	Poll                      bool
	Final                     bool
	ControlPlaneIndependent   bool
	AuthenticationPresent     bool
	Demand                    bool
	Multipoint                bool
	DetectMult                uint8
	Length                    uint8
	MyDiscriminator           uint32
	YourDiscriminator         uint32
	DesiredMinTXInterval      uint32
	RequiredMinRXInterval     uint32
	RequiredMinEchoRXInterval uint32
	AuthenticationSection     *AuthenticationSection
}

func UnmarshalReceivedControlPacket(data []byte) (cp *ControlPacket, err error) {
	cp = &ControlPacket{}
	err = cp.UnmarshalBinary(data)
	if err != nil {
		return nil, err
	}
	if cp.Version != 1 ||
		cp.DetectMult == 0 ||
		cp.Multipoint ||
		cp.MyDiscriminator == 0 ||
		(cp.YourDiscriminator == 0 && cp.State != StateDown && cp.State != StateAdminDown) {
		return nil, errors.New("illegal field values for control packet")
	}
	return cp, nil
}

func (cp *ControlPacket) MarshalBinary() (data []byte, err error) {
	flags := (uint8(cp.State) & 0x03) << 6
	if cp.Poll {
		flags |= 0x20
	}
	if cp.Final {
		flags |= 0x10
	}
	if cp.ControlPlaneIndependent {
		flags |= 0x8
	}
	if cp.AuthenticationPresent {
		flags |= 0x4
	}
	if cp.Demand {
		flags |= 0x2
	}
	if cp.Multipoint {
		flags |= 0x1
	}
	var authSect []byte
	if cp.AuthenticationPresent {
		if cp.AuthenticationSection == nil {
			return nil, errors.New("no auth section presents")
		}
		authSect, err = cp.AuthenticationSection.MarshalBinary()
		if err != nil {
			return nil, err
		}
	}
	if int(cp.Length) != 24+len(authSect) {
		return nil, errors.New("length field doesn't match actual packet")
	}

	buf := bytes.NewBuffer(make([]byte, 0, cp.Length))
	_ = binary.Write(buf, binary.BigEndian, (cp.Version&0x07)<<5|uint8(cp.Diagnostic)&0x1f)
	_ = binary.Write(buf, binary.BigEndian, flags)
	_ = binary.Write(buf, binary.BigEndian, cp.DetectMult)
	_ = binary.Write(buf, binary.BigEndian, cp.Length)
	_ = binary.Write(buf, binary.BigEndian, cp.MyDiscriminator)
	_ = binary.Write(buf, binary.BigEndian, cp.YourDiscriminator)
	_ = binary.Write(buf, binary.BigEndian, cp.DesiredMinTXInterval)
	_ = binary.Write(buf, binary.BigEndian, cp.RequiredMinRXInterval)
	_ = binary.Write(buf, binary.BigEndian, cp.RequiredMinEchoRXInterval)
	_ = binary.Write(buf, binary.BigEndian, cp.RequiredMinRXInterval)
	if authSect != nil {
		_ = binary.Write(buf, binary.BigEndian, authSect)
	}
	return buf.Bytes(), nil
}

func (cp *ControlPacket) UnmarshalBinary(data []byte) error {
	var err error
	length := ControlPacketByteLength(data)
	if length < 0 || length > len(data) {
		return errors.New("length field is greater than the payload")
	}

	cp.Version = (data[0] & 0xe0) >> 5
	cp.Diagnostic = Diagnostic(data[0] & 0x1f)
	cp.State = State((data[1] & 0xd0) >> 6)
	cp.Poll = data[1]&0x20 != 0
	cp.Final = data[1]&0x10 != 0
	cp.ControlPlaneIndependent = data[1]&0x08 != 0
	cp.AuthenticationPresent = data[1]&0x04 != 0
	cp.Demand = data[1]&0x02 != 0
	cp.Multipoint = data[1]&0x01 != 0
	cp.DetectMult = data[2]
	cp.Length = data[3]

	if length < 24 || (cp.AuthenticationPresent && length < 26) {
		return errors.New("length field is less than the minimum correct value")
	}

	cp.MyDiscriminator = binary.BigEndian.Uint32(data[4:8])
	cp.YourDiscriminator = binary.BigEndian.Uint32(data[8:12])
	cp.DesiredMinTXInterval = binary.BigEndian.Uint32(data[12:16])
	cp.RequiredMinRXInterval = binary.BigEndian.Uint32(data[16:20])
	cp.RequiredMinEchoRXInterval = binary.BigEndian.Uint32(data[20:24])

	if cp.AuthenticationPresent {
		authSect := AuthenticationSection{}
		err = authSect.UnmarshalBinary(data[24:])
		if err == nil {
			cp.AuthenticationSection = &authSect
		}
	}

	return err
}

func ControlPacketByteLength(data []byte) int {
	if len(data) < 4 {
		return -1
	}
	return int(data[3])
}
