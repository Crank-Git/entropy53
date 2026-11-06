package gateway

import (
	"strings"
	"testing"
)

func TestValidateAcceptsACompleteConfig(t *testing.T) {
	if err := testConfig(t).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsAConfigWithoutARequiredValue(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ZoneConfig)
		want   string
	}{
		{"zone", func(c *ZoneConfig) { c.Zone = "" }, "zone"},
		{"ns", func(c *ZoneConfig) { c.NS = nil }, "NS"},
		{"soa-rname", func(c *ZoneConfig) { c.SOARname = "" }, "SOA RNAME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t)
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate returned nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not name %q", err, tt.want)
			}
		})
	}
}

func TestValidateRejectsMalformedNames(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ZoneConfig)
		want   string
	}{
		{"zone", func(c *ZoneConfig) { c.Zone = "a..b" }, "zone"},
		{"ns", func(c *ZoneConfig) { c.NS = []string{"ns1.example.net.", "a..b"} }, "NS"},
		{"soa-rname", func(c *ZoneConfig) { c.SOARname = "a..b" }, "SOA RNAME"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t)
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate returned nil")
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "a..b") {
				t.Errorf("error %q does not name %q and the bad value", err, tt.want)
			}
		})
	}
}

func TestValidateRejectsANameServerInsideTheZone(t *testing.T) {
	for _, ns := range []string{"NS1.BEACON.EXAMPLE", "beacon.example.", "ns1.sub.beacon.example."} {
		cfg := testConfig(t)
		cfg.NS = []string{"ns1.example.net.", ns}
		err := cfg.Validate()
		if err == nil {
			t.Errorf("Validate accepted the in-zone NS %q", ns)
			continue
		}
		if !strings.Contains(err.Error(), ns) {
			t.Errorf("error %q does not name %q", err, ns)
		}
	}
}

func TestValidateAcceptsANameServerWithTheZoneAsSuffixText(t *testing.T) {
	cfg := testConfig(t)
	cfg.NS = []string{"ns1.xbeacon.example."}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
