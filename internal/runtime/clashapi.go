package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ClashAPI struct {
	baseURL string
	secret  string
	client  *http.Client
}

func NewClashAPI(baseURL, secret string) *ClashAPI {
	return &ClashAPI{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		client:  &http.Client{},
	}
}

func (c *ClashAPI) SwitchSelector(group, name string) error {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, c.baseURL+"/proxies/"+group, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if len(msg) == 0 {
			return fmt.Errorf("clash api switch selector failed: status %d", resp.StatusCode)
		}
		return fmt.Errorf("clash api switch selector failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
