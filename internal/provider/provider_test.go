package provider

import (
	"strings"
	"testing"
)

func TestGenerateSupportsCountryAliasAndCountrySpecificCredentialRule(t *testing.T) {
	d := Definition{
		ID: "residential-a", Enabled: true, Weight: 50,
		Secrets: map[string]string{"password": "encrypted-at-rest-value"},
		Config: Config{
			Protocol:       "socks5",
			Host:           ValueRule{Default: "gateway.example.net"},
			Port:           PortRule{Default: 9595},
			Username:       ValueRule{Default: "account-res-{{country}}-sid-{{session}}", ByCountry: map[string]string{"us": "account-state-{{country}}_{{state}}-sid-{{session}}"}},
			Password:       ValueRule{Default: "{{secret.password}}"},
			CountryAliases: map[string]string{"gb": "uk"},
			Session:        SessionRule{Type: "int", Min: 1, Max: 1000000},
		},
	}
	us, err := Generate(d, Request{Country: "US", State: "New York"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(us.Username, "state-us_new_york") || us.Password != "encrypted-at-rest-value" {
		t.Fatalf("unexpected US endpoint: %#v", us)
	}
	uk, err := Generate(d, Request{Country: "GB"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uk.Username, "res-uk-") {
		t.Fatalf("country alias was not applied: %q", uk.Username)
	}
}

func TestGenerateSupportsCountryHostPortRangeAndTemplatedPassword(t *testing.T) {
	d := Definition{
		ID: "residential-b", Enabled: true, Weight: 50,
		Secrets: map[string]string{"account": "acct", "passwordPrefix": "prefix"},
		Config: Config{
			Protocol: "socks5",
			Host:     ValueRule{Default: "{{country}}.gateway.example.net"},
			Port:     PortRule{ByCountry: map[string]PortRange{"fr": {From: 40001, To: 49999}}},
			Username: ValueRule{Default: "{{secret.account}}"},
			Password: ValueRule{Default: "{{secret.passwordPrefix}}-{{country}}-{{session}}-{{duration}}m", WithState: "{{secret.passwordPrefix}}-{{country}}_{{state}}-{{session}}-{{duration}}m"},
			Session:  SessionRule{Type: "uuid"}, SessionMinutes: 30,
		},
	}
	endpoint, err := Generate(d, Request{Country: "fr", State: "Ile de France"})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Host != "fr.gateway.example.net" || endpoint.Port < 40001 || endpoint.Port > 49999 {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
	if !strings.HasPrefix(endpoint.Password, "prefix-fr_ile_de_france-") || !strings.HasSuffix(endpoint.Password, "-30m") {
		t.Fatalf("unexpected generated password")
	}
}

func TestGenerateUsesWeightedStateFallbackAndRejectsMissingSecret(t *testing.T) {
	d := Definition{
		ID: "residential-c", Enabled: true, Weight: 1,
		Config: Config{
			Protocol: "socks5", Host: ValueRule{Default: "gateway.example.net"}, Port: PortRule{Default: 1080},
			Username: ValueRule{Default: "user-{{state}}-{{session}}"}, Password: ValueRule{Default: "{{secret.password}}"},
			StateFallbackWeights: map[string]float64{"california": 1}, Session: SessionRule{Type: "alnum", Length: 8},
		},
	}
	if _, err := Generate(d, Request{Country: "us"}); err == nil || !strings.Contains(err.Error(), "missing provider secret") {
		t.Fatalf("expected missing secret error, got %v", err)
	}
	d.Secrets = map[string]string{"password": "secret"}
	endpoint, err := Generate(d, Request{Country: "us"})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.State != "california" || len(endpoint.SessionID) != 8 {
		t.Fatalf("unexpected fallback endpoint: %#v", endpoint)
	}
}

func TestSelectWeightedIgnoresDisabledAndZeroWeight(t *testing.T) {
	selected, err := SelectWeighted([]Definition{{ID: "disabled", Enabled: false, Weight: 100}, {ID: "zero", Enabled: true, Weight: 0}, {ID: "selected", Enabled: true, Weight: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "selected" {
		t.Fatalf("selected %q", selected.ID)
	}
}
