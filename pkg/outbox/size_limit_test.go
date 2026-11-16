package outbox_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	usersv1 "github.com/STECH-Super-App/gen-go-lib/proto/events/users/v1"
	apperr "github.com/STECH-Super-App/go-common/pkg/errors"
	"github.com/STECH-Super-App/go-common/pkg/outbox"
)

// SL-03 / B-04: an event that does not fit one Kafka record used to become an
// unsendable row that stalled the whole service's outbox. PublishProto now
// refuses it in the request that built it.
func TestPublishProto_RefusesAnEventOverTheLimitWithATypedError(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user.events", outbox.WithMaxMessageBytes(2_000))

	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user", AggregateID: "u1",
		Message: &usersv1.UserRegistered{UserId: "u1", Name: strings.Repeat("x", 5_000)},
	})

	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae), "a typed error, got %v", err)
	assert.Equal(t, outbox.ReasonEventTooLarge, ae.Reason)
	assert.Equal(t, http.StatusUnprocessableEntity, ae.Code)
	assert.EqualValues(t, 2_000, ae.Params["limit"])
	assert.Greater(t, ae.Params["size"], 5_000)
	assert.Empty(t, store.Messages, "nothing enters the outbox")
}

func TestPublishProto_AcceptsAnEventUnderTheLimit(t *testing.T) {
	store := outbox.NewTestStore()
	pub := outbox.NewPublisher(store, "user.events", outbox.WithMaxMessageBytes(2_000))

	require.NoError(t, pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user", AggregateID: "u1",
		Message: &usersv1.UserRegistered{UserId: "u1", Name: "Alice"},
	}))
	assert.Len(t, store.Messages, 1)
}

// Headers count: a small payload with huge headers is still one record.
func TestPublishProto_CountsHeadersInTheSize(t *testing.T) {
	pub := outbox.NewPublisher(outbox.NewTestStore(), "user.events", outbox.WithMaxMessageBytes(2_000))

	err := pub.PublishProto(context.Background(), nil, outbox.PublishProtoOptions{
		AggregateType: "user", AggregateID: "u1",
		Message: &usersv1.UserRegistered{UserId: "u1"},
		Headers: map[string]string{"x-big": strings.Repeat("h", 3_000)},
	})

	var ae *apperr.AppError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, outbox.ReasonEventTooLarge, ae.Reason)
}

func TestPublisher_DefaultLimitIsSafelyUnderOneMiB(t *testing.T) {
	assert.Less(t, outbox.DefaultMaxMessageBytes, 1<<20)
	assert.Greater(t, outbox.DefaultMaxMessageBytes, 512<<10)
	assert.Equal(t, outbox.DefaultMaxMessageBytes, outbox.DefaultConfig().MaxMessageBytes)
}
