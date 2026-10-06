package raster

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sidecarLimit accommodates large ISD attitude/ephemeris lists without retaining
// them in catalog metadata. encoding/xml does not resolve external entities.
const sidecarLimit = 4 << 20
const imagePath = "isd/IMD/IMAGE/"

type sidecarMetadata struct {
	Path              string            `json:"path"`
	Schema            string            `json:"schema"`
	Checksum          string            `json:"checksum"`
	SourceFields      map[string]string `json:"source_fields"`
	CloudCover        *float64          `json:"cloud_cover,omitempty"`
	AcquiredAt        *time.Time        `json:"acquired_at,omitempty"`
	AcquisitionSource string            `json:"acquisition_source,omitempty"`
}

func discoverSidecar(path string) (string, error) {
	ext := filepath.Ext(path)
	switch strings.ToLower(ext) {
	case ".tif", ".tiff", ".ntf", ".nitf":
	default:
		return "", nil
	}
	base := strings.TrimSuffix(filepath.Base(path), ext)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	var exact, fallback []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".xml") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if !strings.EqualFold(stem, base) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		candidate := filepath.Join(filepath.Dir(path), name)
		if stem == base {
			exact = append(exact, candidate)
		} else {
			fallback = append(fallback, candidate)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = fallback
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous XML sidecars: %s", strings.Join(matches, ", "))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", nil
}

// parseSidecar uses complete local-name paths, never global tag searches.
// Repeated IMAGE groups have no verified scene/segment association and are rejected.
func parseSidecar(data []byte) (sidecarMetadata, []string, error) {
	m := sidecarMetadata{Schema: "maxar-isd-imd", SourceFields: map[string]string{}}
	if len(data) > sidecarLimit {
		return m, nil, fmt.Errorf("XML exceeds %d-byte limit", sidecarLimit)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var stack []string
	roots, imds, images := 0, 0, 0
	var field, value string
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, fmt.Errorf("malformed XML: %w", err)
		}
		switch v := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				roots++
				if v.Name.Local != "isd" {
					return m, nil, fmt.Errorf("unsupported XML schema")
				}
			}
			if len(stack) >= 64 {
				return m, nil, fmt.Errorf("XML nesting exceeds 64-element limit")
			}
			stack = append(stack, v.Name.Local)
			path := strings.Join(stack, "/")
			if path == "isd/IMD" {
				imds++
			}
			if path == "isd/IMD/IMAGE" {
				images++
			}
			switch path {
			case imagePath + "CLOUDCOVER", imagePath + "FIRSTLINETIME", imagePath + "TLCTIME":
				if _, exists := m.SourceFields[path]; exists {
					return m, nil, fmt.Errorf("duplicate XML field %s", path)
				}
				field, value = path, ""
			default:
				if field != "" {
					return m, nil, fmt.Errorf("nested XML scalar %s", field)
				}
			}
		case xml.CharData:
			if field != "" {
				if len(value)+len(v) > 1024 {
					return m, nil, fmt.Errorf("XML scalar %s exceeds 1024-byte limit", field)
				}
				value += string(v)
			} else if len(stack) == 0 && strings.TrimSpace(string(v)) != "" {
				return m, nil, fmt.Errorf("text outside XML root")
			}
		case xml.EndElement:
			if field != "" {
				m.SourceFields[field] = strings.TrimSpace(value)
				field = ""
			}
			stack = stack[:len(stack)-1]
		}
	}
	if roots != 1 || imds != 1 || images != 1 {
		return m, nil, fmt.Errorf("unsupported XML schema: requires one isd/IMD/IMAGE")
	}
	sum := sha256.Sum256(data)
	m.Checksum = hex.EncodeToString(sum[:])
	var warnings []string
	if raw, ok := m.SourceFields[imagePath+"CLOUDCOVER"]; ok {
		fraction, err := strconv.ParseFloat(raw, 64)
		// DigitalGlobe ISD v1.1.2, p34: fraction 0..1; -999 is unassessed.
		if err == nil && !math.IsNaN(fraction) && !math.IsInf(fraction, 0) && fraction >= 0 && fraction <= 1 {
			percentage := fraction * 100
			m.CloudCover = &percentage
		} else {
			warnings = append(warnings, "invalid CLOUDCOVER (expected finite fraction 0–1; -999 is unassessed)")
		}
	}
	for _, key := range []string{"FIRSTLINETIME", "TLCTIME"} {
		path := imagePath + key
		if raw, ok := m.SourceFields[path]; ok {
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				warnings = append(warnings, "invalid "+key+" (expected RFC3339 timestamp with timezone)")
				continue
			}
			if m.AcquiredAt == nil {
				utc := parsed.UTC()
				m.AcquiredAt = &utc
				m.AcquisitionSource = path
			}
		}
	}
	return m, warnings, nil
}

func applySidecar(path string, result *Inspection) {
	warn := func(err any) { slog.Warn("XML sidecar metadata ignored or incomplete", "raster", path, "warning", err) }
	sidecar, err := discoverSidecar(path)
	if err != nil {
		warn(err)
		return
	}
	if sidecar == "" {
		return
	}
	before, err := os.Lstat(sidecar)
	if err != nil {
		warn(err)
		return
	}
	if !before.Mode().IsRegular() {
		warn("sidecar is no longer a regular file")
		return
	}
	if before.Size() > sidecarLimit {
		warn(fmt.Sprintf("%s exceeds %d-byte limit", sidecar, sidecarLimit))
		return
	}
	file, err := os.Open(sidecar)
	if err != nil {
		warn(err)
		return
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameSource(before, opened) {
		warn("sidecar changed while opening")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, sidecarLimit+1))
	if err != nil {
		warn(err)
		return
	}
	after, err := os.Lstat(sidecar)
	if err != nil || !sameSource(before, after) {
		warn("sidecar changed while reading")
		return
	}
	m, warnings, err := parseSidecar(data)
	if err != nil {
		warn(fmt.Errorf("%s: %w", sidecar, err))
		return
	}
	for _, warning := range warnings {
		warn(sidecar + ": " + warning)
	}
	m.Path = sidecar
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(result.Metadata, &metadata); err != nil || metadata == nil {
		warn("invalid inspection metadata")
		return
	}
	var annotations map[string]json.RawMessage
	if raw := metadata["_aarde"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &annotations); err != nil {
			warn(err)
			return
		}
	}
	if annotations == nil {
		annotations = map[string]json.RawMessage{}
	}
	annotations["xml_sidecar"], _ = json.Marshal(m)
	metadata["_aarde"], _ = json.Marshal(annotations)
	result.Metadata, _ = json.Marshal(metadata)
	if raw, ok := m.SourceFields[imagePath+"CLOUDCOVER"]; ok {
		OverrideCloudCover(result, m.CloudCover, "xml_sidecar/"+imagePath+"CLOUDCOVER", raw)
	}
	if m.AcquiredAt != nil {
		result.AcquiredAt = m.AcquiredAt
	}
}
