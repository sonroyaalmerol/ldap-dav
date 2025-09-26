// TODO: implement sqlite

package sqlite

import (
	"context"
	"time"

	"github.com/sonroyaalmerol/ldap-dav/internal/storage"
)

func (s *Store) CreateSchedulingInbox(ctx context.Context, ownerUserID, ownerGroup string) error {
	return nil
}

func (s *Store) CreateSchedulingOutbox(ctx context.Context, ownerUserID, ownerGroup string) error {
	return nil
}

func (s *Store) GetSchedulingInbox(ctx context.Context, ownerUserID string) (*storage.Calendar, error) {
	return nil, nil
}

func (s *Store) GetSchedulingOutbox(ctx context.Context, ownerUserID string) (*storage.Calendar, error) {
	return nil, nil
}

func (s *Store) StoreSchedulingObject(ctx context.Context, obj *storage.SchedulingObject) error {
	return nil
}

func (s *Store) ListSchedulingObjects(ctx context.Context, calendarID string) ([]*storage.SchedulingObject, error) {
	return nil, nil
}

func (s *Store) StoreAttendeeResponse(ctx context.Context, response *storage.AttendeeResponse) error {
	return nil
}

func (s *Store) StoreFreeBusyInfo(ctx context.Context, info *storage.FreeBusyInfo) error {
	return nil
}

func (s *Store) GetFreeBusyInfo(ctx context.Context, userID string, start, end time.Time) ([]*storage.FreeBusyInfo, error) {
	return nil, nil
}

func (s *Store) DeleteFreeBusyInfo(ctx context.Context, userID, eventUID string) error {
	return nil
}

func (s *Store) GetSchedulingObject(ctx context.Context, calendarID, uid, recipient string) (*storage.SchedulingObject, error) {
	return nil, nil
}

func (s *Store) DeleteSchedulingObject(ctx context.Context, calendarID, uid, recipient string) error {
	return nil
}

func (s *Store) UpdateSchedulingObjectStatus(ctx context.Context, calendarID, uid, recipient, status string) error {
	return nil
}

func (s *Store) GetAttendeeResponse(ctx context.Context, eventUID, attendeeEmail string) (*storage.AttendeeResponse, error) {
	return nil, nil
}

func (s *Store) GetPendingSchedulingObjects(ctx context.Context, limit int) ([]*storage.SchedulingObject, error) {
	return nil, nil
}
