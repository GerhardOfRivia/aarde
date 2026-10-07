package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
)

func TestRemoveOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want removeOptions
	}{
		{[]string{"-image", "xyz"}, removeOptions{"xyz", "default"}},
		{[]string{"--image=xyz"}, removeOptions{"xyz", "default"}},
		{[]string{"-catalog", "project-123"}, removeOptions{"", "project-123"}},
		{[]string{"--catalog=project-123"}, removeOptions{"", "project-123"}},
		{[]string{"-catalog", "default"}, removeOptions{"", "default"}},
	} {
		got, err := parseRemoveOptions(tc.args, io.Discard)
		if err != nil || got != tc.want {
			t.Errorf("%v: got %+v, %v; want %+v", tc.args, got, err, tc.want)
		}
	}
}

func TestRemoveMutuallyExclusiveOptionsBeforeDatabase(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	for _, args := range [][]string{
		{"-image", "xyz", "-catalog", "project-123"},
		{"-catalog", "project-123", "-image", "xyz"},
		{"--image=xyz", "--catalog=default"},
		{"--catalog=project-123", "--image=xyz"},
		{"-image=", "-catalog=project-123"},
		{"-image=xyz", "-catalog="},
		{"-image=", "-catalog="},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out strings.Builder
			err := execute(context.Background(), append([]string{"remove"}, args...), brokenConfirmation{}, &out, io.Discard, "dev")
			if err == nil || !strings.Contains(err.Error(), "-image and -catalog are mutually exclusive") || !strings.Contains(err.Error(), removeUsage) {
				t.Fatalf("expected mutually exclusive flags error before opening the database: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("unexpected prompt or removal output: %s", out.String())
			}
		})
	}
}

func TestRemoveInvalidOptionsBeforeDatabase(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	for _, args := range [][]string{
		{}, {"xyz"}, {"-image"}, {"-catalog"}, {"-image="}, {"-catalog="},
		{"-catalog=project-123", "-image"}, {"-image=xyz", "-catalog"},
		{"-image=xyz", "extra"}, {"-catalog=project-123", "extra"},
		{"-image= xyz"}, {"-catalog=bad/catalog"}, {"-image=bad\nname"},
		{"-catalog=" + strings.Repeat("x", 256)}, {"-catalog=project-123", "-yes"},
	} {
		err := execute(context.Background(), append([]string{"remove"}, args...), strings.NewReader("yes\n"), io.Discard, io.Discard, "dev")
		if err == nil || strings.Contains(err.Error(), "AARDE_DATABASE_URL") {
			t.Errorf("%v should fail validation before opening the database: %v", args, err)
		}
	}
}

func TestRemoveHelpAndDatabaseRequirement(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	for _, args := range [][]string{{"--help"}, {"remove", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out strings.Builder
			if err := runCommand(context.Background(), args, &out); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"-image", "-catalog", "default catalog", "confirmation", "source imagery files are preserved", "exactly one", "mutually exclusive"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help lacks %q: %s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "[-catalog default]") {
				t.Errorf("help advertises combined remove flags: %s", out.String())
			}
		})
	}
	if err := runCommand(context.Background(), []string{"remove", "-image=xyz"}, io.Discard); err == nil || err.Error() != "AARDE_DATABASE_URL is required" {
		t.Fatalf("missing database configuration: %v", err)
	}
}

type removalStub struct {
	imageCalls, catalogCalls int
	err                      error
}

func (s *removalStub) RemoveImage(context.Context, string, string) error {
	s.imageCalls++
	return s.err
}

func (s *removalStub) RemoveCatalog(context.Context, string) (int64, error) {
	s.catalogCalls++
	return 2, s.err
}

func TestCatalogRemovalConfirmation(t *testing.T) {
	for _, tc := range []struct {
		input string
		calls int
	}{
		{"yes\n", 1}, {"yes", 1}, {" yes \r\n", 1},
		{"", 0}, {"\n", 0}, {"no\n", 0}, {"y\n", 0}, {"YES\n", 0},
		{"yesterday\n", 0}, {"no\nyes\n", 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			service := &removalStub{}
			var out strings.Builder
			err := remove(context.Background(), service, removeOptions{catalogID: "project-123"}, strings.NewReader(tc.input), &out)
			if err != nil || service.catalogCalls != tc.calls || service.imageCalls != 0 {
				t.Fatalf("calls: %+v, error: %v", service, err)
			}
			want := "Removal cancelled; no records were deleted."
			if tc.calls != 0 {
				want = `Removed catalog "project-123" (2 image records)`
			}
			if !strings.Contains(out.String(), "Type yes to confirm:") || !strings.Contains(out.String(), want) {
				t.Fatalf("unexpected output: %s", out.String())
			}
		})
	}
}

type brokenConfirmation struct{}

func (brokenConfirmation) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRemovalFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  removeOptions
		input io.Reader
		err   error
	}{
		{"image missing", removeOptions{"xyz", "default"}, nil, catalog.ErrNotFound},
		{"catalog missing", removeOptions{catalogID: "project-123"}, strings.NewReader("yes\n"), catalog.ErrCatalogNotFound},
		{"database failure", removeOptions{catalogID: "project-123"}, strings.NewReader("yes\n"), errors.New("database unavailable")},
		{"input failure", removeOptions{catalogID: "project-123"}, brokenConfirmation{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &removalStub{err: tc.err}
			var out strings.Builder
			err := remove(context.Background(), service, tc.opts, tc.input, &out)
			want := tc.err
			if want == nil {
				want = io.ErrUnexpectedEOF
				if service.catalogCalls != 0 {
					t.Fatal("deleted catalog after input failure")
				}
			}
			if !errors.Is(err, want) || strings.Contains(out.String(), "Removed") {
				t.Fatalf("error: %v, output: %s", err, out.String())
			}
		})
	}
}

func TestImageRemovalDoesNotPrompt(t *testing.T) {
	service := &removalStub{}
	var out strings.Builder
	if err := remove(context.Background(), service, removeOptions{"xyz", "default"}, brokenConfirmation{}, &out); err != nil {
		t.Fatal(err)
	}
	if service.imageCalls != 1 || service.catalogCalls != 0 || !strings.Contains(out.String(), `Removed image "xyz" from catalog "default"`) || strings.Contains(out.String(), "confirm") {
		t.Fatalf("calls: %+v, output: %s", service, out.String())
	}
}

func TestCancelWhileWaitingForRemovalConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	service := &removalStub{}
	done := make(chan error, 1)
	go func() {
		done <- remove(ctx, service, removeOptions{catalogID: "project-123"}, reader, io.Discard)
	}()
	// Writing a partial line ensures the command reached the input read.
	if _, err := io.WriteString(writer, "ye"); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || service.catalogCalls != 0 {
			t.Fatalf("calls: %+v, error: %v", service, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not interrupt the confirmation prompt")
	}
}
