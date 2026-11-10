package touchfw

import (
	"bytes"
	"os"
	"reflect"
	"testing"
)

const realFirmware = "blob/ovt_firmware.img"

func loadReal(t *testing.T) *Image {
	t.Helper()
	data, err := os.ReadFile(realFirmware)
	if os.IsNotExist(err) {
		t.Skip("real firmware is not present")
	}
	if err != nil {
		t.Fatal(err)
	}
	img, err := ParseImage(data)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestSyntheticRoundTrip(t *testing.T) {
	areas := []BuildArea{
		{ID: "BOOT_CONFIG", Flash: 0x1F00, Content: bytes.Repeat([]byte{0xAA}, 0x100)},
		{ID: "APP_CONFIG", Flash: 0x18000, Flags: 1, Content: bytes.Repeat([]byte{0x55}, 0x1000)},
	}
	data, err := BuildImage(areas)
	if err != nil {
		t.Fatal(err)
	}
	img, err := ParseImage(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(img.Areas) != 2 {
		t.Fatalf("got %d areas", len(img.Areas))
	}
	if got := img.Areas[1].Flash; got != 0x18000 {
		t.Fatalf("flash = 0x%X", got)
	}
	if !bytes.Equal(img.Areas[1].Content, areas[1].Content) {
		t.Fatal("content mismatch")
	}
}

func TestRealFirmwareParse(t *testing.T) {
	img := loadReal(t)
	if len(img.Areas) != 6 {
		t.Fatalf("areas = %d, want 6", len(img.Areas))
	}
	if img.Metadata == nil {
		t.Fatal("metadata not parsed")
	}
	if img.Metadata.BuildID != 0x7D2C06C5 {
		t.Fatalf("metadata buildID = 0x%X", img.Metadata.BuildID)
	}

	ac, err := img.AppConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := ac.Validate(); err != nil {
		t.Fatal(err)
	}
	if ac.Header.BuildID != 0x7D2C06C5 {
		t.Fatalf("APP_CONFIG buildID = 0x%X", ac.Header.BuildID)
	}
	if ac.Header.LengthWords != 0x5AB {
		t.Fatalf("length words = 0x%X", ac.Header.LengthWords)
	}

	cases := []struct {
		path string
		want any
	}{
		{"staticConfiguration.numColsDoze", uint64(18)},
		{"staticConfiguration.numRows", uint64(32)},
		{"staticConfiguration.rxCount", uint64(18)},
		{"staticConfiguration.txCount", uint64(32)},
		{"staticConfiguration.frameRate", uint64(40)},
		{"staticConfiguration.gripRedOffset", uint64(133)},
		{"staticConfiguration.noiseLengthScale", []int64{80, 80}},
		{"configBlock.buildID", uint64(0x7D2C06C5)},
	}
	for _, tc := range cases {
		got, err := ac.DecodeField(tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s = %#v, want %#v", tc.path, got, tc.want)
		}
	}
}

func TestRealFirmwareRoundTrip(t *testing.T) {
	img := loadReal(t)
	dir := t.TempDir()
	if err := Unpack(img, dir); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/packed.img"
	if err := Pack(dir, out, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, img.Raw) {
		t.Fatalf("round-trip differs: got %d bytes, want %d", len(got), len(img.Raw))
	}
}

func TestRealFirmwareSetField(t *testing.T) {
	img := loadReal(t)
	ac, err := img.AppConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := ac.SetField("staticConfiguration.numColsDoze", uint64(20)); err != nil {
		t.Fatal(err)
	}
	if err := img.ReplaceAreaContent("APP_CONFIG", ac.Raw); err != nil {
		t.Fatal(err)
	}
	data, err := img.BuildPreserveLayout()
	if err != nil {
		t.Fatal(err)
	}
	edited, err := ParseImage(data)
	if err != nil {
		t.Fatal(err)
	}
	editedAC, err := edited.AppConfig()
	if err != nil {
		t.Fatal(err)
	}
	got, err := editedAC.DecodeField("staticConfiguration.numColsDoze")
	if err != nil {
		t.Fatal(err)
	}
	if got != uint64(20) {
		t.Fatalf("numColsDoze = %#v", got)
	}
}
