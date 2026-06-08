package http

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

// Client handles HTTP requests to the API
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new HTTP client
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetBaseURL updates the base URL
func (c *Client) SetBaseURL(url string) {
	c.baseURL = strings.TrimSuffix(url, "/")
}

// Request represents an API request
type Request struct {
	Method      string
	Path        string
	QueryParams map[string]string
	Headers     map[string]string
	Body        []byte
}

// Response represents an API response
type Response struct {
	StatusCode int
	Status     string
	Headers    http.Header
	Body       []byte
	Duration   time.Duration
}

// Do executes an HTTP request
func (c *Client) Do(ctx context.Context, req *Request) (*Response, error) {
	// Build full URL
	url := c.baseURL + req.Path

	// Add query parameters
	if len(req.QueryParams) > 0 {
		params := make([]string, 0, len(req.QueryParams))
		for k, v := range req.QueryParams {
			params = append(params, fmt.Sprintf("%s=%s", k, v))
		}
		url += "?" + strings.Join(params, "&")
	}

	// Create request body
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set default headers
	httpReq.Header.Set("Accept", "application/json")
	if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	// Set custom headers
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Execute request
	start := time.Now()
	httpResp, err := c.httpClient.Do(httpReq)
	duration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return &Response{
		StatusCode: httpResp.StatusCode,
		Status:     httpResp.Status,
		Headers:    httpResp.Header,
		Body:       respBody,
		Duration:   duration,
	}, nil
}

// Get performs a GET request
func (c *Client) Get(ctx context.Context, path string, queryParams map[string]string) (*Response, error) {
	return c.Do(ctx, &Request{
		Method:      "GET",
		Path:        path,
		QueryParams: queryParams,
	})
}

// Post performs a POST request
func (c *Client) Post(ctx context.Context, path string, body any) (*Response, error) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling body: %w", err)
		}
	}

	return c.Do(ctx, &Request{
		Method: "POST",
		Path:   path,
		Body:   bodyBytes,
	})
}

// Put performs a PUT request
func (c *Client) Put(ctx context.Context, path string, body any) (*Response, error) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling body: %w", err)
		}
	}

	return c.Do(ctx, &Request{
		Method: "PUT",
		Path:   path,
		Body:   bodyBytes,
	})
}

// Patch performs a PATCH request
func (c *Client) Patch(ctx context.Context, path string, body any) (*Response, error) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling body: %w", err)
		}
	}

	return c.Do(ctx, &Request{
		Method: "PATCH",
		Path:   path,
		Body:   bodyBytes,
	})
}

// Delete performs a DELETE request
func (c *Client) Delete(ctx context.Context, path string) (*Response, error) {
	return c.Do(ctx, &Request{
		Method: "DELETE",
		Path:   path,
	})
}

// FormatJSON formats a JSON byte slice for display
func FormatJSON(data []byte) (string, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data), nil // Return as-is if not valid JSON
	}

	formatted, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(data), nil
	}

	return string(formatted), nil
}

// IsSuccess checks if a status code indicates success
func IsSuccess(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
