package testutil

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// CommercialNITF generates synthetic, georeferenced parser fixtures, not
// certified NCDRD products. Each cloud string is an independently stored PIAIMC.
// Layouts: GDAL 3.6.2 nitf_spec.xml and STDI-0006 18 February 2010.
func CommercialNITF(t *testing.T, path string, clouds []string, auxiliary bool) {
	t.Helper()
	csdida := "10SEP2026WV0201001AA01000020260910120000202609111300000001NNtest      "
	if len(csdida) != 70 {
		t.Fatal("fixture CSDIDA width", len(csdida))
	}
	csexra := fmt.Sprintf("%-6s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s%s", "PAN", "43200.000000", "-0001.000000", "020.0", "020.0", "021.0", "020.5", "019.0", "019.0", "019.0", "090.0", "02047", "0000101", "00101", "000.000", "00.000", "000.000", "0", "0", "180.000", "+45.000", "5.0", "001", "001")
	if len(csexra) != 132 {
		t.Fatal("fixture CSEXRA width", len(csexra))
	}
	corners := "Y" + strings.Repeat("+40.00000-106.00000+00000.0", 4)
	csccga := fmt.Sprintf("%-18s%-6s%s%s%s%s%s%s", "PAN", "PAN", "0000001", "00001", "0000010", "00010", "0000002", "00002")
	source := filepath.Join(t.TempDir(), "source.tif")
	GeoTIFF(t, source)
	for index, cloud := range clouds {
		piaimc := fmt.Sprintf("%sN%-12s%-18s%-255s00G%-7s%-32s001N00%sWGEWGE00GE%-8s", cloud, "PUSHBROOM", "SYNTHETIC", "test fixture", "0000001", "camera", "00020.0", "00000000")
		if len(piaimc) != 362 {
			t.Fatal("fixture PIAIMC width", len(piaimc))
		}
		args := []string{"-q", "-of", "NITF", "-co", "ICORDS=G", "-co", "IDATIM=20260910120000", "-co", "IID1=P" + fmt.Sprint(index+1), "-co", "TRE=PIAIMC=" + piaimc}
		if index == 0 {
			args = append(args, "-co", fmt.Sprintf("NUMI=%d", len(clouds)), "-co", "FILE_TRE=CSDIDA="+csdida)
		} else {
			args = append(args, "-co", "APPEND_SUBDATASET=YES")
		}
		if auxiliary && index == len(clouds)-1 {
			args = append(args, "-co", "IID1=CC", "-co", "ICAT=CLOUD", "-co", "TRE=CSCCGA="+csccga)
		} else {
			args = append(args, "-co", "TRE=CSEXRA="+csexra, "-co", "TRE=CSCRNA="+corners, "-co", "TRE=CSPROA="+strings.Repeat(" ", 120))
		}
		GDAL(t, "gdal_translate", append(args, source, path)...)
	}
}
