package main

import (
	"context"
	"flag"
	"io"
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
