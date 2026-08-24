package managed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validSpec() SessionSpec {
	body := []byte("data")
	digest := sha256.Sum256(body)
	return SessionSpec{
		Model:        ModelSelector{Preference: "best"},
		Instructions: "work carefully",
		Task:         "reproduce the result",
		Sandbox: SandboxSpec{
			Packages:  []Package{{Manager: "pip", Name: "pypdf", Version: "5.0"}},
			Bootstrap: [][]string{{"python", "-m", "pip", "check"}},
			Network: NetworkPolicy{
				Mode:           NetworkAllowlist,
				AllowedDomains: []string{"api.example.com"},
				SecretRefs:     map[string]string{"API_TOKEN": "api.example.com:research-token"},
			},
		},
		Resources: []Resource{{Kind: ResourceAttachment, Attachment: &AttachmentResource{
			Path: "inputs/data.csv", Content: body, SHA256: hex.EncodeToString(digest[:]), MediaType: "text/csv",
		}}},
		Evaluation: &EvaluationSpec{Rubric: "the result is reproduced", MaxRevisions: 3},
	}
}

func TestSessionSpecValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}

	tests := []struct {
		name  string
		alter func(*SessionSpec)
		field string
	}{
		{"both model selectors", func(s *SessionSpec) { s.Model.Exact = &ModelRef{Provider: "anthropic", ID: "m"} }, "model"},
		{"unknown preference", func(s *SessionSpec) { s.Model.Preference = "cheapest" }, "model.preference"},
		{"empty instructions", func(s *SessionSpec) { s.Instructions = " " }, "instructions"},
		{"empty task", func(s *SessionSpec) { s.Task = "" }, "task"},
		{"empty rubric", func(s *SessionSpec) { s.Evaluation.Rubric = "" }, "evaluation.rubric"},
		{"zero revision budget", func(s *SessionSpec) { s.Evaluation.MaxRevisions = 0 }, "evaluation.max_revisions"},
		{"shell fragment", func(s *SessionSpec) { s.Sandbox.Bootstrap = [][]string{{}} }, "sandbox.bootstrap[0]"},
		{"open with secrets", func(s *SessionSpec) { s.Sandbox.Network.Mode = NetworkOpen }, "sandbox.network"},
		{"noncanonical domain", func(s *SessionSpec) { s.Sandbox.Network.AllowedDomains[0] = "API.Example.com" }, "sandbox.network.allowed_domains[0]"},
		{"secret outside allowlist", func(s *SessionSpec) { s.Sandbox.Network.SecretRefs["API_TOKEN"] = "other.example.com:key" }, "sandbox.network.secret_refs"},
		{"unsafe attachment", func(s *SessionSpec) { s.Resources[0].Attachment.Path = "../data.csv" }, "resources[0].attachment.path"},
		{"wrong digest", func(s *SessionSpec) { s.Resources[0].Attachment.SHA256 = strings.Repeat("0", 64) }, "resources[0].attachment.sha256"},
		{"unknown resource", func(s *SessionSpec) { s.Resources[0].Kind = "bucket" }, "resources[0].kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			test.alter(&spec)
			err := spec.Validate()
			var invalid *InvalidSpecError
			if !errors.As(err, &invalid) || invalid.Field != test.field {
				t.Fatalf("error = %v, want InvalidSpecError field %q", err, test.field)
			}
		})
	}
}

func TestRepositoryResourceValidation(t *testing.T) {
	spec := validSpec()
	spec.Resources = []Resource{{Kind: ResourceRepository, Repository: &RepositoryResource{
		URL: "https://github.com/example/repo", Destination: "workspace/repro", Access: AccessReadWrite, CredentialRef: "github:repo-token",
	}}}
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid repository: %v", err)
	}
	spec.Resources[0].Repository.CredentialRef = ""
	if err := spec.Validate(); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("read-write repository without credential = %v", err)
	}
	spec.Resources[0].Repository.Access = AccessReadOnly
	if err := spec.Validate(); err != nil {
		t.Fatalf("read-only repository: %v", err)
	}
}

func TestDuplicateResourceDestination(t *testing.T) {
	spec := validSpec()
	copyResource := spec.Resources[0]
	copyAttachment := *copyResource.Attachment
	copyResource.Attachment = &copyAttachment
	spec.Resources = append(spec.Resources, copyResource)
	if err := spec.Validate(); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("duplicate destination = %v", err)
	}
}

func TestSessionRefValidationAndRoundTrip(t *testing.T) {
	ref := SessionRef{Runtime: "anthropic-managed", ID: "session-1", Revision: 2, LocatorVersion: 1, Locator: json.RawMessage(`{"session_id":"s","upload_ids":["f"]}`)}
	if err := ref.Validate(0); err != nil {
		t.Fatalf("valid ref: %v", err)
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SessionRef
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.SameIdentity(ref) || decoded.Revision != ref.Revision || string(decoded.Locator) != string(ref.Locator) {
		t.Fatalf("round trip = %+v, want %+v", decoded, ref)
	}

	tests := []SessionRef{
		{Runtime: "Anthropic", ID: "s", Revision: 1, LocatorVersion: 1},
		{Runtime: "anthropic", ID: "", Revision: 1, LocatorVersion: 1},
		{Runtime: "anthropic", ID: "s", Revision: 0, LocatorVersion: 1},
		{Runtime: "anthropic", ID: "s", Revision: 1, LocatorVersion: 0},
		{Runtime: "anthropic", ID: "s", Revision: 1, LocatorVersion: 1, Locator: json.RawMessage(`{`)},
		{Runtime: "anthropic", ID: "s", Revision: 1, LocatorVersion: 1, Locator: json.RawMessage(`{"api_token":"nope"}`)},
	}
	for _, bad := range tests {
		if err := bad.Validate(0); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Validate(%+v) = %v", bad, err)
		}
	}
	tooLarge := ref
	tooLarge.Locator = json.RawMessage(`{"value":"` + strings.Repeat("x", 20) + `"}`)
	if err := tooLarge.Validate(10); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("oversized locator = %v", err)
	}
}

func TestValidateCapabilities(t *testing.T) {
	capabilities := Capabilities{
		PackageManagers:    map[string]bool{"pip": true},
		NetworkModes:       map[NetworkMode]bool{NetworkAllowlist: true},
		RepositoryModes:    map[AccessMode]bool{AccessReadOnly: true},
		Evaluation:         true,
		MaxAttachmentBytes: 10,
	}
	if err := ValidateCapabilities("memory", capabilities, validSpec()); err != nil {
		t.Fatalf("supported spec: %v", err)
	}
	tests := []struct {
		name  string
		alter func(*SessionSpec)
	}{
		{"manager", func(s *SessionSpec) { s.Sandbox.Packages[0].Manager = "conda" }},
		{"network", func(s *SessionSpec) { s.Sandbox.Network = NetworkPolicy{Mode: NetworkOpen} }},
		{"evaluation", func(*SessionSpec) { capabilities.Evaluation = false }},
		{"attachment size", func(s *SessionSpec) {
			s.Resources[0].Attachment.Content = []byte("much too large")
			sum := sha256.Sum256(s.Resources[0].Attachment.Content)
			s.Resources[0].Attachment.SHA256 = hex.EncodeToString(sum[:])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			test.alter(&spec)
			if err := ValidateCapabilities("memory", capabilities, spec); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want unsupported", err)
			}
		})
	}
}

func TestUsageOperations(t *testing.T) {
	base := Usage{InputFresh: 1, InputCached: 2, CacheWrite: 3, CacheWrite5m: 4, CacheWrite1h: 5, Output: 6, Extra: map[Meter]int64{"container.second": 7}, Details: map[string]int64{"reasoning": 8}}
	other := Usage{InputFresh: 10, InputCached: 20, CacheWrite: 30, CacheWrite5m: 40, CacheWrite1h: 50, Output: 60, Extra: map[Meter]int64{"container.second": 70}, Details: map[string]int64{"reasoning": 80}}
	base.Add(other)
	if got, want := base.TotalTokens(), int64(231); got != want {
		t.Fatalf("TotalTokens = %d, want %d", got, want)
	}
	if got, want := base.CacheWritten(), int64(132); got != want {
		t.Fatalf("CacheWritten = %d, want %d", got, want)
	}
	if base.Extra["container.second"] != 77 || base.Details["reasoning"] != 88 {
		t.Fatalf("maps not accumulated: %+v", base)
	}
	if !base.MonotonicFrom(other) {
		t.Fatal("accumulated usage should be monotonic")
	}
	decreased := base
	decreased.Extra = map[Meter]int64{"container.second": 1}
	if decreased.MonotonicFrom(base) {
		t.Fatal("decreased extra meter reported monotonic")
	}
}

func TestEnumsAndHelpers(t *testing.T) {
	for _, state := range []State{StatePending, StateRunning, StateDone, StateFailed} {
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		var decoded State
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != state {
			t.Fatalf("state round trip %s: %v, %v", state, decoded, err)
		}
	}
	if err := json.Unmarshal([]byte(`"bogus"`), new(State)); err == nil {
		t.Fatal("unknown state accepted")
	}
	for _, grade := range []Grade{GradePending, GradePassed, GradeNeedsRevision, GradeExhausted, GradeFailed} {
		raw, _ := json.Marshal(grade)
		var decoded Grade
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != grade {
			t.Fatalf("grade round trip %s: %v, %v", grade, decoded, err)
		}
	}
	if err := json.Unmarshal([]byte(`"bogus"`), new(Grade)); err == nil {
		t.Fatal("unknown grade accepted")
	}
	if !StateDone.Terminal() || StateRunning.Terminal() || !GradePassed.Decided() || GradePending.Decided() {
		t.Fatal("enum predicates are wrong")
	}
	if got := CanonicalRuntimeName(" Anthropic-Managed "); got != "anthropic-managed" {
		t.Fatalf("canonical name = %q", got)
	}
	if !(Artifact{Path: "x/__pycache__/a.pyc"}).IsCompiled() || (Artifact{Path: "result.json"}).IsCompiled() {
		t.Fatal("compiled artifact classification is wrong")
	}
	if !(Status{Verdict: &Verdict{Grade: GradePassed}}).Passed() || (Status{}).Passed() {
		t.Fatal("status pass classification is wrong")
	}
}

func TestErrors(t *testing.T) {
	invalid := &InvalidSpecError{Field: "field", Message: "bad"}
	if !errors.Is(invalid, ErrInvalidSpec) || !strings.Contains(invalid.Error(), "field") {
		t.Fatal("invalid spec error does not retain context")
	}
	unsupported := &UnsupportedError{Runtime: "x", Field: "f", Capability: "c"}
	if !errors.Is(unsupported, ErrUnsupported) || !strings.Contains(unsupported.Error(), "runtime \"x\"") {
		t.Fatal("unsupported error does not unwrap")
	}
	mismatch := &RuntimeMismatchError{Expected: "a", Actual: "b"}
	if !errors.Is(mismatch, ErrRuntimeMismatch) || !strings.Contains(mismatch.Error(), "expected") {
		t.Fatal("runtime mismatch does not unwrap")
	}
	notFound := &NotFoundError{Runtime: "a", ID: "id"}
	if !errors.Is(notFound, ErrNotFound) || !strings.Contains(notFound.Error(), "a/id") {
		t.Fatal("not found does not unwrap")
	}
	temporary := &TemporaryError{Err: errors.New("retry")}
	if !IsTemporary(temporary) || IsTemporary(errors.New("permanent")) || !errors.Is(temporary, temporary.Err) {
		t.Fatal("temporary classification is wrong")
	}
	cleanup := &CleanupError{Err: temporary}
	if !errors.Is(cleanup, temporary) || !strings.Contains(cleanup.Error(), "cleanup") {
		t.Fatal("cleanup error does not retain cause")
	}
}

func TestRequestSecretsAreReferencedAndCopied(t *testing.T) {
	values := map[string]string{"repo": "first"}
	ctx := WithSecrets(context.Background(), values)
	values["repo"] = "changed"
	if got, ok := Secret(ctx, "repo"); !ok || got != "first" {
		t.Fatalf("Secret = %q, %v", got, ok)
	}
	if _, ok := Secret(ctx, "missing"); ok {
		t.Fatal("missing secret resolved")
	}
}

func TestValidationEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*SessionSpec)
	}{
		{"exact model empty", func(s *SessionSpec) { s.Model = ModelSelector{Exact: &ModelRef{Provider: "anthropic"}} }},
		{"package manager", func(s *SessionSpec) { s.Sandbox.Packages[0].Manager = "/pip" }},
		{"package name", func(s *SessionSpec) { s.Sandbox.Packages[0].Name = "" }},
		{"package version", func(s *SessionSpec) { s.Sandbox.Packages[0].Version = "bad version" }},
		{"bootstrap executable", func(s *SessionSpec) { s.Sandbox.Bootstrap = [][]string{{" "}} }},
		{"bootstrap nul", func(s *SessionSpec) { s.Sandbox.Bootstrap = [][]string{{"ok", "bad\x00arg"}} }},
		{"none domains", func(s *SessionSpec) {
			s.Sandbox.Network = NetworkPolicy{Mode: NetworkNone, AllowedDomains: []string{"api.example.com"}}
		}},
		{"empty allowlist", func(s *SessionSpec) { s.Sandbox.Network = NetworkPolicy{Mode: NetworkAllowlist} }},
		{"unknown network", func(s *SessionSpec) { s.Sandbox.Network = NetworkPolicy{Mode: "private"} }},
		{"IP domain", func(s *SessionSpec) { s.Sandbox.Network.AllowedDomains[0] = "127.0.0.1" }},
		{"bad env", func(s *SessionSpec) {
			s.Sandbox.Network.SecretRefs = map[string]string{"bad-name": "api.example.com:key"}
		}},
		{"bad ref", func(s *SessionSpec) { s.Sandbox.Network.SecretRefs = map[string]string{"TOKEN": "key"} }},
		{"attachment payload", func(s *SessionSpec) { s.Resources[0].Repository = &RepositoryResource{} }},
		{"attachment digest format", func(s *SessionSpec) { s.Resources[0].Attachment.SHA256 = "xyz" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSpec()
			test.alter(&spec)
			if err := spec.Validate(); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("Validate = %v, want invalid spec", err)
			}
		})
	}
}

func TestRepositoryValidationEdgeCases(t *testing.T) {
	base := func() SessionSpec {
		spec := validSpec()
		spec.Resources = []Resource{{Kind: ResourceRepository, Repository: &RepositoryResource{
			URL: "https://github.com/example/repo", Destination: "workspace/repro", Access: AccessReadOnly,
		}}}
		return spec
	}
	tests := []func(*RepositoryResource){
		func(r *RepositoryResource) { r.URL = "http://github.com/example/repo" },
		func(r *RepositoryResource) { r.Destination = "/workspace/repro" },
		func(r *RepositoryResource) { r.Access = "admin" },
	}
	for i, alter := range tests {
		spec := base()
		alter(spec.Resources[0].Repository)
		if err := spec.Validate(); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("case %d = %v", i, err)
		}
	}
	spec := base()
	spec.Resources[0].Attachment = &AttachmentResource{}
	if err := spec.Validate(); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("payload mismatch = %v", err)
	}
}

func TestCapabilitiesRepositoryAndInvalidSpec(t *testing.T) {
	spec := validSpec()
	spec.Resources = []Resource{{Kind: ResourceRepository, Repository: &RepositoryResource{
		URL: "https://github.com/example/repo", Destination: "repro", Access: AccessReadOnly,
	}}}
	caps := Capabilities{
		PackageManagers: map[string]bool{"pip": true},
		NetworkModes:    map[NetworkMode]bool{NetworkAllowlist: true},
		RepositoryModes: map[AccessMode]bool{},
		Evaluation:      true,
	}
	if err := ValidateCapabilities("memory", caps, spec); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("repository capability = %v", err)
	}
	spec.Task = ""
	if err := ValidateCapabilities("memory", caps, spec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("invalid spec = %v", err)
	}
}

func TestUsageInitializesMapsAndDetectsFixedDecrease(t *testing.T) {
	var usage Usage
	usage.Add(Usage{Extra: map[Meter]int64{"call": 2}, Details: map[string]int64{"reasoning": 3}})
	if usage.Extra["call"] != 2 || usage.Details["reasoning"] != 3 {
		t.Fatalf("Add did not initialize maps: %+v", usage)
	}
	if (Usage{InputFresh: 1}).MonotonicFrom(Usage{InputFresh: 2}) {
		t.Fatal("fixed-field decrease accepted")
	}
}

func TestEnumErrorPathsAndModelRef(t *testing.T) {
	if StateUnknown.Valid() || !StatePending.Valid() || State(99).Valid() {
		t.Fatal("state validity is wrong")
	}
	if !strings.Contains(State(99).String(), "99") || !strings.Contains(Grade(99).String(), "99") {
		t.Fatal("unknown enum string lacks value")
	}
	if err := json.Unmarshal([]byte(`1`), new(State)); err == nil {
		t.Fatal("numeric state accepted")
	}
	if err := json.Unmarshal([]byte(`1`), new(Grade)); err == nil {
		t.Fatal("numeric grade accepted")
	}
	if !(ModelRef{Provider: "anthropic", ID: "m"}).Valid() || (ModelRef{Provider: "", ID: "m"}).Valid() {
		t.Fatal("model ref validity is wrong")
	}
}
