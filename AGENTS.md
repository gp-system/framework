# Commit messages

Commits follow **Conventional Commits**, enforced by the
`@commitlint/config-conventional` rules (see `commitlint.config.mjs`). The
`commit-msg` git hook and the CI `commitlint` job
(`wagoid/commitlint-github-action`) check every commit: a non-compliant
message is rejected.

## Format

```
<type>(<scope optional>)<!>: <subject>

<body optional>

<footer optional>
```

- **`type`** is required, lowercase, one of:
  `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`,
  `style`, `revert`.
- **`scope`** is optional, in parentheses, e.g. `feat(dbx): ...`.
- **`!`** after the scope (or after `type` if there's no scope) marks a
  breaking change, e.g. `feat!: ...` or `feat(auth)!: ...`.
- **`subject`** is required, non-empty, must not end with a period, and must
  not start with a capitalized sentence case (use "add", not "Add").
- The **header** (first line) is max 100 characters.
- If there's a **body**/**footer**, a blank line separates it from the header;
  max 100 characters per line.
- A **breaking change** can also be signaled in the footer:
  `BREAKING CHANGE: <description>`.

## Examples

Good:

```
feat: add S3 presigned upload support
fix(rbac): correct permission check for nested roles
docs: update configuration reference for SMTP_*
refactor(dbx)!: rename Transactor.WithinTransaction to WithinTx
```

Bad:

```
Add new feature.        # capitalized start, ends with a period, no type
Fixed bug                # no type, imperative mood required instead of past tense
WIP                     # no type/subject structure
feat: this is a really really really long subject line that goes way past the header limit of one hundred characters
```

## Local checking

One-time setup to enable the hook:

```sh
mise run hooks:install   # npm ci + git config core.hooksPath .githooks
```

After that, every commit runs through `.githooks/commit-msg` (`commitlint
--edit`), so a non-compliant message fails locally before you push. To check
the last commit after the fact:

```sh
mise run commitlint      # commitlint --last --verbose
```

The `Co-Authored-By: ...` trailer on agent-authored commits is a footer and
does not conflict with the rules above.

## Dependabot exemption

`commitlint.config.mjs` ignores commits carrying a `Signed-off-by: dependabot[bot]`
trailer, so dependabot's machine-generated commit bodies (release notes, compare
links) are exempt from `body-max-line-length`. All other rules, and the full
policy above, still apply to human-authored commits.
