package touchfw

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

var synaHeaderID = [4]uint16{0x5953, 0x414E, 0x4320, 0x4746} // "SYNA CFG"

// AppConfigHeader is the 34-byte SYNA CFG header.
type AppConfigHeader struct {
	HeaderID         [4]uint16
	Checksum         uint32
	LengthWords      uint16
	BuildID          uint32
	CustomerConfigID [16]byte
}

// AppConfig owns a mutable copy of the APP_CONFIG area.
type AppConfig struct {
	Raw    []byte
	Header AppConfigHeader
	Schema *Schema
}

func ParseAppConfig(content []byte, schema *Schema) (*AppConfig, error) {
	if len(content) < 34 {
		return nil, fmt.Errorf("APP_CONFIG is too short: %d bytes", len(content))
	}
	raw := append([]byte(nil), content...)
	var h AppConfigHeader
	for i := 0; i < 4; i++ {
		h.HeaderID[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	h.Checksum = binary.LittleEndian.Uint32(raw[8:12])
	h.LengthWords = binary.LittleEndian.Uint16(raw[12:14])
	h.BuildID = binary.LittleEndian.Uint32(raw[14:18])
	copy(h.CustomerConfigID[:], raw[18:34])

	if h.LengthWords == 0 {
		return nil, errors.New("APP_CONFIG length field is zero")
	}
	if int(h.LengthWords)*2 > len(raw) {
		return nil, fmt.Errorf("APP_CONFIG length field 0x%X exceeds area size 0x%X",
			int(h.LengthWords)*2, len(raw))
	}

	return &AppConfig{Raw: raw, Header: h, Schema: schema}, nil
}

func (a *AppConfig) BlockLengthBytes() int {
	n := int(a.Header.LengthWords) * 2
	if n <= 0 || n > len(a.Raw) {
		return len(a.Raw)
	}
	return n
}

func (a *AppConfig) HeaderIDString() string {
	b := make([]byte, 8)
	for i, v := range a.Header.HeaderID {
		b[2*i] = byte(v)
		b[2*i+1] = byte(v >> 8)
	}
	return string(b)
}

func (a *AppConfig) Validate() error {
	if a.Header.HeaderID != synaHeaderID {
		return fmt.Errorf("unexpected APP_CONFIG headerID %q", a.HeaderIDString())
	}
	if a.BlockLengthBytes() > len(a.Raw) {
		return errors.New("APP_CONFIG block length is out of range")
	}
	return nil
}

func (a *AppConfig) DecodeAll() (map[string]any, error) {
	if a.Schema == nil {
		return nil, errors.New("metadata schema is not available")
	}
	return a.Schema.DecodeAll(a.Raw)
}

func (a *AppConfig) DecodeField(path string) (any, error) {
	if a.Schema == nil {
		return nil, errors.New("metadata schema is not available")
	}
	return a.Schema.DecodeField(a.Raw, path)
}

func (a *AppConfig) SetField(path string, value any) error {
	if a.Schema == nil {
		return errors.New("metadata schema is not available")
	}
	return a.Schema.SetField(a.Raw, path, value)
}

// SetChecksum applies a checksum strategy. The vendor checksum in this image
// is documented as Fletcher-32 but does not match the standard variant; use
// "preserve" unless you know the exact vendor algorithm.
func (a *AppConfig) SetChecksum(strategy string) error {
	switch strategy {
	case "", "preserve":
		return nil
	case "zero":
		put32(a.Raw[8:12], 0)
		a.Header.Checksum = 0
		return nil
	case "fletcher32":
		sum := Fletcher32(a.Raw[14:a.BlockLengthBytes()])
		put32(a.Raw[8:12], sum)
		a.Header.Checksum = sum
		return nil
	default:
		return fmt.Errorf("unknown checksum strategy %q", strategy)
	}
}

func (a *AppConfig) ChecksumMatchesFletcher32() bool {
	return a.Header.Checksum == Fletcher32(a.Raw[14:a.BlockLengthBytes()])
}

// Fletcher32 is the standard Fletcher-32 over little-endian 16-bit words.
// It is exposed for experiments; the image in this repository does not match
// this variant.
func Fletcher32(data []byte) uint32 {
	var sum1, sum2 uint32 = 0xFFFF, 0xFFFF
	for i := 0; i+1 < len(data); i += 2 {
		w := uint32(binary.LittleEndian.Uint16(data[i:]))
		sum1 = (sum1 + w) % 0xFFFF
		sum2 = (sum2 + sum1) % 0xFFFF
	}
	if len(data)%2 == 1 {
		sum1 = (sum1 + uint32(data[len(data)-1])) % 0xFFFF
		sum2 = (sum2 + sum1) % 0xFFFF
	}
	return (sum2 << 16) | sum1
}

type AppConfigHeaderDump struct {
	HeaderID             string    `json:"header_id"`
	HeaderIDWords        [4]uint16 `json:"header_id_words"`
	Checksum             uint32    `json:"checksum"`
	ChecksumHex          string    `json:"checksum_hex"`
	LengthWords          uint16    `json:"length_words"`
	BlockLength          int       `json:"block_length"`
	BuildID              uint32    `json:"build_id"`
	BuildIDHex           string    `json:"build_id_hex"`
	CustomerConfigID     string    `json:"customer_config_id"`
	CustomerConfigIDByte []byte    `json:"customer_config_id_bytes"`
}

type AppConfigDump struct {
	Header AppConfigHeaderDump `json:"header"`
	Config map[string]any      `json:"config,omitempty"`
}

func (a *AppConfig) Dump() AppConfigDump {
	h := AppConfigHeaderDump{
		HeaderID:             a.HeaderIDString(),
		HeaderIDWords:        a.Header.HeaderID,
		Checksum:             a.Header.Checksum,
		ChecksumHex:          fmt.Sprintf("0x%08X", a.Header.Checksum),
		LengthWords:          a.Header.LengthWords,
		BlockLength:          a.BlockLengthBytes(),
		BuildID:              a.Header.BuildID,
		BuildIDHex:           fmt.Sprintf("0x%08X", a.Header.BuildID),
		CustomerConfigID:     hex.EncodeToString(a.Header.CustomerConfigID[:]),
		CustomerConfigIDByte: a.Header.CustomerConfigID[:],
	}
	d := AppConfigDump{Header: h}
	if a.Schema != nil {
		if cfg, err := a.Schema.DecodeAll(a.Raw); err == nil {
			d.Config = cfg
		}
	}
	return d
}
