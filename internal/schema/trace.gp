// Package schema keeps trace identity separate from source location. A move
// changes Location but does not change Artifact.ID.
package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"goforge.dev/goplus/std/option"
	"goforge.dev/goplus/std/result"
)

const TraceSchema = "assayxport.trace/v3"
const UnitDirective = "assayxport:unit "

type ArtifactKind enum {
	SpecificationArtifact()
	TutorialArtifact()
	HowToArtifact()
	ReferenceArtifact()
	ExplanationArtifact()
	ReleaseNoteArtifact()
	DecisionArtifact()
	CodeArtifact()
	GeneratedArtifact()
	ExampleArtifact()
	LawArtifact()
	DifferentialArtifact()
	FuzzArtifact()
	BenchmarkArtifact()
	ExternalArtifact()
}

type RelationKind enum {
	SpecifiesRelation()
	DocumentsRelation()
	AnnouncesRelation()
	DecidesRelation()
	ImplementsRelation()
	TestsRelation()
	ExamplesRelation()
	GeneratesRelation()
	SupersedesRelation()
	DerivedFromRelation()
}

type ArtifactID struct { value string }

type TraceArtifact struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Module         string    `json:"module"`
	Version        string    `json:"version,omitempty"`
	Location       *Location `json:"location,omitempty"`
	SemanticDigest string    `json:"semantic_digest,omitempty"`
	ContentDigest  string    `json:"content_digest"`
	PublicAPI      string    `json:"public_api,omitempty"`
	Deployable     bool      `json:"deployable,omitempty"`
	Generated      bool      `json:"generated,omitempty"`
	Provenance     []string  `json:"provenance,omitempty"`
	Generator      string    `json:"generator,omitempty"`
}

type TraceRelation struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

type TraceBundle struct {
	SchemaVersion string          `json:"schema_version"`
	Module        string          `json:"module"`
	Version       string          `json:"version,omitempty"`
	Artifacts     []TraceArtifact `json:"artifacts"`
	Relations     []TraceRelation `json:"relations"`
}

type TraceValidation enum { TraceValid() }

type TraceReport struct {
	Changed []string `json:"changed"`
	Missing []string `json:"missing"`
}

type TraceFailure enum {
	UnsupportedTraceSchema(Found string)
	InvalidArtifactIdentity(Value string)
	DuplicateArtifactIdentity(Value string)
	UnknownArtifactKind(Value string)
	UnknownRelationKind(Value string)
	InvalidTraceDigest(Artifact string, Field string)
	InvalidDeployableKind(Artifact string)
	MissingArtifactEndpoint(Relation string, Endpoint string)
	InvalidDecisionSource(Artifact string)
	UndecidedCode(Artifact string)
	UnderivedGeneratedCode(Artifact string)
	MissingReleaseNote(Artifact string)
	UnreasonedChange(Artifact string)
	InvalidTraceJSON(Cause error)
}

func (failure TraceFailure) Error() string {
	match failure {
	case UnsupportedTraceSchema(found):
		return fmt.Sprintf("trace: unsupported schema %q", found)
	case InvalidArtifactIdentity(value):
		return fmt.Sprintf("trace: invalid artifact id %q", value)
	case DuplicateArtifactIdentity(value):
		return fmt.Sprintf("trace: duplicate artifact id %q", value)
	case UnknownArtifactKind(value):
		return fmt.Sprintf("trace: unknown artifact kind %q", value)
	case UnknownRelationKind(value):
		return fmt.Sprintf("trace: unknown relation kind %q", value)
	case InvalidTraceDigest(artifact, field):
		return fmt.Sprintf("trace: artifact %s has an invalid %s digest", artifact, field)
	case InvalidDeployableKind(artifact):
		return fmt.Sprintf("trace: deployable artifact %s is not code", artifact)
	case MissingArtifactEndpoint(relation, endpoint):
		return fmt.Sprintf("trace: relation %s references missing %s", relation, endpoint)
	case InvalidDecisionSource(artifact):
		return fmt.Sprintf("trace: decides source %s is not an ADR", artifact)
	case UndecidedCode(artifact):
		return fmt.Sprintf("trace: deployable code %s has no governing ADR", artifact)
	case UnderivedGeneratedCode(artifact):
		return fmt.Sprintf("trace: generated code %s has no generator relation", artifact)
	case MissingReleaseNote(artifact):
		return fmt.Sprintf("trace: release note %s is missing or has the wrong kind", artifact)
	case UnreasonedChange(artifact):
		return fmt.Sprintf("trace: changed deployable unit %s has no release-note to ADR closure", artifact)
	case InvalidTraceJSON(cause):
		return fmt.Sprintf("trace: invalid JSON: %v", cause)
	}
}

// assayxport:unit schema.trace.artifact-id
func NewArtifactID(value string) result.Result[ArtifactID, TraceFailure] {
	if value == "" {
		return result.Err[ArtifactID, TraceFailure](InvalidArtifactIdentity(value))
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '.', character == '-', character == '_', character == ':':
		default:
			return result.Err[ArtifactID, TraceFailure](InvalidArtifactIdentity(value))
		}
	}
	return result.Ok[ArtifactID, TraceFailure](ArtifactID{value: value})
}

func ArtifactIDString(id ArtifactID) string { return id.value }

func ArtifactKindString(kind ArtifactKind) string {
	match kind {
	case SpecificationArtifact: return "specification"
	case TutorialArtifact: return "tutorial"
	case HowToArtifact: return "how-to"
	case ReferenceArtifact: return "reference"
	case ExplanationArtifact: return "explanation"
	case ReleaseNoteArtifact: return "release-note"
	case DecisionArtifact: return "adr"
	case CodeArtifact: return "code"
	case GeneratedArtifact: return "generated"
	case ExampleArtifact: return "example"
	case LawArtifact: return "law"
	case DifferentialArtifact: return "differential"
	case FuzzArtifact: return "fuzz"
	case BenchmarkArtifact: return "benchmark"
	case ExternalArtifact: return "external"
	}
}

func ParseArtifactKind(value string) option.Option[ArtifactKind] {
	switch value {
	case "specification": return option.Some[ArtifactKind](SpecificationArtifact())
	case "tutorial": return option.Some[ArtifactKind](TutorialArtifact())
	case "how-to": return option.Some[ArtifactKind](HowToArtifact())
	case "reference": return option.Some[ArtifactKind](ReferenceArtifact())
	case "explanation": return option.Some[ArtifactKind](ExplanationArtifact())
	case "release-note": return option.Some[ArtifactKind](ReleaseNoteArtifact())
	case "adr": return option.Some[ArtifactKind](DecisionArtifact())
	case "code": return option.Some[ArtifactKind](CodeArtifact())
	case "generated": return option.Some[ArtifactKind](GeneratedArtifact())
	case "example": return option.Some[ArtifactKind](ExampleArtifact())
	case "law": return option.Some[ArtifactKind](LawArtifact())
	case "differential": return option.Some[ArtifactKind](DifferentialArtifact())
	case "fuzz": return option.Some[ArtifactKind](FuzzArtifact())
	case "benchmark": return option.Some[ArtifactKind](BenchmarkArtifact())
	case "external": return option.Some[ArtifactKind](ExternalArtifact())
	default: return option.None[ArtifactKind]
	}
}

func RelationKindString(kind RelationKind) string {
	match kind {
	case SpecifiesRelation: return "specifies"
	case DocumentsRelation: return "documents"
	case AnnouncesRelation: return "announces"
	case DecidesRelation: return "decides"
	case ImplementsRelation: return "implements"
	case TestsRelation: return "tests"
	case ExamplesRelation: return "examples"
	case GeneratesRelation: return "generates"
	case SupersedesRelation: return "supersedes"
	case DerivedFromRelation: return "derived-from"
	}
}

func ParseRelationKind(value string) option.Option[RelationKind] {
	switch value {
	case "specifies": return option.Some[RelationKind](SpecifiesRelation())
	case "documents": return option.Some[RelationKind](DocumentsRelation())
	case "announces": return option.Some[RelationKind](AnnouncesRelation())
	case "decides": return option.Some[RelationKind](DecidesRelation())
	case "implements": return option.Some[RelationKind](ImplementsRelation())
	case "tests": return option.Some[RelationKind](TestsRelation())
	case "examples": return option.Some[RelationKind](ExamplesRelation())
	case "generates": return option.Some[RelationKind](GeneratesRelation())
	case "supersedes": return option.Some[RelationKind](SupersedesRelation())
	case "derived-from": return option.Some[RelationKind](DerivedFromRelation())
	default: return option.None[RelationKind]
	}
}

// assayxport:unit schema.trace.validate
func ValidateTrace(bundle TraceBundle) result.Result[TraceValidation, []TraceFailure] {
	failures := []TraceFailure{}
	if bundle.SchemaVersion != TraceSchema {
		failures = append(failures, UnsupportedTraceSchema(bundle.SchemaVersion))
	}
	kinds := map[string]ArtifactKind{}
	for _, artifact := range bundle.Artifacts {
		match NewArtifactID(artifact.ID) {
		case result.Err(failure):
			failures = append(failures, failure)
		case result.Ok(_):
		}
		if _, exists := kinds[artifact.ID]; exists {
			failures = append(failures, DuplicateArtifactIdentity(artifact.ID))
			continue
		}
		match ParseArtifactKind(artifact.Kind) {
		case option.None:
			failures = append(failures, UnknownArtifactKind(artifact.Kind))
		case option.Some(kind):
			kinds[artifact.ID] = kind
			if artifact.Deployable {
				match kind {
				case CodeArtifact, GeneratedArtifact:
				case _:
					failures = append(failures, InvalidDeployableKind(artifact.ID))
				}
			}
		}
		if !validDigest(artifact.ContentDigest) {
			failures = append(failures, InvalidTraceDigest(artifact.ID, "content"))
		}
		if artifact.Deployable && !validDigest(artifact.SemanticDigest) {
			failures = append(failures, InvalidTraceDigest(artifact.ID, "semantic"))
		}
	}

	decided := map[string]bool{}
	derived := map[string]bool{}
	for _, relation := range bundle.Relations {
		match ParseRelationKind(relation.Kind) {
		case option.None:
			failures = append(failures, UnknownRelationKind(relation.Kind))
			continue
		case option.Some(_):
		}
		if _, exists := kinds[relation.From]; !exists {
			failures = append(failures, MissingArtifactEndpoint(relationKey(relation), relation.From))
			continue
		}
		if _, exists := kinds[relation.To]; !exists {
			failures = append(failures, MissingArtifactEndpoint(relationKey(relation), relation.To))
			continue
		}
		switch relation.Kind {
		case "decides":
			match kinds[relation.From] {
			case DecisionArtifact:
				decided[relation.To] = true
			case _:
				failures = append(failures, InvalidDecisionSource(relation.From))
			}
		case "generates":
			derived[relation.To] = true
		}
	}
	for _, artifact := range bundle.Artifacts {
		if artifact.Deployable && !artifact.Generated && !decided[artifact.ID] {
			failures = append(failures, UndecidedCode(artifact.ID))
		}
		if artifact.Generated && !derived[artifact.ID] {
			failures = append(failures, UnderivedGeneratedCode(artifact.ID))
		}
	}
	if len(failures) > 0 {
		return result.Err[TraceValidation, []TraceFailure](failures)
	}
	return result.Ok[TraceValidation, []TraceFailure](TraceValid())
}

// assayxport:unit schema.trace.verify-release
func VerifyRelease(
	baseline TraceBundle,
	current TraceBundle,
	releaseID string,
) result.Result[TraceReport, []TraceFailure] {
	match ValidateTrace(current) {
	case result.Err(failures):
		return result.Err[TraceReport, []TraceFailure](failures)
	case result.Ok(_):
	}
	currentKinds := artifactKinds(current)
	match currentKinds[releaseID] {
	case ReleaseNoteArtifact:
	case _:
		return result.Err[TraceReport, []TraceFailure](
			[]TraceFailure{MissingReleaseNote(releaseID)},
		)
	}
	announced := map[string]bool{}
	for _, relation := range current.Relations {
		if relation.From == releaseID && relation.Kind == "announces" {
			announced[relation.To] = true
		}
	}
	decisions := map[string][]string{}
	allRelations := append([]TraceRelation{}, baseline.Relations...)
	allRelations = append(allRelations, current.Relations...)
	for _, relation := range allRelations {
		if relation.Kind == "decides" {
			decisions[relation.To] = append(decisions[relation.To], relation.From)
		}
	}
	changed := ChangedDeployable(baseline, current)
	missing := []string{}
	for _, artifact := range changed {
		closed := false
		for _, decision := range decisions[artifact] {
			if announced[decision] {
				closed = true
				break
			}
		}
		if !closed { missing = append(missing, artifact) }
	}
	if len(missing) > 0 {
		failures := make([]TraceFailure, len(missing))
		for index, artifact := range missing {
			failures[index] = UnreasonedChange(artifact)
		}
		return result.Err[TraceReport, []TraceFailure](failures)
	}
	return result.Ok[TraceReport, []TraceFailure](TraceReport{Changed: changed, Missing: missing})
}

func ChangedDeployable(baseline TraceBundle, current TraceBundle) []string {
	before := map[string]TraceArtifact{}
	after := map[string]TraceArtifact{}
	for _, artifact := range baseline.Artifacts {
		if artifact.Deployable { before[artifact.ID] = artifact }
	}
	for _, artifact := range current.Artifacts {
		if artifact.Deployable { after[artifact.ID] = artifact }
	}
	changed := []string{}
	for id, artifact := range after {
		previous, exists := before[id]
		if !exists ||
			previous.SemanticDigest != artifact.SemanticDigest ||
			previous.PublicAPI != artifact.PublicAPI {
			changed = append(changed, id)
		}
	}
	for id := range before {
		if _, exists := after[id]; !exists { changed = append(changed, id) }
	}
	sort.Strings(changed)
	return changed
}

func CanonicalTrace(bundle TraceBundle) TraceBundle {
	out := bundle
	out.Artifacts = append([]TraceArtifact{}, bundle.Artifacts...)
	out.Relations = append([]TraceRelation{}, bundle.Relations...)
	sort.Slice(out.Artifacts, func(i, j int) bool {
		if out.Artifacts[i].ID != out.Artifacts[j].ID {
			return out.Artifacts[i].ID < out.Artifacts[j].ID
		}
		return out.Artifacts[i].Kind < out.Artifacts[j].Kind
	})
	sort.Slice(out.Relations, func(i, j int) bool {
		left, right := out.Relations[i], out.Relations[j]
		if left.From != right.From { return left.From < right.From }
		if left.Kind != right.Kind { return left.Kind < right.Kind }
		return left.To < right.To
	})
	return out
}

func BundleDigest(bundle TraceBundle) result.Result[string, error] {
	encoded, encodeError := json.Marshal(CanonicalTrace(bundle))
	match result.Of(encoded, encodeError) {
	case result.Err(cause):
		return result.Err[string, error](cause)
	case result.Ok(content):
		return result.Ok[string, error](DigestBytes(content))
	}
}

func DigestText(content string) string { return DigestBytes([]byte(content)) }

func DigestBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func ArtifactURL(module string, version string, kind ArtifactKind, id string) string {
	name := strings.TrimPrefix(module, "goforge.dev/")
	return "https://goforge.dev/" +
		url.PathEscape(name) + "/" +
		url.PathEscape(version) + "/artifacts/" +
		url.PathEscape(ArtifactKindString(kind)) + "/" +
		url.PathEscape(id)
}

func ParseUnitDirective(comment string) result.Result[ArtifactID, TraceFailure] {
	text := strings.TrimSpace(comment)
	text = strings.TrimLeft(text, "/#*% \t")
	index := strings.Index(text, UnitDirective)
	if index < 0 {
		return result.Err[ArtifactID, TraceFailure](InvalidArtifactIdentity(comment))
	}
	fields := strings.Fields(text[index+len(UnitDirective):])
	if len(fields) == 0 {
		return result.Err[ArtifactID, TraceFailure](InvalidArtifactIdentity(comment))
	}
	return NewArtifactID(fields[0])
}

func artifactKinds(bundle TraceBundle) map[string]ArtifactKind {
	out := map[string]ArtifactKind{}
	for _, artifact := range bundle.Artifacts {
		match ParseArtifactKind(artifact.Kind) {
		case option.Some(kind): out[artifact.ID] = kind
		case option.None:
		}
	}
	return out
}

func validDigest(value string) bool {
	if len(value) != 64 { return false }
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'f':
		default: return false
		}
	}
	return true
}

func relationKey(relation TraceRelation) string {
	return relation.From + " " + relation.Kind + " " + relation.To
}
