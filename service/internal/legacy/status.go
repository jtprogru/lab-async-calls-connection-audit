package legacy

import (
	"io"
	"net/http"
)

const baseURL = "http://payments.internal"

func Status(id string) (string, error) {
	c := &http.Client{Transport: &http.Transport{MaxIdleConns: 10}}
	resp, err := c.Get(baseURL + "/status/" + id)
	defer resp.Body.Close()
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
