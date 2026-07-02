package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	nodeID  string
	token   string
	http    *http.Client
}

func NewClient(baseURL, nodeID, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		nodeID:  strings.TrimSpace(nodeID),
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) PostHeartbeat(ctx context.Context, status, version string, ts time.Time) error {
	return c.doJSON(ctx, http.MethodPost, "/api/agent/v1/heartbeat", HeartbeatRequest{TS: ts.UTC().Unix(), Status: status, AgentVersion: version}, nil)
}

func (c *Client) PostHost(ctx context.Context, host HostInfo) error {
	return c.doJSON(ctx, http.MethodPost, "/api/agent/v1/host", host, nil)
}

func (c *Client) PostState(ctx context.Context, state StateSample) error {
	return c.doJSON(ctx, http.MethodPost, "/api/agent/v1/state", state, nil)
}

func (c *Client) FetchProbeTargets(ctx context.Context) ([]ProbeTarget, error) {
	var response ProbeTargetsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/agent/v1/probe-targets", nil, &response); err != nil {
		return nil, err
	}
	return response.Targets, nil
}

func (c *Client) PostProbeResults(ctx context.Context, rounds []ProbeRound) error {
	payload := ProbeResultsRequest{Rounds: make([]probeRoundPayload, 0, len(rounds))}
	for _, round := range rounds {
		payload.Rounds = append(payload.Rounds, probeRoundPayload{
			TargetID: round.TargetID,
			TS:       round.TS.UTC().Unix(),
			Type:     round.Type,
			Samples:  round.Samples,
		})
	}
	return c.doJSON(ctx, http.MethodPost, "/api/agent/v1/probe-results", payload, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, requestValue any, responseValue any) error {
	if c.baseURL == "" || c.nodeID == "" || c.token == "" {
		return fmt.Errorf("controller url, node id, and token are required")
	}
	var body io.Reader
	if requestValue != nil {
		encoded, err := json.Marshal(requestValue)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Node-ID", c.nodeID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if requestValue != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agent api %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if responseValue == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(responseValue)
}
