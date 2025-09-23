package storage

import (
	"context"
	"time"
)

type Calendar struct {
	ID          string
	OwnerUserID string
	OwnerGroup  string
	URI         string
	DisplayName string
	Description string
	Color       string
	CTag        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Object struct {
	ID         string
	CalendarID string
	UID        string
	ETag       string
	Data       string
	Component  string // VEVENT/VTODO
	StartAt    *time.Time
	EndAt      *time.Time
	UpdatedAt  time.Time
}

type Change struct {
	UID     string
	Deleted bool
	Seq     int64
}

type Store interface {
	Close()
	// Calendars
	CreateCalendar(c Calendar, ownerGroup string, description string) error
	DeleteCalendar(ownerUserID, calURI string) error
	GetCalendarByID(ctx context.Context, id string) (*Calendar, error)
	UpdateCalendarDisplayName(ctx context.Context, ownerUID, calURI string, displayName *string) error
	ListCalendarsByOwnerUser(ctx context.Context, uid string) ([]*Calendar, error)
	ListAllCalendars(ctx context.Context) ([]*Calendar, error)
	UpdateCalendarColor(ctx context.Context, ownerUID, calURI, color string) error

	// Objects
	GetObject(ctx context.Context, calendarID, uid string) (*Object, error)
	PutObject(ctx context.Context, obj *Object) error
	DeleteObject(ctx context.Context, calendarID, uid string, etag string) error
	ListObjects(ctx context.Context, calendarID string, start *time.Time, end *time.Time) ([]*Object, error)
	ListObjectsByComponent(ctx context.Context, calendarID string, components []string, start *time.Time, end *time.Time) ([]*Object, error)
	// Sync tokens
	NewCTag(ctx context.Context, calendarID string) (string, error)
	GetSyncInfo(ctx context.Context, calendarID string) (token string, seq int64, err error)
	ListChangesSince(ctx context.Context, calendarID string, sinceSeq int64, limit int) ([]Change, int64, error)
	RecordChange(ctx context.Context, calendarID, uid string, deleted bool) (newToken string, newSeq int64, err error)
}
