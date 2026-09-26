package httpserver

import (
	"context"
	"net"
	"net/http"
)

type shutdownKey struct{}

// EndStreamsOnShutdown ends every stream context of srv's requests as soon as srv starts shutting down
// Shutdown waits for every active request and a stream never finishes on its own, so an open stream would otherwise hold the shutdown until its deadline
func EndStreamsOnShutdown(srv *http.Server) {
	shutdown, cancel := context.WithCancel(context.Background())
	srv.BaseContext = func(net.Listener) context.Context {
		return context.WithValue(context.Background(), shutdownKey{}, shutdown)
	}
	srv.RegisterOnShutdown(cancel)
}

// StreamContext derives the context of a stream, which also ends once the server serving the request starts shutting down
// Ordinary requests keep their own context, so a graceful shutdown still lets them finish
func StreamContext(ctx context.Context) (context.Context, context.CancelFunc) {
	streamCtx, cancel := context.WithCancel(ctx)
	shutdown, ok := ctx.Value(shutdownKey{}).(context.Context)
	if !ok {
		return streamCtx, cancel
	}

	// The callback is removed when the stream ends, so finished streams don't pile up on the server's shutdown context
	stop := context.AfterFunc(shutdown, cancel)
	return streamCtx, func() {
		stop()
		cancel()
	}
}
