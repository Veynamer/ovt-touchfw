package touchfw

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

const (
	ImageMagic     uint32 = 0x4818472B
	AreaMagic      uint32 = 0x7C05E516
	MetadataMagic  uint32 = 0xC1FB41D8
	DescriptorSize        = 36
)

// Area is one entry in the firmware container. For the compressed metadata
// blob IsMetadata is true and MetaJSON contains the decompressed JSON.
type Area struct {
	Index    int
	Offset   uint32
	ID       string
	Flags    uint32
	Flash    uint32
	Length   uint32
	Checksum uint32
	Content  []byte

	IsMetadata         bool
	MetaCompressedSize uint32
	MetaCompressed     []byte
	MetaJSON           []byte
}

// Image is a parsed firmware container.
type Image struct {
	Raw      []byte
	Magic    uint32
	NumAreas uint32
	Offsets  []uint32
	Areas    []Area
	Metadata *Metadata
}

// Metadata is the optional JSON metadata stored in the last area. It is not
// used by the zeroflash driver, but it contains the full APP_CONFIG schema.
type Metadata struct {
	Raw     []byte
	BuildID uint32
	Schema  *Schema
}

func le32(b []byte) uint32 {
	return binary.LittleEndian.Uint32(b)
}

func put32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}

// ParseImage parses and CRC-checks an OVT/Synaptics container.
func ParseImage(data []byte) (*Image, error) {
	if len(data) < 8 {
		return nil, errors.New("image is shorter than 8 bytes")
	}
	if le32(data[0:4]) != ImageMagic {
		return nil, fmt.Errorf("bad image magic 0x%08X", le32(data[0:4]))
	}
	n := le32(data[4:8])
	if n == 0 || n > 64 {
		return nil, fmt.Errorf("unsupported area count %d", n)
	}
	if len(data) < int(8+4*n) {
		return nil, errors.New("short area offset table")
	}

	img := &Image{
		Raw:      data,
		Magic:    ImageMagic,
		NumAreas: n,
	}
	for i := uint32(0); i < n; i++ {
		off := le32(data[8+4*i:])
		img.Offsets = append(img.Offsets, off)
		if int(off)+4 > len(data) {
			return nil, fmt.Errorf("area %d offset 0x%X is out of range", i, off)
		}

		magic := le32(data[off:])
		if magic == MetadataMagic {
			a, err := parseMetadataArea(data, int(i), off)
			if err != nil {
				return nil, err
			}
			img.Areas = append(img.Areas, *a)
			meta, err := ParseMetadata(a.MetaJSON)
			if err != nil {
				return nil, fmt.Errorf("metadata area: %w", err)
			}
			img.Metadata = meta
			continue
		}

		if magic != AreaMagic {
			return nil, fmt.Errorf("area %d has bad magic 0x%08X", i, magic)
		}
		if int(off)+DescriptorSize > len(data) {
			return nil, fmt.Errorf("area %d descriptor is out of range", i)
		}

		a := Area{
			Index:    int(i),
			Offset:   off,
			ID:       strings.TrimRight(string(data[off+4:off+20]), "\x00 "),
			Flags:    le32(data[off+20:]),
			Flash:    le32(data[off+24:]) * 2,
			Length:   le32(data[off+28:]),
			Checksum: le32(data[off+32:]),
		}
		end := int(off) + DescriptorSize + int(a.Length)
		if end > len(data) {
			return nil, fmt.Errorf("area %s (0x%X, len 0x%X) is out of range", a.ID, off, a.Length)
		}
		a.Content = data[off+DescriptorSize : end]
		if got := crc32.ChecksumIEEE(a.Content); got != a.Checksum {
			return nil, fmt.Errorf("area %s CRC mismatch: got 0x%08X want 0x%08X", a.ID, got, a.Checksum)
		}
		img.Areas = append(img.Areas, a)
	}
	return img, nil
}

func parseMetadataArea(data []byte, index int, off uint32) (*Area, error) {
	if int(off)+8 > len(data) {
		return nil, errors.New("short metadata header")
	}
	compSize := le32(data[off+4:])
	start := int(off) + 8
	end := start + int(compSize)
	if end > len(data) {
		return nil, fmt.Errorf("metadata compressed payload is out of range: 0x%X+0x%X", start, compSize)
	}
	comp := data[start:end]
	zr, err := zlib.NewReader(bytes.NewReader(comp))
	if err != nil {
		return nil, fmt.Errorf("metadata zlib: %w", err)
	}
	raw, err := io.ReadAll(zr)
	closeErr := zr.Close()
	if err != nil {
		return nil, fmt.Errorf("metadata decompress: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("metadata zlib close: %w", closeErr)
	}
	return &Area{
		Index:              index,
		Offset:             off,
		ID:                 "METADATA",
		Length:             compSize,
		IsMetadata:         true,
		MetaCompressedSize: compSize,
		MetaCompressed:     comp,
		MetaJSON:           raw,
	}, nil
}

// ParseMetadata decodes the JSON metadata and its APP_CONFIG schema.
func ParseMetadata(data []byte) (*Metadata, error) {
	var head struct {
		BuildID uint32 `json:"buildID"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("metadata JSON: %w", err)
	}
	schema, err := LoadSchema(data)
	if err != nil {
		return nil, err
	}
	return &Metadata{
		Raw:     data,
		BuildID: head.BuildID,
		Schema:  schema,
	}, nil
}

// FindArea returns the first area with the given ID (case-insensitive).
func (img *Image) FindArea(id string) *Area {
	for i := range img.Areas {
		if strings.EqualFold(img.Areas[i].ID, id) {
			return &img.Areas[i]
		}
	}
	return nil
}

// AppConfig parses APP_CONFIG using the metadata schema, if available.
func (img *Image) AppConfig() (*AppConfig, error) {
	a := img.FindArea("APP_CONFIG")
	if a == nil {
		return nil, errors.New("APP_CONFIG area not found")
	}
	var schema *Schema
	if img.Metadata != nil {
		schema = img.Metadata.Schema
	}
	return ParseAppConfig(a.Content, schema)
}

// ReplaceAreaContent replaces an area's content in memory. The container is
// rebuilt later with BuildPreserveLayout or BuildImage.
func (img *Image) ReplaceAreaContent(id string, content []byte) error {
	a := img.FindArea(id)
	if a == nil {
		return fmt.Errorf("area %s not found", id)
	}
	if a.IsMetadata {
		return errors.New("metadata replacement is not supported in preserve mode")
	}
	a.Content = content
	a.Length = uint32(len(content))
	return nil
}

// BuildPreserveLayout writes changed area contents back into the original
// layout. Area lengths must stay unchanged.
func (img *Image) BuildPreserveLayout() ([]byte, error) {
	out := append([]byte(nil), img.Raw...)
	for _, a := range img.Areas {
		if a.IsMetadata {
			continue
		}
		if len(a.Content) != int(a.Length) {
			return nil, fmt.Errorf("area %s length changed from 0x%X to 0x%X; use BuildImage",
				a.ID, a.Length, len(a.Content))
		}
		end := int(a.Offset) + DescriptorSize + len(a.Content)
		if end > len(out) {
			return nil, fmt.Errorf("area %s does not fit in original layout", a.ID)
		}
		put32(out[a.Offset+28:], uint32(len(a.Content)))
		put32(out[a.Offset+32:], crc32.ChecksumIEEE(a.Content))
		copy(out[a.Offset+DescriptorSize:end], a.Content)
	}
	return out, nil
}

// BuildArea describes one area for BuildImage.
type BuildArea struct {
	Metadata           bool
	ID                 string
	Flags              uint32
	Flash              uint32
	Content            []byte
	MetadataJSON       []byte
	MetadataCompressed []byte
}

// BuildImage lays areas out sequentially. If all sizes are unchanged this
// reproduces the original file layout (descriptor immediately followed by
// content, 4-byte aligned).
func BuildImage(areas []BuildArea) ([]byte, error) {
	if len(areas) == 0 {
		return nil, errors.New("no areas")
	}
	if len(areas) > 64 {
		return nil, errors.New("too many areas")
	}

	headerSize := 8 + 4*len(areas)
	buf := make([]byte, headerSize)
	offsets := make([]uint32, len(areas))

	for i, a := range areas {
		for len(buf)%4 != 0 {
			buf = append(buf, 0)
		}
		offsets[i] = uint32(len(buf))

		if a.Metadata {
			comp := a.MetadataCompressed
			if len(comp) == 0 {
				if a.MetadataJSON == nil {
					return nil, errors.New("metadata area without JSON or compressed data")
				}
				var err error
				comp, err = compressZlib(a.MetadataJSON)
				if err != nil {
					return nil, err
				}
			}
			var hdr [8]byte
			binary.LittleEndian.PutUint32(hdr[0:4], MetadataMagic)
			binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(comp)))
			buf = append(buf, hdr[:]...)
			buf = append(buf, comp...)
			continue
		}

		if a.ID == "" || len(a.ID) > 16 {
			return nil, fmt.Errorf("invalid area ID %q", a.ID)
		}
		if a.Flash%2 != 0 {
			return nil, fmt.Errorf("area %s flash address 0x%X is not even", a.ID, a.Flash)
		}

		descOff := len(buf)
		buf = append(buf, make([]byte, DescriptorSize)...)
		buf = append(buf, a.Content...)

		put32(buf[descOff+0:], AreaMagic)
		for j := 0; j < 16; j++ {
			buf[descOff+4+j] = ' '
		}
		copy(buf[descOff+4:descOff+20], a.ID)
		put32(buf[descOff+20:], a.Flags)
		put32(buf[descOff+24:], a.Flash/2)
		put32(buf[descOff+28:], uint32(len(a.Content)))
		put32(buf[descOff+32:], crc32.ChecksumIEEE(a.Content))
	}

	put32(buf[0:4], ImageMagic)
	put32(buf[4:8], uint32(len(areas)))
	for i, off := range offsets {
		put32(buf[8+4*i:], off)
	}

	if _, err := ParseImage(buf); err != nil {
		return nil, fmt.Errorf("built image is invalid: %w", err)
	}
	return buf, nil
}

func compressZlib(data []byte) ([]byte, error) {
	var b bytes.Buffer
	zw, err := zlib.NewWriterLevel(&b, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// SHA256 returns the hex SHA-256 of the raw image.
func (img *Image) SHA256() string {
	sum := sha256.Sum256(img.Raw)
	return hex.EncodeToString(sum[:])
}

// HeaderJSON is a compact description of the image header and areas.
type HeaderJSON struct {
	Magic    uint32       `json:"magic"`
	NumAreas uint32       `json:"num_areas"`
	FileSize int          `json:"file_size"`
	SHA256   string       `json:"sha256"`
	Areas    []AreaHeader `json:"areas"`
}

type AreaHeader struct {
	Index              int    `json:"index"`
	ID                 string `json:"id"`
	Offset             uint32 `json:"offset"`
	Flags              uint32 `json:"flags,omitempty"`
	Flash              uint32 `json:"flash_addr,omitempty"`
	Length             uint32 `json:"length"`
	Checksum           uint32 `json:"checksum,omitempty"`
	IsMetadata         bool   `json:"is_metadata,omitempty"`
	MetaCompressedSize uint32 `json:"metadata_compressed_size,omitempty"`
}

func (img *Image) Header() HeaderJSON {
	h := HeaderJSON{
		Magic:    img.Magic,
		NumAreas: img.NumAreas,
		FileSize: len(img.Raw),
		SHA256:   img.SHA256(),
	}
	for _, a := range img.Areas {
		h.Areas = append(h.Areas, AreaHeader{
			Index:              a.Index,
			ID:                 a.ID,
			Offset:             a.Offset,
			Flags:              a.Flags,
			Flash:              a.Flash,
			Length:             a.Length,
			Checksum:           a.Checksum,
			IsMetadata:         a.IsMetadata,
			MetaCompressedSize: a.MetaCompressedSize,
		})
	}
	return h
}
