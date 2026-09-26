//go:build unit

package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatchStreamAuthorizationCancelsInvalidCredential(t *testing.T) {
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checks := make(chan time.Time)
	checked := make(chan struct{}, 2)
	valid := true

	go watchStreamAuthorization(streamCtx, cancel, checks, func(context.Context) error {
		isValid := valid
		checked <- struct{}{}
		if isValid {
			return nil
		}
		return errors.New("credential invalid")
	})

	checks <- time.Now()
	<-checked
	require.NoError(t, streamCtx.Err())

	valid = false
	checks <- time.Now()
	select {
	case <-streamCtx.Done():
		require.ErrorIs(t, streamCtx.Err(), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("stream context was not canceled after credential invalidation")
	}
}
