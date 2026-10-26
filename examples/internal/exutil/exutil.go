// Package exutil contains shared helpers for the example commands:
// API key resolution, session setup with signal-based cleanup, confirmation
// prompts, mapping selection, and the save-or-activate flow.
package exutil

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

// APIKey resolves the REST API key:
// -k flag value first, then the API_KEY environment variable, then
// an api_key.conf file in the working directory with a [KEY] section
// containing "api_key = ...".
func APIKey(flagVal string) (string, error) {
	if flagVal != "" {
		return strings.TrimSpace(flagVal), nil
	}
	if env := os.Getenv("API_KEY"); env != "" {
		return strings.TrimSpace(env), nil
	}
	f, err := os.Open("api_key.conf")
	if err != nil {
		return "", fmt.Errorf("API key needed: use -k, set API_KEY, or provide api_key.conf")
	}
	defer f.Close()
	inKeySection := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "["):
			inKeySection = strings.EqualFold(line, "[KEY]")
		case inKeySection && strings.HasPrefix(line, "api_key"):
			if _, val, ok := strings.Cut(line, "="); ok {
				return strings.TrimSpace(val), nil
			}
		}
	}
	return "", fmt.Errorf("no api_key found in api_key.conf ([KEY] section with api_key = ...)")
}

// Host resolves the gateway host: -g flag value first, then the
// GATEWAY_HOST environment variable.
func Host(flagVal string) (string, error) {
	if flagVal != "" {
		return strings.TrimSpace(flagVal), nil
	}
	if env := os.Getenv("GATEWAY_HOST"); env != "" {
		return strings.TrimSpace(env), nil
	}
	return "", fmt.Errorf("gateway host needed: use -g or set GATEWAY_HOST")
}

// Session bundles the gateway client with a signal-aware context so that
// Ctrl-C terminates the REST session before the process exits.
type Session struct {
	GW  *gateway.Client
	Ctx context.Context

	stop func()
}

// Setup resolves the gateway host and API key (see Host and APIKey), creates
// a session, registers signal cleanup, connects, and loads the active
// configuration. It exits the process on failure.
func Setup(gatewayFlag, keyFlag string, port int, debug bool) *Session {
	host, err := Host(gatewayFlag)
	if err != nil {
		log.Fatal(err)
	}
	apiKey, err := APIKey(keyFlag)
	if err != nil {
		log.Fatal(err)
	}
	opts := []gateway.Option{
		gateway.WithInsecureSkipVerify(), // mgmt interfaces are usually self-signed
	}
	if debug {
		opts = []gateway.Option{gateway.WithCustomHTTPClient(debugHTTPClient())}
	}
	if port != 443 {
		opts = append(opts, gateway.WithPort(port))
	}
	gw, err := gateway.New(host, apiKey, opts...)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	s := &Session{GW: gw, Ctx: ctx, stop: stop}

	if err := gw.Connect(ctx); err != nil {
		log.Fatalf("could not create session for gateway %s (check gateway, port, and API key): %v", host, err)
	}
	go func() { // terminate the REST session if a signal cancels the context
		<-ctx.Done()
		if ctx.Err() == context.Canceled {
			return // normal Close()
		}
		log.Println("signal received, terminating session...")
		_ = gw.Close(context.Background())
		os.Exit(1)
	}()

	log.Printf("loading active configuration on gateway %s...", host)
	if _, err := gateway.ExpectNoContent(gw.LoadConfigurationActiveWithResponse(ctx)); err != nil {
		s.Fatal("load active configuration: %v", err)
	}
	return s
}

// Close terminates the REST session (regular end of script).
func (s *Session) Close() {
	_ = s.GW.Close(context.Background())
	s.stop()
}

// Fatal terminates the session and exits with an error message.
func (s *Session) Fatal(format string, args ...any) {
	log.Printf(format, args...)
	_ = s.GW.Close(context.Background())
	os.Exit(1)
}

// Confirm prompts for a yes/no answer with a default.
func Confirm(prompt string, def bool) bool {
	defStr := "y/N"
	if def {
		defStr = "Y/n"
	}
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("%s [%s]: ", prompt, defStr)
		if !sc.Scan() {
			return def
		}
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Println("Please respond with 'y' or 'n'.")
	}
}

// SaveConfig saves the loaded configuration with an optional confirmation
// prompt.
func (s *Session) SaveConfig(comment string, assumeYes bool) {
	if !assumeYes && !Confirm("\nSave the new configuration on target?", false) {
		log.Println("saving configuration cancelled")
		return
	}
	if _, err := gateway.ExpectOK(s.GW.SaveConfigurationWithResponse(s.Ctx, gateway.SaveConfigurationJSONRequestBody{
		Comment: gateway.Ptr(comment),
	})); err != nil {
		s.Fatal("failed to save configuration: %v", err)
	}
	log.Println("target configuration saved")
}

// ValidateConfig prints whether the currently loaded configuration is
// valid, listing the validation errors if it is not. Informational only —
// it never terminates the session.
func (s *Session) ValidateConfig() {
	res, err := gateway.ExpectOK(s.GW.GetValidatorMessagesWithResponse(s.Ctx, &gateway.GetValidatorMessagesParams{
		Filter: gateway.Ptr([]string{"meta.severity==ERROR"}),
	}))
	if err != nil {
		log.Printf("could not validate configuration: %v", err)
		return
	}
	msgs := deref(res.JSON200.Data)
	if len(msgs) == 0 {
		log.Println("configuration is valid")
		return
	}
	log.Printf("configuration is NOT valid (%d error(s)):", len(msgs))
	for _, m := range msgs {
		if detail := deref(deref(m.Attributes).Detail); detail != "" {
			log.Printf("  - %s", detail)
		}
	}
}

// ActivateOrSave activates or saves the configuration with optional
// confirmation, falling back to save when activation fails.
func (s *Session) ActivateOrSave(comment string, assumeYes, activate bool) {
	s.ValidateConfig()
	if !activate {
		s.SaveConfig(comment, assumeYes)
		return
	}
	if !assumeYes && !Confirm("\nActivate the new configuration on target?", false) {
		log.Println("activation cancelled; offering to save configuration instead")
		s.SaveConfig(comment, false)
		return
	}
	if _, err := gateway.ExpectOK(s.GW.ActivateConfigurationWithResponse(s.Ctx, gateway.ActivationRequestDto{
		Comment: gateway.Ptr(comment),
	})); err == nil {
		log.Println("target configuration activated successfully")
		return // activation also saves; don't save again
	}
	log.Println("activation failed; attempting to save the configuration instead")
	s.SaveConfig(comment, false)
}

// Mappings returns all mappings of the loaded configuration whose name
// matches re (nil matches all), sorted by name.
func (s *Session) Mappings(re *regexp.Regexp) []gateway.ResourceObjectMappingDto {
	res, err := gateway.ExpectOK(s.GW.GetMappingsWithResponse(s.Ctx, nil))
	if err != nil {
		s.Fatal("get mappings: %v", err)
	}
	var out []gateway.ResourceObjectMappingDto
	for _, m := range deref(res.JSON200.Data) {
		if re == nil || re.MatchString(MappingName(m)) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return MappingName(out[i]) < MappingName(out[j]) })
	return out
}

// DenyRuleGroups returns the IDs of the built-in deny rule groups whose ID
// matches re (nil matches all). The ID is the group short name, e.g.
// SQLI_PARAM_VALUE, used in the per-mapping deny-rule-group endpoints.
func (s *Session) DenyRuleGroups(re *regexp.Regexp) []string {
	res, err := gateway.ExpectOK(s.GW.GetDenyRuleGroupsWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("get deny rule groups: %v", err)
	}
	var out []string
	for _, g := range deref(res.JSON200.Data) {
		if g.ID == nil {
			continue
		}
		if re == nil || re.MatchString(*g.ID) {
			out = append(out, *g.ID)
		}
	}
	return out
}

// MappingName returns the mapping's name attribute ("" if unset).
func MappingName(m gateway.ResourceObjectMappingDto) string {
	return deref(deref(m.Attributes).Name)
}

// MappingID returns the mapping's numeric resource ID (-1 if unset).
func MappingID(m gateway.ResourceObjectMappingDto) int64 {
	return ResourceID(m.ID)
}

// ResourceID parses a resource's string ID as the numeric ID used in request
// paths (-1 if unset or non-numeric).
func ResourceID(id *string) int64 {
	n, err := strconv.ParseInt(deref(id), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// deref returns *p, or the zero value of T if p is nil — handy for the
// optional (pointer-typed) fields of generated response types.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
