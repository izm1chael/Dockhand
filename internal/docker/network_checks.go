package docker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/dockhand/dockhand/internal/logging"
)

// getContainerIP returns the preferred IP for a container, preferring the primary NetworkSettings
// IPAddress and falling back to the first network endpoint IP if available.
func (s *sdkClient) getContainerIP(st types.ContainerJSON) string {
	if st.NetworkSettings == nil {
		return ""
	}
	ip := st.NetworkSettings.IPAddress
	if ip != "" {
		return ip
	}
	if st.NetworkSettings.Networks != nil {
		for _, n := range st.NetworkSettings.Networks {
			if n.IPAddress != "" {
				return n.IPAddress
			}
		}
	}
	return ""
}

// runNetworkHealthcheck performs HTTP/TCP checks if configured via labels
func (s *sdkClient) runNetworkHealthcheck(ctx context.Context, st types.ContainerJSON, deadline time.Time, interval time.Duration) error {
	ip := s.getContainerIP(st)
	// Check for HTTP Label
	if urlStr, ok := st.Config.Labels["dockhand.check.http"]; ok && urlStr != "" {
		if ip == "" {
			return fmt.Errorf("http check requested but container has no IP address")
		}
		return s.pollNetwork(ctx, "http", urlStr, ip, deadline, interval)
	}

	// Check for TCP Label
	if addrStr, ok := st.Config.Labels["dockhand.check.tcp"]; ok && addrStr != "" {
		if ip == "" {
			return fmt.Errorf("tcp check requested but container has no IP address")
		}
		return s.pollNetwork(ctx, "tcp", addrStr, ip, deadline, interval)
	}

	return nil
}

// pollNetwork loops until the check succeeds or deadline is exceeded
func (s *sdkClient) pollNetwork(ctx context.Context, mode, target, ip string, deadline time.Time, interval time.Duration) error {
	var checkFunc func() error
	var err error
	if mode == "tcp" {
		checkFunc, err = s.buildTCPCheck(ctx, target, ip)
		if err != nil {
			return err
		}
	} else {
		checkFunc, err = s.buildHTTPCheck(ctx, target, ip)
		if err != nil {
			return err
		}
	}

	// Loop until deadline
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := checkFunc(); err == nil {
			logging.Get().Info().Str("mode", mode).Msg("network healthcheck succeeded")
			return nil
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("%s check timed out", mode)
}

// buildTCPCheck constructs a TCP check function for the given target/ip
func (s *sdkClient) buildTCPCheck(ctx context.Context, target, ip string) (func() error, error) {
	address := target
	if !strings.Contains(target, ":") {
		address = net.JoinHostPort(ip, target)
	} else {
		_, port, _ := net.SplitHostPort(target)
		address = net.JoinHostPort(ip, port)
	}
	check := func() error {
		d := net.Dialer{Timeout: 1 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}
	return check, nil
}

// buildHTTPCheck constructs an HTTP check function that rewrites the host to the container IP
func (s *sdkClient) buildHTTPCheck(ctx context.Context, target, ip string) (func() error, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	_, port, _ := net.SplitHostPort(u.Host)
	if port == "" {
		if strings.Contains(u.Host, ":") {
			_, port, _ = net.SplitHostPort(u.Host)
		} else {
			port = "80"
		}
	}
	u.Host = net.JoinHostPort(ip, port)
	finalURL := u.String()

	check := func() error {
		client := http.Client{Timeout: 1 * time.Second}
		req, _ := http.NewRequestWithContext(ctx, "GET", finalURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return nil
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return check, nil
}
