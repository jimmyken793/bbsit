package health

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/kingyoung/bbsit/internal/types"
)

// Check performs a health check with retries.
func Check(healthType types.HealthType, target string, timeout time.Duration, maxRetries int) error {
	switch healthType {
	case types.HealthNone:
		return nil
	case types.HealthHTTP:
		return checkHTTP(target, timeout, maxRetries)
	case types.HealthTCP:
		return checkTCP(target, timeout, maxRetries)
	default:
		return fmt.Errorf("unknown health type: %s", healthType)
	}
}

func checkHTTP(url string, timeout time.Duration, maxRetries int) error {
	client := &http.Client{Timeout: 10 * time.Second}

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(timeout / time.Duration(maxRetries))
		}

		resp, err := client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: %w", i+1, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
		lastErr = fmt.Errorf("attempt %d: status %d", i+1, resp.StatusCode)
	}

	return fmt.Errorf("health check failed after %d attempts: %w", maxRetries, lastErr)
}

func checkTCP(addr string, timeout time.Duration, maxRetries int) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			time.Sleep(timeout / time.Duration(maxRetries))
		}

		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			lastErr = fmt.Errorf("attempt %d: %w", i+1, err)
			continue
		}
		conn.Close()
		return nil
	}

	return fmt.Errorf("tcp check failed after %d attempts: %w", maxRetries, lastErr)
}
