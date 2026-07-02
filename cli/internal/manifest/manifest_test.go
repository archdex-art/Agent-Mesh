package manifest

import (
	"strings"
	"testing"
)

func findingFor(t *testing.T, findings []Finding, field string) *Finding {
	t.Helper()
	for i := range findings {
		if findings[i].Field == field {
			return &findings[i]
		}
	}
	return nil
}

func TestValidateCompleteManifestHasNoFindings(t *testing.T) {
	raw := []byte(`
name: internal-crm
upstream_url: https://mcp.internal.example.com
transport: streamable-http
version: "1.0.0"
owner: platform-team
auth:
  type: oauth2.1
`)
	result, err := Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Fatalf("expected 0 findings for a complete manifest, got %+v", result.Findings)
	}
	if result.HasErrors() {
		t.Fatal("HasErrors() = true, want false")
	}
}

func TestValidateMissingRequiredFieldsAreErrors(t *testing.T) {
	raw := []byte(`
transport: stdio
`)
	result, err := Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("expected HasErrors() = true for a manifest missing name/upstream_url/version/owner")
	}
	for _, field := range []string{"name", "upstream_url", "version", "owner"} {
		f := findingFor(t, result.Findings, field)
		if f == nil {
			t.Fatalf("expected a finding for field %q, got none in %+v", field, result.Findings)
		}
		if f.Severity != SeverityError {
			t.Fatalf("field %q: severity = %v, want error", field, f.Severity)
		}
	}
}

func TestValidateInvalidTransportIsError(t *testing.T) {
	raw := []byte(`
name: x
upstream_url: https://example.com
transport: carrier-pigeon
version: "1.0.0"
owner: someone
auth:
  type: none
`)
	result, err := Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	f := findingFor(t, result.Findings, "transport")
	if f == nil || f.Severity != SeverityError {
		t.Fatalf("expected a transport error finding, got %+v", result.Findings)
	}
}

func TestValidateMalformedURLIsError(t *testing.T) {
	raw := []byte(`
name: x
upstream_url: "not a url"
transport: stdio
version: "1.0.0"
owner: someone
auth:
  type: none
`)
	result, err := Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	f := findingFor(t, result.Findings, "upstream_url")
	if f == nil || f.Severity != SeverityError {
		t.Fatalf("expected an upstream_url error finding, got %+v", result.Findings)
	}
}

func TestValidateMissingAuthIsWarningNotError(t *testing.T) {
	raw := []byte(`
name: x
upstream_url: https://example.com
transport: stdio
version: "1.0.0"
owner: someone
`)
	result, err := Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	f := findingFor(t, result.Findings, "auth")
	if f == nil {
		t.Fatal("expected a finding for the missing auth block")
	}
	if f.Severity != SeverityWarning {
		t.Fatalf("missing auth severity = %v, want warning (must not block registration)", f.Severity)
	}
	if result.HasErrors() {
		t.Fatal("HasErrors() = true, want false: a missing auth block alone must not be a hard error")
	}
}

func TestValidateEmptyAuthTypeIsError(t *testing.T) {
	raw := []byte(`
name: x
upstream_url: https://example.com
transport: stdio
version: "1.0.0"
owner: someone
auth:
  type: ""
`)
	result, err := Validate(raw)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	f := findingFor(t, result.Findings, "auth.type")
	if f == nil || f.Severity != SeverityError {
		t.Fatalf("expected an auth.type error finding, got %+v", result.Findings)
	}
}

func TestValidateUnparseableYAMLReturnsError(t *testing.T) {
	raw := []byte("not: valid: yaml: [")
	if _, err := Validate(raw); err == nil {
		t.Fatal("expected an error for unparseable YAML")
	}
}

func TestFindingStringIncludesSeverityFieldAndMessage(t *testing.T) {
	f := Finding{Severity: SeverityError, Field: "name", Message: "required"}
	s := f.String()
	for _, want := range []string{"ERROR", "name", "required"} {
		if !strings.Contains(s, want) {
			t.Fatalf("Finding.String() = %q, want it to contain %q", s, want)
		}
	}
}
