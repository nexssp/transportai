package ta2a

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

func (t *Transport) dispatchWebhook(ctx context.Context, url string, task Task) {
	data, err := json.Marshal(task)
	if err != nil {
		return
	}

	client := t.webhookClient
	if client == nil {
		client = http.DefaultClient
	}

	baseCtx := context.WithoutCancel(ctx)

	for attempt := 1; attempt <= 3; attempt++ {
		reqCtx, cancel := context.WithTimeout(baseCtx, 5*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(data)) //nolint:gosec // webhook URL provided by the A2A client who requests delivery
		if err != nil {
			cancel()
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(headerA2AAttempt, strconv.Itoa(attempt))

		if t.webhookSecret != "" {
			mac := hmac.New(sha256.New, []byte(t.webhookSecret))
			mac.Write(data)
			req.Header.Set(headerA2ASignature, "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}

		resp, err := client.Do(req) //nolint:gosec // req.URL is the client-supplied callback URL for this A2A task
		cancel()
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = resp.Body.Close()
			return
		}
		if resp != nil {
			_ = resp.Body.Close()
		}

		if attempt < 3 {
			time.Sleep(time.Duration(attempt*50) * time.Millisecond)
		}
	}

	slog.Warn("a2a_webhook_delivery_exhausted", "url", url, "task_id", task.ID)
}
