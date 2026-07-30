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
	CodeURL      string
	TokenURL     string
	Product      string
	HTTPClient   *http.Client
	Open         func(url string) error
	PollInterval time.Duration
	// Sleep is used between polls (defaults to time.Sleep). Tests inject a stub.
	Sleep func(time.Duration)
}

// ErrDeviceDenied is returned when the user denies the device authorization.
var ErrDeviceDenied = errors.New("authorization was denied")

// ErrDeviceExpired is returned when the device code expires before approval.
var ErrDeviceExpired = errors.New("device code expired before authorization")

// DeviceToken runs the device flow and returns the access token (+ scope).
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
	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	product := opts.Product
	if product == "" {
		product = "app"
	}

	form := url.Values{"client_id": {opts.ClientID}}
	if opts.Scope != "" {
		form.Set("scope", opts.Scope)
	}
	var dc DeviceCode
	if err := postFormJSON(ctx, client, opts.CodeURL, form, &dc); err != nil {
		return "", "", fmt.Errorf("requesting device code: %w", err)
	}
	uri := dc.VerificationURIComplete
	if uri == "" {
		uri = dc.VerificationURI
	}
	fmt.Fprintf(w, "\nTo authorize %s, open %s\nand enter the code: %s\n\nWaiting for authorization…\n",
		product, uri, dc.UserCode)
	_ = open(uri)

	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if opts.PollInterval > 0 {
		interval = opts.PollInterval
	}
	if interval < time.Second {
		interval = time.Second
	}
	maxPolls := 12
	if dc.ExpiresIn > 0 {
		secs := int(interval / time.Second)
		if secs < 1 {
			secs = 1
		}
		maxPolls = dc.ExpiresIn/secs + 1
	}

	poll := url.Values{
		"client_id":   {opts.ClientID},
		"device_code": {dc.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	for i := 0; i < maxPolls; i++ {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		sleep(interval)

		var tr struct {
			AccessToken      string `json:"access_token"`
			Scope            string `json:"scope"`
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		if err := postFormJSON(ctx, client, opts.TokenURL, poll, &tr); err != nil {
			return "", "", fmt.Errorf("polling for token: %w", err)
		}
		switch tr.Error {
		case "":
			if tr.AccessToken != "" {
				return tr.AccessToken, tr.Scope, nil
			}
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token":
			return "", "", ErrDeviceExpired
		case "access_denied":
			return "", "", ErrDeviceDenied
		default:
			return "", "", fmt.Errorf("device flow failed: %s (%s)", tr.Error, tr.ErrorDescription)
		}
	}
	return "", "", errors.New("timed out waiting for authorization")
}

func postFormJSON(ctx context.Context, client *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}
