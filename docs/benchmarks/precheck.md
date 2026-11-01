# Install-directory precheck benchmark

`inspectInstallDir` needs only to distinguish a missing/empty directory, a directory
with `.runner`, and another nonempty directory. Previously `os.ReadDir` loaded and
sorted every directory entry. The new implementation opens the directory and reads
one entry, retaining the existing `.runner` check and symlink behavior.

Measured on 2026-11-02: Go 1.27.3, Linux arm64, kernel
7.0.9-205.fc44.aarch64, Docker overlay filesystem, 5 available CPUs, benchmark
`-cpu=1`. The same container, filesystem, fixtures and command were used before
and after; no other validation task from this maintenance run was active during
either measurement. External host load is not controlled. Earlier measurements
that overlapped compilation were discarded. Fixtures contain empty regular files;
this is a warm directory precheck measurement, not a cold-storage or end-to-end API
benchmark. Setup and cleanup are outside the timed section.

```sh
TMPDIR=/var/tmp go test ./internal/handler -run '^$' \
  -bench BenchmarkInspectInstallDir -benchmem -benchtime=300ms -count=10 -cpu=1
```

Ten-sample medians, before → after:

- Empty directory: 14,973 → 11,193 ns/op; 840 → 840 B/op; 10 → 10 allocs/op.
- One file: 13,815 → 13,223.5 ns/op; 936 → 936 B/op; 13 → 13 allocs/op.
- 1,000 files: 362,301.5 → 43,037.5 ns/op; 116,045 → 952 B/op;
  2,021 → 13 allocs/op. Observed time reduction: 88.1%.
- 10,000 files: 3,711,058 → 43,669 ns/op; 1,466,977.5 → 952 B/op;
  20,029 → 13 allocs/op. Observed time reduction: 98.8%.

For 10,000 files the inverse median time is about 269 → 22,899 calls/second;
this is single-operation benchmark capacity, not measured HTTP throughput.
Timing samples vary under this virtualized host. The deterministic allocation
reduction and removal of whole-directory enumeration are the strongest evidence;
no service-wide performance claim follows from these numbers.

The baseline uses production `precheck.go` from commit `919225d`; the only changed
production operation is the directory read. [Before samples](precheck-before.txt)
and [after samples](precheck-after.txt) retain all ten runs. The benchmark source
is [precheck_benchmark_test.go](../../internal/handler/precheck_benchmark_test.go).
No API fields, configuration, persisted records or upgrade/rollback requirements
change. Tests retain missing, empty, hidden-file, ordinary-file, registered and
symlink behavior, in addition to existing conflict checks.
