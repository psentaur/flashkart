package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func runLambda(ctx context.Context, address string, log *slog.Logger) error {
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
		_, drainErr := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if drainErr != nil {
			return drainErr
		}
		if resp.StatusCode != http.StatusOK || id == "" {
			return fmt.Errorf("Lambda invocation polling returned HTTP %d or omitted request ID", resp.StatusCode)
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+url.PathEscape(id)+"/error",
			strings.NewReader(`{"errorType":"NOT_IMPLEMENTED","errorMessage":"Notification handler is pending"}`))
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
			return fmt.Errorf("Lambda invocation error reporting returned HTTP %d", result.StatusCode)
		}
		log.Info("invocation marked failed", "request_id", id)
	}
	return nil
}

func validRuntimeAddress(value string) bool {
	return address(value, false) && !strings.ContainsAny(value, "/?#@")
}
