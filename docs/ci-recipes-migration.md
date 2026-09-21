# Migrating CI shell to ci-recipes

[soulteary/ci-recipes](https://github.com/soulteary/ci-recipes) replaces
repository-specific CI shell programs with one tested, cross-platform Go binary.
This file is the consumer-side counterpart of that project's
[`docs/migration-matrix.md`](https://github.com/soulteary/ci-recipes/blob/main/docs/migration-matrix.md):
it records which of this repository's shell surfaces belong there, which do not,
and what the move buys and costs.

Audited against `soulteary/ci-recipes@83ccd6f83d7e7ef40f5d6faf2e11960f1de74a78`.
That project publishes no tags yet and its changelog is still `[Unreleased]`, so
a consumer pins a full commit — which is what its own README recommends.

This file has no translated counterparts on purpose.
`scripts/check-docs-structure.sh` mirrors `README.md`, `guide.md` and
`development.md` only; this is a build decision, not product behaviour.

## Inventory

| Shell surface | Invoked by | Verdict |
|---|---|---|
| `scripts/check-version-consistency.sh` | `ci-consistency.yml`, `make check` | **Candidate** — `runner-fleet check-version-consistency` |
| `scripts/check-docs-structure.sh` | `ci-consistency.yml`, `make check` | **Candidate** — `runner-fleet check-docs-structure` |
| `scripts/install-runner.sh` | `Dockerfile` → operator `docker exec`; the registration job in `internal/runner` | Out of scope — runtime entrypoint. Its right home is Go code *in this repository*. |
| `examples/deploy/standalone/run.sh` | Operators, by hand | Out of scope — example |
| `.github/actions/detect-go-module-root/action.yml` | Every Go CI and release job | Out of scope — inline workflow shell |
| 17 multi-line `run: \|` blocks across five workflows | Workflows | Out of scope — inline workflow shell |

The four "out of scope" rows are not a judgement call. ci-recipes' own matrix
draws the line itself: "Runtime entrypoints, examples, Bash test files outside
the linked script directories, and inline workflow shell blocks are outside this
migration." Apply that boundary here and exactly the two `scripts/check-*.sh`
files remain — the two things `ci-consistency.yml` runs, and nothing else.

`grantseal check-doc-consistency` is *not* a recipe this repository could reuse.
Despite the name it is a hard-coded list of grantseal's own drift rules, down to
an error-code count in its docs. There is no existing recipe to generalize; a
`runner-fleet` source would be new code.

## What the two candidates are worth migrating for

Three defects in the shell versions were reproduced during this audit. All three
are now fixed *in shell*, so migration is an improvement rather than a rescue.
They are still the argument for it, because each is a defect class that shell
makes easy to write and Go makes hard.

### 1. Fail-open file enumeration — a silent green light

`check-version-consistency.sh` built its file list inside a command
substitution ending in `|| true`. A pipeline reports the exit status of its
*last* command, so `git ls-files` failing was indistinguishable from it
returning nothing: the loop never ran, the hit file stayed empty, and the script
printed its all-clear and exited 0.

Reproduced by running it outside a git work tree. `git` printed
`fatal: not a git repository`, the only stale version reference in the tree was
never read, and the script exited 0 saying every version matched. Realistic
triggers are not exotic: a tarball checkout, an unreadable index, or — the
likely one — `make check` inside a container where the bind-mounted repository
is owned by another UID and git refuses with `detected dubious ownership`.

A Go recipe does not get to make this mistake by omission: `exec.Command(...).Run()`
returns an error that has to be assigned, and ci-recipes' stated contract is
that "Git, coverage, archive, document, and JSON parse failures are fail-closed".

### 2. Word splitting drops files from the scan — another silent green light

`for f in $FILES` splits on whitespace and then glob-expands each word. A path
containing a space became two nonexistent paths, both skipped by the
`[ -f "$f" ] || continue` guard without a word of output.

Reproduced with a single stale reference in a file named `stale doc.md`: exit 0,
all-clear. In Go the list is a `[]string`; there is no splitting step to get
wrong.

### 3. Fence tracking knew only one of Markdown's two fence markers

`check-docs-structure.sh` toggled its in-code-block flag on `/^```/` only, so a
`~~~` block was not a block at all and a column-1 `# comment` inside one was
counted as a heading. Measured: a two-heading document reported three.

This one cuts both ways. Where both sides use `~~~` the bogus heading appears in
both sequences and the check passes while silently mismodelling the document.
Where the English file uses `~~~` and a translation uses ` ``` ` around the same
block, the sequences differ and all five translations are reported as
structurally wrong when they are identical — verified against the pre-fix
script, which failed five files that had no defect. A CI gate that cries wolf
gets switched off.

The fix also made a closing fence require the character that opened it, so a
`~~~` line inside a ` ``` ` block no longer ends the block early.

## What migration costs

**A Go toolchain in a job that needs none.** `ci-consistency.yml` is today two
jobs of `actions/checkout` plus `sh`: no `setup-go`, no module cache, no network
past the checkout. Calling ci-recipes adds `actions/setup-go` and
`go install github.com/soulteary/ci-recipes/cmd/ci-recipes@<commit>` — a module
download plus a build of the entire CLI, all four repositories' recipes, to run
two checks. Measured here: ~18 s of cold `go build` for an 11 MB binary, before
`setup-go` and the download. Published release binaries would remove nearly all
of it; ci-recipes publishes none.

**Cross-repository release coupling.** Both checks encode facts about *this*
repository: the baseline path `internal/config/config.go` and its `tag = "…"`
shape, the `version-check-ignore` marker, the scanned extension list,
`LANGS="zh fr de ko ja"`, `DOCS="development.md guide.md README.md"`. Adding a
seventh UI language is one line here today; afterwards it is a ci-recipes
commit, a pin bump, and two repositories' CI to get back to green. This is a
real cost, but it is the same trade Stargate and Grantseal already accepted.

**No tag to pin.** With no releases, the pin is a bare commit SHA and nothing
distinguishes a compatible bump from a breaking one.

**A smaller blast radius than it looks.** These two checks gate *this*
repository's release hygiene. Breaking them breaks a PR gate, not a published
artifact — unlike the release and signing recipes that make up most of
ci-recipes today.

## Proposed recipe surface

Two commands, keeping the old script basenames so the workflow replacement stays
obvious, and searching upward for the repository root the way Stargate's
`check-*` commands do:

```console
ci-recipes runner-fleet check-version-consistency [ROOT]
ci-recipes runner-fleet check-docs-structure [ROOT]
```

Both should fail closed (exit 1 on policy violation, exit 2 on unusable input or
missing infrastructure) and keep emitting `::error file=…,line=…::` so
annotations still land on the diff.

The parameterization is worth getting right, because it is what decides whether
a routine change here needs a release there. Read the tunables from the
repository instead of compiling them in — a `scripts/version-check.conf`-style
file holding the baseline path, the regex, the ignore marker and the scanned
extensions, and the language and document lists for the structure check. Absent
config means the defaults above. A Go recipe also lifts the gaps the extension
allowlist leaves: the current globs skip `*.json`, `*.html` and `*.txt`, so a
version string in the six i18n bundles or in `templates/index.html` would not be
seen, and the one in `scripts/apt-packages.txt` is a historical reference that
goes unchecked either way.

Both checks want table-driven tests over temporary repositories — the three
defects above are each a two-line fixture, and none of them had any coverage.

## What lands in this repository once the recipes exist

```diff
   version:
     name: Version consistency
     steps:
       - name: Checkout code
         uses: actions/checkout@v6
+      - name: Set up Go
+        uses: actions/setup-go@v6
+        with:
+          go-version: ${{ env.GO_VERSION }}
+      - name: Install ci-recipes
+        run: go install github.com/soulteary/ci-recipes/cmd/ci-recipes@<pinned-commit>
       - name: Check version references are in sync
-        run: sh scripts/check-version-consistency.sh
+        run: ci-recipes runner-fleet check-version-consistency
```

`make check` changes the same way and would need the binary on `PATH`, so it
needs the same `command -v` guard and install hint `make lint` already has for
`golangci-lint`. Keeping the two scripts as thin forwarders is not worth it: two
ways to run one check is the thing the migration is supposed to remove. Delete
them, and update the `## Releasing` section of `docs/development.md` plus its
five translations, which name both scripts by path.

## Recommendation

Migrate both, but not as the next thing.

The case is sound — the two scripts are exactly what ci-recipes was built for,
they are the only shell in this repository that clears its own boundary, and the
three reproduced defects are all defect classes the Go port structurally
prevents. But the trade only pays once ci-recipes publishes tagged release
binaries. Until then the move adds a Go toolchain, a full CLI build and an
unversioned commit pin to a job whose entire current cost is a checkout, in
exchange for correctness properties the in-shell fixes have already secured.
The sequence that makes sense is: tag and publish ci-recipes, add the
`runner-fleet` source with the config-file parameterization above, then flip
`ci-consistency.yml` and `make check` in one commit and delete both scripts.

## `install-runner.sh`: the bigger prize, in the wrong repository

The most valuable thing to rewrite in Go here is the one file ci-recipes will
not take. `scripts/install-runner.sh` does architecture detection from
`uname -m`, GitHub release resolution, SHA-256 resolution from two different
response shapes, download, checksum verification and extraction — 141 lines of
`curl | sed | awk` on the trust boundary, invoked from Go code in
`internal/runner` and by operators through `docker exec`. It is also the only
one of these scripts whose failure reaches a user's machine rather than a PR.

It is a runtime entrypoint, so it is out of ci-recipes' scope by that project's
own rule, and it should be: this repository already ships two Go binaries, and
the logic belongs in `internal/runner` where the registration job that calls it
lives. That would drop the `sha256sum` dependency (absent on macOS), replace the
`awk` JSON scraping with `encoding/json`, and make the fallback-version and
checksum-precedence rules testable — none of which needs another repository.
