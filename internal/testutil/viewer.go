package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ViewerNITF writes three different native imagery segments, two making a
// synthetic PAN image, and a real encoded 2x2 cloud grid spanning both halves.
// No mock GDAL responses or persistent fixture binaries are involved.
func ViewerNITF(t *testing.T, path string, cloudMetadata string, shape bool) {
	t.Helper()
	dir := t.TempDir()
	options := []string{}
	if shape {
		source := filepath.Join(dir, "cloud.geojson")
		// An outer ring, a hole, and a second polygon, all inside the imagery extent.
		data := `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},"geometry":{"type":"MultiPolygon","coordinates":[[[[-106,40],[-105.5,40],[-105.5,39.5],[-106,39.5],[-106,40]],[[-105.9,39.9],[-105.9,39.8],[-105.8,39.8],[-105.8,39.9],[-105.9,39.9]]],[[[-105.4,39.4],[-105.2,39.4],[-105.2,39.2],[-105.4,39.2],[-105.4,39.4]]]]}}]}`
		if err := os.WriteFile(source, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		GDAL(t, "ogr2ogr", "-f", "ESRI Shapefile", filepath.Join(dir, "cloud.shp"), source)
		var payload []byte
		var offsets strings.Builder
		for _, ext := range []string{"shp", "shx", "dbf"} {
			fmt.Fprintf(&offsets, "%s%06d", strings.ToUpper(ext), len(payload))
			b, e := os.ReadFile(filepath.Join(dir, "cloud."+ext))
			if e != nil {
				t.Fatal(e)
			}
			payload = append(payload, b...)
		}
		custom := fmt.Sprintf("%-25s%-10s%-18s%s", "CLOUD_SHAPES", "POLYGON", "PAN", offsets.String())
		// DESVER + NITF 2.1 security block + DESSHL + versioned user subheader.
		des := "01" + "U" + strings.Repeat(" ", 166) + "0080" + custom + string(payload)
		options = append(options, "-co", "DES=CSSHPA DES="+escapeNITF(des))
	}
	for i, burn := range []int{30, 110, 220} {
		source := filepath.Join(dir, fmt.Sprintf("s%d.tif", i))
		w, h := 8, 4
		left, top, right, bottom := "-106", "40", "-105", "39.5"
		if i == 1 {
			top, bottom = "39.5", "39"
		}
		if i == 2 {
			w, h = 4, 4
			left, top, right, bottom = "-106", "40", "-105", "39"
		}
		GDAL(t, "gdal_create", "-of", "GTiff", "-outsize", fmt.Sprint(w), fmt.Sprint(h), "-bands", "1", "-burn", fmt.Sprint(burn), "-a_srs", "EPSG:4326", "-a_ullr", left, top, right, bottom, source)

		cat, iid, level, attach, row := "PAN", fmt.Sprintf("P1SEG%d", i), 51+i, 0, 0
		if i == 1 {
			attach, row = 51, 4
		}
		if i == 2 {
			cat, iid, level = "MS", "M1SEG0", 101
		}
		args := []string{"-q", "-of", "NITF", "-co", "ICORDS=G", "-co", "ICAT=" + cat, "-co", "IID1=" + iid, "-co", fmt.Sprintf("IDLVL=%03d", level), "-co", fmt.Sprintf("IALVL=%03d", attach), "-co", fmt.Sprintf("ILOCROW=%05d", row)}
		if i == 0 {
			args = append(args, "-co", "NUMI=4")
			if shape {
				args = append(args, "-co", "NUMDES=1")
			}
		} else {
			args = append(args, "-co", "APPEND_SUBDATASET=YES")
		}

		GDAL(t, "gdal_translate", append(args, source, path)...)
	}
	// PGM is a tiny, portable categorical source; values exercise all classes.
	mask := filepath.Join(dir, "cloud.pgm")
	if err := os.WriteFile(mask, append([]byte("P5\n2 2\n255\n"), 0, 255, 127, 255), 0600); err != nil {
		t.Fatal(err)
	}
	if cloudMetadata == "valid" {
		cloudMetadata = fmt.Sprintf("%-18s%-6s%07d%05d%07d%05d%07d%05d", "PAN", "PAN", 1, 1, 4, 4, 2, 2)
	}
	args := []string{"-q", "-of", "NITF", "-co", "APPEND_SUBDATASET=YES", "-co", "ICAT=CLOUD", "-co", "IREP=NODISPLY", "-co", "IDLVL=999", "-co", "IALVL=000", "-co", "IID1=CLOUD"}
	args = append(args, options...)
	if cloudMetadata != "" {
		args = append(args, "-co", "TRE=CSCCGA="+cloudMetadata)
	}
	GDAL(t, "gdal_translate", append(args, mask, path)...)
}
func escapeNITF(s string) string {
	var out strings.Builder
	for _, b := range []byte(s) {
		switch b {
		case 0:
			out.WriteString(`\0`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		default:
			out.WriteByte(b)
		}
	}
	return out.String()
}
