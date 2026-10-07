package raster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func viewerTestPlan(t *testing.T, path string, inspection Inspection) *ViewerPlan {
	t.Helper()
	p, err := BuildViewer(context.Background(), path, inspection.Checksum, inspection.Format, inspection.Segments, inspection.Metadata, "auto", DefaultViewerOptions())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func rendered(t *testing.T, p *ViewerPlan, id string) image.Image {
	t.Helper()
	data, kind, err := p.Render(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "image/png" {
		t.Fatal(kind)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func snapshot(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

// Independently project catalog lon/lat as OpenLayers does, then apply only the
// viewer's translation and uniform scale. The reference must never unrotate or
// independently stretch the axes of the source image.
func catalogViewerPoint(t *testing.T, p *ViewerPlan, lon, lat float64) [2]float64 {
	t.Helper()
	a := p.reference.Transform
	if len(a) != 6 || a[2] != 0 || a[4] != 0 || a[1] <= 0 || a[5] != -a[1] || p.reference.CRS.WKT != "EPSG:3857" {
		t.Fatalf("viewer must preserve catalog map shape: %+v", p.reference)
	}
	x := 6378137 * lon * math.Pi / 180
	y := 6378137 * math.Log(math.Tan(math.Pi/4+lat*math.Pi/360))
	return [2]float64{(x - a[0]) / a[1], (y - a[3]) / a[1]}
}

func assertViewerPoint(t *testing.T, got, want [2]float64) {
	t.Helper()
	if math.Hypot(got[0]-want[0], got[1]-want[1]) > 1e-6 {
		t.Fatalf("viewer point %v differs from catalog projection %v", got, want)
	}
}

func TestViewerMatchesCatalogFootprint(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, kind := range []string{"rotated", "projected", "gcp"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scene")
			switch kind {
			case "rotated":
				testutil.GDAL(t, "python3", "-c", `from osgeo import gdal, osr
import sys
d=gdal.GetDriverByName('GTiff').Create(sys.argv[1],16,8,1,gdal.GDT_Byte)
s=osr.SpatialReference();s.ImportFromEPSG(4326)
d.SetProjection(s.ExportToWkt());d.SetGeoTransform([-106,.05,.02,40,.01,-.08])
d.GetRasterBand(1).Fill(42);d=None`, path)
			case "projected":
				testutil.GDAL(t, "gdal_create", "-of", "GTiff", "-outsize", "16", "8", "-burn", "42", "-a_srs", "EPSG:32613", "-a_ullr", "300000", "4400000", "310000", "4390000", path)
			case "gcp":
				testutil.NITF(t, path, "IGEOLO=400000N1060000W400000N1050000W390000N1043000W390000N1060000W")
			}
			in, err := Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			p := viewerTestPlan(t, path, in)
			l := p.Manifest.Layers[0]
			if l.Registration != "Registered" {
				t.Fatalf("cataloged georeferencing ignored: %+v", l)
			}
			var polygons [][][][]float64
			if err := json.Unmarshal(in.Footprint.Coordinates, &polygons); err != nil {
				t.Fatal(err)
			}
			n := l.MeshSize
			// Walk the viewer's outer pixel edges in catalog perimeter order.
			var boundary [][2]float64
			for edge := range 4 {
				for k := 0; k < n; k++ {
					indexes := [4]int{k, k*(n+1) + n, n*(n+1) + n - k, (n - k) * (n + 1)}
					boundary = append(boundary, l.Mesh[indexes[edge]])
				}
			}
			ring := polygons[0][0]
			for _, coord := range ring[:len(ring)-1] {
				// GDAL affine extents can reverse winding and round lon/lat to
				// seven decimals; TPS has all 64 unrounded edge samples.
				want := catalogViewerPoint(t, p, coord[0], coord[1])
				distance := math.Inf(1)
				for _, got := range boundary {
					distance = math.Min(distance, math.Hypot(got[0]-want[0], got[1]-want[1]))
				}
				if distance > 1e-4 {
					t.Fatalf("catalog boundary %v is %g viewer units from image boundary", coord, distance)
				}
			}
			before, _ := json.Marshal(l.Mesh)
			p.Manifest.Resolution = "preview"
			if err := planViewerResolution(&p.Manifest, DefaultViewerOptions()); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(p.Manifest.Layers[0].Mesh)
			if !bytes.Equal(before, after) {
				t.Fatal("display downsampling changed map shape")
			}
			rendered(t, p, "segment-0")
		})
	}
}

func TestViewerNativeCloudGridAndShapes(t *testing.T) {
	testutil.RequireGDAL(t)
	path := filepath.Join(t.TempDir(), "native.ntf")
	testutil.ViewerNITF(t, path, "valid", true)
	before := snapshot(t, path)
	inspection, err := Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Segments) != 4 {
		t.Fatalf("segments: %d", len(inspection.Segments))
	}
	p := viewerTestPlan(t, path, inspection)
	if len(p.Manifest.Layers) != 5 {
		data, _ := os.ReadFile(path)
		t.Fatalf("layers: %d warnings: %v header: %q", len(p.Manifest.Layers), p.Manifest.Warnings, data[354:500])
	}
	for i, want := range []uint32{30, 110, 220} {
		im := rendered(t, p, p.Manifest.Layers[i].ID)
		r, g, b, a := im.At(0, 0).RGBA()
		if r != want*257 || g != r || b != r || a != 65535 {
			t.Fatalf("segment %d content: %d %d %d %d", i, r, g, b, a)
		}
	}
	cloud := p.Manifest.Layers[3]
	if cloud.Role != "cloud_grid" || cloud.Unsupported != "" || cloud.Registration != "Registered" {
		t.Fatalf("cloud: %+v", cloud)
	}
	assertNear := func(got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-6 {
			t.Fatalf("placement got %g want %g", got, want)
		}
	}
	bottom := catalogViewerPoint(t, p, -105, 39)
	middle := catalogViewerPoint(t, p, -105, 39.5)
	assertNear(p.Manifest.Layers[1].Extent[1], bottom[1])
	assertNear(p.Manifest.Layers[1].Extent[3], middle[1])
	assertNear(cloud.Extent[0], 0)
	assertNear(cloud.Extent[1], bottom[1])
	assertNear(cloud.Extent[2], 8)
	assertNear(cloud.Extent[3], 0)
	// A cloud cell at the attachment seam must follow the same nonlinear map
	// transformation as both imagery constituents.
	n := cloud.MeshSize
	assertViewerPoint(t, cloud.Mesh[(n/2)*(n+1)], p.Manifest.Layers[1].Mesh[0])
	first := p.Manifest.Layers[0]
	assertViewerPoint(t, cloud.Mesh[(n/2)*(n+1)], first.Mesh[first.MeshSize*(first.MeshSize+1)])
	im := rendered(t, p, cloud.ID)
	_, _, _, clear := im.At(0, 0).RGBA()
	r, g, b, a := im.At(1, 0).RGBA()
	if clear != 0 || r != 30*257 || g != 215*257 || b != 65535 || a != 65535 {
		t.Fatalf("cloud classes clear=%d cloud=%d/%d/%d/%d", clear, r, g, b, a)
	}
	r, g, b, a = im.At(0, 1).RGBA()
	if r != 65535 || g != 0 || b != 190*257 || a != 65535 {
		t.Fatal("reserved cloud value became probability")
	}
	shape := p.Manifest.Layers[4]
	if shape.Unsupported != "" {
		t.Fatalf("shape: %+v", shape)
	}
	if shape.Rendering != "outline" {
		t.Fatal("double cloud fill")
	}
	data, kind, err := p.Render(context.Background(), shape.ID)
	if err != nil || kind != "application/geo+json" {
		t.Fatalf("shape: %s %v", kind, err)
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Type        string
				Coordinates [][][][]float64
			}
		}
	}
	if err = json.Unmarshal(data, &fc); err != nil {
		t.Fatal(err)
	}
	if len(fc.Features) != 1 || fc.Features[0].Geometry.Type != "MultiPolygon" || len(fc.Features[0].Geometry.Coordinates) != 2 || len(fc.Features[0].Geometry.Coordinates[0]) != 2 {
		t.Fatalf("holes/multipart lost: %s", data)
	}
	assertNear(fc.Features[0].Geometry.Coordinates[0][0][0][0], 0)
	assertNear(fc.Features[0].Geometry.Coordinates[0][0][0][1], 0)
	shapeCorner := fc.Features[0].Geometry.Coordinates[0][0][2]
	assertViewerPoint(t, [2]float64{shapeCorner[0], shapeCorner[1]}, catalogViewerPoint(t, p, -105.5, 39.5))
	if snapshot(t, path) != before {
		t.Fatal("source modified")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*"))
	if len(files) != 1 {
		t.Fatalf("sidecars created: %v", files)
	}
}
func TestViewerTIFFAndSingleNITF(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, format := range []string{"GTiff", "NITF"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image")
			if format == "GTiff" {
				testutil.GeoTIFF(t, path)
			} else {
				testutil.NITF(t, path)
			}
			i, e := Inspect(context.Background(), path)
			if e != nil {
				t.Fatal(e)
			}
			p := viewerTestPlan(t, path, i)
			im := rendered(t, p, "segment-0")
			r, _, _, a := im.At(0, 0).RGBA()
			if r != 42*257 || a != 65535 {
				t.Fatal("wrong image content")
			}
			if p.Manifest.CloudStatus != "absent" || len(p.Manifest.Layers) != 1 {
				t.Fatal("fabricated cloud layer")
			}
		})
	}
}
func TestViewerMalformedCloudDoesNotHideImagery(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, meta := range []string{"", "malformed"} {
		path := filepath.Join(t.TempDir(), "bad.ntf")
		testutil.ViewerNITF(t, path, meta, false)
		p, e := BuildViewer(context.Background(), path, "", "NITF", make([]Segment, 4), nil, "auto", DefaultViewerOptions())
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Manifest.Layers) != 4 || p.Manifest.Layers[3].Unsupported == "" {
			t.Fatal("missing unsupported cloud state")
		}
		rendered(t, p, "segment-2")
	}
}
func TestViewerBudgetAndRevision(t *testing.T) {
	o := DefaultViewerOptions()
	m := ViewerManifest{Resolution: "auto", Layers: []ViewerLayer{{Width: 100_000, Height: 200_000}, {Width: 8000, Height: 8000}, {Width: 8, Height: 8}}}
	if err := planViewerResolution(&m, o); err != nil {
		t.Fatal(err)
	}
	if m.NativeAvailable || m.DisplayPixels > o.ScenePixels || m.Layers[2].DisplayWidth != 8 {
		t.Fatal("invalid resolution budget")
	}
	for _, l := range m.Layers {
		if l.DisplayWidth > o.MaxDimension || int64(l.DisplayWidth)*int64(l.DisplayHeight) > o.LayerPixels {
			t.Fatal("layer budget")
		}
	}
	m.Resolution = "native"
	if !errors.Is(planViewerResolution(&m, o), ErrViewerLimit) {
		t.Fatal("unsafe native accepted")
	}
	path := filepath.Join(t.TempDir(), "source.tif")
	os.WriteFile(path, []byte("before"), 0600)
	a, e := ViewerRevision(path, "sum", o)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(path, []byte("after!"), 0600)
	b, _ := ViewerRevision(path, "sum", o)
	if a == b {
		t.Fatal("source revision unchanged")
	}
	os.WriteFile(path+".aux.xml", []byte("sidecar"), 0600)
	c, _ := ViewerRevision(path, "sum", o)
	if b == c {
		t.Fatal("sidecar revision unchanged")
	}
	link := path + ".link"
	os.Symlink(path, link)
	if _, e = ViewerRevision(link, "", o); e == nil {
		t.Fatal("symlink accepted")
	}
	o.SourceRoots = []string{filepath.Join(t.TempDir(), "elsewhere")}
	if _, e = ViewerRevision(path, "", o); e == nil {
		t.Fatal("source root escaped")
	}
}
func TestViewerAttachmentCycleAndMagnification(t *testing.T) {
	makeLayer := func(d, a, row, mag string) ViewerLayer {
		h, _ := json.Marshal(map[string]string{"NITF_IDLVL": d, "NITF_IALVL": a, "NITF_ILOC_ROW": row, "NITF_ILOC_COLUMN": "0", "NITF_IMAG": mag})
		return ViewerLayer{raw: info{Metadata: map[string]json.RawMessage{"": h}}}
	}
	for _, ls := range [][]ViewerLayer{{makeLayer("51", "52", "0", "1"), makeLayer("52", "51", "4", "1")}, {makeLayer("51", "7", "0", "1")}, {makeLayer("51", "0", "0", "2")}, {makeLayer("51", "0", "0", "1"), makeLayer("51", "0", "4", "1")}} {
		for _, l := range imageLayouts(ls) {
			if l.valid {
				t.Fatal("invalid attachment accepted")
			}
		}
	}
	ls := imageLayouts([]ViewerLayer{makeLayer("51", "0", "0", "1"), makeLayer("52", "51", "4", "1"), makeLayer("53", "52", "8", "1")})
	if ls[2].root != 0 || ls[2].y != 12 {
		t.Fatal("display level confused with image index")
	}
}

func TestViewerHighBitNoDataAlphaPaletteAndRotation(t *testing.T) {
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	// Python is supplied with gdal-bin in the Debian integration image; it writes
	// tiny original sources, not part of the application runtime rendering path.
	source := filepath.Join(dir, "display.tif")
	testutil.GDAL(t, "python3", "-c", `from osgeo import gdal, osr
import struct,sys
p=sys.argv[1]
s=osr.SpatialReference();s.ImportFromEPSG(4326)
d=gdal.GetDriverByName('GTiff').Create(p,3,1,4,gdal.GDT_UInt16)
d.SetProjection(s.ExportToWkt());d.SetGeoTransform([-106,.1,.02,40,.01,-.1])
for i,c in enumerate([gdal.GCI_RedBand,gdal.GCI_GreenBand,gdal.GCI_BlueBand,gdal.GCI_AlphaBand]):
 b=d.GetRasterBand(i+1);b.SetColorInterpretation(c)
 b.WriteRaster(0,0,3,1,struct.pack('<HHH',*([0,32768,65535] if i<3 else [65535,32768,0])))
d=None
`, source)
	i, e := Inspect(context.Background(), source)
	if e != nil {
		t.Fatal(e)
	}
	p := viewerTestPlan(t, source, i)
	im := rendered(t, p, "segment-0")
	r, _, _, a := im.At(0, 0).RGBA()
	if r != 0 || a != 65535 {
		t.Fatal("valid black is transparent")
	}
	r, _, _, a = im.At(1, 0).RGBA()
	if a < 32000 || a > 34000 || r < 16000 || r > 17000 {
		t.Fatalf("high-bit/alpha scaling: %d %d", r, a)
	}
	_, _, _, a = im.At(2, 0).RGBA()
	if a != 0 {
		t.Fatal("alpha lost")
	}
	source2 := filepath.Join(dir, "nodata.tif")
	testutil.GDAL(t, "gdal_translate", "-q", "-b", "1", "-a_nodata", "65535", source, source2)
	i, e = Inspect(context.Background(), source2)
	if e != nil {
		t.Fatal(e)
	}
	p = viewerTestPlan(t, source2, i)
	im = rendered(t, p, "segment-0")
	r, _, _, a = im.At(0, 0).RGBA()
	if r != 0 || a != 65535 {
		t.Fatal("nodata made black transparent")
	}
	_, _, _, a = im.At(2, 0).RGBA()
	if a != 0 {
		t.Fatal("nodata lost")
	}
	palette := filepath.Join(dir, "palette.tif")
	testutil.GDAL(t, "python3", "-c", `from osgeo import gdal
import sys
d=gdal.GetDriverByName('GTiff').Create(sys.argv[1],2,1,1,gdal.GDT_Byte)
t=gdal.ColorTable();t.SetColorEntry(0,(0,0,0,255));t.SetColorEntry(1,(20,180,70,255))
b=d.GetRasterBand(1);b.SetRasterColorTable(t);b.SetRasterColorInterpretation(gdal.GCI_PaletteIndex);b.WriteRaster(0,0,2,1,bytes([0,1]));d=None`, palette)
	p, e = BuildViewer(context.Background(), palette, "", "GTiff", nil, nil, "auto", DefaultViewerOptions())
	if e != nil {
		t.Fatal(e)
	}
	im = rendered(t, p, "segment-0")
	r, g, b, a := im.At(1, 0).RGBA()
	if r != 20*257 || g != 180*257 || b != 70*257 || a != 65535 {
		t.Fatal("palette colors lost")
	}
	// The non-georeferenced single image uses its original pixel coordinates.
	if p.Manifest.Layers[0].Extent != [4]float64{0, -1, 2, 0} {
		t.Fatal("single-image pixel placement")
	}
}

func TestViewerGeographicTransformsRotationAndDisplayScale(t *testing.T) {
	testutil.RequireGDAL(t)
	ref := info{Transform: []float64{100, 2, 0, 200, 0, -2}}
	ref.CRS.WKT = "EPSG:3857"
	other := info{Transform: []float64{104, 1, .5, 198, .25, -1}}
	other.CRS.WKT = "EPSG:3857"
	p := &ViewerPlan{Manifest: ViewerManifest{Format: "NITF", Layers: []ViewerLayer{{raw: ref, Role: "imagery", Width: 8, Height: 8}, {raw: other, Role: "imagery", Width: 4, Height: 2}}}}
	if e := placeViewer(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	l := p.Manifest.Layers[1]
	want := [][2]float64{{2, -1}, {4, -.5}, {2.5, -2}, {4.5, -1.5}}
	for i, v := range want {
		if l.Mesh[i] != v {
			t.Fatalf("rotation/scale %v != %v", l.Mesh, want)
		}
	}
	before, _ := json.Marshal(l.Mesh)
	p.Manifest.Resolution = "preview"
	if e := planViewerResolution(&p.Manifest, DefaultViewerOptions()); e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(p.Manifest.Layers[1].Mesh)
	if !bytes.Equal(before, after) {
		t.Fatal("downsampling changed placement")
	}
	points, e := transformPoints(context.Background(), [][2]float64{{1, 1}}, "EPSG:4326", "EPSG:3857")
	if e != nil {
		t.Fatal(e)
	}
	if math.Abs(points[0][0]-111319.490793) > 1e-3 || math.Abs(points[0][1]-111325.142866) > 1e-3 {
		t.Fatal("CRS was assigned instead of transformed")
	}
}

func TestViewerCancellationAndTemporaryCleanup(t *testing.T) {
	testutil.RequireGDAL(t)
	path := filepath.Join(t.TempDir(), "source.tif")
	testutil.GeoTIFF(t, path)
	i, e := Inspect(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	p := viewerTestPlan(t, path, i)
	p.Options.TempDir = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e = p.Render(ctx, "segment-0"); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel: %v", e)
	}
	rendered(t, p, "segment-0")
	files, _ := os.ReadDir(p.Options.TempDir)
	if len(files) != 0 {
		t.Fatal("render temporary files leaked")
	}
}

func TestViewerNoCloudFromPercentageAndMaskImmutability(t *testing.T) {
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mask.tif")
	testutil.GDAL(t, "python3", "-c", `from osgeo import gdal,osr
import sys
d=gdal.GetDriverByName('GTiff').Create(sys.argv[1],2,1,1,gdal.GDT_Byte)
s=osr.SpatialReference();s.ImportFromEPSG(4326);d.SetProjection(s.ExportToWkt());d.SetGeoTransform([0,1,0,1,0,-1]);d.SetMetadataItem('CLOUD_COVER','75')
b=d.GetRasterBand(1);b.WriteRaster(0,0,2,1,bytes([0,70]));b.CreateMaskBand(gdal.GMF_PER_DATASET);b.GetMaskBand().WriteRaster(0,0,2,1,bytes([255,0]));d=None`, path)
	sources := map[string][32]byte{}
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		name := filepath.Join(dir, f.Name())
		sources[name] = snapshot(t, name)
	}
	in, e := Inspect(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	p := viewerTestPlan(t, path, in)
	if p.Manifest.CloudStatus != "absent" || len(p.Manifest.Layers) != 1 {
		t.Fatal("cloud-cover percentage fabricated geometry")
	}
	im := rendered(t, p, "segment-0")
	_, _, _, a := im.At(0, 0).RGBA()
	if a != 65535 {
		t.Fatal("valid masked black became transparent")
	}
	_, _, _, a = im.At(1, 0).RGBA()
	if a != 0 {
		t.Fatal("external validity mask lost")
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(sources) {
		t.Fatal("source sidecars created")
	}
	for name, hash := range sources {
		if snapshot(t, name) != hash {
			t.Fatal("source or sidecar modified", name)
		}
	}
}

func TestViewerCloudAuxiliaryIngestionIsRoleAware(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, metadata := range []string{"valid", "", "malformed"} {
		path := filepath.Join(t.TempDir(), "cloud.ntf")
		testutil.ViewerNITF(t, path, metadata, false)
		in, e := Inspect(context.Background(), path)
		if e != nil {
			t.Fatal(e)
		}
		if len(in.Segments) != 4 || in.Footprint.Type == "" {
			t.Fatal("primary catalog coverage lost")
		}
		if in.Segments[3].Width != 2 {
			t.Fatal("cloud source dimensions lost")
		}
	}
	// Existing primary validation must still reject a segment without georeferencing.
	path := filepath.Join(t.TempDir(), "primary.ntf")
	testutil.DiverseMultiNITF(t, path, true)
	if _, e := Inspect(context.Background(), path); e == nil {
		t.Fatal("primary georeferencing validation weakened")
	}
}
