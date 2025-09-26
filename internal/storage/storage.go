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

type Contact struct {
	ID            string
	AddressbookID string
	UID           string
	Data          string
	ETag          string
	UpdatedAt     time.Time
}

type Addressbook struct {
	ID          string
	OwnerUserID string
	OwnerGroup  string
	URI         string
	DisplayName string
	Description string
	CTag        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SchedulingObject struct {
	ID         string
	CalendarID string
	UID        string
	ETag       string
	Data       string
	Method     string // REQUEST, REPLY, CANCEL, etc.
	Recipient  string
	Originator string
	Status     string // pending, delivered, failed
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type AttendeeResponse struct {
	ID             string
	EventUID       string
	CalendarID     string
	AttendeeEmail  string
	ResponseStatus string // ACCEPTED, DECLINED, TENTATIVE
	ResponseData   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type FreeBusyInfo struct {
	ID        string
	UserID    string
	StartTime time.Time
	EndTime   time.Time
	BusyType  string // BUSY, BUSY-UNAVAILABLE, BUSY-TENTATIVE
	EventUID  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store interface {
	Close()
	// Calendars
	CreateCalendar(c Calendar, ownerGroup string, description string) error
	DeleteCalendar(ownerUserID, calURI string) error
	GetCalendarByURI(ctx context.Context, uri string) (*Calendar, error)
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

	CreateAddressbook(a Addressbook, ownerGroup string, description string) error
	DeleteAddressbook(ownerUserID, abURI string) error
	GetAddressbookByURI(ctx context.Context, uri string) (*Addressbook, error)
	UpdateAddressbookDisplayName(ctx context.Context, ownerUID, abURI string, displayName *string) error
	ListAddressbooksByOwnerUser(ctx context.Context, uid string) ([]*Addressbook, error)
	ListAllAddressbooks(ctx context.Context) ([]*Addressbook, error)

	GetContact(ctx context.Context, addressbookID, uid string) (*Contact, error)
	PutContact(ctx context.Context, c *Contact) error
	DeleteContact(ctx context.Context, addressbookID, uid string, etag string) error

	ListContacts(ctx context.Context, addressbookID string) ([]*Contact, error)

	ListContactsByFilter(ctx context.Context, addressbookID string, propNames []string) ([]*Contact, error)

	NewAddressbookCTag(ctx context.Context, addressbookID string) (string, error)
	GetAddressbookSyncInfo(ctx context.Context, addressbookID string) (token string, seq int64, err error)
	ListAddressbookChangesSince(ctx context.Context, addressbookID string, sinceSeq int64, limit int) ([]Change, int64, error)
	RecordAddressbookChange(ctx context.Context, addressbookID, uid string, deleted bool) (newToken string, newSeq int64, err error)

	CreateSchedulingInbox(ctx context.Context, ownerUserID, ownerGroup string) error
	CreateSchedulingOutbox(ctx context.Context, ownerUserID, ownerGroup string) error

	GetSchedulingInbox(ctx context.Context, ownerUserID string) (*Calendar, error)
	GetSchedulingOutbox(ctx context.Context, ownerUserID string) (*Calendar, error)

	StoreSchedulingObject(ctx context.Context, obj *SchedulingObject) error
	GetSchedulingObject(ctx context.Context, calendarID, uid, recipient string) (*SchedulingObject, error)

	ListSchedulingObjects(ctx context.Context, calendarID string) ([]*SchedulingObject, error)
	DeleteSchedulingObject(ctx context.Context, calendarID, uid, recipient string) error
	DeleteOldSchedulingObjects(ctx context.Context, cutoff time.Time) error
	DeleteOldAttendeeResponses(ctx context.Context, cutoff time.Time) error
	DeleteOldFreeBusyInfo(ctx context.Context, cutoff time.Time) error

	UpdateSchedulingObjectStatus(ctx context.Context, calendarID, uid, recipient, status string) error
	StoreAttendeeResponse(ctx context.Context, response *AttendeeResponse) error

	GetAttendeeResponse(ctx context.Context, eventUID, attendeeEmail string) (*AttendeeResponse, error)
	ListAttendeeResponses(ctx context.Context, eventUID string) ([]*AttendeeResponse, error)

	StoreFreeBusyInfo(ctx context.Context, info *FreeBusyInfo) error
	GetFreeBusyInfo(ctx context.Context, userID string, start, end time.Time) ([]*FreeBusyInfo, error)
	DeleteFreeBusyInfo(ctx context.Context, userID, eventUID string) error

	GetPendingSchedulingObjects(ctx context.Context, limit int) ([]*SchedulingObject, error)
}
