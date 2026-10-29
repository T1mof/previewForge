package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPResponseIsNeverNetworkPolicyDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	if err := run(context.Background(), []string{"http-denied", server.URL}, &bytes.Buffer{}); err == nil {
		t.Fatal("HTTP 403 was mistaken for network blocking")
	}
	if err := run(context.Background(), []string{"http-denied", ":bad-url"}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid URL was mistaken for network blocking")
	}
}

func TestSuccessfulHTTPRequiresStatusBodyAndHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "preview.example.test" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(witness))
	}))
	defer server.Close()
	if err := run(context.Background(), []string{"http", server.URL, witness, "preview.example.test"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"http", server.URL, "wrong-body", "preview.example.test"}, &bytes.Buffer{}); err == nil {
		t.Fatal("wrong server body was accepted")
	}
}
