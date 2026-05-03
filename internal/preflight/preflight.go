package preflight

import (
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/config"
	"github.com/Enriquefft/openclaw-kapso-whatsapp/internal/kapso"
	"github.com/gorilla/websocket"
)

// Result represents the outcome of a single preflight check.
type Result struct {
	Name   string // e.g. "Config loaded"
	Status string // "OK", "WARN", "FAIL"
	Detail string // human-readable detail
}

// Options allows injecting dependencies for testing.
type Options struct {
	KapsoClient   *kapso.Client          // if nil, created from config
	GatewayDialer func(url string) error // if nil, uses default WebSocket dial
}

// Run executes all preflight checks and writes status lines to w.
// Returns a non-nil error if any check has Status "FAIL".
func Run(cfg *config.Config, w io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	var results []Result

	results = append(results, checkConfig(cfg))
	results = append(results, checkEnvVars(cfg)...)
	results = append(results, checkKapsoCredentials(cfg, opts.KapsoClient))
	results = append(results, checkGateway(cfg, opts.GatewayDialer))

	hasFail := false
	for _, r := range results {
		_, _ = fmt.Fprintf(w, "  [%-4s]  %s", r.Status, r.Name)
		if r.Detail != "" {
			_, _ = fmt.Fprintf(w, " -- %s", r.Detail)
		}
		_, _ = fmt.Fprintln(w)
		if r.Status == "FAIL" {
			hasFail = true
		}
	}

	if hasFail {
		return fmt.Errorf("one or more preflight checks failed")
	}
	return nil
}

func checkConfig(cfg *config.Config) Result {
	return Result{
		Name:   "Config loaded",
		Status: "OK",
		Detail: fmt.Sprintf("mode=%s", cfg.Delivery.Mode),
	}
}

func checkEnvVars(cfg *config.Config) []Result {
	var results []Result

	if cfg.Kapso.APIKey != "" {
		results = append(results, Result{Name: "KAPSO_API_KEY", Status: "OK"})
	} else {
		results = append(results, Result{Name: "KAPSO_API_KEY", Status: "FAIL", Detail: "not set"})
	}

	if cfg.Kapso.PhoneNumberID != "" {
		results = append(results, Result{Name: "KAPSO_PHONE_NUMBER_ID", Status: "OK"})
	} else {
		results = append(results, Result{Name: "KAPSO_PHONE_NUMBER_ID", Status: "FAIL", Detail: "not set"})
	}

	return results
}

func checkKapsoCredentials(cfg *config.Config, client *kapso.Client) Result {
	if cfg.Kapso.APIKey == "" || cfg.Kapso.PhoneNumberID == "" {
		return Result{
			Name:   "Kapso credentials",
			Status: "FAIL",
			Detail: "skipped (missing API key or phone number ID)",
		}
	}

	if client == nil {
		client = kapso.NewClient(cfg.Kapso.APIKey, cfg.Kapso.PhoneNumberID)
	}

	_, err := client.ListMessages(kapso.ListMessagesParams{Limit: 1})
	if err != nil {
		return Result{
			Name:   "Kapso credentials",
			Status: "FAIL",
			Detail: err.Error(),
		}
	}

	return Result{
		Name:   "Kapso credentials",
		Status: "OK",
		Detail: "API key valid",
	}
}

func checkGateway(cfg *config.Config, dialFn func(string) error) Result {
	gwURL := cfg.Gateway.URL
	if gwURL == "" {
		return Result{
			Name:   "Gateway connectivity",
			Status: "WARN",
			Detail: "no gateway URL configured",
		}
	}

	u, err := url.Parse(gwURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return Result{
			Name:   "Gateway connectivity",
			Status: "FAIL",
			Detail: fmt.Sprintf("invalid gateway URL: %s", gwURL),
		}
	}

	if dialFn != nil {
		err = dialFn(gwURL)
	} else {
		err = defaultGatewayDial(gwURL)
	}

	if err != nil {
		// Trim common verbose prefixes for cleaner output.
		msg := err.Error()
		if idx := strings.LastIndex(msg, ": "); idx != -1 {
			msg = msg[idx+2:]
		}
		return Result{
			Name:   "Gateway connectivity",
			Status: "FAIL",
			Detail: fmt.Sprintf("%s -- %s", gwURL, msg),
		}
	}

	return Result{
		Name:   "Gateway connectivity",
		Status: "OK",
		Detail: gwURL,
	}
}

func defaultGatewayDial(gwURL string) error {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(gwURL, nil)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
