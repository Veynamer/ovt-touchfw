package touchfw

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ManifestFormat = "ovt-touchfw-manifest-v1"

type Manifest struct {
	Format   string         `json:"format"`
	Magic    uint32         `json:"magic"`
	NumAreas uint32         `json:"num_areas"`
	FileSize int            `json:"file_size"`
	SHA256   string         `json:"sha256"`
	Areas    []ManifestArea `json:"areas"`
}

type ManifestArea struct {
	Index          int    `json:"index"`
	Kind           string `json:"kind"` // "area" or "metadata"
	ID             string `json:"id,omitempty"`
	Offset         uint32 `json:"offset"`
	Flags          uint32 `json:"flags,omitempty"`
	Flash          uint32 `json:"flash_addr,omitempty"`
	Length         uint32 `json:"length,omitempty"`
	Checksum       uint32 `json:"checksum,omitempty"`
	File           string `json:"file,omitempty"`
	CompressedFile string `json:"compressed_file,omitempty"`
	MetadataSHA256 string `json:"metadata_sha256,omitempty"`
	CompressedSize uint32 `json:"compressed_size,omitempty"`
}

// Unpack writes the image contents, metadata JSON, APP_CONFIG dump and a
// manifest that can be fed back to Pack.
func Unpack(img *Image, dir string) error {
	areasDir := filepath.Join(dir, "areas")
	if err := os.MkdirAll(areasDir, 0o755); err != nil {
		return err
	}

	manifest := Manifest{
		Format:   ManifestFormat,
		Magic:    img.Magic,
		NumAreas: img.NumAreas,
		FileSize: len(img.Raw),
		SHA256:   img.SHA256(),
	}

	for _, a := range img.Areas {
		ma := ManifestArea{
			Index:  a.Index,
			Offset: a.Offset,
		}
		if a.IsMetadata {
			pretty, err := prettyJSON(a.MetaJSON)
			if err != nil {
				return fmt.Errorf("metadata pretty print: %w", err)
			}
			metaName := "metadata.json"
			if err := os.WriteFile(filepath.Join(dir, metaName), pretty, 0o644); err != nil {
				return err
			}
			compName := filepath.ToSlash(filepath.Join("areas", fmt.Sprintf("%02d_metadata.z", a.Index)))
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(compName)), a.MetaCompressed, 0o644); err != nil {
				return err
			}
			ma.Kind = "metadata"
			ma.ID = "METADATA"
			ma.Length = a.Length
			ma.CompressedSize = a.MetaCompressedSize
			ma.File = metaName
			ma.CompressedFile = compName
			ma.MetadataSHA256 = sha256Hex(pretty)
			manifest.Areas = append(manifest.Areas, ma)
			continue
		}

		name := fmt.Sprintf("%02d_%s.bin", a.Index, sanitizeID(a.ID))
		rel := filepath.ToSlash(filepath.Join("areas", name))
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), a.Content, 0o644); err != nil {
			return err
		}
		ma.Kind = "area"
		ma.ID = a.ID
		ma.Flags = a.Flags
		ma.Flash = a.Flash
		ma.Length = a.Length
		ma.Checksum = a.Checksum
		ma.File = rel
		manifest.Areas = append(manifest.Areas, ma)
	}

	if err := writeJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "header.json"), img.Header()); err != nil {
		return err
	}

	ac, err := img.AppConfig()
	if err == nil {
		if err := writeJSON(filepath.Join(dir, "appconfig.json"), ac.Dump()); err != nil {
			return err
		}
	}
	return nil
}

// Pack rebuilds an image from an unpack directory. If recompress is false and
// the metadata JSON is unchanged, the original compressed metadata is reused.
func Pack(dir, out string, recompress bool) error {
	var manifest Manifest
	if err := readJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return err
	}
	if manifest.Format != "" && manifest.Format != ManifestFormat {
		return fmt.Errorf("unsupported manifest format %q", manifest.Format)
	}

	areas := make([]BuildArea, 0, len(manifest.Areas))
	for _, ma := range manifest.Areas {
		if ma.Kind == "metadata" {
			metaPath := filepath.Join(dir, filepath.FromSlash(ma.File))
			meta, err := os.ReadFile(metaPath)
			if err != nil {
				return err
			}
			if !json.Valid(meta) {
				return fmt.Errorf("%s is not valid JSON", metaPath)
			}
			ba := BuildArea{Metadata: true, MetadataJSON: meta}
			if !recompress && ma.CompressedFile != "" && ma.MetadataSHA256 != "" {
				if sha256Hex(meta) == ma.MetadataSHA256 {
					compPath := filepath.Join(dir, filepath.FromSlash(ma.CompressedFile))
					if comp, err := os.ReadFile(compPath); err == nil {
						ba.MetadataCompressed = comp
					}
				}
			}
			areas = append(areas, ba)
			continue
		}

		path := filepath.Join(dir, filepath.FromSlash(ma.File))
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		areas = append(areas, BuildArea{
			ID:      ma.ID,
			Flags:   ma.Flags,
			Flash:   ma.Flash,
			Content: content,
		})
	}

	data, err := BuildImage(areas)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	_, err = ParseImage(data)
	return err
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func prettyJSON(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "AREA"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, id)
}
