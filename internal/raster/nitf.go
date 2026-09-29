package raster

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Optional GDAL domains can be objects, XML strings, arrays, or other JSON.
// Preserve their raw representation and extract only string-valued scalars.
func scalarMetadata(metadata map[string]json.RawMessage) map[string]map[string]string {
	result := make(map[string]map[string]string)
	for domain, value := range metadata {
		if domain == "_aarde" {
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil {
			continue
		}
		result[domain] = make(map[string]string)
		for key, raw := range object {
			var scalar string
			if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &scalar) == nil {
				result[domain][key] = scalar
			}
		}
	}
	return result
}

// GDAL's NITF driver enumerates *all* IM segments in SUBDATASETS when the
// physical container has >1 image. For one image it omits that domain, but
// exposes image-header fields; for zero images it exposes only file headers.
// This contract is verified against real GDAL output in integration tests.
// Band count is never used to establish the image-segment count.
func validateSingleNITF(raw info) error {
	unknown := errors.New("cannot reliably establish NITF image-segment count from GDAL container metadata; use a supported GDAL NITF driver and a complete, valid file")
	if domain, ok := raw.Metadata["SUBDATASETS"]; ok {
		var sub map[string]string
		if json.Unmarshal(domain, &sub) != nil || sub == nil {
			return unknown
		}
		if len(sub) > 0 {
			if len(sub)%2 != 0 {
				return unknown
			}
			count := len(sub) / 2
			for n := 1; n <= count; n++ {
				name := sub[fmt.Sprintf("SUBDATASET_%d_NAME", n)]
				desc := sub[fmt.Sprintf("SUBDATASET_%d_DESC", n)]
				if !strings.HasPrefix(name, fmt.Sprintf("NITF_IM:%d:", n-1)) || desc == "" {
					return unknown
				}
			}
			if count > 1 {
				return fmt.Errorf("unsupported NITF: found %d image segments; only single-image NITF files are supported", count)
			}
			// A singleton list is not the supported driver's container contract.
			return unknown
		}
	}
	header := scalarMetadata(raw.Metadata)[""]
	switch header["NITF_FHDR"] {
	case "NITF02.10", "NITF02.00", "NITF01.10", "NSIF01.00":
	default:
		return unknown
	}
	_, id := header["NITF_IID1"]
	_, compression := header["NITF_IC"]
	_, representation := header["NITF_IREP"]
	if !id && !compression && !representation {
		return errors.New("unsupported NITF: no image segments found in GDAL container metadata")
	}
	if !id || header["NITF_IC"] == "" || header["NITF_IREP"] == "" {
		return unknown
	}
	return nil
}

func nitfAcquisitionTime(header map[string]string) *time.Time {
	// Only the documented NITF 2.1 CCYYMMDDhhmmss UTC encoding is accepted.
	// Do not guess a century/timezone for legacy values or use NITF_FDT.
	if header["NITF_FHDR"] != "NITF02.10" {
		return nil
	}
	raw := header["NITF_IDATIM"]
	if len(raw) != 14 {
		return nil
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return nil
		}
	}
	year, _ := strconv.Atoi(raw[:4])
	if year == 0 {
		return nil
	}
	value, err := time.Parse("20060102150405", raw)
	if err != nil || value.Format("20060102150405") != raw {
		return nil
	}
	return &value
}
