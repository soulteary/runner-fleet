# Repeated credential warning benchmark

Scope: calls to `WarnAgentTokenUnavailable` after the same installation directory
has already emitted its warning. This is a repeated failure/status-polling path,
not normal successful authentication or end-to-end HTTP throughput.

Both variants ran in the same Linux arm64 container using Go 1.27.3, CPU=1,
300 ms per sample, ten samples, `-benchmem`, without the race detector. External
host load was uncontrolled. Setup emits one warning before the timer starts.
The benchmark calls production code; it does not replace the implementation.

Command:

```sh
go test ./internal/runner -run '^$' -bench BenchmarkWarnAgentTokenUnavailableRepeated -count=10 -cpu=1 -benchtime=300ms -benchmem
```

Before: 200.2 ns/op median, 32 B/op and 2 allocs/op.
After: 80.62 ns/op median, 0 B/op and 0 allocs/op.

A cache lookup avoids constructing a candidate `sync.Once` and boxed key when
the path is already known. On cache misses, `LoadOrStore` still selects a single
winner, and `sync.Once` serializes the actual log write. The warning text and
one-warning-per-directory behavior are unchanged. A 64-goroutine regression
runs repeatedly with the race detector.

Raw samples: [before](credential-warning-before.txt), [after](credential-warning-after.txt).
