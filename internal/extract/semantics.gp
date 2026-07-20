package extract

import (
	"errors"
	"fmt"

	"goforge.dev/assayxport/internal/schema"
)

// ExtractionFailure attributes failure to the extractor that produced it.
type ExtractionFailure struct {
	Language string
	Cause error
}

// ExtractionOutcome makes complete, useful partial, and fatal extraction
// mutually exclusive instead of coordinating warnings with a nil error.
//goplus:derive off
type ExtractionOutcome enum {
	ExtractionComplete(packages []schema.Package, languages []string)
	ExtractionPartial(packages []schema.Package, languages []string, warnings []ExtractionFailure)
	ExtractionFailed(failures []ExtractionFailure)
}

// Capability classes let generic pipeline code ask only for the extraction
// mode it consumes. Interface adapters below keep ordinary Go extractors valid.
type StreamingCapability[E any] class {
	Stream(e E, root string, emit func(schema.Package) error) error
}

type SkeletonCapability[E any] class {
	Enumerate(e E, root string) ([]schema.Package, error)
	Identity(e E, p schema.Package) string
	law IdentityIsStable(e E, p schema.Package) { return Identity(e, p) == p.ID }
}

type DemandCapability[E any] class {
	SkeletonCapability[E]
	One(e E, root string, p schema.Package) (schema.Package, error)
}

instance StreamExtractorCapability StreamingCapability[StreamExtractor] {
	Stream(e StreamExtractor, root string, emit func(schema.Package) error) error { return e.ExtractStream(root, emit) }
}

//goplus:laws off
instance SkeletonExtractorCapability SkeletonCapability[SkeletonExtractor] {
	Enumerate(e SkeletonExtractor, root string) ([]schema.Package, error) { return e.Skeleton(root) }
	Identity(_ SkeletonExtractor, p schema.Package) string { return p.ID }
}

//goplus:laws off
instance DemandExtractorCapability DemandCapability[DemandExtractor] {
	Enumerate(e DemandExtractor, root string) ([]schema.Package, error) { return e.Skeleton(root) }
	Identity(_ DemandExtractor, p schema.Package) string { return p.ID }
	One(e DemandExtractor, root string, p schema.Package) (schema.Package, error) { return e.ExtractOne(root, p) }
}

// IdentityWitness keeps the capability identity law executable without asking
// a property generator to manufacture interface implementations.
type IdentityWitness struct{}

instance PackageIdentityLaw SkeletonCapability[IdentityWitness] {
	Enumerate(_ IdentityWitness, _ string) ([]schema.Package, error) { return nil, nil }
	Identity(_ IdentityWitness, p schema.Package) string { return p.ID }
}

func StreamWith[E StreamingCapability](e E, root string, emit func(schema.Package) error) error { return Stream(e, root, emit) }
func SkeletonWith[E SkeletonCapability](e E, root string) ([]schema.Package, error) { return Enumerate(e, root) }
func ExtractOneWith[E DemandCapability](e E, root string, p schema.Package) (schema.Package, error) { return One(e, root, p) }

// LegacyOutcome is the one compatibility projection for callers that still
// consume parallel Go return values. The match is exhaustive in Go+.
func LegacyOutcome(outcome ExtractionOutcome) ([]schema.Package, []string, []error, error) {
	match outcome {
	case ExtractionComplete(packages, languages):
		return packages, languages, nil, nil
	case ExtractionPartial(packages, languages, warnings):
		errList := make([]error, len(warnings))
		for i, warning := range warnings { errList[i] = fmt.Errorf("%s: %w", warning.Language, warning.Cause) }
		return packages, languages, errList, nil
	case ExtractionFailed(failures):
		errList := make([]error, len(failures))
		for i, failure := range failures { errList[i] = fmt.Errorf("%s: %w", failure.Language, failure.Cause) }
		return nil, nil, nil, errors.Join(errList...)
	}
}
