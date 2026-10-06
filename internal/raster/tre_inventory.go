package raster

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const enrichmentLimit = 1 << 20
const maxTRERecords = 512
const maxTREFields = 256
const maxDiagnostics = 128

type nitfDiagnostic struct {
	Code         string `json:"code"`
	Severity     string `json:"severity"`
	Scope        string `json:"scope"`
	SegmentIndex *int   `json:"segment_index,omitempty"`
	Source       string `json:"source_field"`
	Message      string `json:"message"`
}

type treSource struct {
	Domain string            `json:"domain"`
	Path   string            `json:"path"`
	Raw    string            `json:"raw,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

type treRecord struct {
	ID              string            `json:"id"`
	Tag             string            `json:"tag"`
	Occurrence      int               `json:"occurrence"`
	Storage         string            `json:"storage_scope"`
	Describes       string            `json:"semantic_scope"`
	SegmentIndex    *int              `json:"segment_index,omitempty"`
	ObservedSegment *int              `json:"observed_segment,omitempty"`
	Association     map[string]string `json:"association,omitempty"`
	Sources         []treSource       `json:"sources"`
	Fields          map[string]string `json:"fields"`
}

func semanticScope(tag string) string {
	switch tag {
	case "CSDIDA", "CSEPHA", "CSSFAA":
		return "dataset"
	case "CSEXRA", "CSCRNA", "CSPROA", "RPC00A", "RPC00B", "USE00A":
		return "sensor_sub_image"
	case "CSCCGA", "PIAIMC", "STDIDC":
		return "segment"
	default:
		return "unresolved"
	}
}

func addDiagnostic(ds *[]nitfDiagnostic, code, scope string, segment *int, source, message string) {
	if len(*ds) >= maxDiagnostics {
		return
	}
	if len(*ds) == maxDiagnostics-1 {
		code, source, message = "diagnostics_truncated", "", "Additional enrichment diagnostics omitted at the 128-entry limit."
	}
	*ds = append(*ds, nitfDiagnostic{code, "warning", scope, segment, source, message})
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// XML locations describe actual storage, not the schema's expected location.
// In GDAL 3.6.2 a selected image still exposes file TREs and all overflow TREs.
func setTRELocation(r *treRecord, location string, segment *int) {
	r.Storage = "unresolved"
	switch {
	case location == "file":
		r.Storage = "file"
	case location == "image":
		r.Storage = "image_segment"
		r.SegmentIndex = segment
	case location == "des" || strings.HasPrefix(location, "des "):
		r.Storage = "overflow"
	}
	if location != "" {
		r.Association = map[string]string{"gdal_location": location}
	}
}

// Streaming XML with explicit limits. Never resolves entities, executes markup,
// or accepts partially parsed records from a malformed document.
func xmlTREs(document, path string, segment *int) ([]treRecord, error) {
	if len(document) > enrichmentLimit {
		return nil, fmt.Errorf("TRE XML exceeds 1 MiB enrichment limit")
	}
	decoder := xml.NewDecoder(strings.NewReader(document))
	var stack []string
	var records []treRecord
	var current *treRecord
	roots := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			if len(stack) >= 32 {
				return nil, fmt.Errorf("TRE XML exceeds 32-element nesting limit")
			}
			if len(stack) == 0 {
				roots++
				if v.Name.Local != "tres" {
					return nil, fmt.Errorf("unsupported TRE XML root")
				}
			}
			attrs := map[string]string{}
			for _, a := range v.Attr {
				if len(a.Value) > 4096 {
					return nil, fmt.Errorf("TRE XML attribute exceeds 4096 bytes")
				}
				attrs[a.Name.Local] = a.Value
			}
			stack = append(stack, v.Name.Local)
			if v.Name.Local == "tre" {
				if len(stack) != 2 || current != nil || len(records) >= maxTRERecords {
					return nil, fmt.Errorf("invalid TRE nesting or more than 512 records")
				}
				if len(attrs["name"]) == 0 || len(attrs["name"]) > 6 {
					return nil, fmt.Errorf("invalid TRE tag name length")
				}
				current = &treRecord{Tag: attrs["name"], Describes: semanticScope(attrs["name"]), ObservedSegment: segment, Fields: map[string]string{}}
				setTRELocation(current, attrs["location"], segment)
				// Preserve available associations without interpreting undocumented ones.
				for k, value := range attrs {
					if k != "name" && k != "location" {
						if current.Association == nil {
							current.Association = map[string]string{}
						}
						current.Association[k] = value
					}
				}
				current.Sources = []treSource{{Domain: "xml:TRE", Path: fmt.Sprintf("%s/tres/tre[%d]", path, len(records))}}
			} else if current != nil && v.Name.Local == "field" {
				if len(current.Fields) >= maxTREFields {
					return nil, fmt.Errorf("TRE exceeds 256 decoded fields")
				}
				key := attrs["name"]
				// Nested/loop fields are inventory only, never promoted to a top-level scalar.
				if len(stack) != 3 {
					key = strings.Join(stack[2:len(stack)-1], "/") + "/" + key
				}
				base := key
				for n := 2; ; n++ {
					if _, ok := current.Fields[key]; !ok {
						break
					}
					key = fmt.Sprintf("%s#%d", base, n)
				}
				current.Fields[key] = attrs["value"]
			} else if current != nil && v.Name.Local == "error" {
				return nil, fmt.Errorf("GDAL reports a TRE decoding error; raw metadata retained")
			}
		case xml.EndElement:
			if v.Name.Local == "tre" && current != nil {
				current.Sources[0].Fields = current.Fields
				records = append(records, *current)
				current = nil
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && strings.TrimSpace(string(v)) != "" {
				return nil, fmt.Errorf("text outside TRE XML root")
			}
		}
	}
	if roots != 1 {
		return nil, fmt.Errorf("expected one TRE XML root")
	}
	return records, nil
}

func rawTREFields(tag, payload string) (map[string]string, error) {
	layout, ok := treLayouts[tag]
	if !ok {
		return map[string]string{}, nil
	}
	width := 0
	for _, f := range layout {
		width += f.width
	}
	// These supported TREs contain printable ASCII, so backslash-escaped binary
	// is an unsupported variant, not something to coerce into the dated layout.
	if len(payload) != width {
		return nil, fmt.Errorf("%s payload has %d bytes; supported layout has %d", tag, len(payload), width)
	}
	for _, c := range []byte(payload) {
		if c < 32 || c > 126 {
			return nil, fmt.Errorf("%s payload is not printable ASCII", tag)
		}
	}
	fields := map[string]string{}
	offset := 0
	for _, f := range layout {
		fields[f.name] = payload[offset : offset+f.width]
		offset += f.width
	}
	return fields, nil
}

func treTag(key string) (string, int) {
	parts := strings.Split(key, "_")
	if len(parts) == 2 {
		if n, err := strconv.Atoi(parts[1]); err == nil && n >= 2 {
			return parts[0], n
		}
	}
	return key, 1
}

func compatibleFields(a, b map[string]string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for key, value := range b {
		if strings.TrimSpace(a[key]) != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}

func collectTREs(metadata map[string]json.RawMessage, segment *int) ([]treRecord, []nitfDiagnostic) {
	var records []treRecord
	var diagnostics []nitfDiagnostic
	warn := func(code, path, message string) {
		addDiagnostic(&diagnostics, code, "unresolved", segment, path, message)
	}
	// Handle gdalinfo's XML scalar, array of documents, and object of documents.
	var documents []struct{ path, text string }
	raw := metadata["xml:TRE"]
	if len(raw) > enrichmentLimit {
		warn("enrichment_limit", "metadata/xml:TRE", "TRE XML exceeds 1 MiB; original metadata retained.")
	} else if len(raw) > 0 {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			appendDoc := func(path string, v any) {
				if len(path) > 512 {
					warn("enrichment_limit", "metadata/xml:TRE", "XML document source path exceeds 512 bytes; original metadata retained.")
					return
				}
				if s, ok := v.(string); ok {
					documents = append(documents, struct{ path, text string }{path, s})
				}
			}
			switch v := value.(type) {
			case string:
				appendDoc("metadata/xml:TRE", v)
			case []any:
				for i, s := range v {
					appendDoc(fmt.Sprintf("metadata/xml:TRE/%d", i), s)
				}
			case map[string]any:
				for _, key := range sortedKeys(v) {
					appendDoc("metadata/xml:TRE/"+key, v[key])
				}
			}
		}
	}
	for _, doc := range documents {
		parsed, err := xmlTREs(doc.text, doc.path, segment)
		if err != nil {
			warn("tre_xml_invalid", doc.path, err.Error())
			continue
		}
		if len(records)+len(parsed) > maxTRERecords {
			warn("enrichment_limit", doc.path, "More than 512 TRE occurrences; original metadata retained.")
			break
		}
		records = append(records, parsed...)
	}
	// Reconcile each raw occurrence one-to-one with decoded XML. Duplicate XML
	// occurrences remain distinct even when their field values are identical.
	scalars := scalarMetadata(metadata)
	rawKeys := sortedKeys(scalars["TRE"])
	sort.SliceStable(rawKeys, func(i, j int) bool {
		a, n := treTag(rawKeys[i])
		b, m := treTag(rawKeys[j])
		if a == b {
			return n < m
		}
		return a < b
	})
	used := map[int]bool{}
	rawCounts := map[string]int{}
	for _, key := range rawKeys {
		tag, _ := treTag(key)
		rawCounts[tag]++
	}
	for _, key := range rawKeys {
		tag, _ := treTag(key)
		payload := scalars["TRE"][key]
		path := "metadata/TRE/" + key
		if len(payload) > enrichmentLimit {
			warn("enrichment_limit", path, "TRE payload exceeds enrichment limit; original retained.")
			continue
		}
		fields, err := rawTREFields(tag, payload)
		if err != nil {
			warn("tre_layout_unsupported", path, err.Error())
		}
		source := treSource{Domain: "TRE", Path: path, Raw: payload, Fields: fields}
		if _, supported := treLayouts[tag]; !supported {
			// The original domain already retains this payload. Do not duplicate
			// potentially large ephemeris/opaque data merely to inventory its tag.
			source.Raw = ""
		}
		match := -1
		for i, r := range records {
			if !used[i] && r.Tag == tag && compatibleFields(r.Fields, fields) {
				match = i
				break
			}
		}
		// For tags outside the five fixed layouts, GDAL preserves tag occurrence
		// order in both domains. Pair only when both expose the same total count.
		if _, supported := treLayouts[tag]; !supported && match < 0 {
			count := 0
			for _, r := range records {
				if r.Tag == tag && r.Sources[0].Domain == "xml:TRE" {
					count++
				}
			}
			if count == rawCounts[tag] {
				for i, r := range records {
					if r.Tag == tag && !used[i] && r.Sources[0].Domain == "xml:TRE" {
						match = i
						break
					}
				}
			}
		}
		if match >= 0 {
			records[match].Sources = append(records[match].Sources, source)
			used[match] = true
			continue
		}
		if len(records) >= maxTRERecords {
			warn("enrichment_limit", path, "More than 512 TRE occurrences; original retained.")
			break
		}
		records = append(records, treRecord{Tag: tag, Storage: "unresolved", Describes: semanticScope(tag), ObservedSegment: segment, Fields: fields, Sources: []treSource{source}})
	}
	// Flattened fields do not establish storage scope: GDAL merges file and
	// image metadata here. They are alternate representations, not new evidence.
	for _, domain := range sortedKeys(scalars) {
		if domain == "TRE" {
			continue
		}
		groups := map[string]map[string]string{}
		for _, key := range sortedKeys(scalars[domain]) {
			if !strings.HasPrefix(key, "NITF_") {
				continue
			}
			tag, field, ok := strings.Cut(strings.TrimPrefix(key, "NITF_"), "_")
			if !ok || len(tag) != 6 || field == "" {
				continue
			}
			validTag := true
			for _, c := range tag {
				if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					validTag = false
				}
			}
			if !validTag {
				continue
			}
			if groups[tag] == nil {
				groups[tag] = map[string]string{}
			}
			if len(groups[tag]) >= maxTREFields || len(scalars[domain][key]) > 4096 {
				warn("enrichment_limit", "metadata/"+domain+"/"+key, "Flattened TRE fields exceed the enrichment limit; original metadata retained.")
				continue
			}
			groups[tag][field] = scalars[domain][key]
		}
		for _, tag := range sortedKeys(groups) {
			fields := groups[tag]
			source := treSource{Domain: domain, Path: "metadata/" + domain + "/NITF_" + tag + "_*", Fields: fields}
			match := -1
			count := 0
			for i, r := range records {
				if r.Tag == tag && compatibleFields(r.Fields, fields) {
					if match < 0 {
						match = i
					}
					count++
				}
			}
			if match >= 0 {
				records[match].Sources = append(records[match].Sources, source)
				if count > 1 {
					warn("tre_representation_ambiguous", source.Path, "Flattened fields match multiple occurrences; attached to the first for traceability, not counted as additional evidence.")
				}
				continue
			}
			for _, r := range records {
				if r.Tag == tag {
					warn("tre_representation_conflict", source.Path, "Flattened fields disagree with the available TRE occurrences; retained as unresolved representation, not independent profile evidence.")
					break
				}
			}
			if len(records) >= maxTRERecords {
				warn("enrichment_limit", source.Path, "More than 512 TRE occurrences; original retained.")
				break
			}
			records = append(records, treRecord{Tag: tag, Storage: "unresolved", Describes: semanticScope(tag), ObservedSegment: segment, Fields: fields, Sources: []treSource{source}})
		}
	}
	counts := map[string]int{}
	for i := range records {
		r := &records[i]
		key := r.Storage + "/" + r.Tag
		counts[key]++
		r.Occurrence = counts[key]
		r.ID = fmt.Sprintf("%s/%d", key, r.Occurrence)
		if segment != nil && r.Storage != "file" && r.Storage != "overflow" {
			r.ID = fmt.Sprintf("segment/%d/%s", *segment, r.ID)
		}
		if r.Storage == "unresolved" {
			warn("tre_scope_unresolved", r.Sources[0].Path, "Storage and coverage cannot be established from this representation; values are reported without assigning them to this image.")
		}
		if r.Storage == "overflow" {
			warn("overflow_association_unresolved", r.Sources[0].Path, "GDAL exposes a DES TRE location but may omit DES index, DESITEM and coverage; no image association is assumed.")
		}
	}
	return records, diagnostics
}

func decodeMetadata(raw json.RawMessage) map[string]json.RawMessage {
	metadata := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &metadata)
	if metadata == nil {
		metadata = map[string]json.RawMessage{}
	}
	return metadata
}

func storeAnnotation(raw json.RawMessage, key string, value any) json.RawMessage {
	metadata := decodeMetadata(raw)
	annotations := decodeMetadata(metadata["_aarde"])
	annotations[key], _ = json.Marshal(value)
	metadata["_aarde"], _ = json.Marshal(annotations)
	result, _ := json.Marshal(metadata)
	return result
}

// Used only for comparison, never to rewrite the retained GDAL domains.
func sameRecord(a, b treRecord) bool {
	x, _ := json.Marshal(a.Fields)
	y, _ := json.Marshal(b.Fields)
	return a.Tag == b.Tag && a.Storage == b.Storage && bytes.Equal(x, y)
}
