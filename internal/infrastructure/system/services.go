package system

import (
	"time"

	"github.com/google/uuid"

	"membox/internal/domain/catalog"
)

type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }

type IDGenerator struct{}

func (IDGenerator) NewDocumentID() (catalog.DocumentID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return catalog.DocumentID(id.String()), nil
}

func (IDGenerator) NewResourceID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return "res-" + id.String(), nil
}
