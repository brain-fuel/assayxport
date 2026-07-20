package schema

import "goforge.dev/goplus/std/result"

type SourceLine int
type SourceColumn int
type CallCount int
type Arity int
type ByteSize int64
type ByteBudget int64
type TreeVersion uint64
type Priority int
type WorkerCount int

type UnitFailure enum { OutOfRange(unit string, value int64) }

func PositiveSourceLine(v int) result.Result[SourceLine, UnitFailure] { if v < 1 { return result.Err[SourceLine, UnitFailure]{Err: OutOfRange("source line", int64(v))} }; return result.Ok[SourceLine, UnitFailure]{Value: SourceLine(v)} }
func PositiveSourceColumn(v int) result.Result[SourceColumn, UnitFailure] { if v < 1 { return result.Err[SourceColumn, UnitFailure]{Err: OutOfRange("source column", int64(v))} }; return result.Ok[SourceColumn, UnitFailure]{Value: SourceColumn(v)} }
func PositiveCallCount(v int) result.Result[CallCount, UnitFailure] { if v < 1 { return result.Err[CallCount, UnitFailure]{Err: OutOfRange("call count", int64(v))} }; return result.Ok[CallCount, UnitFailure]{Value: CallCount(v)} }
func NonnegativeArity(v int) result.Result[Arity, UnitFailure] { if v < 0 { return result.Err[Arity, UnitFailure]{Err: OutOfRange("arity", int64(v))} }; return result.Ok[Arity, UnitFailure]{Value: Arity(v)} }
func PositiveWorkerCount(v int) result.Result[WorkerCount, UnitFailure] { if v < 1 { return result.Err[WorkerCount, UnitFailure]{Err: OutOfRange("worker count", int64(v))} }; return result.Ok[WorkerCount, UnitFailure]{Value: WorkerCount(v)} }
