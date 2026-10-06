package raster

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"strings"
	"testing"
)

func trePayload(t *testing.T, tag string, fields map[string]string) string {
	t.Helper()
	var b strings.Builder
	for _, f := range treLayouts[tag] {
		v := fields[f.name]
		if len(v) > f.width {
			t.Fatalf("%s.%s too wide", tag, f.name)
		}
		b.WriteString(v + strings.Repeat(" ", f.width-len(v)))
	}
	return b.String()
}
func identificationFields() map[string]string {
	return map[string]string{"DAY": "10", "MONTH": "SEP", "YEAR": "2026", "PLATFORM_CODE": "WV", "VEHICLE_ID": "02", "PASS": "01", "OPERATION": "001", "SENSOR_ID": "AA", "PRODUCT_ID": "01", "RESERVED_0": "0000", "TIME": "20260910120000", "PROCESS_TIME": "20260911130000", "RESERVED_1": "00", "RESERVED_2": "01", "RESERVED_3": "N", "RESERVED_4": "N", "SOFTWARE_VERSION_NUMBER": "test"}
}
func treXML(tag, location string, fields map[string]string) string {
	escape := func(s string) string { var b strings.Builder; _ = xml.EscapeText(&b, []byte(s)); return b.String() }
	s := `<tre name="` + tag + `" location="` + location + `">`
	for _, f := range sortedKeys(fields) {
		s += `<field name="` + f + `" value="` + escape(fields[f]) + `"/>`
	}
	return s + `</tre>`
}
func metadataJSON(v map[string]any) map[string]json.RawMessage {
	b, _ := json.Marshal(v)
	return decodeMetadata(b)
}
func valueNamed(t *testing.T, values []commercialValue, name string) commercialValue {
	t.Helper()
	for _, v := range values {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("missing %s in %+v", name, values)
	return commercialValue{}
}
func commercialFrom(t *testing.T, raw json.RawMessage) commercialNITF {
	t.Helper()
	var c commercialNITF
	m := decodeMetadata(raw)
	a := decodeMetadata(m["_aarde"])
	if err := json.Unmarshal(a["commercial_nitf"], &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCommercialRepresentations(t *testing.T) {
	fields := identificationFields()
	payload := trePayload(t, "CSDIDA", fields)
	flat := map[string]string{}
	for k, v := range fields {
		flat["NITF_CSDIDA_"+k] = v
	}
	for _, tc := range []struct {
		name    string
		domains map[string]any
	}{
		{"flattened", map[string]any{"": flat}},
		{"xml string", map[string]any{"xml:TRE": "<tres>" + treXML("CSDIDA", "file", fields) + "</tres>"}},
		{"xml array", map[string]any{"xml:TRE": []string{"<tres>" + treXML("CSDIDA", "file", fields) + "</tres>"}}},
		{"xml object", map[string]any{"xml:TRE": map[string]string{"document": "<tres>" + treXML("CSDIDA", "file", fields) + "</tres>"}}},
		{"raw", map[string]any{"TRE": map[string]string{"CSDIDA": payload}}},
		{"all", map[string]any{"": flat, "TRE": map[string]string{"CSDIDA": payload}, "xml:TRE": "<tres>" + treXML("CSDIDA", "file", fields) + "</tres>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := metadataJSON(tc.domains)
			index := 3
			records, diags := collectTREs(m, &index)
			if len(records) != 1 {
				t.Fatalf("records: %+v", records)
			}
			v := normalizeCommercial(records, &diags)
			if valueNamed(t, v, "dataset_platform_code").Value != "WV" || valueNamed(t, v, "dataset_vehicle_id").Value != "02" || valueNamed(t, v, "dataset_product_id").Value != "01" {
				t.Fatal(v)
			}
			if valueNamed(t, v, "dataset_collection_time").Value != "2026-09-10T12:00:00Z" || valueNamed(t, v, "dataset_processing_time").Value != "2026-09-11T13:00:00Z" {
				t.Fatal(v)
			}
			if tc.name == "all" && (len(records[0].Sources) != 3 || records[0].Storage != "file" || records[0].SegmentIndex != nil) {
				t.Fatal(records)
			}
		})
	}
}

func TestTREOccurrencesAndScope(t *testing.T) {
	f := map[string]string{"CLOUDCVR": "001"}
	payload := trePayload(t, "PIAIMC", f)
	decoded, _ := rawTREFields("PIAIMC", payload)
	document := "<tres>" + treXML("PIAIMC", "file", decoded) + treXML("PIAIMC", "image", decoded) + treXML("PIAIMC", "image", decoded) + treXML("CSEPHA", "des TRE_OVERFLOW", map[string]string{"NUM_EPHEM": "001"}) + "</tres>"
	m := metadataJSON(map[string]any{"xml:TRE": document, "TRE": map[string]string{"PIAIMC": payload, "PIAIMC_2": payload, "PIAIMC_3": payload, "CSEPHA": "opaque"}, "": map[string]string{"NITF_PIAIMC_CLOUDCVR": "001"}})
	index := 4
	records, ds := collectTREs(m, &index)
	if len(records) != 4 {
		t.Fatalf("duplicate representations: %+v", records)
	}
	if records[0].Storage != "file" || records[0].SegmentIndex != nil || records[1].Storage != "image_segment" || *records[1].SegmentIndex != 4 || records[2].Occurrence != 2 || records[3].Storage != "overflow" || records[3].SegmentIndex != nil {
		t.Fatal(records)
	}
	if len(records[3].Sources) != 2 {
		t.Fatal("unhandled schema TRE counted twice")
	}
	des := inspectDES(records, m)
	if des.Status != "present" || des.Complete || len(ds) == 0 {
		t.Fatal(des, ds)
	}
}

func TestPIAIMCCloudEncoding(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{{"000", 0}, {"001", 1}, {"100", 100}, {"999", -1}, {"", -1}, {"   ", -1}, {"NaN", -1}, {"Inf", -1}, {"-01", -1}, {"101", -1}, {"0.1", -1}, {"1e2", -1}, {"1", -1}, {"bad", -1}} {
		t.Run(tc.raw, func(t *testing.T) {
			got := piaimcCloud(tc.raw)
			if tc.want < 0 {
				if got != nil {
					t.Fatal(got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatal(got)
			}
		})
	}
}

func TestCommercialNumbersAndTiming(t *testing.T) {
	for _, tc := range []struct {
		tag, field, name, raw string
		want                  any
	}{
		{"CSEXRA", "MAX_GSD", "max_gsd", "010.0", 0.254}, {"CSEXRA", "GEO_MEAN_GSD", "geo_mean_gsd", "000.0", float64(0)}, {"CSEXRA", "ALONG_SCAN_GSD", "along_scan_gsd", "N/A", nil}, {"CSEXRA", "MAX_GSD", "max_gsd", "1000", nil},
		{"PIAIMC", "MEANGSD", "reported_mean_gsd", "00100.0", 2.54}, {"PIAIMC", "MEANGSD", "reported_mean_gsd", "NaN", nil},
		{"CSEXRA", "SUN_ELEVATION", "sun_elevation", "-90.000", float64(-90)}, {"CSEXRA", "SUN_ELEVATION", "sun_elevation", "90.001", nil},
		{"CSEXRA", "TIME_IMAGE_DURATION", "image_duration", "-0001.000000", float64(-1)}, {"CSEXRA", "TIME_FIRST_LINE_IMAGE", "first_line_seconds_from_midnight", "43200.000000", float64(43200)},
		{"CSCRNA", "ULCNR_LAT", "reported_ulcnr_latitude", "+00.00000", float64(0)}, {"CSCRNA", "ULCNR_LONG", "reported_ulcnr_longitude", "-180.00000", nil},
		{"CSDIDA", "TIME", "dataset_collection_time", "20260229123456", nil}, {"CSDIDA", "TIME", "dataset_collection_time", "20240229123456", "2024-02-29T12:34:56Z"}, {"CSDIDA", "TIME", "dataset_collection_time", "00000000000000", nil}, {"CSDIDA", "TIME", "dataset_collection_time", "20260910", nil},
		{"CSDIDA", "PLATFORM_CODE", "dataset_platform_code", "ZZ", "ZZ"}, {"CSDIDA", "PRODUCT_ID", "dataset_product_id", "09", "09"},
	} {
		t.Run(tc.field+tc.raw, func(t *testing.T) {
			r := treRecord{Tag: tc.tag, Storage: "file", Fields: map[string]string{tc.field: tc.raw}, Sources: []treSource{{Domain: "xml:TRE"}}}
			var ds []nitfDiagnostic
			v := valueNamed(t, normalizeCommercial([]treRecord{r}, &ds), tc.name)
			if want, ok := tc.want.(float64); ok {
				got, ok := v.Value.(float64)
				if !ok || math.Abs(got-want) > 1e-9 {
					t.Fatalf("%+v want %v", v, want)
				}
			} else if v.Value != tc.want {
				t.Fatalf("%+v want %v", v, tc.want)
			}
			if v.Raw != tc.raw || v.Field != tc.field || v.Domain != "xml:TRE" {
				t.Fatal(v)
			}
		})
	}
}

func TestNCDRDProfileRules(t *testing.T) {
	index := 0
	id := treRecord{ID: "file/CSDIDA/1", Tag: "CSDIDA", Storage: "file", Fields: identificationFields()}
	image := treRecord{ID: "segment/0/CSEXRA/1", Tag: "CSEXRA", Storage: "image_segment", SegmentIndex: &index, Fields: map[string]string{"SENSOR": "PAN", "MAX_GSD": "020.0"}}
	header := map[string]string{"NITF_FHDR": "NITF02.10", "NITF_IID1": "P1", "NITF_ISORCE": "DigitalGlobe", "NITF_IID2": "arbitrary NCDRD text"}
	conflict := id
	conflict.Fields = identificationFields()
	conflict.Fields["VEHICLE_ID"] = "03"
	unresolved := id
	unresolved.Storage = "unresolved"
	bad := id
	bad.Fields = identificationFields()
	bad.Fields["TIME"] = "bad"
	for _, tc := range []struct {
		name    string
		records []treRecord
		status  string
	}{
		{"likely", []treRecord{id, image}, "likely"}, {"partial", []treRecord{id}, "possible"}, {"image only", []treRecord{image}, "possible"}, {"unresolved", []treRecord{unresolved, image}, "possible"}, {"conflict", []treRecord{id, conflict, image}, "possible"}, {"bad identification", []treRecord{bad, image}, "possible"}, {"absent", nil, "unknown"}, {"generic RPC", []treRecord{{Tag: "RPC00B"}, {Tag: "STDIDC"}, {Tag: "PIAIMC"}, {Tag: "USE00A"}}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := assessNCDRD(tc.records, []map[string]string{header}, nil)
			if p.Status != tc.status || p.Conformance != "not_evaluated" || p.RulesVersion == "" {
				t.Fatal(p)
			}
		})
	}
	header["NITF_FHDR"] = "NITF02.00"
	p := assessNCDRD([]treRecord{id, image}, []map[string]string{header}, nil)
	if p.Status != "possible" || len(p.Conflicting) == 0 {
		t.Fatal(p)
	}
}

func TestCommercialCloudPrecedence(t *testing.T) {
	index := 0
	r := treRecord{ID: "segment/0/image_segment/PIAIMC/1", Tag: "PIAIMC", Storage: "image_segment", SegmentIndex: &index, Fields: map[string]string{"CLOUDCVR": "001"}}
	for _, tc := range []struct {
		primary, secondary string
		want               float64
		conflict           bool
	}{{"bad", "", 1, false}, {"999", "12", 12, true}, {"0", "12", 0, true}, {"50", "", 50, true}} {
		var ds []nitfDiagnostic
		m := metadataJSON(map[string]any{"": map[string]string{"CLOUD_COVER": tc.primary, "CLOUD_COVER_PERCENTAGE": tc.secondary}})
		c := embeddedCloud(m, []treRecord{r}, &ds)
		if c.Value == nil || *c.Value != tc.want || c.Conflicting != tc.conflict {
			t.Fatal(c)
		}
		result := Inspection{Format: "NITF", CloudCover: c.Value, Metadata: json.RawMessage(`{}`)}
		result.Metadata = storeAnnotation(result.Metadata, "commercial_nitf", commercialNITF{Version: 1, Cloud: c})
		sidecar := 20.0
		OverrideCloudCover(&result, &sidecar, "xml_sidecar/isd/IMD/IMAGE/CLOUDCOVER", "0.2")
		got := commercialFrom(t, result.Metadata)
		if *result.CloudCover != 20 || got.Cloud.SelectedSource != "xml_sidecar/isd/IMD/IMAGE/CLOUDCOVER" {
			t.Fatal(got)
		}
		zero := 0.0
		OverrideCloudCover(&result, &zero, "explicit_override", "0")
		got = commercialFrom(t, result.Metadata)
		if *result.CloudCover != 0 || got.Cloud.SelectedSource != "explicit_override" || !got.Cloud.Conflicting {
			t.Fatal(got)
		}
	}
	for _, other := range []*float64{nil, ptrFloat(40), ptrFloat(1)} {
		parts := []commercialNITF{{Cloud: cloudSelection{Value: ptrFloat(1)}}, {Cloud: cloudSelection{Value: other}}}
		segments := []Segment{{CloudCover: ptrFloat(1)}, {CloudCover: other}}
		var ds []nitfDiagnostic
		c := aggregateCloud(segments, parts, &ds)
		if (c.Value != nil) != (other != nil && *other == 1) {
			t.Fatal(c)
		}
	}
}
func ptrFloat(n float64) *float64 { return &n }

func TestCommercialLimitsAndDES(t *testing.T) {
	for _, document := range []string{`<tres><tre`, `<tres>` + strings.Repeat(`<x>`, 33) + strings.Repeat(`</x>`, 33) + `</tres>`, strings.Repeat(" ", enrichmentLimit+1), `<!DOCTYPE tres [<!ENTITY x SYSTEM "file:///etc/passwd">]><tres>&x;</tres>`} {
		records, ds := collectTREs(metadataJSON(map[string]any{"xml:TRE": document}), nil)
		if len(records) != 0 || len(ds) == 0 {
			t.Fatal("invalid XML accepted")
		}
	}
	records, ds := collectTREs(metadataJSON(map[string]any{"TRE": map[string]string{"CSDIDA": "truncated", "ZZZZZZ": "unknown preserved"}}), nil)
	if len(records) != 2 || len(ds) == 0 || len(records[0].Fields) != 0 {
		t.Fatal(records, ds)
	}
	many := map[string]string{}
	for i := 1; i <= maxTRERecords+3; i++ {
		many[fmt.Sprintf("ZZZZZZ_%d", i)] = "raw"
	}
	records, ds = collectTREs(metadataJSON(map[string]any{"TRE": many}), nil)
	if len(records) != maxTRERecords || len(ds) > maxDiagnostics {
		t.Fatal(len(records), len(ds))
	}
	for _, tc := range []struct {
		metadata map[string]json.RawMessage
		status   string
	}{{nil, "not_inspected"}, {metadataJSON(map[string]any{"": map[string]string{"NITF_NUMDES": "0"}}), "absent"}} {
		got := inspectDES(nil, tc.metadata)
		if got.Status != tc.status {
			t.Fatal(got)
		}
	}
}

func TestSharedCornersAndFileEvidenceIsolation(t *testing.T) {
	m := nitfInfo(t)
	metadata := m["metadata"].(map[string]any)
	corners := map[string]string{"PREDICT_CORNERS": "Y", "ULCNR_LAT": "+10.00000", "ULCNR_LONG": "+020.00000", "ULCNR_HT": "+00000.0"}
	metadata["xml:TRE"] = "<tres>" + treXML("CSDIDA", "file", identificationFields()) + treXML("CSCRNA", "image", corners) + "</tres>"
	original, err := parseRasterInfo(encodeInfo(t, m), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := original
	result.Segments = []Segment{{Index: 0, Metadata: original.Metadata, Footprint: original.Footprint}, {Index: 1, Metadata: original.Metadata, Footprint: original.Footprint}}
	var container info
	_ = json.Unmarshal(encodeInfo(t, m), &container)
	enrichNITF(&result, container)
	file := commercialFrom(t, result.Metadata)
	if len(file.Records) != 1 || file.Records[0].Tag != "CSDIDA" {
		t.Fatal(file.Records)
	}
	for _, s := range result.Segments {
		c := commercialFrom(t, s.Metadata)
		if len(c.Records) != 1 || c.Records[0].Tag != "CSCRNA" || len(c.FileRecordRefs) != 1 || c.Values[0].Scope != "sensor_sub_image" || string(s.Footprint.JSON()) != string(original.Footprint.JSON()) {
			t.Fatal(c)
		}
	}
	if string(result.Footprint.JSON()) != string(original.Footprint.JSON()) || result.AcquiredAt == nil || result.AcquiredAt.Format("20060102150405") != "20260910120000" {
		t.Fatal("geometry or acquisition changed")
	}
}

func TestConflictingRepresentationsAndAuxiliaryScope(t *testing.T) {
	fields := identificationFields()
	flat := map[string]string{"NITF_CSDIDA_VEHICLE_ID": "99"}
	records, ds := collectTREs(metadataJSON(map[string]any{"": flat, "xml:TRE": "<tres>" + treXML("CSDIDA", "file", fields) + "</tres>"}), nil)
	if len(records) != 2 || len(ds) == 0 {
		t.Fatal(records, ds)
	}
	p := assessNCDRD(records, nil, ds)
	if p.Status != "possible" || len(p.Conflicting) == 0 {
		t.Fatal(p)
	}
	grid := map[string]string{"CCG_SOURCE": "PAN", "REG_SENSOR": "PAN", "ORIGIN_LINE": "0000001", "ORIGIN_SAMPLE": "00001", "AS_CELL_SIZE": "0000010", "CS_CELL_SIZE": "00010", "CCG_MAX_LINE": "0000002", "CCG_MAX_SAMPLE": "00002"}
	for _, scope := range []string{"file", "image", "des TRE_OVERFLOW", ""} {
		t.Run("auxiliary "+scope, func(t *testing.T) {
			index := 1
			m := metadataJSON(map[string]any{"xml:TRE": "<tres>" + treXML("CSCCGA", scope, grid) + "</tres>"})
			r, d := collectTREs(m, &index)
			c := newCommercial(r, d, m)
			if (c.Role == "cloud_grid") != (scope == "image") {
				t.Fatal(c.Role)
			}
		})
	}
}

func TestDatasetTimeDoesNotSupplyAcquisition(t *testing.T) {
	m := nitfInfo(t)
	metadata := m["metadata"].(map[string]any)
	header := metadata[""].(map[string]any)
	delete(header, "NITF_IDATIM")
	header["NITF_FDT"] = "20260911130000"
	metadata["xml:TRE"] = "<tres>" + treXML("CSDIDA", "file", identificationFields()) + treXML("CSEXRA", "image", map[string]string{"TIME_FIRST_LINE_IMAGE": "43200.000000"}) + "</tres>"
	got, err := ParseInfo(encodeInfo(t, m))
	if err != nil || got.AcquiredAt != nil {
		t.Fatal(got, err)
	}
	c := commercialFrom(t, got.Metadata)
	if valueNamed(t, c.Values, "dataset_collection_time").Value == nil || valueNamed(t, c.Values, "dataset_processing_time").Value == nil {
		t.Fatal(c.Values)
	}
}

func TestPhysicalEnrichmentLimit(t *testing.T) {
	m := nitfInfo(t)
	metadata := m["metadata"].(map[string]any)
	metadata["xml:TRE"] = "<tres>" + strings.Repeat(treXML("CSPROA", "image", map[string]string{"BAND": "shared"}), maxTRERecords/2+1) + "</tres>"
	original, err := parseRasterInfo(encodeInfo(t, m), nil)
	if err != nil {
		t.Fatal(err)
	}
	result := original
	result.Segments = []Segment{{Index: 0, Metadata: original.Metadata}, {Index: 1, Metadata: original.Metadata}, {Index: 2, Metadata: original.Metadata}}
	var container info
	_ = json.Unmarshal(encodeInfo(t, m), &container)
	enrichNITF(&result, container)
	count := len(commercialFrom(t, result.Metadata).Records)
	for _, s := range result.Segments {
		c := commercialFrom(t, s.Metadata)
		count += len(c.Records)
		if len(c.Profile.Evidence) > 32 || len(c.Diagnostics) > maxDiagnostics {
			t.Fatal("unbounded annotation")
		}
	}
	if count != maxTRERecords {
		t.Fatal("physical-file record limit", count)
	}
	if c := commercialFrom(t, result.Segments[2].Metadata); len(c.Diagnostics) == 0 || len(c.Records) != 0 || c.Profile.Status == "likely" {
		t.Fatal(c)
	}
}

func TestCommercialTargetRepresentationParity(t *testing.T) {
	for _, tc := range []struct {
		tag    string
		fields map[string]string
	}{
		{"CSEXRA", map[string]string{"SENSOR": "PAN", "MAX_GSD": "010.0", "TIME_IMAGE_DURATION": "-0001.000000", "SUN_ELEVATION": "+45.000"}},
		{"PIAIMC", map[string]string{"CLOUDCVR": "001", "MEANGSD": "00020.0", "SENSNAME": "UNKNOWN SENSOR", "SENSMODE": "PUSHBROOM"}},
		{"CSCRNA", map[string]string{"PREDICT_CORNERS": "N", "ULCNR_LAT": "+00.00000", "ULCNR_LONG": "+000.00000", "ULCNR_HT": "+00000.0"}},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			payload := trePayload(t, tc.tag, tc.fields)
			fields, err := rawTREFields(tc.tag, payload)
			if err != nil {
				t.Fatal(err)
			}
			flat := map[string]string{}
			for k, v := range fields {
				flat["NITF_"+tc.tag+"_"+k] = strings.TrimSpace(v)
			}
			var baseline string
			for _, domains := range []map[string]any{{"TRE": map[string]string{tc.tag: payload}}, {"xml:TRE": "<tres>" + treXML(tc.tag, "image", fields) + "</tres>"}, {"": flat}} {
				index := 0
				records, ds := collectTREs(metadataJSON(domains), &index)
				if len(records) != 1 {
					t.Fatal(records)
				}
				values := normalizeCommercial(records, &ds)
				normalized := map[string]any{}
				for _, v := range values {
					normalized[v.Name] = []any{v.Value, v.Units}
				}
				encoded, _ := json.Marshal(normalized)
				if baseline == "" {
					baseline = string(encoded)
				} else if string(encoded) != baseline {
					t.Fatalf("representations differ: %s / %s", encoded, baseline)
				}
			}
		})
	}
}
