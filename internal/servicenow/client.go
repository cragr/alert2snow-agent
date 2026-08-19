package servicenow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/models"
)

// Client handles communication with the ServiceNow Table API.
type Client struct {
	baseURL      string
	endpointPath string
	username     string
	password     string
	rootCause    string
	httpClient   *http.Client
	retryConfig  RetryConfig
	logger       *slog.Logger
}

// NewClient creates a new ServiceNow API client.
func NewClient(cfg *config.Config, logger *slog.Logger) *Client {
	return &Client{
		baseURL:      cfg.ServiceNowBaseURL,
		endpointPath: cfg.ServiceNowEndpointPath,
		username:     cfg.ServiceNowUsername,
		password:     cfg.ServiceNowPassword,
		rootCause:    cfg.ServiceNowRootCause,
		httpClient:   &http.Client{Timeout: 30_000_000_000}, // 30 seconds
		retryConfig:  DefaultRetryConfig(),
		logger:       logger,
	}
}

// CreateIncidentResult contains the result of creating an incident.
type CreateIncidentResult struct {
	SysID  string
	Number string
}

// CreateIncident creates a new incident in ServiceNow and returns the incident number.
func (c *Client) CreateIncident(ctx context.Context, incident models.ServiceNowIncident) (*CreateIncidentResult, error) {
	endpoint := c.baseURL + c.endpointPath

	body, err := json.Marshal(incident)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal incident: %w", err)
	}

	c.logger.Debug("creating incident in ServiceNow",
		"correlation_id", incident.CorrelationID,
		"short_description", incident.ShortDescription,
	)

	var result *CreateIncidentResult

	err = WithRetry(ctx, c.retryConfig, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		if err := c.checkResponse(resp); err != nil {
			return err
		}

		// Parse response to extract incident number
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		var snowResp models.ServiceNowResponse
		if err := json.Unmarshal(respBody, &snowResp); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}

		result = &CreateIncidentResult{
			SysID:  snowResp.Result.SysID,
			Number: snowResp.Result.Number,
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
}

// closedStates lists ServiceNow incident states that count as not-open:
// 6 = Resolved, 7 = Closed, 8 = Canceled. Incidents in these states must not
// suppress a new incident when the same alert fires again later.
const closedStates = "6,7,8"

// maxOpenIncidentsPerCorrelationID bounds the resolved-path query. Anything
// approaching this is a backlog of pre-dedup duplicates, not normal operation.
const maxOpenIncidentsPerCorrelationID = 100

// FindIncidentByCorrelationID returns the most recently created OPEN incident
// for the given correlation ID, or nil if none exists.
func (c *Client) FindIncidentByCorrelationID(ctx context.Context, correlationID string) (*models.ServiceNowResult, error) {
	results, err := c.findOpenIncidents(ctx, correlationID, 1)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0], nil
}

// FindOpenIncidentsByCorrelationID returns every open incident for the given
// correlation ID. The resolved path uses this so that duplicates created before
// dedup was in place are all closed, rather than just one of them.
func (c *Client) FindOpenIncidentsByCorrelationID(ctx context.Context, correlationID string) ([]models.ServiceNowResult, error) {
	return c.findOpenIncidents(ctx, correlationID, maxOpenIncidentsPerCorrelationID)
}

// findOpenIncidents queries for open incidents matching a correlation ID,
// newest first, up to limit.
func (c *Client) findOpenIncidents(ctx context.Context, correlationID string, limit int) ([]models.ServiceNowResult, error) {
	// Encoded query: correlation_id = <id> AND state NOT IN (6,7,8), newest
	// first. Built through url.Values so the whole thing is escaped exactly
	// once — the "^" separators and the "NOT IN" operator do not survive being
	// concatenated into a raw URL.
	query := fmt.Sprintf("correlation_id=%s^stateNOT IN%s^ORDERBYDESCsys_created_on",
		correlationID, closedStates)

	params := url.Values{}
	params.Set("sysparm_query", query)
	params.Set("sysparm_limit", strconv.Itoa(limit))
	params.Set("sysparm_fields", "sys_id,number,state,correlation_id,short_description")

	endpoint := fmt.Sprintf("%s%s?%s", c.baseURL, c.endpointPath, params.Encode())

	c.logger.Debug("searching for open incidents by correlation_id",
		"correlation_id", correlationID,
		"limit", limit,
	)

	var results []models.ServiceNowResult

	err := WithRetry(ctx, c.retryConfig, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		if err := c.checkResponse(resp); err != nil {
			return err
		}

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		var listResp models.ServiceNowListResponse
		if err := json.Unmarshal(respBody, &listResp); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}

		results = listResp.Result

		return nil
	})

	if err != nil {
		return nil, err
	}

	return results, nil
}

// ResolveIncident updates an incident's state to resolved.
func (c *Client) ResolveIncident(ctx context.Context, sysID string) error {
	endpoint := fmt.Sprintf("%s%s/%s", c.baseURL, c.endpointPath, sysID)

	payload := models.ServiceNowUpdatePayload{
		State:        models.StateResolved,
		CloseCode:    "Solved (Permanently)",
		CloseNotes:   "Alert resolved - condition cleared automatically",
		RootCause:    c.rootCause,
		RestoredDate: time.Now().UTC().Format("01/02/2006 03:04:05 PM"),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal update payload: %w", err)
	}

	c.logger.Debug("resolving incident in ServiceNow",
		"sys_id", sysID,
	)

	return WithRetry(ctx, c.retryConfig, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		c.setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		if err := c.checkResponse(resp); err != nil {
			return err
		}

		return nil
	})
}

// setHeaders sets common headers for ServiceNow API requests.
func (c *Client) setHeaders(req *http.Request) {
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

// checkResponse validates the HTTP response from ServiceNow. Non-2xx responses
// are returned as *RetryableError carrying the status code; whether that status
// is actually worth retrying is decided by IsRetryable, so the classification
// rule lives in exactly one place.
func (c *Client) checkResponse(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)

	c.logger.Error("ServiceNow API error",
		"status_code", resp.StatusCode,
		"response", string(body),
	)

	return &RetryableError{
		Err:        fmt.Errorf("ServiceNow API returned status %d: %s", resp.StatusCode, string(body)),
		StatusCode: resp.StatusCode,
	}
}
