package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"/": "root", "C:": "c", "/Volumes/My Disk": "volumes_my_disk", "Tp0D": "tp0d"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTempLabelsAndKinds(t *testing.T) {
	if got := tempLabel("k10temp_tctl", 1); got != "CPU tctl" {
		t.Errorf("tempLabel = %q", got)
	}
	if got := tempLabel("nvme_composite", 2); got != "NVMe 2 composite" {
		t.Errorf("tempLabel = %q", got)
	}
	for key, want := range map[string]string{"k10temp_tctl": "cpu", "nvme_composite": "ssd", "amdgpu_edge": "gpu", "acpitz": ""} {
		if got := tempKind(key); got != want {
			t.Errorf("tempKind(%q) = %q, want %q", key, got, want)
		}
	}
}

// Dashboards may connect from the local network; arbitrary websites may not.
func TestOriginAllowed(t *testing.T) {
	cases := map[string]bool{
		"":                         true,
		"http://192.168.1.20:7878": true,
		"http://pulsedeck.app":     true,
		"http://localhost:3000":    true,
		"https://example.com":      false,
		"https://evil.example":     false,
	}
	for origin, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "http://10.0.0.5:7878/ws", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if got := originAllowed(r); got != want {
			t.Errorf("originAllowed(%q) = %v, want %v", origin, got, want)
		}
	}
}

// The settings page must answer only to a browser on this computer.
func TestLocalOnly(t *testing.T) {
	ok := localOnly(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	try := func(method, remote, host string, headers map[string]string) int {
		r := httptest.NewRequest(method, "http://"+host+"/api/admin/state", nil)
		r.RemoteAddr, r.Host = remote, host
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		ok(w, r)
		return w.Code
	}
	admin := map[string]string{"X-Pulsedeck-Admin": "1"}
	if c := try("GET", "127.0.0.1:5000", "localhost:7878", nil); c != 200 {
		t.Errorf("local GET = %d", c)
	}
	if c := try("GET", "192.168.1.9:5000", "192.168.1.20:7878", nil); c != 403 {
		t.Errorf("LAN GET = %d, want 403", c)
	}
	if c := try("GET", "127.0.0.1:5000", "evil.example", nil); c != 403 {
		t.Errorf("rebinding GET = %d, want 403", c)
	}
	if c := try("POST", "127.0.0.1:5000", "localhost:7878", nil); c != 403 {
		t.Errorf("POST without header = %d, want 403", c)
	}
	if c := try("POST", "127.0.0.1:5000", "localhost:7878", admin); c != 200 {
		t.Errorf("local POST = %d", c)
	}
	if c := try("POST", "127.0.0.1:5000", "localhost:7878", map[string]string{"X-Pulsedeck-Admin": "1", "Origin": "https://evil.example"}); c != 403 {
		t.Errorf("cross-origin POST = %d, want 403", c)
	}
}
