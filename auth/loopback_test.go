package auth

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestLoopbackAuthCodeHappy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var buf bytes.Buffer
	errCh := make(chan error, 1)
	codeCh := make(chan string, 1)

	go func() {
		code, redirect, err := LoopbackAuthCode(ctx, &buf, "github", LoopbackOptions{
			Product: "test",
			Timeout: 5 * time.Second,
			Open: func(authURL string) error {
				go func() {
					time.Sleep(20 * time.Millisecond)
					resp, err := http.Get(authURL)
					if err != nil {
						errCh <- err
						return
					}
					resp.Body.Close()
				}()
				return nil
			},
		}, func(redirect, state string) string {
			return fmt.Sprintf("%s?code=authcode&state=%s", redirect, state)
		})
		if err != nil {
			errCh <- err
			return
		}
		if redirect == "" {
			errCh <- fmt.Errorf("empty redirect")
			return
		}
		codeCh <- code
	}()

	select {
	case err := <-errCh:
		t.Fatal(err)
	case code := <-codeCh:
		if code != "authcode" {
			t.Fatalf("code = %q", code)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestLoopbackAuthCodeStateMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, _, err := LoopbackAuthCode(ctx, &bytes.Buffer{}, "svc", LoopbackOptions{
			Timeout: 5 * time.Second,
			Open: func(authURL string) error {
				go func() {
					time.Sleep(20 * time.Millisecond)
					resp, err := http.Get(authURL + "wrong")
					if err == nil {
						resp.Body.Close()
					}
				}()
				return nil
			},
		}, func(redirect, state string) string {
			return fmt.Sprintf("%s?code=x&state=not-%s", redirect, state)
		})
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected state mismatch error")
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestLoopbackAuthCodeErrorParam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, _, err := LoopbackAuthCode(ctx, &bytes.Buffer{}, "svc", LoopbackOptions{
			Timeout: 5 * time.Second,
			Open: func(authURL string) error {
				go func() {
					time.Sleep(20 * time.Millisecond)
					resp, err := http.Get(authURL)
					if err == nil {
						resp.Body.Close()
					}
				}()
				return nil
			},
		}, func(redirect, state string) string {
			return fmt.Sprintf("%s?error=access_denied&state=%s", redirect, state)
		})
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected authorization error")
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}
