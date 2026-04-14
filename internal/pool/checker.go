package pool

import (
	"net/http"
	"net/url"
	"time"

	"proxypools/internal/model"
)

type SelectorSwitcher interface {
	SwitchSelector(group, name string) error
}

type CheckerConfig struct {
	ProbeURL         string
	HealthProxyURL   string
	SelectorSwitcher SelectorSwitcher
}

type Checker struct {
	client   *http.Client
	probeURL string
	switcher SelectorSwitcher
}

func NewChecker(cfg CheckerConfig) *Checker {
	transport := &http.Transport{}
	if cfg.HealthProxyURL != "" {
		proxyURL, _ := url.Parse(cfg.HealthProxyURL)
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	return &Checker{
		client:   &http.Client{Timeout: 10 * time.Second, Transport: transport},
		probeURL: cfg.ProbeURL,
		switcher: cfg.SelectorSwitcher,
	}
}

func (c *Checker) ProbeNode(outboundTag string, in model.NodeRuntimeStatus) (model.NodeRuntimeStatus, error) {
	if c.switcher != nil {
		if err := c.switcher.SwitchSelector("health-check", outboundTag); err != nil {
			in.ConsecutiveFailures++
			in.State = "cooldown"
			return in, err
		}
	}

	started := time.Now()
	resp, err := c.client.Get(c.probeURL)
	if err != nil {
		in.ConsecutiveFailures++
		in.State = "cooldown"
		return in, err
	}
	defer resp.Body.Close()

	in.LatencyMS = int(time.Since(started).Milliseconds())
	if in.LatencyMS <= 0 {
		in.LatencyMS = 1
	}
	in.RecentSuccessRate = 1
	in.ConsecutiveFailures = 0
	in.State = "active"
	return in, nil
}
