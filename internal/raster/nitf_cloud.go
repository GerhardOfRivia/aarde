package raster

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

type cloudSource struct {
	Source          string   `json:"source"`
	Domain          string   `json:"domain"`
	TRE             string   `json:"tre,omitempty"`
	Field           string   `json:"field"`
	Occurrence      int      `json:"occurrence,omitempty"`
	OriginalUnits   string   `json:"original_units"`
	Units           string   `json:"units"`
	Raw             string   `json:"raw"`
	Value           *float64 `json:"value"`
	SegmentIndex    *int     `json:"segment_index,omitempty"`
	ObservedSegment *int     `json:"observed_segment,omitempty"`
	Warning         string   `json:"warning,omitempty"`
}
type cloudSelection struct {
	Value          *float64      `json:"value"`
	SelectedSource string        `json:"selected_source"`
	Sources        []cloudSource `json:"sources"`
	Conflicting    bool          `json:"conflicting"`
}

func chooseCloud(sources []cloudSource, diagnostics *[]nitfDiagnostic) cloudSelection {
	c := cloudSelection{Sources: sources}
	for _, s := range sources {
		if s.Value == nil {
			continue
		}
		if c.Value == nil {
			c.Value = s.Value
			c.SelectedSource = s.Source
		} else if *c.Value != *s.Value {
			c.Conflicting = true
		}
	}
	if c.Conflicting {
		addDiagnostic(diagnostics, "cloud_source_conflict", "unresolved", nil, c.SelectedSource, "Cloud-cover sources disagree; the first valid source in documented precedence was selected.")
	}
	return c
}

func embeddedCloud(metadata map[string]json.RawMessage, records []treRecord, diagnostics *[]nitfDiagnostic) cloudSelection {
	sources := []cloudSource{}
	scalars := scalarMetadata(metadata)
	// Preserve the pre-existing key/domain/key order, including malformed fallback.
	for _, target := range []string{"CLOUD_COVER", "CLOUD_COVER_PERCENTAGE", "EO:CLOUD_COVER"} {
		for _, domain := range sortedKeys(scalars) {
			for _, key := range sortedKeys(scalars[domain]) {
				if strings.EqualFold(key, target) {
					original := scalars[domain][key]
					var value *float64
					warning := ""
					n, err := strconv.ParseFloat(strings.TrimSpace(original), 64)
					if err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 100 {
						value = &n
					} else {
						warning = "Invalid embedded percentage; lower-priority sources remain eligible."
					}
					sources = append(sources, cloudSource{Source: "metadata/" + domain + "/" + key, Domain: domain, Field: key, OriginalUnits: "%", Units: "%", Raw: original, Value: value, Warning: warning})
					if warning != "" {
						addDiagnostic(diagnostics, "cloud_value_invalid", "unresolved", nil, "metadata/"+domain+"/"+key, warning)
					}
				}
			}
		}
	}
	for _, r := range records {
		if r.Tag == "PIAIMC" {
			raw, ok := r.Fields["CLOUDCVR"]
			if !ok {
				continue
			}
			n := piaimcCloud(raw)
			warning := ""
			if n == nil {
				warning = "Unknown PIAIMC percentage (000–100 valid; 999 unknown)."
			}
			if r.Storage != "image_segment" || r.SegmentIndex == nil {
				n = nil
				warning = "PIAIMC segment association is unresolved; not used as segment cloud cover."
			}
			domain := ""
			if len(r.Sources) > 0 {
				domain = r.Sources[0].Domain
			}
			sources = append(sources, cloudSource{Source: r.ID + "/PIAIMC.CLOUDCVR", Domain: domain, TRE: r.Tag, Field: "CLOUDCVR", Occurrence: r.Occurrence, OriginalUnits: "%", Units: "%", Raw: raw, Value: n, SegmentIndex: r.SegmentIndex, Warning: warning})
		}
	}
	return chooseCloud(sources, diagnostics)
}

func aggregateCloud(segments []Segment, parts []commercialNITF, diagnostics *[]nitfDiagnostic) cloudSelection {
	c := cloudSelection{Sources: []cloudSource{}}
	var firstKnown *float64
	for i, s := range segments {
		c.Sources = append(c.Sources, parts[i].Cloud.Sources...)
		if i == 0 {
			c.Value = s.CloudCover
			c.SelectedSource = parts[i].Cloud.SelectedSource
		}
		if s.CloudCover == nil || c.Value == nil || *s.CloudCover != *c.Value {
			c.Value = nil
		}
		c.Conflicting = c.Conflicting || parts[i].Cloud.Conflicting
		if s.CloudCover != nil {
			if firstKnown == nil {
				firstKnown = s.CloudCover
			} else if *firstKnown != *s.CloudCover {
				c.Conflicting = true
			}
		}
	}
	if len(segments) > 1 {
		c.SelectedSource = "segments/unanimous"
		if c.Value == nil {
			c.SelectedSource = ""
			addDiagnostic(diagnostics, "segment_cloud_not_unanimous", "file", nil, "segments/cloud_cover", "Segment cloud percentages differ or are missing; file cloud cover is unknown (no averaging).")
		}
	}
	return c
}

// OverrideCloudCover records selection at file level only. Existing segment
// values remain independently inspected. Legacy/GeoTIFF metadata is unchanged.
func OverrideCloudCover(result *Inspection, value *float64, source, raw string) {
	var c commercialNITF
	metadata := decodeMetadata(result.Metadata)
	annotations := decodeMetadata(metadata["_aarde"])
	if json.Unmarshal(annotations["commercial_nitf"], &c) == nil && c.Version == 1 {
		prior := c.Cloud
		domain, units := "explicit_override", "%"
		if strings.HasPrefix(source, "xml_sidecar/") {
			domain, units = "xml_sidecar", "fraction"
		}
		warning := ""
		if value == nil {
			warning = "Invalid override source; prior file cloud selection retained."
			addDiagnostic(&c.Diagnostics, "cloud_value_invalid", "file", nil, source, warning)
		}
		sources := append([]cloudSource{{Source: source, Domain: domain, Field: source, OriginalUnits: units, Units: "%", Raw: raw, Value: value, Warning: warning}}, prior.Sources...)
		c.Cloud = chooseCloud(sources, &c.Diagnostics)
		// Segment evidence can disagree, but an invalid sidecar must not replace
		// the unanimous-or-unknown file aggregate with the first segment value.
		if value == nil {
			c.Cloud.Value = prior.Value
			c.Cloud.SelectedSource = prior.SelectedSource
		}
		result.Metadata = storeAnnotation(result.Metadata, "commercial_nitf", c)
	}
	if value != nil {
		result.CloudCover = value
	}
}
