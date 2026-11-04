package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func (r *Runner) call(ctx context.Context, path string, value any, result any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.send(ctx, path, encoded, result)
}
func (r *Runner) send(ctx context.Context, path string, body []byte, result any) error {
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		request, err := http.NewRequestWithContext(ctx, "POST", r.config.URL+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+r.config.Token)
		request.Header.Set("Content-Type", "application/json")
		response, err := r.http.Do(request)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 32769))
			response.Body.Close()
			if len(data) > 32768 {
				return fmt.Errorf("bridge response exceeds 32768 bytes")
			}
			if readErr != nil {
				last = readErr
			} else if response.StatusCode >= 200 && response.StatusCode < 300 {
				return json.Unmarshal(data, result)
			} else {
				var problem struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(data, &problem)
				last = fmt.Errorf("bridge returned HTTP %d (%s)", response.StatusCode, problem.Error.Code)
				if response.StatusCode != 429 && response.StatusCode != 502 && response.StatusCode != 503 && response.StatusCode != 504 {
					return last
				}
			}
		} else {
			last = err
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return last
}
