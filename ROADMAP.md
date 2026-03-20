# Roadmap: Unify wekatester + tatool into a single tool

## Context
Today there are two separate CLI tools customers need:
- **wekatester** (Python, compiled binary) — distributed fio benchmarking across Weka clusters
- **tatool** (Go binary) — runs diagnostic TA scripts across cluster nodes via SSH/K8s

The team wants a single tool: `wekatester tatool` runs the diagnostic scripts.

## Options for Unification

### Option A: Rewrite wekatester in Go (recommended long-term)
Port wekatester's fio distribution logic to Go. Merge with tatool into one binary.

**Pros:**
- Single static binary, zero dependencies
- One language, one build system
- Go's concurrency model fits distributed SSH execution well
- Already proven with tatool

**Cons:**
- Significant effort to reimplement wekatester's Python logic
- Team needs Go familiarity
- Risk of introducing bugs in proven benchmarking code

**Rough scope:** ~2-3 weeks for an experienced Go dev

### Option B: Go CLI wrapper that bundles both
New Go CLI binary that embeds tatool natively and bundles the wekatester Python binary.

```
weka-tools tatool [flags]    → runs tatool (native Go)
weka-tools bench [flags]     → runs embedded wekatester (Python binary)
```

**Pros:**
- Quick to ship — no rewrite needed
- tatool stays Go, wekatester stays Python
- Single download for customers

**Cons:**
- Larger binary (~45MB combined)
- Two runtimes embedded
- Awkward to maintain long-term

**Rough scope:** ~2-3 days

### Option C: Add tatool to Python wekatester
Port tatool's Go logic to Python. Add as subcommand in wekatester codebase.

**Pros:**
- Team stays in Python
- One codebase, one language

**Cons:**
- Loses Go's benefits (static binary, cross-compilation, performance)
- Python runtime/packaging complexity returns
- Regression from what we just built

**Rough scope:** ~1 week

## Recommendation
**Short-term:** Option B — ship a Go wrapper that bundles both binaries. Customers get one install, two tools.

**Long-term:** Option A — incrementally port wekatester to Go as the team gains Go experience.

## Next Steps
1. Get team alignment on which option
2. If Option B: create `weka-tools` CLI in Go that embeds tatool + wekatester binary
3. Update install script and release pipeline
4. Deprecate standalone tatool-bin download
