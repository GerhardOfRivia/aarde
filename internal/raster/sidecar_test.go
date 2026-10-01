package raster

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func xmlScene(fields string) string {
	return "<isd><IMD><GENERATIONTIME>2030-01-01T00:00:00Z</GENERATIONTIME><IMAGE>" + fields + "</IMAGE></IMD></isd>"
}
func writeSidecarTest(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSidecarPairing(t *testing.T) {
	for _, ext := range []string{".tif", ".TIFF", ".ntf", ".NiTf"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			raster := filepath.Join(dir, "Scene"+ext)
			writeSidecarTest(t, filepath.Join(dir, "SCENE.XML"), "")
			got, err := discoverSidecar(raster)
			if err != nil || filepath.Base(got) != "SCENE.XML" {
				t.Fatalf("fallback: %s %v", got, err)
			}
			writeSidecarTest(t, filepath.Join(dir, "Scene.xMl"), "")
			got, err = discoverSidecar(raster)
			if err != nil || filepath.Base(got) != "Scene.xMl" {
				t.Fatalf("exact: %s %v", got, err)
			}
			writeSidecarTest(t, filepath.Join(dir, "Scene.XML"), "")
			if _, err = discoverSidecar(raster); err == nil {
				t.Fatal("ambiguous exact match accepted")
			}
		})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "scene.tif")
	writeSidecarTest(t, filepath.Join(dir, "SCENE.xml"), "")
	writeSidecarTest(t, filepath.Join(dir, "Scene.XML"), "")
	if _, err := discoverSidecar(path); err == nil {
		t.Fatal("ambiguous fallback accepted")
	}
	dir = t.TempDir()
	target := filepath.Join(dir, "target.xml")
	writeSidecarTest(t, target, "")
	if err := os.Symlink(target, filepath.Join(dir, "scene.XML")); err != nil {
		t.Fatal(err)
	}
	if got, err := discoverSidecar(filepath.Join(dir, "scene.ntf")); got != "" || err != nil {
		t.Fatalf("symlink: %s %v", got, err)
	}
}

func TestSidecarCloudUnits(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		want  float64
		valid bool
	}{
		{"3.000000000000000e-03", .3, true}, {"0", 0, true}, {"0.5", 50, true}, {"1", 100, true},
		{"-999", 0, false}, {"-0.1", 0, false}, {"1.01", 0, false}, {"100", 0, false}, {"NaN", 0, false}, {"Inf", 0, false}, {"1e999", 0, false}, {"", 0, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			m, w, err := parseSidecar([]byte(xmlScene("<CLOUDCOVER>" + tc.raw + "</CLOUDCOVER>")))
			if err != nil || (m.CloudCover != nil) != tc.valid || m.CloudCover != nil && *m.CloudCover != tc.want || (!tc.valid && len(w) == 0) {
				t.Fatalf("%+v %v %v", m, w, err)
			}
		})
	}
}

func TestSidecarPathsTimesAndFailures(t *testing.T) {
	first := "2021-09-11T05:15:29.005479Z"
	fallback := "2020-01-01T01:00:00+01:00"
	for _, tc := range []struct{ fields, want string }{
		{"<FIRSTLINETIME>" + first + "</FIRSTLINETIME><TLCTIME>" + fallback + "</TLCTIME>", first},
		{"<FIRSTLINETIME>bad</FIRSTLINETIME><TLCTIME>" + fallback + "</TLCTIME>", "2020-01-01T00:00:00Z"},
		{"<TLCTIME>" + fallback + "</TLCTIME>", "2020-01-01T00:00:00Z"},
		{"<FIRSTLINETIME>2020-01-01T00:00:00</FIRSTLINETIME>", ""}, {"", ""},
	} {
		m, _, err := parseSidecar([]byte(xmlScene(tc.fields)))
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if m.AcquiredAt != nil {
			got = m.AcquiredAt.Format(time.RFC3339Nano)
		}
		if got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
	xml := `<x:isd xmlns:x="urn:test"><x:IMD><CLOUDCOVER>1</CLOUDCOVER><x:IMAGE><x:CLOUDCOVER>3e-3</x:CLOUDCOVER><other><CLOUDCOVER>1</CLOUDCOVER></other></x:IMAGE></x:IMD></x:isd>`
	m, _, err := parseSidecar([]byte(xml))
	if err != nil || m.CloudCover == nil || *m.CloudCover != .3 {
		t.Fatalf("namespace/path: %+v %v", m, err)
	}
	for _, bad := range []string{"<isd>", "<other/>", "<isd><IMD/></isd>", xmlScene("<CLOUDCOVER>0</CLOUDCOVER><CLOUDCOVER>1</CLOUDCOVER>"), xmlScene("") + "<isd/>", `<!DOCTYPE isd [<!ENTITY external SYSTEM "file:///etc/passwd">]>` + xmlScene("<CLOUDCOVER>&external;</CLOUDCOVER>"), strings.Repeat(" ", sidecarLimit+1), xmlScene("<CLOUDCOVER>" + strings.Repeat("0", 1025) + "</CLOUDCOVER>"), "<isd>" + strings.Repeat("<nested>", 64) + strings.Repeat("</nested>", 64) + "</isd>"} {
		if _, _, err := parseSidecar([]byte(bad)); err == nil {
			t.Fatal("accepted invalid/unsupported XML")
		}
	}
}

func TestApplySidecarPreservesSourcesAndWarnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scene.ntf")
	cloud := 12.0
	acquired := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	original := Inspection{CloudCover: &cloud, AcquiredAt: &acquired, Checksum: strings.Repeat("a", 64), Metadata: json.RawMessage(`{"":{"CLOUD_COVER":"12"},"TRE":{"source":"keep"},"_aarde":{"format":"NITF"}}`), Segments: []Segment{{CloudCover: &cloud, AcquiredAt: &acquired, Metadata: json.RawMessage(`{"source":"segment"}`)}}}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	for _, tc := range []struct {
		name, data string
		warning    bool
	}{
		{"missing", "", false}, {"malformed", "<isd>", true}, {"unsupported", "<other/>", true}, {"oversized", strings.Repeat(" ", sidecarLimit+1), true}, {"invalid", xmlScene("<CLOUDCOVER>NaN</CLOUDCOVER><FIRSTLINETIME>bad</FIRSTLINETIME>"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			if tc.name != "missing" {
				writeSidecarTest(t, filepath.Join(dir, "scene.XML"), tc.data)
			}
			got := original
			applySidecar(path, &got)
			if got.CloudCover != original.CloudCover || got.AcquiredAt != original.AcquiredAt {
				t.Fatal("erased embedded values")
			}
			if (logs.Len() > 0) != tc.warning {
				t.Fatalf("warnings: %s", logs.String())
			}
		})
	}
	writeSidecarTest(t, filepath.Join(dir, "scene.XML"), xmlScene("<CLOUDCOVER>0</CLOUDCOVER><FIRSTLINETIME>2021-09-11T05:15:29.005479Z</FIRSTLINETIME>"))
	got := original
	applySidecar(path, &got)
	if got.CloudCover == nil || *got.CloudCover != 0 || got.AcquiredAt.Year() != 2021 || got.Checksum != original.Checksum || !reflect.DeepEqual(got.Segments, original.Segments) {
		t.Fatalf("precedence/segments: %+v", got)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal(original.Metadata, &before)
	_ = json.Unmarshal(got.Metadata, &after)
	for _, key := range []string{"", "TRE"} {
		if !bytes.Equal(before[key], after[key]) {
			t.Fatal("lost GDAL metadata")
		}
	}
	var annotation struct {
		Format  string          `json:"format"`
		Sidecar sidecarMetadata `json:"xml_sidecar"`
	}
	_ = json.Unmarshal(after["_aarde"], &annotation)
	if annotation.Format != "NITF" || annotation.Sidecar.Path != filepath.Join(dir, "scene.XML") || len(annotation.Sidecar.Checksum) != 64 || annotation.Sidecar.Schema != "maxar-isd-imd" || annotation.Sidecar.SourceFields[imagePath+"CLOUDCOVER"] != "0" {
		t.Fatalf("provenance: %+v", annotation)
	}
	writeSidecarTest(t, filepath.Join(dir, "scene.xml"), xmlScene("<CLOUDCOVER>1</CLOUDCOVER>"))
	logs.Reset()
	got = original
	applySidecar(path, &got)
	if logs.Len() == 0 || got.CloudCover != original.CloudCover {
		t.Fatal("ambiguous sidecar used")
	}
}

func TestUnreadableSidecar(t *testing.T) {
	dir := t.TempDir()
	xml := filepath.Join(dir, "scene.XML")
	writeSidecarTest(t, xml, xmlScene("<CLOUDCOVER>1</CLOUDCOVER>"))
	if err := os.Chmod(xml, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(xml, 0600) })
	if f, err := os.Open(xml); err == nil {
		f.Close()
		t.Skip("current user can read mode-000 files")
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	cloud := 12.0
	got := Inspection{CloudCover: &cloud, Metadata: json.RawMessage(`{}`)}
	applySidecar(filepath.Join(dir, "scene.tif"), &got)
	if logs.Len() == 0 || got.CloudCover != &cloud {
		t.Fatal("unreadable sidecar did not warn/preserve embedded value")
	}
}
