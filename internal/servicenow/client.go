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

// FindIncidentByCorrelationID searches for an existing incident by correlation ID.
func (c *Client) FindIncidentByCorrelationID(ctx context.Context, correlationID string) (*models.ServiceNowResult, error) {
	// Build query URL with correlation_id filter
	endpoint := fmt.Sprintf("%s%s?sysparm_query=correlation_id=%s&sysparm_limit=1",
		c.baseURL, c.endpointPath, url.QueryEscape(correlationID))

	c.logger.Debug("searching for incident by correlation_id",
		"correlation_id", correlationID,
	)

	var result *models.ServiceNowResult

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

		if len(listResp.Result) > 0 {
			result = &listResp.Result[0]
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return result, nil
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

// checkResponse validates the HTTP response from ServiceNow.
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
