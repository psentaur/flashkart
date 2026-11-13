package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flashkart/internal/market"
)

func runLambda(ctx context.Context, address string, handle func(context.Context, []byte) (market.BatchResponse, error)) error {
	if !validRuntimeAddress(address) {
		return fmt.Errorf("invalid Lambda Runtime API address")
	}
	base := "http://" + address + "/2018-06-01/runtime/invocation/"
	client := &http.Client{}
	report := &http.Client{Timeout: 5 * time.Second}
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"next", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
		invocationID := resp.Header.Get("Lambda-Runtime-Invocation-Id")
		body, drainErr := io.ReadAll(io.LimitReader(resp.Body, 7<<20))
		resp.Body.Close()
		if drainErr != nil {
			return drainErr
		}
		if resp.StatusCode != http.StatusOK || id == "" {
			return fmt.Errorf("Lambda invocation polling returned HTTP %d or omitted request ID", resp.StatusCode)
		}
		work, cancel := context.WithTimeout(ctx, 20*time.Second)
		response, handleErr := handle(work, body)
		cancel()
		path := "response"
		var resultBody any = response
		if handleErr != nil {
			path = "error"
			resultBody = map[string]string{"errorType": "Function.InvalidEvent", "errorMessage": "Invalid SQS invocation"}
		}
		encoded, err := json.Marshal(resultBody)
		if err != nil {
			return err
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+url.PathEscape(id)+"/"+path, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if invocationID != "" {
			req.Header.Set("Lambda-Runtime-Invocation-Id", invocationID)
		}
		result, err := report.Do(req)
		if err != nil {
			return err
		}
		result.Body.Close()
		if result.StatusCode != http.StatusAccepted {
			return fmt.Errorf("Lambda invocation response returned HTTP %d", result.StatusCode)
		}
	}
	return nil
}

func validRuntimeAddress(value string) bool {
	return address(value, false) && !strings.ContainsAny(value, "/?#@")
}
