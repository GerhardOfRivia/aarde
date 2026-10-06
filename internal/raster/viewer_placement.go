package raster

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type imageLayout struct {
	root   int
	x, y   float64
	valid  bool
	reason string
}

// Attachment levels are references to IDLVL, never array indexes. Only unit
// IMAG is supported (the NCDRD 2010 profile); other magnifications are explicit.
func imageLayouts(layers []ViewerLayer) []imageLayout {
	out := make([]imageLayout, len(layers))
	levels := map[int]int{}
	duplicate := map[int]bool{}
	for i, l := range layers {
		h := scalarMetadata(l.raw.Metadata)[""]
		v, ok := headerInt(h, "NITF_IDLVL")
		if ok {
			if _, yes := levels[v]; yes {
				duplicate[v] = true
			}
			levels[v] = i
		}
	}
	states := make([]int, len(layers))
	var visit func(int) imageLayout
	visit = func(i int) imageLayout {
		if states[i] == 2 {
			return out[i]
		}
		if states[i] == 1 {
			return imageLayout{reason: "Attachment cycle"}
		}
		states[i] = 1
		h := scalarMetadata(layers[i].raw.Metadata)[""]
		d, dok := headerInt(h, "NITF_IDLVL")
		a, aok := headerInt(h, "NITF_IALVL")
		x, xok := headerInt(h, "NITF_ILOC_COLUMN")
		y, yok := headerInt(h, "NITF_ILOC_ROW")
		r := imageLayout{root: i, reason: "No usable attachment layout"}
		if dok && aok && xok && yok && !duplicate[d] && d > 0 {
			mag, e := strconv.ParseFloat(strings.TrimSpace(h["NITF_IMAG"]), 64)
			if e != nil || mag != 1 {
				r.reason = "Unsupported IMAG (only unit magnification is supported)"
			} else if a == 0 {
				r = imageLayout{i, float64(x), float64(y), true, ""}
			} else if parent, ok := levels[a]; ok && !duplicate[a] {
				p := visit(parent)
				if p.valid {
					r = imageLayout{p.root, p.x + float64(x), p.y + float64(y), true, ""}
				} else {
					r.reason = p.reason
				}
			} else {
				r.reason = "Broken attachment reference"
			}
		}
		states[i] = 2
		out[i] = r
		return r
	}
	for i := range layers {
		visit(i)
	}
	return out
}

func affinePoint(t []float64, x, y float64) [2]float64 {
	return [2]float64{t[0] + x*t[1] + y*t[2], t[3] + x*t[4] + y*t[5]}
}
func viewerPoint(ref info, p [2]float64) [2]float64 {
	t := ref.Transform
	d := t[1]*t[5] - t[2]*t[4]
	x, y := p[0]-t[0], p[1]-t[3]
	return [2]float64{(t[5]*x - t[2]*y) / d, -(-t[4]*x + t[1]*y) / d}
}
func transformPoints(ctx context.Context, points [][2]float64, from, to string) ([][2]float64, error) {
	if from == to {
		return points, nil
	}
	var input strings.Builder
	for _, p := range points {
		fmt.Fprintf(&input, "%.15g %.15g\n", p[0], p[1])
	}
	data, err := runGDAL(ctx, "gdaltransform", strings.NewReader(input.String()), max(4096, len(points)*100), "-s_srs", from, "-t_srs", to)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(points) {
		return nil, errors.New("coordinate transformation failed")
	}
	result := make([][2]float64, len(points))
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, errors.New("coordinate transformation failed")
		}
		for j := 0; j < 2; j++ {
			v, e := strconv.ParseFloat(fields[j], 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, errors.New("invalid transformed coordinate")
			}
			result[i][j] = v
		}
	}
	return result, nil
}
func layerExtent(l *ViewerLayer) {
	l.Extent = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range l.Mesh {
		l.Extent[0] = math.Min(l.Extent[0], p[0])
		l.Extent[1] = math.Min(l.Extent[1], p[1])
		l.Extent[2] = math.Max(l.Extent[2], p[0])
		l.Extent[3] = math.Max(l.Extent[3], p[1])
	}
}
func placeViewer(ctx context.Context, p *ViewerPlan) error {
	layers := p.Manifest.Layers
	layouts := imageLayouts(layers)
	refIndex := -1
	for i, l := range layers {
		if l.Role == "imagery" && validAffine(l.raw) {
			refIndex = i
			break
		}
	}
	if refIndex >= 0 {
		p.reference = layers[refIndex].raw
	}
	// A common reference-pixel plane avoids huge geographic coordinates and lets
	// unregistered components remain inspectable, separated from registered data.
	nextX := 0.0
	groups := map[int]float64{}
	groupWidths := map[int]float64{}
	for i, l := range layers {
		group, x := i, 0.0
		if layouts[i].valid {
			group = layouts[i].root
			x = layouts[i].x - layouts[group].x
		}
		groupWidths[group] = math.Max(groupWidths[group], x+float64(max(l.Width, 1)))
	}
	for i := range layers {
		l := &layers[i]
		if l.Role == "cloud_grid" {
			continue
		}
		src := l.raw
		layout := layouts[i]
		x, y := 0.0, 0.0
		if layout.valid && layout.root != i {
			src = layers[layout.root].raw
			x = layout.x - layouts[layout.root].x
			y = layout.y - layouts[layout.root].y
			l.Placement = "NITF IDLVL/IALVL/ILOC unit-magnification synthetic image"
		} else {
			l.Placement = "Segment affine geotransform"
		}
		n := 1
		if validAffine(src) && validAffine(p.reference) && src.CRS.WKT != p.reference.CRS.WKT {
			n = 16
			l.Placement += "; GDAL CRS transformation approximated by a 16 × 16 display mesh"
		}
		var points [][2]float64
		for row := 0; row <= n; row++ {
			for col := 0; col <= n; col++ {
				px, py := x+float64(col)*float64(max(l.Width, 1))/float64(n), y+float64(row)*float64(max(l.Height, 1))/float64(n)
				if validAffine(src) && validAffine(p.reference) {
					points = append(points, affinePoint(src.Transform, px, py))
				} else {
					points = append(points, [2]float64{px, -py})
				}
			}
		}
		registered := validAffine(src) && validAffine(p.reference)
		if registered {
			var err error
			points, err = transformPoints(ctx, points, src.CRS.WKT, p.reference.CRS.WKT)
			if err != nil {
				registered = false
				l.Warnings = append(l.Warnings, "Geographic transformation failed")
			} else {
				for j := range points {
					points[j] = viewerPoint(p.reference, points[j])
				}
			}
		}
		if registered {
			l.Registration = "Registered"
		} else {
			group := i
			if layout.valid {
				group = layout.root
			}
			offset, ok := groups[group]
			if !ok {
				offset = nextX
				groups[group] = offset
				nextX += groupWidths[group] + math.Max(groupWidths[group]*.1, 32)
			}
			points = nil
			n = 1
			for row := 0; row <= 1; row++ {
				for col := 0; col <= 1; col++ {
					points = append(points, [2]float64{offset + x + float64(col*max(l.Width, 1)), -y - float64(row*max(l.Height, 1))})
				}
			}
			l.Registration = "Unregistered"
			l.Placement = "Separated image-coordinate layout"
			if layout.valid && layout.root != i {
				l.Registration = "Registered within synthetic image"
				l.Placement = "NITF attachment layout; group not geographically registered"
			} else if len(layers) == 1 {
				l.Registration = "Image coordinates"
			}
			if !layout.valid && p.Manifest.Format == "NITF" {
				l.Warnings = append(l.Warnings, layout.reason)
			}
		}
		l.Mesh = points
		l.MeshSize = n
		layerExtent(l)
		nextX = math.Max(nextX, l.Extent[2]+float64(max(l.Width, 256))*.1)
	}
	// Move unregistered roots clear of geographic coverage, retaining attachments.
	maxRegistered := 0.0
	for _, l := range layers {
		if l.Registration == "Registered" {
			maxRegistered = math.Max(maxRegistered, l.Extent[2])
		}
	}
	if refIndex >= 0 {
		for i := range layers {
			l := &layers[i]
			if l.Role != "cloud_grid" && l.Registration != "Registered" {
				for j := range l.Mesh {
					l.Mesh[j][0] += maxRegistered + 256
				}
				layerExtent(l)
			}
		}
	}
	for i := range layers {
		l := &layers[i]
		if l.Role != "cloud_grid" {
			continue
		}
		if err := placeCloud(l, i, layers, layouts); err != nil {
			l.Registration = "Cloud registration unavailable"
			l.Unsupported = err.Error()
			l.Placement = "Unregistered cloud data; no overlay fabricated"
			l.MeshSize = 1
			l.Mesh = [][2]float64{{nextX, 0}, {nextX + float64(max(l.Width, 1)), 0}, {nextX, -float64(max(l.Height, 1))}, {nextX + float64(max(l.Width, 1)), -float64(max(l.Height, 1))}}
			layerExtent(l)
			p.Manifest.CloudStatus = "registration_unavailable"
		}
	}
	p.Manifest.Layers = layers
	p.Manifest.Extent = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, l := range layers {
		if l.Role == "cloud_grid" && l.Unsupported != "" {
			continue
		}
		p.Manifest.Extent[0] = math.Min(p.Manifest.Extent[0], l.Extent[0])
		p.Manifest.Extent[1] = math.Min(p.Manifest.Extent[1], l.Extent[1])
		p.Manifest.Extent[2] = math.Max(p.Manifest.Extent[2], l.Extent[2])
		p.Manifest.Extent[3] = math.Max(p.Manifest.Extent[3], l.Extent[3])
	}
	if math.IsInf(p.Manifest.Extent[0], 0) {
		p.Manifest.Extent = [4]float64{0, -1, 1, 0}
	}
	return nil
}

func cloudGridRecord(raw info, index int) (treRecord, error) {
	if strings.TrimSpace(scalarMetadata(raw.Metadata)[""]["NITF_ICAT"]) != "CLOUD" {
		return treRecord{}, errors.New("not a cloud grid")
	}
	records, diagnostics := collectTREs(raw.Metadata, &index)
	var found []treRecord
	for _, d := range diagnostics {
		if d.Code == "tre_representation_conflict" {
			return treRecord{}, errors.New("conflicting CSCCGA metadata")
		}
	}
	for _, r := range records {
		if r.Tag == "CSCCGA" && r.Storage == "image_segment" {
			found = append(found, r)
		}
	}
	if len(found) != 1 || !validCloudGrid(found[0]) {
		return treRecord{}, errors.New("Missing, malformed or unsupported image-scoped CSCCGA (NCDRD 2010)")
	}
	r := found[0]
	rows, _ := strconv.Atoi(strings.TrimSpace(r.Fields["CCG_MAX_LINE"]))
	cols, _ := strconv.Atoi(strings.TrimSpace(r.Fields["CCG_MAX_SAMPLE"]))
	if len(raw.Size) != 2 || rows != raw.Size[1] || cols != raw.Size[0] {
		return r, errors.New("CSCCGA dimensions differ from cloud raster")
	}
	return r, nil
}
func placeCloud(l *ViewerLayer, index int, layers []ViewerLayer, layouts []imageLayout) error {
	r, err := cloudGridRecord(l.raw, index)
	if err != nil {
		return err
	}
	sensor := strings.TrimSpace(r.Fields["REG_SENSOR"])
	ref := -1
	for i, c := range layers {
		if c.Role != "imagery" || !layouts[i].valid || layouts[i].root != i {
			continue
		}
		h := scalarMetadata(c.raw.Metadata)[""]
		cat := strings.TrimSpace(h["NITF_ICAT"])
		iid := strings.TrimSpace(h["NITF_IID1"])
		match := cat == sensor || (len(iid) > 1 && iid[1] >= '1' && iid[1] <= '9' && ((sensor == "PAN" && iid[0] == 'P') || (sensor == "MS" && iid[0] == 'M')))
		if match {
			if ref >= 0 {
				return errors.New("CSCCGA sensor resolves to multiple synthetic images")
			}
			ref = i
		}
	}
	if ref < 0 {
		return errors.New("CSCCGA reference synthetic image unavailable")
	}
	// Reject broken constituent chains for this sensor, even if the first image opens.
	for i, c := range layers {
		h := scalarMetadata(c.raw.Metadata)[""]
		if strings.TrimSpace(h["NITF_ICAT"]) == sensor && !layouts[i].valid {
			return errors.New("Invalid reference-image attachment chain")
		}
	}
	root := layers[ref]
	if root.MeshSize != 1 {
		return errors.New("Cloud grid registration to nonlinear reference transform unsupported")
	}
	number := func(k string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(r.Fields[k]), 64); return v }
	x, y := number("ORIGIN_SAMPLE")-1, number("ORIGIN_LINE")-1
	cw, ch := number("CS_CELL_SIZE"), number("AS_CELL_SIZE")
	a, b, c := root.Mesh[0], root.Mesh[1], root.Mesh[2]
	place := func(x, y float64) [2]float64 {
		return [2]float64{a[0] + (b[0]-a[0])*x/float64(root.Width) + (c[0]-a[0])*y/float64(root.Height), a[1] + (b[1]-a[1])*x/float64(root.Width) + (c[1]-a[1])*y/float64(root.Height)}
	}
	l.Mesh = [][2]float64{place(x, y), place(x+float64(l.Width)*cw, y), place(x, y+float64(l.Height)*ch), place(x+float64(l.Width)*cw, y+float64(l.Height)*ch)}
	l.MeshSize = 1
	l.Registration = "Registered"
	l.Placement = fmt.Sprintf("CSCCGA image segment %d → %s synthetic image rooted at segment %d; one-based origin; cell %.0f × %.0f pixels", index, sensor, ref, cw, ch)
	if root.Registration != "Registered" {
		l.Registration = "Registered within synthetic image"
	}
	l.Warnings = append(l.Warnings, "Registration source: "+r.Sources[0].Path+" ("+r.Storage+")")
	layerExtent(l)
	return nil
}
