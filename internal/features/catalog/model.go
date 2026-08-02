package catalog

import (
	"errors"
	"time"
)

var (
	ErrForbidden          = errors.New("catalog management is forbidden")
	ErrInvalidLocation    = errors.New("catalog location is unavailable")
	ErrDuplicateReference = errors.New("catalog reference already exists")
	ErrDuplicateImport    = errors.New("catalog file was already imported")
	ErrImportNotReady     = errors.New("catalog import is not ready")
	ErrImportEmpty        = errors.New("catalog import has no publishable rows")
	ErrPublicationChanged = errors.New("catalog changed since import analysis")
	ErrAlreadyRolledBack  = errors.New("catalog publication was already rolled back")
)

type CatalogLocation struct {
	ID     string
	Name   string
	Active bool
}

type CatalogItemRecord struct {
	ID              string
	Kind            string
	Reference       string
	Name            string
	Description     string
	PriceKind       string
	AmountCents     *int64
	MaxAmountCents  *int64
	TaxBasis        string
	VATBasisPoints  int
	Currency        string
	PerHour         bool
	DurationMinutes *int
	EffectiveFrom   *time.Time
	EffectiveTo     *time.Time
	LocationScope   string
	LocationIDs     []string
	LocationNames   []string
	SourceImportID  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ArchivedAt      *time.Time
}

type CatalogFilter struct {
	Kind  string
	Query string
}

type CatalogOverview struct {
	Organization string
	CanManage    bool
	Items        []CatalogItemRecord
	Counts       map[string]int
	Locations    []CatalogLocation
}

type ItemInput struct {
	Kind            string
	Reference       string
	Name            string
	Description     string
	PriceKind       string
	AmountCents     *int64
	MaxAmountCents  *int64
	TaxBasis        string
	VATBasisPoints  int
	DurationMinutes *int
	EffectiveFrom   *time.Time
	EffectiveTo     *time.Time
	LocationScope   string
	LocationIDs     []string
}

type UploadInput struct {
	LocationID string
	Filename   string
	MediaType  string
	Content    []byte
	Size       int64
}

type ImportRecord struct {
	ID            string
	LocationID    string
	LocationName  string
	Filename      string
	Format        string
	MediaType     string
	Size          int64
	Checksum      []byte
	UploadedBy    string
	UploadedAt    time.Time
	Status        string
	Rejection     string
	Mode          string
	PublishedAt   *time.Time
	ValidRows     int
	AmbiguousRows int
	RejectedRows  int
}

type ImportRowRecord struct {
	Number          int
	Classification  string
	Raw             map[string]string
	Kind            string
	Reference       string
	Name            string
	Description     string
	PriceKind       string
	AmountCents     *int64
	MaxAmountCents  *int64
	TaxBasis        string
	VATBasisPoints  int
	DurationMinutes *int
	EffectiveFrom   *time.Time
	EffectiveTo     *time.Time
	Issue           string
	MatchingItemID  string
	MatchingName    string
}

type ImportPreview struct {
	Organization string
	CanManage    bool
	Import       ImportRecord
	Rows         []ImportRowRecord
}

type ImportsOverview struct {
	Organization string
	CanManage    bool
	Imports      []ImportRecord
}

type PublishPlan struct {
	Create int
	Update int
	Remove int
	Skip   int
}

type PublicationRecord struct {
	ID          string
	ImportID    string
	Version     int
	Mode        string
	PublishedAt time.Time
	RolledBack  bool
}
