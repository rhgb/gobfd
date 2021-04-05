package bfd

import "encoding"

type (
	AuthType uint8
)

const (
	AuthTypeNone           AuthType = 0
	AuthTypeSimple         AuthType = 1 // Simple Password
	AuthTypeKeyedMd5       AuthType = 2 // Keyed MD5
	AuthTypeMeticulousMd5  AuthType = 3 // Meticulous Keyed MD5
	AuthTypeKeyedSha1      AuthType = 4 // Keyed SHA1
	AuthTypeMeticulousSha1 AuthType = 5 // Meticulous Keyed SHA1
)

type AuthenticationSection struct {
	AuthType           AuthType
	AuthLen            uint8
	AuthenticationData *AuthenticationData
}

func (authSect AuthenticationSection) MarshalBinary() (data []byte, err error) {
	// todo
	return nil, nil
}

func (authSect *AuthenticationSection) UnmarshalBinary(data []byte) error {
	// todo
	return nil
}

type AuthenticationData interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}
