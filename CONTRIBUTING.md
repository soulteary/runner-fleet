# Contributing

Thanks for looking. This file is the short version; [`docs/development.md`](docs/development.md)
has the detail — build, HTTP API, how running state is determined, and why the tests are shaped
the way they are.

## Before you push

Run one command:

```bash
make check
```

It is everything CI runs, in the order CI runs it: `gofmt`, `go vet`, `golangci-lint`,
`go test -race ./...`, and the two `ci-recipes` consistency checks. The point of having a single
target is that nobody has to remember the list — the item you forget is always the one that turns
the PR red. The consistency checks need a one-time install:

```bash
make install-ci-recipes
```

`-race` is not optional here. The registration queue, the per-runner `runnerOps` lock and the
single-winner creation in `EnsureAgentToken` rest on conventions the type system does not enforce,
so a data race has to fail the build rather than surface later as an occasional wrong status.

## What the checks will tell you

Several of them exist because the exact mistake already happened once. Knowing what they guard
saves a round trip:

| If this goes red | It means |
|---|---|
| `Docs structure consistency` | English gained a section the five translations did not |
| `TestTranslations_…` | English gained a table row, code block, list item or link that a translation did not — the heading check cannot see those |
| `TestEnvVars_…` / `TestConfigFields_…` | You added an environment variable or a `yaml` field and did not document it in all six guides |
| `TestPathRefs_…` | A `.yml`, `.example`, `Dockerfile` or `.go` comment points at a repo path that does not exist |
| `TestQuickStart_…` | The quick-start commands in `README.md` and `docs/guide.md` drifted apart |
| `Quick start runs` | The guide's quick-start block does not actually work — CI runs it against a freshly built image |
| `Version consistency` | A version reference disagrees with the baseline in `internal/config/config.go` |

## Documentation changes

The English files under `docs/` are the originals; `docs/<lang>/` are translations of them.
Change English and the five translations in the same commit — the checks above will not let the
two drift, and a half-translated section is worse than an untranslated one because nothing looks
wrong.

Two files under `docs/` are deliberately untranslated, because they record build decisions rather
than product behaviour: [`ci-recipes-migration.md`](docs/ci-recipes-migration.md) and
[`docs-improvement-plan.md`](docs/docs-improvement-plan.md).

A line that legitimately cites an older version — release notes, upgrade instructions — carries a
`version-check-ignore` marker so the version check skips it.

## CHANGELOG entries

New entries go under `## [Unreleased]`, in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) sections (`Added`, `Changed`, `Fixed`,
`Security`). Aim for **one sentence on what changed and what it means for someone upgrading**,
plus a link if there is more to say.

Some historical entries run to a hundred words and read as design notes. They are staying as they
are — rewriting them is risk without benefit — but new ones do not need to match them. The reader
of a changelog is deciding whether an upgrade affects them; the reasoning behind a change belongs
in the code comment or the commit message, where this repository already keeps it.

Releases are cut from tags. `v*.*.*` triggers a check that the baseline in
`internal/config/config.go` equals the tag and that `CHANGELOG.md` has the matching `## [X.Y.Z]`
section and link definition — see [Releasing](docs/development.md#releasing).

## Commits and pull requests

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
(`fix(examples): …`, `docs: …`, `chore(ci): …`). Say what changed and why; the diff already says
how.

There is a [pull request template](.github/pull_request_template.md). It asks what the change does,
how you know it works, and what you considered and rejected — that last one is usually the part a
reviewer most needs and least often gets.

## Reporting a vulnerability

Not here — see [SECURITY.md](SECURITY.md).
