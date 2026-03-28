package types

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		input    string
		wantAddr string
		wantPort int
		wantErr  bool
	}{
		{"8080", "", 8080, false},
		{"127.0.0.1:8080", "127.0.0.1", 8080, false},
		{"0.0.0.0:3000", "0.0.0.0", 3000, false},
		{"localhost:9090", "localhost", 9090, false},
		{"", "", 0, true},
		{"not-a-port", "", 0, true},
	}
	for _, tc := range tests {
		pm := PortMapping{HostPort: tc.input}
		addr, port, err := pm.ParseHostPort()
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseHostPort(%q) expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHostPort(%q) error: %v", tc.input, err)
			continue
		}
		if addr != tc.wantAddr || port != tc.wantPort {
			t.Errorf("ParseHostPort(%q) = (%q, %d), want (%q, %d)", tc.input, addr, port, tc.wantAddr, tc.wantPort)
		}
	}
}

func TestPortMapping_UnmarshalJSON_String(t *testing.T) {
	data := `{"host_port": "localhost:8080", "container_port": 80}`
	var pm PortMapping
	if err := json.Unmarshal([]byte(data), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pm.HostPort != "localhost:8080" {
		t.Errorf("HostPort = %q, want %q", pm.HostPort, "localhost:8080")
	}
	if pm.ContainerPort != 80 {
		t.Errorf("ContainerPort = %d, want 80", pm.ContainerPort)
	}
}

func TestPortMapping_UnmarshalJSON_LegacyInt(t *testing.T) {
	data := `{"host_port": 8080, "container_port": 80, "protocol": "tcp"}`
	var pm PortMapping
	if err := json.Unmarshal([]byte(data), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pm.HostPort != "8080" {
		t.Errorf("HostPort = %q, want %q", pm.HostPort, "8080")
	}
}

func TestPortMapping_UnmarshalYAML_String(t *testing.T) {
	data := "host_port: \"localhost:8080\"\ncontainer_port: 80\n"
	var pm PortMapping
	if err := yaml.Unmarshal([]byte(data), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pm.HostPort != "localhost:8080" {
		t.Errorf("HostPort = %q, want %q", pm.HostPort, "localhost:8080")
	}
}

func TestPortMapping_UnmarshalYAML_LegacyInt(t *testing.T) {
	data := "host_port: 8080\ncontainer_port: 80\n"
	var pm PortMapping
	if err := yaml.Unmarshal([]byte(data), &pm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pm.HostPort != "8080" {
		t.Errorf("HostPort = %q, want %q", pm.HostPort, "8080")
	}
}
