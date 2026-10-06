package raster

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type displayBand struct {
	Band       int                          `json:"band"`
	Type       string                       `json:"type"`
	Color      string                       `json:"colorInterpretation"`
	Metadata   map[string]map[string]string `json:"metadata"`
	ColorTable *struct {
		Entries [][]int `json:"entries"`
	} `json:"colorTable"`
}

func bandRange(b displayBand) (float64, float64, error) {
	lo, hi := 0.0, 255.0
	switch b.Type {
	case "Byte":
	case "UInt16":
		hi = 65535
	case "Int16":
		lo, hi = -32768, 32767
	case "UInt32":
		hi = 4294967295
	case "Int32":
		lo, hi = -2147483648, 2147483647
	default:
		return 0, 0, errors.New("Unsupported sample type; viewer supports Byte and 16/32-bit integer imagery")
	}
	if b.Type == "Byte" || b.Type == "UInt16" || b.Type == "UInt32" {
		if bits, e := strconv.Atoi(b.Metadata["IMAGE_STRUCTURE"]["NBITS"]); e == nil && bits > 0 && bits <= 32 {
			hi = math.Pow(2, float64(bits)) - 1
		}
	}
	return lo, hi, nil
}
func displayBands(raw info) ([]displayBand, []int, int, error) {
	bands := make([]displayBand, len(raw.Bands))
	rgb := map[string]int{}
	alpha := -1
	for i, b := range raw.Bands {
		if err := json.Unmarshal(b, &bands[i]); err != nil {
			return nil, nil, 0, err
		}
		if bands[i].Band != i+1 {
			return nil, nil, 0, errors.New("invalid band numbering")
		}
		rgb[bands[i].Color] = i
		if bands[i].Color == "Alpha" {
			alpha = i
		}
	}
	selected := []int{0}
	r, rok := rgb["Red"]
	g, gok := rgb["Green"]
	b, bok := rgb["Blue"]
	if rok && gok && bok {
		selected = []int{r, g, b}
	}
	for _, i := range selected {
		if _, _, err := bandRange(bands[i]); err != nil {
			return nil, nil, 0, err
		}
	}
	return bands, selected, alpha, nil
}

func (p *ViewerPlan) Render(ctx context.Context, id string) ([]byte, string, error) {
	var l *ViewerLayer
	for i := range p.Manifest.Layers {
		if p.Manifest.Layers[i].ID == id {
			l = &p.Manifest.Layers[i]
			break
		}
	}
	if l == nil {
		return nil, "", errors.New("unknown viewer layer")
	}
	if l.Unsupported != "" {
		return nil, "", errors.New(l.Unsupported)
	}
	if l.Role == "cloud_shapes" {
		return l.geometry, "application/geo+json", nil
	}
	bands, selected, alpha, err := displayBands(l.raw)
	if err != nil {
		return nil, "", err
	}
	cloud := l.Role == "cloud_grid"
	if cloud && (len(bands) != 1 || bands[0].Type != "Byte") {
		return nil, "", errors.New("Unsupported cloud encoding: NCDRD 2010 requires one Byte band")
	}
	dir, err := os.MkdirTemp(p.Options.TempDir, "aarde-viewer-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "display.bin")
	// This scratch pixel buffer has no geographic meaning. Discard source GCPs
	// and use a dummy north-up affine so ENVI accepts rotated/sheared sources.
	// The original transform remains exclusively in the immutable placement mesh.
	args := []string{"-q", "-nogcp", "-a_srs", "EPSG:3857", "-a_ullr", "0", strconv.Itoa(l.DisplayHeight), strconv.Itoa(l.DisplayWidth), "0", "-of", "ENVI", "-co", "INTERLEAVE=BSQ", "-ot", "Float32", "-outsize", strconv.Itoa(l.DisplayWidth), strconv.Itoa(l.DisplayHeight), "-r", "nearest", "-mask", "none"}
	for _, i := range selected {
		args = append(args, "-b", strconv.Itoa(i+1))
	}
	alphaPlane := -1
	planes := len(selected)
	if alpha >= 0 {
		alphaPlane = planes
		planes++
		args = append(args, "-b", strconv.Itoa(alpha+1))
	}
	// Keep validity separate from sample scaling, so valid zero/black stays opaque.
	for _, i := range selected {
		args = append(args, "-b", "mask,"+strconv.Itoa(i+1))
		planes++
	}
	args = append(args, l.selector, dest)
	if _, err = runGDAL(ctx, "gdal_translate", nil, 4096, args...); err != nil {
		return nil, "", err
	}
	count := l.DisplayWidth * l.DisplayHeight
	st, err := os.Stat(dest)
	if err != nil {
		return nil, "", err
	}
	if st.Size() != int64(count)*int64(planes)*4 {
		return nil, "", errors.New("unexpected bounded render dimensions")
	}
	raw, err := os.ReadFile(dest)
	if err != nil {
		return nil, "", err
	}
	header, err := os.ReadFile(filepath.Join(dir, "display.hdr"))
	if err != nil {
		return nil, "", err
	}
	var endian binary.ByteOrder = binary.LittleEndian
	if strings.Contains(string(header), "byte order = 1") {
		endian = binary.BigEndian
	}
	sample := func(plane, pixel int) float64 {
		offset := (plane*count + pixel) * 4
		return float64(math.Float32frombits(endian.Uint32(raw[offset : offset+4])))
	}
	out := image.NewNRGBA(image.Rect(0, 0, l.DisplayWidth, l.DisplayHeight))
	scale := func(value, lo, hi float64) uint8 {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0
		}
		return uint8(math.Round(math.Max(0, math.Min(255, (value-lo)*255/(hi-lo)))))
	}
	ranges := make([][2]float64, len(selected))
	for j, i := range selected {
		lo, hi, _ := bandRange(bands[i])
		ranges[j] = [2]float64{lo, hi}
	}
	for pixel := 0; pixel < count; pixel++ {
		if pixel%65536 == 0 && ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		c := color.NRGBA{A: 255}
		if cloud {
			switch sample(0, pixel) {
			case 0:
				c = color.NRGBA{}
			case 255:
				c = color.NRGBA{R: 30, G: 215, B: 255, A: 255}
			default:
				c = color.NRGBA{R: 255, G: 0, B: 190, A: 255}
			}
		} else if palette := bands[selected[0]].ColorTable; palette != nil {
			v := int(sample(0, pixel))
			if v < 0 || v >= len(palette.Entries) || len(palette.Entries[v]) != 4 {
				return nil, "", errors.New("invalid palette entry")
			}
			e := palette.Entries[v]
			c = color.NRGBA{uint8(e[0]), uint8(e[1]), uint8(e[2]), uint8(e[3])}
		} else {
			c.R = scale(sample(0, pixel), ranges[0][0], ranges[0][1])
			c.G, c.B = c.R, c.R
			if len(selected) == 3 {
				c.G = scale(sample(1, pixel), ranges[1][0], ranges[1][1])
				c.B = scale(sample(2, pixel), ranges[2][0], ranges[2][1])
			}
		}
		if alphaPlane >= 0 {
			lo, hi, e := bandRange(bands[alpha])
			if e != nil {
				return nil, "", e
			}
			c.A = uint8(uint16(c.A) * uint16(scale(sample(alphaPlane, pixel), lo, hi)) / 255)
		}
		// Cloud zero is intrinsically clear; other categorical values remain explicit.
		for plane := planes - len(selected); plane < planes; plane++ {
			if sample(plane, pixel) == 0 {
				c.A = 0
			}
		}
		off := pixel * 4
		out.Pix[off], out.Pix[off+1], out.Pix[off+2], out.Pix[off+3] = c.R, c.G, c.B, c.A
	}
	var result bytes.Buffer
	if err = png.Encode(&result, out); err != nil {
		return nil, "", fmt.Errorf("encode display: %w", err)
	}
	return result.Bytes(), "image/png", nil
}
