package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"ovt-touchfw/internal/touchfw"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "unpack":
		err = cmdUnpack(os.Args[2:])
	case "pack":
		err = cmdPack(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "get":
		err = cmdGet(os.Args[2:])
	case "set":
		err = cmdSet(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ovt-touchfw - OVT/Synaptics TD4160 firmware container tool

Usage:
  touchfw unpack <image.img> <outdir>
  touchfw pack [-recompress] <indir> <out.img>
  touchfw verify <image.img>
  touchfw list <image.img>
  touchfw get <image.img> <path>
  touchfw set [-checksum=preserve|zero|fletcher32] <image.img> <out.img> <path=value>...

Examples:
  touchfw verify X6816DRU_TD4160_TM_fw.img
  touchfw unpack X6816DRU_TD4160_TM_fw.img fw-unpacked
  touchfw get X6816DRU_TD4160_TM_fw.img staticConfiguration.numColsDoze
  touchfw set X6816DRU_TD4160_TM_fw.img new.img staticConfiguration.numColsDoze=20

Field paths:
  staticConfiguration.<name>
  dynamicConfiguration.<name>
  touchReportDefinition
  configBlock.buildID | headerID | checksum | length | customerConfigId
`)
}

func loadImage(path string) (*touchfw.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return touchfw.ParseImage(data)
}

func cmdUnpack(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: touchfw unpack <image.img> <outdir>")
	}
	img, err := loadImage(args[0])
	if err != nil {
		return err
	}
	return touchfw.Unpack(img, args[1])
}

func cmdPack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	recompress := fs.Bool("recompress", false, "recompress metadata.json instead of reusing metadata.z")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return fmt.Errorf("usage: touchfw pack [-recompress] <indir> <out.img>")
	}
	return touchfw.Pack(rest[0], rest[1], *recompress)
}

func cmdVerify(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: touchfw verify <image.img>")
	}
	img, err := loadImage(args[0])
	if err != nil {
		return err
	}

	fmt.Printf("OK  size=%d sha256=%s areas=%d\n", len(img.Raw), img.SHA256(), img.NumAreas)
	for _, a := range img.Areas {
		if a.IsMetadata {
			fmt.Printf("  [%d] %-16s off=0x%06X compressed=0x%X json=%d",
				a.Index, a.ID, a.Offset, a.MetaCompressedSize, len(a.MetaJSON))
			if img.Metadata != nil {
				fmt.Printf(" buildID=0x%08X", img.Metadata.BuildID)
			}
			fmt.Println()
			continue
		}
		fmt.Printf("  [%d] %-16s off=0x%06X flash=0x%05X len=0x%05X flags=%d crc=0x%08X\n",
			a.Index, a.ID, a.Offset, a.Flash, a.Length, a.Flags, a.Checksum)
	}

	ac, err := img.AppConfig()
	if err != nil {
		fmt.Println("APP_CONFIG: not available:", err)
		return nil
	}
	fmt.Printf("APP_CONFIG: header=%q buildID=0x%08X length=0x%X block=%d checksum=0x%08X\n",
		ac.HeaderIDString(), ac.Header.BuildID, ac.Header.LengthWords,
		ac.BlockLengthBytes(), ac.Header.Checksum)
	fmt.Printf("  standard Fletcher-32 match: %v\n", ac.ChecksumMatchesFletcher32())
	if img.Metadata != nil && img.Metadata.BuildID != ac.Header.BuildID {
		fmt.Printf("  warning: metadata buildID 0x%08X != APP_CONFIG buildID 0x%08X\n",
			img.Metadata.BuildID, ac.Header.BuildID)
	}
	return nil
}

func cmdList(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: touchfw list <image.img>")
	}
	img, err := loadImage(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("%-3s %-16s %-10s %-10s %-10s %-6s %-10s\n",
		"#", "ID", "offset", "flash", "length", "flags", "crc")
	for _, a := range img.Areas {
		if a.IsMetadata {
			fmt.Printf("%-3d %-16s 0x%06X   %-10s 0x%06X %-6s %-10s\n",
				a.Index, a.ID, a.Offset, "-", a.MetaCompressedSize, "-", "-")
			continue
		}
		fmt.Printf("%-3d %-16s 0x%06X   0x%05X   0x%05X %-6d 0x%08X\n",
			a.Index, a.ID, a.Offset, a.Flash, a.Length, a.Flags, a.Checksum)
	}
	return nil
}

func cmdGet(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: touchfw get <image.img> <path>")
	}
	img, err := loadImage(args[0])
	if err != nil {
		return err
	}
	ac, err := img.AppConfig()
	if err != nil {
		return err
	}
	v, err := ac.DecodeField(args[1])
	if err != nil {
		return err
	}
	fmt.Println(touchfw.FormatValue(v))
	return nil
}

func cmdSet(args []string) error {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	checksum := fs.String("checksum", "preserve", "preserve, zero or fletcher32")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 3 {
		return fmt.Errorf("usage: touchfw set [-checksum=...] <image.img> <out.img> <path=value>...")
	}
	in, out := rest[0], rest[1]
	assignments := rest[2:]

	img, err := loadImage(in)
	if err != nil {
		return err
	}
	ac, err := img.AppConfig()
	if err != nil {
		return err
	}
	if ac.Schema == nil {
		return fmt.Errorf("metadata schema is not available; cannot edit fields")
	}

	changed := false
	for _, assignment := range assignments {
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("bad assignment %q, want path=value", assignment)
		}
		path := strings.TrimSpace(parts[0])
		raw := strings.TrimSpace(parts[1])

		_, field, err := ac.Schema.Resolve(path)
		if err != nil {
			return err
		}
		value, err := touchfw.ParseFieldValue(field, raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := ac.SetField(path, value); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Printf("set %s = %s\n", path, touchfw.FormatValue(value))
		changed = true
	}

	if err := ac.SetChecksum(*checksum); err != nil {
		return err
	}
	if changed && (*checksum == "" || *checksum == "preserve") {
		fmt.Println("warning: APP_CONFIG checksum preserved; the vendor Fletcher-32 variant is not implemented")
	}
	if *checksum == "fletcher32" {
		fmt.Println("warning: wrote standard Fletcher-32; this may not match the vendor algorithm")
	}

	if err := img.ReplaceAreaContent("APP_CONFIG", ac.Raw); err != nil {
		return err
	}
	data, err := img.BuildPreserveLayout()
	if err != nil {
		return err
	}
	if _, err := touchfw.ParseImage(data); err != nil {
		return fmt.Errorf("rebuilt image is invalid: %w", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", out, len(data))
	return nil
}
