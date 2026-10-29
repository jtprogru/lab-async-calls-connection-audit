package checkout

import (
	"fmt"
	"net/http"
	"strings"
)

// Refund отправляет возврат через DefaultClient: без таймаута и контекста.
func Refund(id string) error {
	resp, err := http.Post("http://payments.internal/refund", "application/json",
		strings.NewReader(`{"order_id":"`+id+`"}`))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refund: %s", resp.Status)
	}
	return nil
}
