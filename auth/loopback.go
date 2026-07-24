package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const defaultLoopbackTimeout = 3 * time.Minute

// OpenURL opens a URL in the user's browser. Tests may override.
var OpenURL = func(url string) error {
	return errors.New("no browser opener configured")
}

// LoopbackOptions configures an OAuth authorization-code loopback.
type LoopbackOptions struct {
	// Product is shown in the callback page and terminal prompt (e.g. "munin").
	Product string
	// Timeout defaults to 3 minutes.
	Timeout time.Duration
	// Open, if set, opens the auth URL (defaults to OpenURL).
	Open func(url string) error
}

// LoopbackAuthCode starts a localhost callback server, opens the authorize URL
// built by buildURL(redirect, state), and returns the authorization code.
func LoopbackAuthCode(ctx context.Context, w io.Writer, service string, opts LoopbackOptions, buildURL func(redirect, state string) string) (code, redirect string, err error) {
	product := opts.Product
	if product == "" {
		product = "app"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultLoopbackTimeout
	}
	open := opts.Open
	if open == nil {
		open = OpenURL
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("starting local callback server: %w", err)
	}
	defer ln.Close()

	redirect = fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	state, err := randomState()
	if err != nil {
		return "", "", fmt.Errorf("generating oauth state: %w", err)
	}

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			fmt.Fprintf(rw, "Authorization failed: %s. You can close this window.", e)
			resCh <- result{err: fmt.Errorf("authorization error: %s", e)}
			return
		}
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(rw, "state mismatch", http.StatusBadRequest)
			resCh <- result{err: errors.New("state mismatch (possible CSRF); aborting")}
			return
		}
		fmt.Fprintf(rw, "%s is now authorized for %s. You can close this window and return to the terminal.", product, service)
		resCh <- result{code: q.Get("code")}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	authURL := buildURL(redirect, state)
	fmt.Fprintf(w, "\nOpen this URL to authorize %s for %s:\n\n  %s\n\nWaiting for authorization…\n", product, service, authURL)
	_ = open(authURL)

	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case <-time.After(timeout):
		return "", "", errors.New("timed out waiting for authorization")
	case res := <-resCh:
		if res.err != nil {
			return "", "", res.err
		}
		if res.code == "" {
			return "", "", errors.New("no authorization code received")
		}
		return res.code, redirect, nil
	}
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
