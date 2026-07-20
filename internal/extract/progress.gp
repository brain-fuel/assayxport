package extract

import "goforge.dev/assayxport/internal/schema"

type PackagePhase enum {
	DiscoveredPhase
	ScheduledPhase
	ExtractingPhase
	ReadyPhase
	FailedPhase
}

// ProgressivePackage is indexed by the only phase in which its payload is
// valid. Indices erase from generated Go but prevent invalid Go+ transitions.
//goplus:derive off
type ProgressivePackage[p PackagePhase] enum {
	DiscoveredPackage(pkg schema.Package) ProgressivePackage[DiscoveredPhase]
	ScheduledPackage(pkg schema.Package) ProgressivePackage[ScheduledPhase]
	ExtractingPackage(pkg schema.Package) ProgressivePackage[ExtractingPhase]
	ReadyPackage(pkg schema.Package) ProgressivePackage[ReadyPhase]
	FailedPackage(pkg schema.Package, cause error) ProgressivePackage[FailedPhase]
}

func Discover(pkg schema.Package) ProgressivePackage[DiscoveredPhase] { return DiscoveredPackage(pkg) }

func Schedule(pkg ProgressivePackage[DiscoveredPhase]) ProgressivePackage[ScheduledPhase] {
	match pkg {
	case DiscoveredPackage(value):
		return ScheduledPackage(value)
	}
}

func BeginExtraction(pkg ProgressivePackage[ScheduledPhase]) ProgressivePackage[ExtractingPhase] {
	match pkg {
	case ScheduledPackage(value):
		return ExtractingPackage(value)
	}
}

func FinishExtraction(_ ProgressivePackage[ExtractingPhase], full schema.Package) ProgressivePackage[ReadyPhase] {
	return ReadyPackage(full)
}

func FailExtraction(pkg ProgressivePackage[ExtractingPhase], cause error) ProgressivePackage[FailedPhase] {
	match pkg {
	case ExtractingPackage(value):
		return FailedPackage(value, cause)
	}
}

func ScheduledValue(pkg ProgressivePackage[ScheduledPhase]) schema.Package {
	match pkg {
	case ScheduledPackage(value):
		return value
	}
}

func ExtractingValue(pkg ProgressivePackage[ExtractingPhase]) schema.Package {
	match pkg {
	case ExtractingPackage(value):
		return value
	}
}

func ReadyValue(pkg ProgressivePackage[ReadyPhase]) schema.Package {
	match pkg {
	case ReadyPackage(value):
		return value
	}
}
