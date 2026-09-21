# Migrating CI shell to ci-recipes

[soulteary/ci-recipes](https://github.com/soulteary/ci-recipes) replaces
repository-specific CI shell programs with one tested, cross-platform Go binary.
This file is the consumer-side counterpart of that project's
[`docs/migration-matrix.md`](https://github.com/soulteary/ci-recipes/blob/main/docs/migration-matrix.md):
it records which of this repository's shell surfaces belong there, which do not,
what the move buys and costs, and what is left to do.

**Status.** The two recipes exist. They were implemented against
`soulteary/ci-recipes@83ccd6f83d7e7ef40f5d6faf2e11960f1de74a78` and live on that
project's `claude/runner-fleet-recipes-vkkn7s` branch at commit
`99245b4`; this repository has not switched to them yet, and
[the last section](#what-is-left) says why and what the switch looks like.

This file has no translated counterparts on purpose.
`scripts/check-docs-structure.sh` mirrors `README.md`, `guide.md` and
`development.md` only; this is a build decision, not product behaviour.

## Inventory

| Shell surface | Invoked by | Verdict |
|---|---|---|
| `scripts/check-version-consistency.sh` | `ci-consistency.yml`, `make check` | **Migrated** → `runner-fleet check-version-consistency` |
| `scripts/check-docs-structure.sh` | `ci-consistency.yml`, `make check` | **Migrated** → `runner-fleet check-docs-structure` |
| `scripts/install-runner.sh` | `Dockerfile` → operator `docker exec`; the registration job in `internal/runner` | Out of scope — runtime entrypoint. Its right home is Go code *in this repository*. |
| `examples/deploy/standalone/run.sh` | Operators, by hand | Out of scope — example |
| `.github/actions/detect-go-module-root/action.yml` | Every Go CI and release job | Out of scope — inline workflow shell |
| 17 multi-line `run: \|` blocks across five workflows | Workflows | Out of scope — inline workflow shell |

The four "out of scope" rows are not a judgement call. ci-recipes' own migration
matrix draws the line itself: "Runtime entrypoints, examples, Bash test files
outside the linked script directories, and inline workflow shell blocks are
outside this migration." Apply that boundary here and exactly the two
`scripts/check-*.sh` files remain — the two things `ci-consistency.yml` runs, and
nothing else.

`grantseal check-doc-consistency` is *not* a recipe this repository could reuse.
Despite the name it is a hard-coded list of grantseal's own drift rules, down to
an error-code count in its docs. There was no existing recipe to generalize, so
the `runner-fleet` source is new code.

## What the migration was worth

Four defects in the shell were reproduced during the audit, then written as
regression tests in the recipes. All four are also fixed *in shell*, so this
repository is correct today either way; they remain the argument for the move,
because each is a defect class that shell makes easy to write and Go makes hard.

Each fix was verified by mutation: reintroducing the defect in the Go code makes
exactly one test fail, and makes it fail the way the shell failed — a green exit
where the check should have been red.

### 1. Fail-open file enumeration — a silent green light

`check-version-consistency.sh` built its file list inside a command substitution
ending in `|| true`. A pipeline reports the exit status of its *last* command, so
`git ls-files` failing was indistinguishable from it returning nothing: the loop
never ran, the hit file stayed empty, and the script printed its all-clear and
exited 0.

Reproduced by running it outside a git work tree. `git` printed
`fatal: not a git repository`, the only stale version reference in the tree was
never read, and the script exited 0 saying every version matched. Realistic
triggers are not exotic: a tarball checkout, an unreadable index, or — the likely
one — `make check` inside a container where the bind-mounted repository is owned
by another UID and git refuses with `detected dubious ownership`.

### 2. Word splitting drops files from the scan — another silent green light

`for f in $FILES` splits on whitespace and then glob-expands each word. A path
containing a space became two nonexistent paths, both skipped by the
`[ -f "$f" ] || continue` guard without a word of output.

Reproduced with a single stale reference in a file named `stale doc.md`: exit 0,
all-clear. In Go the list is a `[]string`, so there is no splitting step to get
wrong; `git ls-files -z` also removes the quoting the shell would have had to
undo for paths with newlines or non-ASCII bytes.

### 3. A documentation check that compared nothing still passed

`check-docs-structure.sh` skipped any document whose English original was absent
(`[ -f "$base" ] || continue`). With no `docs/` tree at all, every iteration was
skipped, the failure flag stayed 0, and the script printed "各语言文档章节结构一致"
and exited 0. Reproduced in an empty directory. A renamed directory, a wrong
path, or a run from the wrong working directory all land here.

### 4. Fence tracking knew only one of Markdown's two fence markers

The same script toggled its in-code-block flag on `/^```/` only, so a `~~~` block
was not a block at all and a column-1 `# comment` inside one was counted as a
heading. Measured: a two-heading document reported three.

This one cuts both ways. Where both sides use `~~~` the bogus heading appears in
both sequences and the check passes while silently mismodelling the document.
Where the English file uses `~~~` and a translation uses ` ``` ` around the same
block, the sequences differ and all five translations are reported as
structurally wrong when they are identical — verified against the pre-fix script,
which failed five files that had no defect. A CI gate that cries wolf gets
switched off.

## What the recipes do differently

Beyond the four fixes, three deliberate changes:

**Repository-owned settings.** Both checks encode facts about *this* repository.
Compiling them into the binary would turn "add a seventh UI language" into a
ci-recipes release plus a pin bump here, so they are read from
`scripts/ci-recipes.conf` instead — `KEY = VALUE` lines, `#` comments, an unknown
key is an error, and a list-valued key replaces the default on its first
appearance and appends on later ones:

```ini
version_source = internal/config/config.go
version_baseline_regex = tag = "(v[0-9]+\.[0-9]+\.[0-9]+)"
version_reference_regex = (v[0-9]+\.[0-9]+\.[0-9]+)
version_reference_regex = main\.Version=([0-9]+\.[0-9]+\.[0-9]+) => v${1}
version_ignore_marker = version-check-ignore
version_globs = *.md *.yml *.yaml *.example Makefile *Dockerfile* *.sh *.go
version_exclude = CHANGELOG.md
docs_root = docs
docs_languages = zh fr de ko ja
docs_files = development.md guide.md README.md
```

The file is optional: the built-in defaults are exactly the values above, which
are this repository's current behaviour, and a ci-recipes test asserts that the
documented file parses back to precisely those defaults. `version_globs` entries
stay Git pathspecs, so matching remains what `git ls-files` does rather than a
reimplementation of it.

**Exit codes follow ci-recipes' convention.** A policy violation exits 1, and
unusable input or infrastructure — an unreadable baseline, a git that will not
run, a configuration that would compare nothing — exits 2. The shell used 1 for
both. Nothing in CI distinguishes them, but the convention is worth having.

**Root resolution.** Neither recipe searches upward for a marker. Like the shell
they replace, they resolve relative paths from the current directory, which is
GitHub Actions' default working directory; an explicit `ROOT` argument overrides
it.

Output is otherwise preserved. Operator-facing messages stay in Chinese and the
`::error file=…,line=…::` annotations are byte-identical, so a contributor reads
the same thing on a pull request. Parity was verified against this repository's
real tree on both paths: the passing run is identical, and a failing run differs
only in the remediation hint saying to rerun the check rather than the script,
plus the one-line English summary `cli.Exit` prints for every ci-recipes recipe.

## What the switch costs

**A Go toolchain in a job that needs none.** `ci-consistency.yml` is today two
jobs of `actions/checkout` plus `sh`: no `setup-go`, no module cache, no network
past the checkout. Calling ci-recipes adds `actions/setup-go` and
`go install github.com/soulteary/ci-recipes/cmd/ci-recipes@<pin>` — a module
download plus a build of the entire CLI, all five repositories' recipes, to run
two checks. Measured here: ~18 s of cold `go build` for an 11 MB binary, before
`setup-go` and the download. Published release binaries would remove nearly all
of it.

**Cross-repository release coupling.** Reduced, not removed. The config file
keeps the per-repository facts here, but the check *logic* now lives elsewhere: a
change to how headings are compared, or to how a version reference is
recognized, is a ci-recipes commit and a pin bump.

**A smaller blast radius than it looks.** These two checks gate *this*
repository's release hygiene. Breaking them breaks a PR gate, not a published
artifact — unlike the release and signing recipes that make up most of
ci-recipes.

## What is left

One thing, and it is not in this repository: **ci-recipes publishes no tags.**
Its changelog is still `[Unreleased]`, so the only thing to pin is a bare commit
SHA with nothing to distinguish a compatible bump from a breaking one — and today
that SHA is on an unmerged branch, which a squash merge would orphan and take
this repository's CI down with it.

So the sequence is: merge the ci-recipes branch, tag a release there and publish
binaries for it, then land the switch below in one commit.

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
+        run: go install github.com/soulteary/ci-recipes/cmd/ci-recipes@<tag>
       - name: Check version references are in sync
-        run: sh scripts/check-version-consistency.sh
+        run: ci-recipes runner-fleet check-version-consistency
```

The `docs` job changes the same way, `make check` needs the `command -v` guard
and install hint `make lint` already has for `golangci-lint`, and
`scripts/ci-recipes.conf` gets committed with the contents shown above. Keeping
the two scripts as thin forwarders is not worth it — two ways to run one check is
the thing the migration is supposed to remove — so delete them, and update the
`## Releasing` section of `docs/development.md` plus its five translations, which
name both scripts by path.

## `install-runner.sh`: the bigger prize, in the wrong repository

The most valuable thing to rewrite in Go here is the one file ci-recipes will not
take. `scripts/install-runner.sh` does architecture detection from `uname -m`,
GitHub release resolution, SHA-256 resolution from two different response shapes,
download, checksum verification and extraction — 141 lines of `curl | sed | awk`
on the trust boundary, invoked from Go code in `internal/runner` and by operators
through `docker exec`. It is also the only one of these scripts whose failure
reaches a user's machine rather than a PR.

It is a runtime entrypoint, so it is out of ci-recipes' scope by that project's
own rule, and it should be: this repository already ships two Go binaries, and
the logic belongs in `internal/runner` where the registration job that calls it
lives. That would drop the `sha256sum` dependency (absent on macOS), replace the
`awk` JSON scraping with `encoding/json`, and make the fallback-version and
checksum-precedence rules testable — none of which needs another repository.
