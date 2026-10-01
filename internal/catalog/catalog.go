package catalog

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/google/uuid"
)

var (
	ErrNotFound        = errors.New("imagery not found")
	ErrConflict        = errors.New("image ID already exists with a different checksum")
	ErrInvalidGeometry = errors.New("invalid geometry")
)

// Imagery is shared by the CLI and server, independently of API serialization.
type Imagery struct {
	Segments                           []raster.Segment
	ID                                 uuid.UUID
	CatalogID, ImageID, DisplayName    string
	AcquiredAt                         *time.Time
	CloudCover                         *float64 // Percentage from 0 to 100; nil means unknown.
	ImportedAt, CreatedAt              time.Time
	Footprint                          geo.Geometry
	Checksum, AssetLocation, SourceCRS string
	Width, Height, BandCount           int
	Metadata                           json.RawMessage
}

// Format reads the detected driver saved by inspection. Older records have no
// annotation: report unknown without guessing from extensions or opening assets.
func (i Imagery) Format() *string {
	var metadata struct {
		Aarde struct {
			Format string `json:"format"`
		} `json:"_aarde"`
	}
	if json.Unmarshal(i.Metadata, &metadata) != nil {
		return nil
	}
	if metadata.Aarde.Format != "GTiff" && metadata.Aarde.Format != "NITF" {
		return nil
	}
	return &metadata.Aarde.Format
}

type Query struct {
	CatalogID                    string
	ImageIDs                     []string
	Geometry                     *geo.Geometry
	CloudCoverLT                 *float64   // Strict scene cloud-cover threshold in percent; nil means any.
	CloudCoverLTE                *float64   // Inclusive maximum; mutually exclusive with CloudCoverLT.
	CloudCoverUnknown            string     // exclude, include, or only; empty selects the default.
	AcquiredFrom, AcquiredBefore *time.Time // Inclusive lower and exclusive upper bounds.
	Limit, Offset                int
}

type Page struct {
	Items         []Imagery
	Limit, Offset int
	HasMore       bool
}

// Store is the persistence boundary; spatial correctness is tested against PostGIS.
type Store interface {
	Get(context.Context, string, string) (Imagery, error)
	ByChecksum(context.Context, string, string) (Imagery, error)
	Insert(context.Context, Imagery) (Imagery, bool, error)
	Search(context.Context, Query) (Page, error)
	Catalogs(context.Context) ([]string, error)
	ValidateGeometry(context.Context, geo.Geometry) error
	Ping(context.Context) error
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
func (s *Service) Get(ctx context.Context, cat, id string) (Imagery, error) {
	return s.store.Get(ctx, cat, id)
}
func (s *Service) Catalogs(ctx context.Context) ([]string, error) { return s.store.Catalogs(ctx) }
func (s *Service) Ping(ctx context.Context) error                 { return s.store.Ping(ctx) }

func ValidateName(name string) error {
	if len(name) == 0 || len(name) > 255 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\\x00\r\n\t") {
		return errors.New("IDs must be 1-255 characters without slashes, control characters, or surrounding whitespace")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return errors.New("IDs must not contain control characters")
		}
	}
	return nil
}

func NormalizeQuery(q *Query) error {
	if err := ValidateCloudCover(q.CloudCoverLT); err != nil {
		return fmt.Errorf("cloud_cover_lt: %w", err)
	}
	if err := ValidateCloudCover(q.CloudCoverLTE); err != nil {
		return fmt.Errorf("cloud_cover_lte: %w", err)
	}
	if q.CloudCoverLT != nil && q.CloudCoverLTE != nil {
		return errors.New("cloud_cover_lt and cloud_cover_lte cannot be combined")
	}
	hasThreshold := q.CloudCoverLT != nil || q.CloudCoverLTE != nil
	if q.CloudCoverUnknown == "" {
		q.CloudCoverUnknown = "include"
		if hasThreshold {
			q.CloudCoverUnknown = "exclude"
		}
	}
	switch q.CloudCoverUnknown {
	case "include", "exclude":
	case "only":
		if hasThreshold {
			return errors.New("cloud_cover_unknown=only cannot be combined with a cloud-cover threshold")
		}
	default:
		return errors.New("cloud_cover_unknown must be exclude, include, or only")
	}
	if q.AcquiredFrom != nil && q.AcquiredBefore != nil && !q.AcquiredFrom.Before(*q.AcquiredBefore) {
		return errors.New("acquired_before must be after acquired_from")
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 200 || q.Offset < 0 || q.Offset > 1000000 {
		return errors.New("limit must be 1-200 and offset 0-1000000")
	}
	if q.CatalogID != "" {
		if err := ValidateName(q.CatalogID); err != nil {
			return err
		}
	}
	if len(q.ImageIDs) > 100 {
		return errors.New("at most 100 image IDs are allowed")
	}
	for _, id := range q.ImageIDs {
		if err := ValidateName(id); err != nil {
			return err
		}
	}
	if q.Geometry != nil {
		if err := q.Geometry.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidGeometry, err)
		}
	}
	return nil
}

func (s *Service) Search(ctx context.Context, q Query) (Page, error) {
	if err := NormalizeQuery(&q); err != nil {
		return Page{}, err
	}
	if q.Geometry != nil {
		if err := s.store.ValidateGeometry(ctx, *q.Geometry); err != nil {
			return Page{}, err
		}
	}
	return s.store.Search(ctx, q)
}

func ValidateCloudCover(value *float64) error {
	if value != nil && (math.IsNaN(*value) || *value < 0 || *value > 100) {
		return errors.New("cloud cover must be a finite percentage between 0 and 100")
	}
	return nil
}

func ValidateImagery(i Imagery) error {
	if err := ValidateCloudCover(i.CloudCover); err != nil {
		return err
	}
	if err := ValidateName(i.CatalogID); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	if err := ValidateName(i.ImageID); err != nil {
		return fmt.Errorf("image: %w", err)
	}
	if len(i.Segments) > 1 {
		if i.Width != 0 || i.Height != 0 || i.BandCount != 0 || i.SourceCRS != "" {
			return errors.New("multi-image aggregate dimensions, bands and CRS must be unavailable")
		}

	} else if i.Width <= 0 || i.Height <= 0 || i.BandCount <= 0 || i.SourceCRS == "" || i.AssetLocation == "" {
		return errors.New("raster requires dimensions, bands, a CRS, and an asset location")
	}
	for index, segment := range i.Segments {
		if segment.Index != index || segment.Width <= 0 || segment.Height <= 0 || segment.BandCount <= 0 || segment.SourceCRS == "" || !json.Valid(segment.Metadata) {
			return fmt.Errorf("invalid image segment %d", index)
		}
		if err := segment.Footprint.Validate(); err != nil {
			return fmt.Errorf("image segment %d: %w", index, err)
		}
		if err := ValidateCloudCover(segment.CloudCover); err != nil {
			return fmt.Errorf("image segment %d: %w", index, err)
		}
	}
	if i.AssetLocation == "" {
		return errors.New("asset location is required")
	}
	b, err := hex.DecodeString(i.Checksum)
	if err != nil || len(b) != 32 {
		return errors.New("checksum must be SHA-256")
	}
	if !json.Valid(i.Metadata) {
		return errors.New("invalid raster metadata")
	}
	if err := i.Footprint.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidGeometry, err)
	}
	return nil
}

// CheckImport does no writes. A checksum match wins over a filename conflict.
func (s *Service) CheckImport(ctx context.Context, i Imagery) (Imagery, bool, error) {
	if err := ValidateImagery(i); err != nil {
		return Imagery{}, false, err
	}
	existing, err := s.store.ByChecksum(ctx, i.CatalogID, i.Checksum)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Imagery{}, false, err
	}
	existing, err = s.store.Get(ctx, i.CatalogID, i.ImageID)
	if err == nil {
		// Another importer may have inserted between the two reads.
		if existing.Checksum == i.Checksum {
			return existing, true, nil
		}
		return Imagery{}, false, ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return Imagery{}, false, err
	}
	if err := s.store.ValidateGeometry(ctx, i.Footprint); err != nil {
		return Imagery{}, false, err
	}
	return i, false, nil
}

func (s *Service) Import(ctx context.Context, i Imagery) (Imagery, bool, error) {
	existing, found, err := s.CheckImport(ctx, i)
	if err != nil || found {
		return existing, found, err
	}
	i.ID = uuid.New()
	i.ImportedAt = time.Now().UTC()
	i.CreatedAt = i.ImportedAt
	return s.store.Insert(ctx, i)
}
