package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceCode is the first response from an OAuth device-authorization endpoint.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceFlowOptions configures a device-code grant.
type DeviceFlowOptions struct {
	ClientID     string
	Scope        string
	CodeURL      string // e.g. https://github.com/login/device/code
	TokenURL     string // e.g. https://github.com/login/oauth/access_token
	Product      string
	HTTPClient   *http.Client
	Open         func(url string) error
	PollInterval time.Duration
}

// DeviceToken runs the device flow and returns the access token JSON body.
func DeviceToken(ctx context.Context, w io.Writer, opts DeviceFlowOptions) (accessToken, scope string, err error) {
	if opts.ClientID == "" || opts.CodeURL == "" || opts.TokenURL == "" {
		return "", "", errors.New("device flow: ClientID, CodeURL, and TokenURL are required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	open := opts.Open
	if open == nil {
		open = OpenURL
	}
	product := opts.Product
	if product == "" {
		product = "app"
	}

	form := url.Values{"client_id": {opts.ClientID}}
	if opts.Scope != "" {
		form.Set("scope", opts.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.CodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("device code request: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var dc DeviceCode
	if err := json.Unmarshal(body, &dc); err != nil {
		return "", "", err
	}
	uri := dc.VerificationURIComplete
	if uri == "" {
		uri = dc.VerificationURI
	}
	fmt.Fprintf(w, "\nAuthorize %s — enter code %s at:\n\n  %s\n\nWaiting…\n", product, dc.UserCode, uri)
	_ = open(uri)

	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if opts.PollInterval > 0 {
		interval = opts.PollInterval
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	if dc.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}

	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if time.Now().After(deadline) {
			return "", "", errors.New("device flow timed out")
		}
		tok, sc, pending, err := pollDeviceToken(ctx, client, opts.TokenURL, opts.ClientID, dc.DeviceCode)
		if err != nil {
			return "", "", err
		}
		if pending {
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(interval):
				continue
			}
		}
		return tok, sc, nil
	}
}

func pollDeviceToken(ctx context.Context, client *http.Client, tokenURL, clientID, deviceCode string) (token, scope string, pending bool, err error) {
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	switch payload.Error {
	case "", "null":
		if payload.AccessToken == "" {
			return "", "", true, nil
		}
		return payload.AccessToken, payload.Scope, false, nil
	case "authorization_pending", "slow_down":
		return "", "", true, nil
	default:
		return "", "", false, fmt.Errorf("device token: %s", payload.Error)
	}
}
