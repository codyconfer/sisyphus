package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceTokenHappy(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			_ = json.NewEncoder(w).Encode(DeviceCode{
				DeviceCode:      "dc",
				UserCode:        "ABCD-1234",
				VerificationURI: "http://example.test/verify",
				ExpiresIn:       60,
				Interval:        1,
			})
		case "/oauth/token":
			n := polls.Add(1)
			if n < 2 {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "tok",
				"scope":        "repo",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	tok, scope, err := DeviceToken(context.Background(), &buf, DeviceFlowOptions{
		ClientID:   "cid",
		CodeURL:    srv.URL + "/device/code",
		TokenURL:   srv.URL + "/oauth/token",
		HTTPClient: srv.Client(),
		Open:       func(string) error { return nil },
		Sleep:      func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tok" || scope != "repo" {
		t.Fatalf("tok=%q scope=%q", tok, scope)
	}
	if !bytes.Contains(buf.Bytes(), []byte("ABCD-1234")) {
		t.Fatalf("prompt missing user code: %s", buf.String())
	}
}

func TestDeviceTokenDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			_ = json.NewEncoder(w).Encode(DeviceCode{
				DeviceCode: "dc", UserCode: "U", VerificationURI: "http://x", ExpiresIn: 30, Interval: 1,
			})
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	_, _, err := DeviceToken(context.Background(), &bytes.Buffer{}, DeviceFlowOptions{
		ClientID: "cid", CodeURL: srv.URL + "/device/code", TokenURL: srv.URL + "/oauth/token",
		HTTPClient: srv.Client(), Open: func(string) error { return nil }, Sleep: func(time.Duration) {},
	})
	if !errors.Is(err, ErrDeviceDenied) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeviceTokenExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			_ = json.NewEncoder(w).Encode(DeviceCode{
				DeviceCode: "dc", UserCode: "U", VerificationURI: "http://x", ExpiresIn: 30, Interval: 1,
			})
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	_, _, err := DeviceToken(context.Background(), &bytes.Buffer{}, DeviceFlowOptions{
		ClientID: "cid", CodeURL: srv.URL + "/device/code", TokenURL: srv.URL + "/oauth/token",
		HTTPClient: srv.Client(), Open: func(string) error { return nil }, Sleep: func(time.Duration) {},
	})
	if !errors.Is(err, ErrDeviceExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestDeviceTokenSubSecondPollInterval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			_ = json.NewEncoder(w).Encode(DeviceCode{
				DeviceCode: "dc", UserCode: "U", VerificationURI: "http://x", ExpiresIn: 5, Interval: 1,
			})
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "t"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	tok, _, err := DeviceToken(context.Background(), &bytes.Buffer{}, DeviceFlowOptions{
		ClientID: "cid", CodeURL: srv.URL + "/device/code", TokenURL: srv.URL + "/oauth/token",
		HTTPClient: srv.Client(), Open: func(string) error { return nil }, Sleep: func(time.Duration) {},
		PollInterval: 100 * time.Millisecond,
	})
	if err != nil || tok != "t" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
}

func TestDeviceTokenRequiresFields(t *testing.T) {
	_, _, err := DeviceToken(context.Background(), &bytes.Buffer{}, DeviceFlowOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}
