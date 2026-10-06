package raster

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type commercialValue struct {
	Name          string `json:"name"`
	Record        string `json:"record"`
	Domain        string `json:"domain"`
	TRE           string `json:"tre"`
	Field         string `json:"field"`
	Path          string `json:"source_path"`
	Occurrence    int    `json:"occurrence"`
	SegmentIndex  *int   `json:"segment_index,omitempty"`
	Scope         string `json:"semantic_scope"`
	Raw           string `json:"raw"`
	OriginalUnits string `json:"original_units"`
	Value         any    `json:"value"`
	Units         string `json:"units"`
	Warning       string `json:"warning,omitempty"`
}

type profileEvidence struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Message string `json:"message"`
}
type nitfProfile struct {
	Candidate    string            `json:"candidate"`
	Status       string            `json:"status"`
	RulesVersion string            `json:"rules_version"`
	Conformance  string            `json:"conformance"`
	Evidence     []profileEvidence `json:"evidence"`
	Missing      []string          `json:"missing"`
	Conflicting  []string          `json:"conflicting"`
	Uninspected  []string          `json:"uninspected"`
}
type desInventory struct {
	Status      string   `json:"status"`
	Complete    bool     `json:"complete"`
	Identifiers []string `json:"identifiers"`
	Sources     []string `json:"sources"`
	Limitation  string   `json:"limitation"`
}
type commercialNITF struct {
	Version        int               `json:"version"`
	Profile        nitfProfile       `json:"profile"`
	Role           string            `json:"role"`
	Records        []treRecord       `json:"records"`
	FileRecordRefs []string          `json:"file_record_refs,omitempty"`
	Values         []commercialValue `json:"values"`
	Cloud          cloudSelection    `json:"cloud"`
	DES            desInventory      `json:"des"`
	Diagnostics    []nitfDiagnostic  `json:"diagnostics"`
}

var decimalTRE = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)
var digitsTRE = regexp.MustCompile(`^[0-9]+$`)

func treNumber(raw string, min, max float64) *float64 {
	s := strings.TrimSpace(raw)
	if !decimalTRE.MatchString(s) {
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < min || n > max {
		return nil
	}
	return &n
}

func piaimcCloud(raw string) *float64 {
	// The width and integer syntax are part of PIAIMC's encoding. No fraction heuristic.
	s := strings.TrimSpace(raw)
	if len(s) != 3 || !digitsTRE.MatchString(s) {
		return nil
	}
	return treNumber(s, 0, 100)
}

func treTimestamp(raw string) *time.Time {
	return nitfAcquisitionTime(map[string]string{"NITF_FHDR": "NITF02.10", "NITF_IDATIM": strings.TrimSpace(raw)})
}

func normalizeCommercial(records []treRecord, diagnostics *[]nitfDiagnostic) []commercialValue {
	values := []commercialValue{}
	for _, r := range records {
		add := func(field, name, originalUnits, units string, value any, warning string) {
			raw, ok := r.Fields[field]
			if !ok {
				return
			}
			source := r.Sources[0]
			if r.Storage == "unresolved" {
				if warning != "" {
					warning += " "
				}
				warning += "Storage/segment association unresolved."
			}
			values = append(values, commercialValue{name, r.ID, source.Domain, r.Tag, field, source.Path + "/" + field, r.Occurrence, r.SegmentIndex, r.Describes, raw, originalUnits, value, units, warning})
			if warning != "" {
				addDiagnostic(diagnostics, "commercial_field_uncertain", r.Storage, r.SegmentIndex, source.Path+"/"+field, warning)
			}
		}
		textField := func(field, name string) {
			raw := strings.TrimSpace(r.Fields[field])
			var value any
			if raw != "" {
				value = raw
			}
			add(field, name, "code/text", "code/text", value, "")
		}
		number := func(field, name, unit string, min, max, factor float64) {
			n := treNumber(r.Fields[field], min, max)
			var value any
			warning := ""
			normalizedUnit := unit
			if factor != 1 {
				normalizedUnit = "m"
			}
			if n != nil {
				value = *n * factor
			} else {
				warning = "Unavailable or outside the supported field encoding/range."
			}
			if field == "TIME_FIRST_LINE_IMAGE" {
				warning = "Seconds from midnight for the synthetic array; date and individual-segment association are not established."
			}
			if field == "TIME_IMAGE_DURATION" && n != nil && *n < 0 {
				warning = "Negative duration denotes reverse chronological ordering (STDI-0006 table 3.5-1 note 1)."
			}
			add(field, name, unit, normalizedUnit, value, warning)
		}
		switch r.Tag {
		case "CSDIDA":
			for _, field := range []string{"PLATFORM_CODE", "VEHICLE_ID", "SENSOR_ID", "PRODUCT_ID", "PASS", "OPERATION", "SOFTWARE_VERSION_NUMBER", "DAY", "MONTH", "YEAR"} {
				textField(field, "dataset_"+strings.ToLower(field))
			}
			for _, field := range []string{"TIME", "PROCESS_TIME"} {
				var value any
				warning := ""
				if t := treTimestamp(r.Fields[field]); t != nil {
					value = t.Format(time.RFC3339)
				} else {
					warning = "Invalid or unavailable full UTC timestamp; no date-only timestamp is fabricated."
				}
				name := "dataset_collection_time"
				if field == "PROCESS_TIME" {
					name = "dataset_processing_time"
				} else {
					warning += " Dataset timing only; may denote imaging end for reverse ordering. Not promoted to segment acquisition."
				}
				add(field, name, "UTC YYYYMMDDhhmmss", "UTC", value, strings.TrimSpace(warning))
			}
		case "CSEXRA":
			textField("SENSOR", "sensor_designation")
			for _, field := range []string{"MAX_GSD", "ALONG_SCAN_GSD", "CROSS_SCAN_GSD", "GEO_MEAN_GSD", "A_S_VERT_GSD", "C_S_VERT_GSD", "GEO_MEAN_VERT_GSD"} {
				number(field, strings.ToLower(field), "in", 0, 999.9, 0.0254)
			}
			for _, field := range []string{"ANGLE_TO_NORTH", "AZ_OF_OBLIQUITY", "SUN_AZIMUTH"} {
				number(field, strings.ToLower(field), "deg", 0, 360, 1)
			}
			number("GSD_BETA_ANGLE", "gsd_beta_angle", "deg", 0, 180, 1)
			number("OBLIQUITY_ANGLE", "obliquity_angle", "deg", 0, 90, 1)
			number("SUN_ELEVATION", "sun_elevation", "deg", -90, 90, 1)
			number("TIME_FIRST_LINE_IMAGE", "first_line_seconds_from_midnight", "s", 0, 86400, 1)
			number("TIME_IMAGE_DURATION", "image_duration", "s", -9999.999999, 86400, 1)
		case "PIAIMC":
			textField("SENSNAME", "sensor_name")
			textField("SENSMODE", "sensor_mode")
			number("MEANGSD", "reported_mean_gsd", "in", 0, 99999.9, 0.0254)
			var value any
			warning := ""
			if n := piaimcCloud(r.Fields["CLOUDCVR"]); n != nil {
				value = *n
			} else {
				warning = "Unknown cloud cover: expected 000–100; 999 is unknown."
			}
			add("CLOUDCVR", "cloud_cover", "%", "%", value, warning)
		case "CSCRNA":
			var predicted any
			warning := ""
			switch strings.TrimSpace(r.Fields["PREDICT_CORNERS"]) {
			case "Y":
				predicted = true
			case "N":
				predicted = false
			default:
				warning = "Unknown prediction code retained."
			}
			add("PREDICT_CORNERS", "corners_predicted", "Y/N", "boolean", predicted, warning)
			for _, corner := range []string{"ULCNR", "URCNR", "LRCNR", "LLCNR"} {
				number(corner+"_LAT", "reported_"+strings.ToLower(corner)+"_latitude", "deg", -90, 90, 1)
				number(corner+"_LONG", "reported_"+strings.ToLower(corner)+"_longitude", "deg", -179.99999, 180, 1)
				number(corner+"_HT", "reported_"+strings.ToLower(corner)+"_height", "m WGS84 ellipsoid", -610, 10668, 1)
			}
		}
	}
	return values
}

// This checks only the supported identification layout, not NCDRD conformance.
func validCSDIDA(r treRecord) bool {
	for _, f := range treLayouts["CSDIDA"] {
		v, ok := r.Fields[f.name]
		if !ok || len(v) > f.width {
			return false
		}
		if f.name != "SOFTWARE_VERSION_NUMBER" && len(strings.TrimSpace(v)) != f.width {
			return false
		}
	}
	for _, f := range []string{"VEHICLE_ID", "PASS", "OPERATION", "DAY", "YEAR"} {
		if !digitsTRE.MatchString(r.Fields[f]) {
			return false
		}
	}
	for _, f := range []string{"PLATFORM_CODE", "SENSOR_ID", "PRODUCT_ID"} {
		for _, c := range r.Fields[f] {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	if treNumber(r.Fields["PASS"], 1, 99) == nil || r.Fields["MONTH"] != strings.ToUpper(r.Fields["MONTH"]) {
		return false
	}
	date, err := time.Parse("02Jan2006", r.Fields["DAY"]+strings.Title(strings.ToLower(r.Fields["MONTH"]))+r.Fields["YEAR"])
	t := treTimestamp(r.Fields["TIME"])
	if err != nil || t == nil || treTimestamp(r.Fields["PROCESS_TIME"]) == nil || date.Format("20060102") != t.Format("20060102") {
		return false
	}
	for f, v := range map[string]string{"RESERVED_0": "0000", "RESERVED_1": "00", "RESERVED_2": "01", "RESERVED_3": "N", "RESERVED_4": "N"} {
		if r.Fields[f] != v {
			return false
		}
	}
	return true
}

func assessNCDRD(records []treRecord, headers []map[string]string, diagnostics []nitfDiagnostic) nitfProfile {
	p := nitfProfile{Candidate: "NCDRD", Status: "unknown", RulesVersion: "aarde-ncdrd-1", Conformance: "not_evaluated", Evidence: []profileEvidence{}, Missing: []string{}, Conflicting: []string{}, Uninspected: []string{"DES payloads and complete overflow associations", "IID2 vendor/chipping components", "NCDRD conformance and specification revision"}}
	validID, commercial, headerOK, partial := false, false, false, false
	var ids []treRecord
	for _, r := range records {
		if r.Tag == "CSDIDA" {
			partial = true
			p.Evidence = append(p.Evidence, profileEvidence{"identification", r.ID, "Dataset identification TRE exposed in " + r.Storage + " scope."})
			if r.Storage == "file" && validCSDIDA(r) {
				validID = true
				ids = append(ids, r)
			} else if r.Storage == "file" {
				p.Conflicting = append(p.Conflicting, r.ID+": incomplete or unsupported CSDIDA identification layout")
			}
		}
		if r.Tag == "CSEXRA" || r.Tag == "CSCRNA" || r.Tag == "CSPROA" || r.Tag == "CSCCGA" {
			partial = true
			p.Evidence = append(p.Evidence, profileEvidence{"commercial_tre", r.ID, "Commercial-source TRE; describes " + r.Describes + "."})
		}
		if r.Storage == "image_segment" && r.Tag == "CSEXRA" && strings.TrimSpace(r.Fields["SENSOR"]) != "" && treNumber(r.Fields["MAX_GSD"], 0, 999.9) != nil {
			if r.SegmentIndex != nil && *r.SegmentIndex < len(headers) {
				h := headers[*r.SegmentIndex]
				iid := h["NITF_IID1"]
				if len(iid) >= 2 && (iid[0] == 'P' || iid[0] == 'M') && iid[1] >= '1' && iid[1] <= '9' {
					commercial = true
					headerOK = headerOK || h["NITF_FHDR"] == "NITF02.10"
					p.Evidence = append(p.Evidence, profileEvidence{"header", fmt.Sprintf("segment/%d/metadata//NITF_IID1", *r.SegmentIndex), "IID1 follows the documented Px/Mx prefix."})
				}
			}
		}
	}
	for _, r := range ids[min(1, len(ids)):] {
		if !sameRecord(ids[0], r) {
			p.Conflicting = append(p.Conflicting, "Different file-scoped CSDIDA occurrences")
		}
	}
	for _, h := range headers {
		if h["NITF_FHDR"] != "" && h["NITF_FHDR"] != "NITF02.10" && partial {
			p.Conflicting = append(p.Conflicting, "Header version differs from the supported NITF 2.1 profile")
		}
	}
	for _, d := range diagnostics {
		switch d.Code {
		case "tre_representation_conflict":
			p.Conflicting = append(p.Conflicting, d.Source+": "+d.Message)
		case "tre_xml_invalid", "enrichment_limit", "tre_layout_unsupported", "diagnostics_truncated":
			p.Uninspected = append(p.Uninspected, d.Source+": "+d.Message)
		}
	}
	if !validID {
		p.Missing = append(p.Missing, "Complete, structurally supported file-scoped CSDIDA")
	}
	if !commercial {
		p.Missing = append(p.Missing, "Associated CSEXRA with sensor and valid MAX_GSD on an image with documented Px/Mx IID1")
	}
	if !headerOK {
		p.Missing = append(p.Missing, "Compatible NITF 2.1 image header")
	}
	if partial {
		p.Status = "possible"
	}
	if validID && commercial && headerOK && len(p.Conflicting) == 0 && len(p.Uninspected) == 3 {
		p.Status = "likely"
	}
	// The assessment is repeated on segment annotations for consistent API/UI
	// presentation. Keep its evidence and diagnostics compact even for 999 images.
	if len(p.Evidence) > 32 {
		p.Evidence = p.Evidence[:32]
		p.Uninspected = append(p.Uninspected, "Evidence display limited to 32 entries; full retained records carry source locations")
	}
	if len(p.Conflicting) > 32 {
		p.Conflicting = p.Conflicting[:32]
	}
	if len(p.Uninspected) > 32 {
		p.Uninspected = p.Uninspected[:32]
	}
	return p
}

func inspectDES(records []treRecord, metadata map[string]json.RawMessage) desInventory {
	d := desInventory{Status: "not_inspected", Identifiers: []string{}, Sources: []string{}, Limitation: "xml:DES is not requested: it can expose large payloads. Selected TRE domains provide only partial DES coverage. GDAL VALIDATE is not requested; the GDAL 3.6.2 baseline lacks this option. No conformance validation performed."}
	for _, r := range records {
		if r.Storage == "overflow" {
			d.Status = "present"
			id := strings.TrimSpace(strings.TrimPrefix(r.Association["gdal_location"], "des"))
			if id != "" {
				d.Identifiers = append(d.Identifiers, id)
			}
			d.Sources = append(d.Sources, r.Sources[0].Path)
		}
	}
	// Only an explicitly exposed zero count establishes absence. Missing does not.
	if count, ok := scalarMetadata(metadata)[""]["NITF_NUMDES"]; ok && digitsTRE.MatchString(count) && treNumber(count, 0, 0) != nil && d.Status != "present" {
		d.Status = "absent"
		d.Complete = true
		d.Sources = append(d.Sources, "metadata//NITF_NUMDES")
	}
	return d
}

func newCommercial(records []treRecord, diagnostics []nitfDiagnostic, metadata map[string]json.RawMessage) commercialNITF {
	if records == nil {
		records = []treRecord{}
	}
	if diagnostics == nil {
		diagnostics = []nitfDiagnostic{}
	}
	c := commercialNITF{Version: 1, Role: "unresolved", Records: records, Diagnostics: diagnostics, DES: inspectDES(records, metadata)}
	c.Values = normalizeCommercial(records, &c.Diagnostics)
	for _, r := range records {
		if r.Tag == "CSCCGA" && r.Storage == "image_segment" && validCloudGrid(r) {
			c.Role = "cloud_grid"
		}
	}
	c.Cloud = embeddedCloud(metadata, records, &c.Diagnostics)
	return c
}

func validCloudGrid(r treRecord) bool {
	if s := strings.TrimSpace(r.Fields["REG_SENSOR"]); s != "PAN" && s != "MS" {
		return false
	}
	if s := strings.ReplaceAll(strings.TrimSpace(r.Fields["CCG_SOURCE"]), " ", ""); s != "PAN" && s != "MS" && s != "PAN,MS" {
		return false
	}
	for _, f := range []string{"ORIGIN_LINE", "ORIGIN_SAMPLE", "AS_CELL_SIZE", "CS_CELL_SIZE", "CCG_MAX_LINE", "CCG_MAX_SAMPLE"} {
		raw := strings.TrimSpace(r.Fields[f])
		maxValue := 9999999.0
		if f == "ORIGIN_LINE" || f == "ORIGIN_SAMPLE" {
			maxValue = 1
		}
		if f == "CS_CELL_SIZE" || f == "CCG_MAX_SAMPLE" {
			maxValue = 99999
		}
		if !digitsTRE.MatchString(raw) || treNumber(raw, 1, maxValue) == nil {
			return false
		}
	}
	return true
}

// Enrich after independent raster validation, before sidecar and explicit overrides.
// Physical-file evidence is stored once; each segment carries references to it.
func enrichNITF(result *Inspection, container info) {
	fileRecords, fileDiagnostics := collectTREs(container.Metadata, nil)
	kept := []treRecord{}
	for _, r := range fileRecords {
		if r.Storage != "image_segment" {
			kept = append(kept, r)
		}
	}
	fileRecords = kept
	remaining := maxTRERecords - len(fileRecords)
	headers := []map[string]string{}
	all := append([]treRecord{}, fileRecords...)
	parts := make([]commercialNITF, len(result.Segments))
	allDiagnostics := append([]nitfDiagnostic{}, fileDiagnostics...)
	for i := range result.Segments {
		s := &result.Segments[i]
		metadata := decodeMetadata(s.Metadata)
		index := s.Index
		headers = append(headers, scalarMetadata(metadata)[""])
		records, diagnostics := collectTREs(metadata, &index)
		local := []treRecord{}
		refs := []string{}
		for _, r := range records {
			if r.Storage == "file" || r.Storage == "overflow" {
				found := false
				for _, f := range fileRecords {
					if f.ID == r.ID && sameRecord(f, r) {
						found = true
						break
					}
				}
				if !found {
					if remaining == 0 {
						addDiagnostic(&diagnostics, "enrichment_limit", "file", nil, "metadata", "Physical-file enrichment exceeds 512 TRE occurrences; original metadata retained.")
						continue
					}
					remaining--
					fileRecords = append(fileRecords, r)
					all = append(all, r)
				}
				refs = append(refs, r.ID)
			} else {
				if remaining == 0 {
					addDiagnostic(&diagnostics, "enrichment_limit", "image_segment", &index, "metadata", "Physical-file enrichment exceeds 512 TRE occurrences; original segment metadata retained.")
					break
				}
				remaining--
				local = append(local, r)
				all = append(all, r)
			}
		}
		allDiagnostics = append(allDiagnostics, diagnostics...)
		parts[i] = newCommercial(local, diagnostics, metadata)
		parts[i].FileRecordRefs = refs
		for j := range parts[i].Cloud.Sources {
			parts[i].Cloud.Sources[j].ObservedSegment = &index
		}
		for j := range parts[i].Diagnostics {
			if parts[i].Diagnostics[j].Scope == "unresolved" && parts[i].Diagnostics[j].SegmentIndex == nil {
				parts[i].Diagnostics[j].SegmentIndex = &index
			}
		}
		s.CloudCover = parts[i].Cloud.Value
	}
	profile := assessNCDRD(all, headers, allDiagnostics)
	for i := range parts {
		parts[i].Profile = profile
		result.Segments[i].Metadata = storeAnnotation(result.Segments[i].Metadata, "commercial_nitf", parts[i])
	}
	file := newCommercial(fileRecords, fileDiagnostics, container.Metadata)
	file.Profile = profile
	file.Role = "dataset"
	file.Cloud = aggregateCloud(result.Segments, parts, &file.Diagnostics)
	result.CloudCover = file.Cloud.Value
	result.Metadata = storeAnnotation(result.Metadata, "commercial_nitf", file)
}
