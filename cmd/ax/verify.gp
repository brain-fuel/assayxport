package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"goforge.dev/assayxport/internal/schema"
	"goforge.dev/goplus/std/result"
)

type TraceCommandResult enum { TraceCommandCompleted() }

type TraceCommandFailure enum {
	TraceFlagFailure(Cause error)
	TraceReadFailure(Path string, Cause error)
	TraceDecodeFailure(Path string, Cause error)
	TraceRejected(Causes []schema.TraceFailure)
	TraceWriteFailure(Cause error)
	TraceUsageFailure(Detail string)
}

func (failure TraceCommandFailure) Error() string {
	match failure {
	case TraceFlagFailure(cause): return cause.Error()
	case TraceReadFailure(path, cause): return fmt.Sprintf("read %s: %v", path, cause)
	case TraceDecodeFailure(path, cause): return fmt.Sprintf("decode %s: %v", path, cause)
	case TraceRejected(causes):
		lines := make([]string, len(causes))
		for index, cause := range causes { lines[index] = cause.Error() }
		return strings.Join(lines, "\n")
	case TraceWriteFailure(cause): return fmt.Sprintf("write report: %v", cause)
	case TraceUsageFailure(detail): return detail
	}
}

func runVerifyCmd(arguments []string) error {
	match verifyTraceCommand(arguments) {
	case result.Ok(TraceCommandCompleted): return nil
	case result.Err(failure): return fmt.Errorf("%s", failure.Error())
	}
}

func verifyTraceCommand(arguments []string) result.Result[TraceCommandResult, TraceCommandFailure] {
	flags := flag.NewFlagSet("ax verify", flag.ContinueOnError)
	baselinePath := flags.String("baseline", "", "previous release trace bundle")
	releaseID := flags.String("release", "", "release-note artifact id")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ax verify [trace.json] [-baseline previous.json -release release.id]")
		flags.PrintDefaults()
	}
	path, rest := splitPath(arguments)
	if path == "." { path = "assayxport.trace.json" }
	match result.Of(true, flags.Parse(rest)) {
	case result.Err(cause):
		return result.Err[TraceCommandResult, TraceCommandFailure](TraceFlagFailure(cause))
	case result.Ok(_):
	}
	if flags.NArg() > 0 { path = flags.Arg(0) }
	match readTraceBundle(path) {
	case result.Err(failure):
		return result.Err[TraceCommandResult, TraceCommandFailure](failure)
	case result.Ok(current):
		if *baselinePath == "" {
			match schema.ValidateTrace(current) {
			case result.Err(causes):
				return result.Err[TraceCommandResult, TraceCommandFailure](TraceRejected(causes))
			case result.Ok(_):
				return writeTraceReport(schema.TraceReport{Changed: []string{}, Missing: []string{}})
			}
		}
		if *releaseID == "" {
			return result.Err[TraceCommandResult, TraceCommandFailure](
				TraceUsageFailure("-release is required with -baseline"),
			)
		}
		match readTraceBundle(*baselinePath) {
		case result.Err(failure):
			return result.Err[TraceCommandResult, TraceCommandFailure](failure)
		case result.Ok(baseline):
			match schema.VerifyRelease(baseline, current, *releaseID) {
			case result.Err(causes):
				return result.Err[TraceCommandResult, TraceCommandFailure](TraceRejected(causes))
			case result.Ok(report):
				return writeTraceReport(report)
			}
		}
	}
}

func readTraceBundle(path string) result.Result[schema.TraceBundle, TraceCommandFailure] {
	content, readError := os.ReadFile(path)
	match result.Of(content, readError) {
	case result.Err(cause):
		return result.Err[schema.TraceBundle, TraceCommandFailure](TraceReadFailure(path, cause))
	case result.Ok(raw):
		var bundle schema.TraceBundle
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		match result.Of(true, decoder.Decode(&bundle)) {
		case result.Err(cause):
			return result.Err[schema.TraceBundle, TraceCommandFailure](TraceDecodeFailure(path, cause))
		case result.Ok(_):
			return result.Ok[schema.TraceBundle, TraceCommandFailure](bundle)
		}
	}
}

func writeTraceReport(report schema.TraceReport) result.Result[TraceCommandResult, TraceCommandFailure] {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	match result.Of(true, encoder.Encode(report)) {
	case result.Err(cause):
		return result.Err[TraceCommandResult, TraceCommandFailure](TraceWriteFailure(cause))
	case result.Ok(_):
		return result.Ok[TraceCommandResult, TraceCommandFailure](TraceCommandCompleted())
	}
}
