package touchfw

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Region describes a sub-block of APP_CONFIG. All offsets are in bits.
type Region struct {
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	Struct string `json:"struct"`
}

// Field is one schema entry. Offset and Length are in bits. Values are read
// LSB-first, which is how the vendor JSON describes the binary layout.
type Field struct {
	Offset       int             `json:"offset"`
	Length       int             `json:"length"`
	Signed       bool            `json:"signed"`
	FractionBits int             `json:"fractionBits"`
	Elements     int             `json:"elements"`
	ElementBits  int             `json:"elementBits"`
	Dims         []int           `json:"dims"`
	Type         string          `json:"type"`
	BaseType     string          `json:"baseType"`
	Default      json.RawMessage `json:"default"`
	ArrayLength  json.RawMessage `json:"arraylength"`
}

// ConfigBlock is the header/schema part of the metadata JSON.
type ConfigBlock struct {
	TouchReportDefinition Field  `json:"touchReportDefinition"`
	HeaderID              Field  `json:"headerID"`
	Checksum              Field  `json:"checksum"`
	DynamicConfig         Region `json:"dynamicConfig"`
	BuildID               Field  `json:"buildID"`
	Length                Field  `json:"length"`
	CustomerConfigID      Field  `json:"customerConfigId"`
	StaticConfig          Region `json:"staticConfig"`
}

// Schema is the decoded metadata JSON. Static and Dynamic map field names to
// bit-addressed fields relative to their region base.
type Schema struct {
	ConfigBlock     ConfigBlock                `json:"configBlock"`
	Static          map[string]Field           `json:"staticConfiguration"`
	Dynamic         map[string]Field           `json:"dynamicConfiguration"`
	TouchInfoPacket map[string]json.RawMessage `json:"touchInfoPacket"`
	Enums           map[string]json.RawMessage `json:"enums"`
}

func LoadSchema(data []byte) (*Schema, error) {
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("schema JSON: %w", err)
	}
	if s.ConfigBlock.StaticConfig.Length == 0 && s.ConfigBlock.DynamicConfig.Length == 0 {
		return nil, errors.New("schema has no config regions")
	}
	return &s, nil
}

func (f Field) Count() int {
	if f.Elements > 0 {
		return f.Elements
	}
	if len(f.Dims) > 0 {
		n := 1
		for _, d := range f.Dims {
			if d > 0 {
				n *= d
			}
		}
		if n > 0 {
			return n
		}
	}
	return 1
}

func (f Field) BitsPerElement() int {
	if f.ElementBits > 0 {
		return f.ElementBits
	}
	if n := f.Count(); n > 0 && f.Length > 0 {
		return f.Length / n
	}
	return f.Length
}

// Resolve maps a CLI path such as staticConfiguration.numColsDoze to a bit
// base and field description.
func (s *Schema) Resolve(path string) (int, Field, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, Field{}, errors.New("empty field path")
	}
	parts := strings.Split(path, ".")
	region := strings.ToLower(parts[0])

	switch region {
	case "static", "staticconfiguration":
		if len(parts) != 2 {
			return 0, Field{}, fmt.Errorf("use %s.<field>", parts[0])
		}
		f, ok := s.Static[parts[1]]
		if !ok {
			return 0, Field{}, fmt.Errorf("unknown static field %q", parts[1])
		}
		return s.ConfigBlock.StaticConfig.Offset, f, nil

	case "dynamic", "dynamicconfiguration":
		if len(parts) != 2 {
			return 0, Field{}, fmt.Errorf("use %s.<field>", parts[0])
		}
		f, ok := s.Dynamic[parts[1]]
		if !ok {
			return 0, Field{}, fmt.Errorf("unknown dynamic field %q", parts[1])
		}
		return s.ConfigBlock.DynamicConfig.Offset, f, nil

	case "touchreportdefinition", "touch":
		if len(parts) != 1 {
			return 0, Field{}, errors.New("touchReportDefinition is a single array")
		}
		return 0, s.ConfigBlock.TouchReportDefinition, nil

	case "configblock", "header":
		if len(parts) != 2 {
			return 0, Field{}, fmt.Errorf("use %s.<field>", parts[0])
		}
		switch parts[1] {
		case "headerID", "headerid":
			return 0, s.ConfigBlock.HeaderID, nil
		case "checksum":
			return 0, s.ConfigBlock.Checksum, nil
		case "length":
			return 0, s.ConfigBlock.Length, nil
		case "buildID", "buildid":
			return 0, s.ConfigBlock.BuildID, nil
		case "customerConfigId", "customerConfigID", "customerconfigid":
			return 0, s.ConfigBlock.CustomerConfigID, nil
		default:
			return 0, Field{}, fmt.Errorf("unknown configBlock field %q", parts[1])
		}
	default:
		return 0, Field{}, fmt.Errorf("unknown region %q", parts[0])
	}
}

// DecodeAll returns a JSON-friendly view of the whole APP_CONFIG block.
func (s *Schema) DecodeAll(data []byte) (map[string]any, error) {
	out := map[string]any{}

	cb := map[string]any{}
	for name, f := range map[string]Field{
		"headerID":         s.ConfigBlock.HeaderID,
		"checksum":         s.ConfigBlock.Checksum,
		"length":           s.ConfigBlock.Length,
		"buildID":          s.ConfigBlock.BuildID,
		"customerConfigId": s.ConfigBlock.CustomerConfigID,
	} {
		v, err := decodeField(data, 0, f)
		if err != nil {
			return nil, fmt.Errorf("configBlock.%s: %w", name, err)
		}
		cb[name] = v
	}
	out["configBlock"] = cb

	st := make(map[string]any, len(s.Static))
	for name, f := range s.Static {
		v, err := decodeField(data, s.ConfigBlock.StaticConfig.Offset, f)
		if err != nil {
			return nil, fmt.Errorf("staticConfiguration.%s: %w", name, err)
		}
		st[name] = v
	}
	out["staticConfiguration"] = st

	dy := make(map[string]any, len(s.Dynamic))
	for name, f := range s.Dynamic {
		v, err := decodeField(data, s.ConfigBlock.DynamicConfig.Offset, f)
		if err != nil {
			return nil, fmt.Errorf("dynamicConfiguration.%s: %w", name, err)
		}
		dy[name] = v
	}
	out["dynamicConfiguration"] = dy

	touch, err := decodeField(data, 0, s.ConfigBlock.TouchReportDefinition)
	if err != nil {
		return nil, fmt.Errorf("touchReportDefinition: %w", err)
	}
	out["touchReportDefinition"] = touch

	return out, nil
}

func (s *Schema) DecodeField(data []byte, path string) (any, error) {
	base, f, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	return decodeField(data, base, f)
}

func (s *Schema) SetField(data []byte, path string, value any) error {
	base, f, err := s.Resolve(path)
	if err != nil {
		return err
	}
	return encodeField(data, base, f, value)
}

func decodeField(data []byte, baseBits int, f Field) (any, error) {
	count := f.Count()
	if count <= 0 || f.Length <= 0 {
		return nil, errors.New("invalid field dimensions")
	}
	if count == 1 {
		v, err := readBits(data, baseBits+f.Offset, f.Length)
		if err != nil {
			return nil, err
		}
		if f.Length == 1 {
			return v != 0, nil
		}
		if f.Signed {
			return signExtend(v, f.Length), nil
		}
		return v, nil
	}

	bits := f.BitsPerElement()
	if bits <= 0 {
		return nil, errors.New("invalid element width")
	}
	if f.Signed {
		out := make([]int64, count)
		for i := range out {
			v, err := readBits(data, baseBits+f.Offset+i*bits, bits)
			if err != nil {
				return nil, err
			}
			out[i] = signExtend(v, bits)
		}
		return out, nil
	}
	out := make([]uint64, count)
	for i := range out {
		v, err := readBits(data, baseBits+f.Offset+i*bits, bits)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func encodeField(data []byte, baseBits int, f Field, value any) error {
	count := f.Count()
	if count <= 0 || f.Length <= 0 {
		return errors.New("invalid field dimensions")
	}
	if count == 1 {
		u, err := valueToUint64(value, f.Signed, f.Length)
		if err != nil {
			return err
		}
		return writeBits(data, baseBits+f.Offset, f.Length, u)
	}

	bits := f.BitsPerElement()
	if bits <= 0 {
		return errors.New("invalid element width")
	}
	items, err := valueToSlice(value)
	if err != nil {
		return err
	}
	if len(items) != count {
		return fmt.Errorf("expected %d elements, got %d", count, len(items))
	}
	for i, item := range items {
		u, err := valueToUint64(item, f.Signed, bits)
		if err != nil {
			return fmt.Errorf("element %d: %w", i, err)
		}
		if err := writeBits(data, baseBits+f.Offset+i*bits, bits, u); err != nil {
			return err
		}
	}
	return nil
}

// ParseFieldValue converts a CLI string to a value suitable for SetField.
func ParseFieldValue(f Field, s string) (any, error) {
	if f.Count() > 1 {
		return parseArrayString(s)
	}
	if f.Length == 1 {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
	}
	if f.Signed || strings.HasPrefix(strings.TrimSpace(s), "-") {
		v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 64)
		if err != nil {
			return nil, err
		}
		return v, nil
	}
	v, err := strconv.ParseUint(strings.TrimSpace(s), 0, 64)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func parseArrayString(s string) ([]any, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var out []any
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	parts := strings.Split(s, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func valueToSlice(value any) ([]any, error) {
	switch v := value.(type) {
	case []any:
		return v, nil
	case []string:
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out, nil
	case []int:
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out, nil
	case []int64:
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out, nil
	case []uint64:
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out, nil
	case string:
		return parseArrayString(v)
	default:
		return nil, fmt.Errorf("expected an array, got %T", value)
	}
}

func valueToUint64(value any, signed bool, bits int) (uint64, error) {
	var u uint64
	switch v := value.(type) {
	case bool:
		if v {
			u = 1
		}
	case json.Number:
		return valueToUint64(string(v), signed, bits)
	case float64:
		if v < 0 {
			if !signed {
				return 0, fmt.Errorf("negative value %v for unsigned field", v)
			}
			u = uint64(int64(v))
		} else {
			u = uint64(v)
		}
	case int:
		return valueToUint64(int64(v), signed, bits)
	case int8:
		return valueToUint64(int64(v), signed, bits)
	case int16:
		return valueToUint64(int64(v), signed, bits)
	case int32:
		return valueToUint64(int64(v), signed, bits)
	case int64:
		if v < 0 {
			if !signed {
				return 0, fmt.Errorf("negative value %d for unsigned field", v)
			}
			u = uint64(v)
		} else {
			u = uint64(v)
		}
	case uint:
		u = uint64(v)
	case uint8:
		u = uint64(v)
	case uint16:
		u = uint64(v)
	case uint32:
		u = uint64(v)
	case uint64:
		u = v
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, errors.New("empty numeric value")
		}
		if signed || strings.HasPrefix(s, "-") {
			n, err := strconv.ParseInt(s, 0, 64)
			if err != nil {
				return 0, err
			}
			if n < 0 && !signed {
				return 0, fmt.Errorf("negative value %d for unsigned field", n)
			}
			u = uint64(n)
		} else {
			n, err := strconv.ParseUint(s, 0, 64)
			if err != nil {
				return 0, err
			}
			u = n
		}
	default:
		return 0, fmt.Errorf("unsupported value type %T", value)
	}

	if bits < 64 {
		u &= (uint64(1) << uint(bits)) - 1
	}
	return u, nil
}

func signExtend(v uint64, bits int) int64 {
	if bits <= 0 || bits >= 64 {
		return int64(v)
	}
	if v&(uint64(1)<<uint(bits-1)) != 0 {
		return int64(v | (^uint64(0) << uint(bits)))
	}
	return int64(v)
}

func readBits(data []byte, bitPos, n int) (uint64, error) {
	if n < 0 || n > 64 {
		return 0, fmt.Errorf("invalid bit width %d", n)
	}
	if bitPos < 0 || bitPos+n > len(data)*8 {
		return 0, fmt.Errorf("bit range %d..%d is out of bounds", bitPos, bitPos+n)
	}
	var v uint64
	for i := 0; i < n; i++ {
		if data[(bitPos+i)/8]&(1<<uint((bitPos+i)%8)) != 0 {
			v |= uint64(1) << uint(i)
		}
	}
	return v, nil
}

func writeBits(data []byte, bitPos, n int, v uint64) error {
	if n < 0 || n > 64 {
		return fmt.Errorf("invalid bit width %d", n)
	}
	if bitPos < 0 || bitPos+n > len(data)*8 {
		return fmt.Errorf("bit range %d..%d is out of bounds", bitPos, bitPos+n)
	}
	for i := 0; i < n; i++ {
		idx := (bitPos + i) / 8
		mask := byte(1) << uint((bitPos+i)%8)
		if v&(uint64(1)<<uint(i)) != 0 {
			data[idx] |= mask
		} else {
			data[idx] &^= mask
		}
	}
	return nil
}

// FormatValue returns a compact string for CLI output.
func FormatValue(v any) string {
	switch v := v.(type) {
	case []uint64:
		b, _ := json.Marshal(v)
		return string(b)
	case []int64:
		b, _ := json.Marshal(v)
		return string(b)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}
