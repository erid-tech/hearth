package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

// Listen binds a Unix socket at socketPath, serves handler, and shuts
// down cleanly when ctx is done. The socket file is created with mode
// 0600 and removed on exit. A stale socket file at socketPath is
// removed before binding.
func Listen(ctx context.Context, socketPath string, handler http.Handler) error {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = lis.Close()
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	select {
	case err := <-serveErr:
		_ = os.Remove(socketPath)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			_ = err // best-effort shutdown
		}
		_ = os.Remove(socketPath)
		return nil
	}
}
