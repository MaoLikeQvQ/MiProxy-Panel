package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

type testSubscriptionClient struct{ http *http.Client }

func newSubscriptionTestClient(path string) *testSubscriptionClient {
	return &testSubscriptionClient{http: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}}
}
func (c *testSubscriptionClient) request(ctx context.Context, method, path string, input, output any) error {
	var body bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(res.Body).Decode(output)
	}
	return nil
}
func (c *testSubscriptionClient) applySubscription(ctx context.Context, id string) (applyResult, error) {
	var result applyResult
	err := c.request(ctx, "POST", "/api/subscriptions/"+url.PathEscape(id)+"/apply", struct{}{}, &result)
	if err == nil && !result.Applied {
		err = fmt.Errorf("reload failed")
	}
	return result, err
}
func (c *testSubscriptionClient) switchSubscription(ctx context.Context, id string, result *applyResult) error {
	value, err := c.applySubscription(ctx, id)
	*result = value
	return err
}
