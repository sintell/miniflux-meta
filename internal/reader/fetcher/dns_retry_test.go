// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fetcher // import "miniflux.app/v2/internal/reader/fetcher"

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDialWithDNSRetries(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		attempts int
	}{
		{"DNS timeout", &net.DNSError{IsTimeout: true}, 3},
		{"temporary DNS failure", &net.OpError{Op: "dial", Err: &net.DNSError{IsTemporary: true}}, 3},
		{"hostname not found", &net.DNSError{IsNotFound: true}, 1},
		{"permanent DNS failure", &net.DNSError{Err: "invalid hostname"}, 1},
		{"TCP timeout", &net.OpError{Op: "dial", Err: context.DeadlineExceeded}, 1},
		{"private network blocked", ErrPrivateNetworkHost, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			_, err := dialWithDNSRetries(context.Background(), "tcp", "example.org:443", func(context.Context, string, string) (net.Conn, error) {
				attempts++
				return nil, tt.err
			})
			if !errors.Is(err, tt.err) || attempts != tt.attempts {
				t.Fatalf("Expected %d attempts and error %v, got %d attempts and error %v", tt.attempts, tt.err, attempts, err)
			}
		})
	}
}

func TestDialWithDNSRetriesRecovers(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	attempts := 0
	conn, err := dialWithDNSRetries(context.Background(), "tcp", "example.org:443", func(_ context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" || addr != "example.org:443" {
			t.Fatalf("Unexpected dial arguments: %s %s", network, addr)
		}
		attempts++
		if attempts == 1 {
			return nil, &net.DNSError{IsTimeout: true}
		}
		return client, nil
	})
	if err != nil || conn != client || attempts != 2 {
		t.Fatalf("Expected successful connection after two attempts, got %v after %d attempts", err, attempts)
	}
}

func TestDialWithDNSRetriesDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	attempts := 0
	_, err := dialWithDNSRetries(ctx, "tcp", "example.org:443", func(context.Context, string, string) (net.Conn, error) {
		attempts++
		return nil, &net.DNSError{IsTimeout: true}
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatalf("Expected deadline to stop retry backoff after one attempt, got %v after %d attempts", err, attempts)
	}
}

func TestDialWithDNSRetriesCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dialWithDNSRetries(ctx, "tcp", "example.org:443", func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("Cancelled context should not start dialing")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected cancellation, got %v", err)
	}
}
