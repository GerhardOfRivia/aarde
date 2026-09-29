package main

import (
	"context"
	"flag"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestInterspersedFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	recursive := fs.Bool("recursive", false, "")
	cat := fs.String("catalog", "default", "")
	if err := parse(fs, []string{"./imagery", "--recursive", "--catalog", "breckenridge"}); err != nil {
		t.Fatal(err)
	}
	if !*recursive || *cat != "breckenridge" || fs.NArg() != 1 || fs.Arg(0) != "./imagery" {
		t.Fatal("flags after path were not parsed")
	}
}
func TestVersionWithoutDatabase(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	var out strings.Builder
	if err := run(context.Background(), []string{"version"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "aarde") {
		t.Fatal("missing version")
	}
}

func TestInvalidCloudCoverFlag(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	for _, value := range []string{"-1", "101", "NaN", "+Inf", "-Inf", "bad", ""} {
		var out strings.Builder
		err := run(context.Background(), []string{"import", "missing.tif", "--dry-run", "--cloud-cover=" + value}, &out)
		if err == nil || !strings.Contains(err.Error(), "cloud cover") {
			t.Errorf("value %q: %v", value, err)
		}
	}
}

func TestSearchCloudCoverFlag(t *testing.T) {
	for _, raw := range []string{"", "0", "19.9", "20", "100"} {
		args := []string{"--id", "one,two", "--catalog", "example", "--limit", "1", "--offset", "2"}
		if raw != "" {
			args = append(args, "--cloud-cover-lt", raw)
		}
		q, err := parseSearchQuery(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if raw == "" {
			if q.CloudCoverLT != nil {
				t.Fatal("omission became zero")
			}
		} else {
			want, _ := strconv.ParseFloat(raw, 64)
			if q.CloudCoverLT == nil || *q.CloudCoverLT != want {
				t.Fatalf("threshold %q lost: %+v", raw, q)
			}
		}
		if q.CatalogID != "example" || strings.Join(q.ImageIDs, ",") != "one,two" || q.Limit != 1 || q.Offset != 2 {
			t.Fatalf("other filters lost: %+v", q)
		}
	}
	q, err := parseSearchQuery([]string{"--id=one", "--cloud-cover-lt=0"}, io.Discard)
	if err != nil || q.CloudCoverLT == nil || *q.CloudCoverLT != 0 {
		t.Fatalf("explicit zero lost: %+v %v", q, err)
	}
}

func TestSearchInvalidCloudCoverFlag(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	for _, raw := range []string{"", "-1", "100.01", "NaN", "+Inf", "-Inf", "1e400", "bad", "20%"} {
		err := run(context.Background(), []string{"search", "--id=one", "--cloud-cover-lt=" + raw}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "cloud_cover_lt") {
			t.Errorf("value %q: %v", raw, err)
		}
	}
	if _, err := parseSearchQuery([]string{"--id=one", "--cloud-cover-lt"}, io.Discard); err == nil {
		t.Fatal("accepted missing flag value")
	}
}

func TestSearchCloudCoverHelp(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	var out strings.Builder
	if err := run(context.Background(), []string{"search", "--help"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"cloud-cover-lt", "strictly less", "0–100", "unknown", "scene"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("help lacks %q: %s", expected, out.String())
		}
	}
}
