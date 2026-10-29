// probe is a disposable e2e client/server, not part of the operator installation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const witness = "previewforge-network-witness"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		args = []string{"serve", "8080", "8081"}
	}
	if args[0] == "hold" {
		<-ctx.Done()
		return nil
	}
	if args[0] == "serve" {
		return serve(ctx, args[1:])
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := map[string]any{"operation": args[0]}
	switch args[0] {
	case "http", "http-denied":
		if len(args) < 2 {
			return fmt.Errorf("http requires URL")
		}
		result["target"] = args[1]
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, args[1], nil)
		if err != nil {
			return err
		}
		if len(args) > 3 {
			req.Host = args[3]
		}
		transport := &http.Transport{DisableKeepAlives: true, Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(req)
		if args[0] == "http-denied" {
			if response != nil {
				response.Body.Close()
				return fmt.Errorf("unexpected HTTP response: %s", response.Status)
			}
			if !networkFailure(err) {
				return fmt.Errorf("expected network failure, got %v", err)
			}
			result["blocked"], result["error"] = true, err.Error()
		} else {
			if err != nil {
				return err
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
			if err != nil {
				return err
			}
			if response.StatusCode != 200 || len(args) > 2 && !strings.Contains(string(body), args[2]) {
				return fmt.Errorf("unexpected HTTP response %s: %s", response.Status, body)
			}
			result["status"], result["body"] = response.StatusCode, string(body)
		}
	case "tcp", "tcp-denied":
		if len(args) != 2 {
			return fmt.Errorf("tcp requires address:port")
		}
		result["target"] = args[1]
		connection, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", args[1])
		if connection != nil {
			connection.Close()
		}
		if args[0] == "tcp-denied" {
			if !networkFailure(err) {
				return fmt.Errorf("expected network failure, got %v", err)
			}
			result["blocked"], result["error"] = true, err.Error()
		} else if err != nil {
			return err
		}
	case "dns":
		if len(args) != 4 || args[1] != "udp" && args[1] != "tcp" {
			return fmt.Errorf("dns requires udp|tcp DNS-IP hostname")
		}
		ip := net.ParseIP(args[2])
		if ip == nil {
			return fmt.Errorf("DNS server must be a literal IP")
		}
		resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, args[1], net.JoinHostPort(ip.String(), "53"))
		}}
		addresses, err := resolver.LookupHost(ctx, args[3])
		if err != nil || len(addresses) == 0 {
			return fmt.Errorf("DNS query failed: %v", err)
		}
		result["protocol"], result["server"], result["name"], result["addresses"] = args[1], args[2], args[3], addresses
	case "resolve":
		if len(args) != 2 {
			return fmt.Errorf("resolve requires hostname")
		}
		addresses, err := net.DefaultResolver.LookupIP(ctx, "ip4", args[1])
		if err != nil || len(addresses) == 0 {
			return fmt.Errorf("resolve failed: %v", err)
		}
		result["address"] = addresses[0].String()
	default:
		return fmt.Errorf("unknown operation %q", args[0])
	}
	result["ok"] = true
	return json.NewEncoder(out).Encode(result)
}

func networkFailure(err error) bool {
	if err == nil {
		return false
	}
	var op *net.OpError
	return errors.As(err, &op) || errors.Is(err, context.DeadlineExceeded)
}

func serve(ctx context.Context, ports []string) error {
	if len(ports) == 0 {
		return fmt.Errorf("serve requires ports")
	}
	servers := make([]*http.Server, 0, len(ports))
	errorsCh := make(chan error, len(ports))
	defer func() {
		for _, server := range servers {
			server.Close()
		}
	}()
	for _, port := range ports {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port %q", port)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("", port))
		if err != nil {
			return err
		}
		server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, witness) })}
		servers = append(servers, server)
		go func() { errorsCh <- server.Serve(listener) }()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-errorsCh:
		return err
	}
}
