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
	if i.AcquiredAt != nil {
		t.Fatal("export time must not be acquisition time")
	}
	if i.Width != 20 || i.Height != 10 || i.BandCount != 1 {
		t.Fatalf("incorrect dimensions: %+v", i)
	}
	base["metadata"] = map[string]any{"": map[string]string{"ACQUISITION_DATETIME": "2026-09-10T12:00:00-06:00"}}
	data, _ = json.Marshal(base)
	i, err = ParseInfo(data)
	if err != nil || i.AcquiredAt == nil || i.AcquiredAt.Hour() != 18 {
		t.Fatalf("acquisition time: %+v %v", i.AcquiredAt, err)
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
