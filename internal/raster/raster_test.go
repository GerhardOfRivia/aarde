package raster

import (
	"encoding/json"
	"testing"
)

func TestParseInfo(t *testing.T) {
	base := map[string]any{"driverShortName": "GTiff", "size": []int{20, 10}, "coordinateSystem": map[string]any{"wkt": "PROJCRS[UTM zone 13N]"}, "cornerCoordinates": map[string]any{"upperLeft": []int{400000, 4400000}, "upperRight": []int{401000, 4400000}, "lowerRight": []int{401000, 4399000}, "lowerLeft": []int{400000, 4399000}}, "wgs84Extent": map[string]any{"type": "Polygon", "coordinates": [][][]float64{{{-106, 40}, {-105, 40}, {-105, 39}, {-106, 39}, {-106, 40}}}}, "bands": []any{map[string]int{"band": 1}}, "metadata": map[string]any{"": map[string]string{"TIFFTAG_DATETIME": "2026:09:10 12:00:00"}}}
	data, _ := json.Marshal(base)
	i, err := ParseInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if i.CloudCover != nil {
		t.Fatal("missing cloud cover must be unknown")
	}
	if i.AcquiredAt != nil {
		t.Fatal("export time must not be acquisition time")
	}
	if i.Width != 20 || i.Height != 10 || i.BandCount != 1 {
		t.Fatalf("incorrect dimensions: %+v", i)
	}
	base["metadata"] = map[string]any{"": map[string]string{"ACQUISITION_DATETIME": "2026-09-10T12:00:00-06:00", "CLOUD_COVER": "12.5"}}
	data, _ = json.Marshal(base)
	i, err = ParseInfo(data)
	if err != nil || i.AcquiredAt == nil || i.AcquiredAt.Hour() != 18 {
		t.Fatalf("acquisition time: %+v %v", i.AcquiredAt, err)
	}
	if i.CloudCover == nil || *i.CloudCover != 12.5 {
		t.Fatalf("cloud cover: %v", i.CloudCover)
	}
	base["coordinateSystem"] = nil
	data, _ = json.Marshal(base)
	if _, err = ParseInfo(data); err == nil {
		t.Fatal("accepted missing CRS")
	}
	if _, err = ParseInfo([]byte("corrupt")); err == nil {
		t.Fatal("accepted corrupt metadata")
	}
}
func TestAmbiguousAcquisitionTime(t *testing.T) {
	if acquisitionTime(map[string]map[string]string{"": {"ACQUISITION_TIME": "2026-09-10 12:00:00"}}) != nil {
		t.Fatal("accepted timestamp without timezone")
	}
}

func TestCloudCoverMetadata(t *testing.T) {
	for _, tc := range []struct {
		key, raw string
		want     float64
		valid    bool
	}{
		{"CLOUD_COVER", "0", 0, true}, {"cloud_cover_percentage", " 12.5 ", 12.5, true},
		{"eo:cloud_cover", "100", 100, true}, {"CLOUD_COVER", "0.5", 0.5, true},
		{"CLOUD_COVER", "-1", 0, false}, {"CLOUD_COVER", "100.01", 0, false},
		{"CLOUD_COVER", "NaN", 0, false}, {"CLOUD_COVER", "+Inf", 0, false},
		{"CLOUD_COVER", "", 0, false}, {"CLOUD_COVER", "unknown", 0, false},
		{"CLOUD_COVER", "12%", 0, false}, {"unrelated", "12", 0, false},
	} {
		t.Run(tc.key+tc.raw, func(t *testing.T) {
			got := cloudCover(map[string]map[string]string{"domain": {tc.key: tc.raw}})
			if (got != nil) != tc.valid || got != nil && *got != tc.want {
				t.Fatalf("got %v; want %v (valid %v)", got, tc.want, tc.valid)
			}
		})
	}
	metadata := map[string]map[string]string{"z": {"CLOUD_COVER": "90"}, "": {"cloud_cover": "20", "CLOUD_COVER_PERCENTAGE": "30"}}
	for range 20 {
		if got := cloudCover(metadata); got == nil || *got != 20 {
			t.Fatal("unstable metadata priority")
		}
	}
}
