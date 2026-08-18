package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"motd-status-agent/internal/protocol"
)

func TestHandlerReturnsSortedSnapshot(t *testing.T) {
	service := &Service{Collector: CollectFunc(func(context.Context) ([]protocol.Workload, error) {
		return []protocol.Workload{{Name: "z", State: protocol.StateStopped, Health: protocol.HealthNone}, {Name: "a", State: protocol.StateRunning, Health: protocol.HealthHealthy}}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "http://agent/v1/status", nil)
	response := httptest.NewRecorder()
	service.handle(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	var body protocol.StatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Workloads) != 2 || body.Workloads[0].Name != "a" {
		t.Fatalf("unexpected workloads: %+v", body.Workloads)
	}
}

func TestHandlerMapsCollectionErrorsWithoutLeakingDetails(t *testing.T) {
	service := &Service{Collector: CollectFunc(func(context.Context) ([]protocol.Workload, error) {
		return nil, errors.New("secret command output")
	})}
	request := httptest.NewRequest(http.MethodGet, "http://agent/v1/status", nil)
	response := httptest.NewRecorder()
	service.handle(response, request)
	if response.Code != http.StatusInternalServerError || response.Body.String() == "" {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if contains := response.Body.String(); contains == "secret command output" {
		t.Fatal("raw collection error leaked")
	}
}

func TestHandlerTimeoutIsExplicit(t *testing.T) {
	service := &Service{Collector: CollectFunc(func(ctx context.Context) ([]protocol.Workload, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})}
	request := httptest.NewRequest(http.MethodGet, "http://agent/v1/status", nil)
	response := httptest.NewRecorder()
	start := time.Now()
	service.handle(response, request)
	if response.Code != http.StatusGatewayTimeout || time.Since(start) < CollectLimit {
		t.Fatalf("unexpected timeout response: %d", response.Code)
	}
}

func TestHandlerRejectsUnsupportedRequestShape(t *testing.T) {
	service := &Service{Collector: CollectFunc(func(context.Context) ([]protocol.Workload, error) {
		t.Fatal("collector should not be called")
		return nil, nil
	})}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "http://agent/v1/status", nil),
		httptest.NewRequest(http.MethodGet, "http://agent/v1/status?x=1", nil),
	} {
		response := httptest.NewRecorder()
		service.handle(response, request)
		if response.Code != http.StatusMethodNotAllowed && response.Code != http.StatusBadRequest {
			t.Fatalf("unexpected status: %d", response.Code)
		}
	}
}
