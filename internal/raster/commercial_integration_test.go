package raster

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestGDALCommercialNITF(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, tc := range []struct {
		name   string
		clouds []string
		aux    bool
		want   *float64
	}{
		{"single", []string{"001"}, false, ptrFloat(1)}, {"unanimous", []string{"001", "001"}, false, ptrFloat(1)},
		{"distinct", []string{"001", "100"}, false, nil}, {"missing", []string{"001", "999"}, false, nil},
		{"cloud grid", []string{"001", "999"}, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "commercial.ntf")
			testutil.CommercialNITF(t, path, tc.clouds, tc.aux)
			before, _ := os.ReadFile(path)
			_ = os.Chmod(path, 0444)
			result, err := Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			file := commercialFrom(t, result.Metadata)
			if file.Profile.Status != "likely" || result.Format != "NITF" || len(result.Segments) != len(tc.clouds) || len(file.Records) != 1 {
				t.Fatalf("profile=%+v records=%+v", file.Profile, file.Records)
			}
			if (result.CloudCover == nil) != (tc.want == nil) || result.CloudCover != nil && *result.CloudCover != *tc.want {
				t.Fatal(result.CloudCover)
			}
			for i, s := range result.Segments {
				c := commercialFrom(t, s.Metadata)
				expected := piaimcCloud(tc.clouds[i])
				if (s.CloudCover == nil) != (expected == nil) || s.CloudCover != nil && *s.CloudCover != *expected {
					t.Fatal(c.Cloud)
				}
				if len(c.FileRecordRefs) != 1 {
					t.Fatal(c.FileRecordRefs)
				}
				for _, r := range c.Records {
					if r.Storage != "image_segment" || *r.SegmentIndex != i {
						t.Fatal(r)
					}
					if len(r.Sources) < 2 {
						t.Fatalf("representations not reconciled: %+v", r)
					}
				}
				if tc.aux && i == len(tc.clouds)-1 {
					if c.Role != "cloud_grid" {
						t.Fatal(c.Role)
					}
				} else {
					if c.Role != "unresolved" {
						t.Fatal(c.Role)
					}
					v := valueNamed(t, c.Values, "image_duration")
					if v.Value != float64(-1) {
						t.Fatal(v)
					}
				}
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("source changed")
			}
			entries, _ := os.ReadDir(filepath.Dir(path))
			if len(entries) != 1 {
				t.Fatal("source sidecars written")
			}
		})
	}
	t.Run("ordinary", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ordinary.ntf")
		testutil.NITF(t, path, "IID1=P1")
		result, err := Inspect(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if c := commercialFrom(t, result.Metadata); c.Profile.Status != "unknown" || c.DES.Status != "not_inspected" {
			t.Fatal(c)
		}
	})
	t.Run("sidecar", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "scene.ntf")
		testutil.CommercialNITF(t, path, []string{"001", "100"}, false)
		for _, cloud := range []string{"0.2", "malformed"} {
			sidecar := strings.TrimSuffix(path, ".ntf") + ".xml"
			_ = os.WriteFile(sidecar, []byte("<isd><IMD><IMAGE><CLOUDCOVER>"+cloud+"</CLOUDCOVER></IMAGE></IMD></isd>"), 0600)
			result, err := Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			c := commercialFrom(t, result.Metadata)
			if cloud == "0.2" {
				if result.CloudCover == nil || *result.CloudCover != 20 || !strings.HasPrefix(c.Cloud.SelectedSource, "xml_sidecar/") || !c.Cloud.Conflicting {
					t.Fatal(c.Cloud)
				}
			} else if result.CloudCover != nil || c.Cloud.Value != nil {
				t.Fatal("bad sidecar broke aggregation")
			}
		}
	})
}
