package payments

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const baseURL = "http://payments.internal"

// Client общий на весь процесс: один пул, таймаут 3 секунды.
var Client = &http.Client{Timeout: 3 * time.Second}

func Charge(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/charge",
		strings.NewReader(`{"order_id":"`+id+`"}`))
	if err != nil {
		return err
	}
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("charge: %s", resp.Status)
	}
	return nil
}
