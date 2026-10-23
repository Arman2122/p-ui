package controlplane

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestDurableEnrollmentRequestRetainsIdentityAfterLostResponse(t *testing.T) {
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	request := EnrollmentRequest{
		Version: 1, RequestID: uuid.NewString(), Name: "local-node",
		Enrollment: Enrollment{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), Ticket: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), ExpiresUnixMilli: time.Now().Add(9 * time.Minute).UnixMilli()},
	}
	first, err := store.createEnrollmentRequest(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.createEnrollmentRequest(t.Context(), request)
	if err != nil || first != second || second != request.Enrollment {
		t.Fatalf("lost-response replay changed enrollment: %+v %+v %v", first, second, err)
	}
	for _, model := range []any{&model.FleetNode{}, &model.FleetEnrollment{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("replay minted extra %T rows: %d %v", model, count, err)
		}
	}
	for _, change := range []func(*EnrollmentRequest){
		func(r *EnrollmentRequest) { r.Name = "different-name" },
		func(r *EnrollmentRequest) { r.Enrollment.NodeID = uuid.NewString() },
		func(r *EnrollmentRequest) { r.Enrollment.InstallationID = uuid.NewString() },
		func(r *EnrollmentRequest) { r.Enrollment.ExpiresUnixMilli-- },
		func(r *EnrollmentRequest) {
			r.Enrollment.Ticket = base64.RawURLEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
		},
	} {
		conflicting := request
		change(&conflicting)
		if _, err := store.createEnrollmentRequest(t.Context(), conflicting); !errors.Is(err, ErrEnrollment) {
			t.Fatalf("changed durable enrollment request accepted: %v", err)
		}
	}
	expired := request
	expired.Enrollment.ExpiresUnixMilli = time.Now().Add(-time.Second).UnixMilli()
	if _, err := store.createEnrollmentRequest(t.Context(), expired); !errors.Is(err, ErrEnrollment) {
		t.Fatalf("expired enrollment reminted identity: %v", err)
	}
}
