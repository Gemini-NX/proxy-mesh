package provider

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Definition struct {
	ID      string            `json:"id"`
	Enabled bool              `json:"enabled"`
	Weight  int               `json:"weight"`
	Config  Config            `json:"config"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

type Config struct {
	Protocol               string                        `json:"protocol"`
	Host                   ValueRule                     `json:"host"`
	Port                   PortRule                      `json:"port"`
	Username               ValueRule                     `json:"username"`
	Password               ValueRule                     `json:"password"`
	CountryAliases         map[string]string             `json:"countryAliases,omitempty"`
	StateFallbackWeights   map[string]float64            `json:"stateFallbackWeights,omitempty"`
	StateFallbackByCountry map[string]map[string]float64 `json:"stateFallbackByCountry,omitempty"`
	Session                SessionRule                   `json:"session"`
	SessionMinutes         int                           `json:"sessionMinutes,omitempty"`
	RouteTTLMinutes        int                           `json:"routeTTLMinutes,omitempty"`
}

type ValueRule struct {
	Default   string            `json:"default"`
	WithState string            `json:"withState,omitempty"`
	WithCity  string            `json:"withCity,omitempty"`
	ByCountry map[string]string `json:"byCountry,omitempty"`
}

type PortRule struct {
	Default   int                  `json:"default,omitempty"`
	ByCountry map[string]PortRange `json:"byCountry,omitempty"`
}

type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type SessionRule struct {
	Type   string `json:"type"`
	Min    int64  `json:"min,omitempty"`
	Max    int64  `json:"max,omitempty"`
	Length int    `json:"length,omitempty"`
}

type Request struct {
	Country string `json:"country"`
	State   string `json:"state,omitempty"`
	City    string `json:"city,omitempty"`
}

type Endpoint struct {
	ProviderID string
	Protocol   string
	Host       string
	Port       int
	Username   string
	Password   string
	Country    string
	State      string
	City       string
	SessionID  string
}

var (
	validID          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	secretExpression = regexp.MustCompile(`\{\{secret\.([A-Za-z0-9_.-]+)\}\}`)
	anyExpression    = regexp.MustCompile(`\{\{[^{}]+\}\}`)
)

func Validate(d Definition) error {
	if !validID.MatchString(d.ID) {
		return errors.New("provider id must be 1-64 lowercase letters, digits, dots, underscores or hyphens")
	}
	if d.Weight < 0 || d.Weight > 10000 {
		return errors.New("provider weight must be between 0 and 10000")
	}
	if !strings.EqualFold(d.Config.Protocol, "socks5") {
		return errors.New("only socks5 providers are supported")
	}
	if d.Config.Host.Default == "" && len(d.Config.Host.ByCountry) == 0 {
		return errors.New("provider host rule is required")
	}
	for _, template := range templates(d.Config.Password) {
		if template != "" && !strings.Contains(template, "{{secret.") {
			return errors.New("provider password templates must reference an encrypted secret")
		}
	}
	for _, rule := range []ValueRule{d.Config.Host, d.Config.Username, d.Config.Password} {
		for _, template := range templates(rule) {
			for _, match := range secretExpression.FindAllStringSubmatch(template, -1) {
				if _, ok := d.Secrets[match[1]]; !ok {
					return fmt.Errorf("missing provider secret %q", match[1])
				}
			}
		}
	}
	if err := validatePortRule(d.Config.Port); err != nil {
		return err
	}
	switch d.Config.Session.Type {
	case "uuid", "int", "alnum":
	default:
		return errors.New("session.type must be uuid, int or alnum")
	}
	if d.Config.Session.Type == "int" && (d.Config.Session.Min < 0 || d.Config.Session.Max <= d.Config.Session.Min) {
		return errors.New("integer session range is invalid")
	}
	if d.Config.Session.Type == "alnum" && (d.Config.Session.Length < 1 || d.Config.Session.Length > 128) {
		return errors.New("alnum session length must be between 1 and 128")
	}
	if d.Config.SessionMinutes < 0 || d.Config.RouteTTLMinutes < 0 {
		return errors.New("sessionMinutes and routeTTLMinutes must not be negative")
	}
	return nil
}

func templates(rule ValueRule) []string {
	out := []string{rule.Default, rule.WithState, rule.WithCity}
	for _, value := range rule.ByCountry {
		out = append(out, value)
	}
	return out
}

func Generate(d Definition, req Request) (Endpoint, error) {
	if err := Validate(d); err != nil {
		return Endpoint{}, err
	}
	country := normalizeCountry(req.Country)
	if alias := d.Config.CountryAliases[country]; alias != "" {
		country = normalizeCountry(alias)
	}
	if country == "" {
		return Endpoint{}, errors.New("country is required")
	}
	state, city := normalizeLocation(req.State), normalizeLocation(req.City)
	stateWeights := d.Config.StateFallbackByCountry[country]
	if len(stateWeights) == 0 {
		stateWeights = d.Config.StateFallbackWeights
	}
	if state == "" && len(stateWeights) > 0 {
		var err error
		state, err = weightedString(stateWeights)
		if err != nil {
			return Endpoint{}, err
		}
	}
	session, err := generateSession(d.Config.Session)
	if err != nil {
		return Endpoint{}, err
	}
	vars := map[string]string{"country": country, "state": state, "city": city, "session": session, "duration": fmt.Sprint(d.Config.SessionMinutes)}
	host, err := render(selectTemplate(d.Config.Host, country, state, city), vars, d.Secrets)
	if err != nil {
		return Endpoint{}, fmt.Errorf("render host: %w", err)
	}
	username, err := render(selectTemplate(d.Config.Username, country, state, city), vars, d.Secrets)
	if err != nil {
		return Endpoint{}, fmt.Errorf("render username: %w", err)
	}
	password, err := render(selectTemplate(d.Config.Password, country, state, city), vars, d.Secrets)
	if err != nil {
		return Endpoint{}, fmt.Errorf("render password: %w", err)
	}
	port, err := selectPort(d.Config.Port, country)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{ProviderID: d.ID, Protocol: "socks5", Host: host, Port: port, Username: username, Password: password, Country: country, State: state, City: city, SessionID: session}, nil
}

func SelectWeighted(definitions []Definition) (Definition, error) {
	enabled := make([]Definition, 0, len(definitions))
	var total int64
	for _, d := range definitions {
		if d.Enabled && d.Weight > 0 {
			enabled = append(enabled, d)
			total += int64(d.Weight)
		}
	}
	if total == 0 {
		return Definition{}, errors.New("no enabled weighted proxy provider")
	}
	n, err := cryptoInt(0, total-1)
	if err != nil {
		return Definition{}, err
	}
	for _, d := range enabled {
		n -= int64(d.Weight)
		if n < 0 {
			return d, nil
		}
	}
	return Definition{}, errors.New("provider selection failed")
}

func selectTemplate(rule ValueRule, country, state, city string) string {
	if value := rule.ByCountry[country]; value != "" {
		return value
	}
	if city != "" && rule.WithCity != "" {
		return rule.WithCity
	}
	if state != "" && rule.WithState != "" {
		return rule.WithState
	}
	return rule.Default
}

func render(template string, vars, secrets map[string]string) (string, error) {
	out := template
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = strings.ReplaceAll(out, "{{"+key+"}}", vars[key])
	}
	var missing string
	out = secretExpression.ReplaceAllStringFunc(out, func(expression string) string {
		name := secretExpression.FindStringSubmatch(expression)[1]
		value, ok := secrets[name]
		if !ok {
			missing = name
			return expression
		}
		return value
	})
	if missing != "" {
		return "", fmt.Errorf("missing provider secret %q", missing)
	}
	if expression := anyExpression.FindString(out); expression != "" {
		return "", fmt.Errorf("unresolved template expression %q", expression)
	}
	return out, nil
}

func validatePortRule(rule PortRule) error {
	if rule.Default != 0 && (rule.Default < 1 || rule.Default > 65535) {
		return errors.New("default provider port is invalid")
	}
	for country, value := range rule.ByCountry {
		if normalizeCountry(country) == "" || value.From < 1 || value.To > 65535 || value.To < value.From {
			return fmt.Errorf("provider port range for %q is invalid", country)
		}
	}
	if rule.Default == 0 && len(rule.ByCountry) == 0 {
		return errors.New("provider port rule is required")
	}
	return nil
}

func selectPort(rule PortRule, country string) (int, error) {
	value, ok := rule.ByCountry[country]
	if !ok {
		if rule.Default == 0 {
			return 0, fmt.Errorf("provider does not support country %q", country)
		}
		return rule.Default, nil
	}
	n, err := cryptoInt(int64(value.From), int64(value.To))
	return int(n), err
}

func generateSession(rule SessionRule) (string, error) {
	switch rule.Type {
	case "uuid":
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
	case "int":
		n, err := cryptoInt(rule.Min, rule.Max)
		return fmt.Sprint(n), err
	case "alnum":
		const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
		out := make([]byte, rule.Length)
		for i := range out {
			n, err := cryptoInt(0, int64(len(alphabet)-1))
			if err != nil {
				return "", err
			}
			out[i] = alphabet[n]
		}
		return string(out), nil
	default:
		return "", errors.New("unsupported session type")
	}
}

func weightedString(weights map[string]float64) (string, error) {
	keys := make([]string, 0, len(weights))
	var total uint64
	for key, weight := range weights {
		if weight <= 0 {
			continue
		}
		keys = append(keys, key)
		total += uint64(weight * 1_000_000)
	}
	if total == 0 {
		return "", errors.New("state fallback weights are invalid")
	}
	sort.Strings(keys)
	n, err := cryptoUint(total)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		weight := uint64(weights[key] * 1_000_000)
		if n < weight {
			return normalizeLocation(key), nil
		}
		n -= weight
	}
	return "", errors.New("state selection failed")
}

func cryptoInt(min, max int64) (int64, error) {
	if max < min {
		return 0, errors.New("invalid random range")
	}
	n, err := cryptoUint(uint64(max-min) + 1)
	return min + int64(n), err
}

func cryptoUint(limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, errors.New("invalid random limit")
	}
	ceiling := ^uint64(0) - (^uint64(0) % limit)
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		n := binary.BigEndian.Uint64(b[:])
		if n < ceiling {
			return n % limit, nil
		}
	}
}

func normalizeCountry(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeLocation(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), " ", "_")
}
