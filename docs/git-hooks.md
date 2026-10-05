# Git hooks

The optional `git` adapter manages small, project-local hook entries. It does
not install a `git` shim, change `core.hooksPath`, or run hooks itself; Git runs
the hook file as usual. Install the adapter with `ctx adapter add git` after
obtaining the optional catalog, or select it during installation with
`./install.sh --adapters git`.

From a non-bare Git repository:

```sh
ctx run git hooks status
ctx run git hooks install pre-commit -- ./scripts/check.sh
ctx run git hooks status pre-commit
ctx run git hooks remove pre-commit
```

`install` creates an executable hook or inserts a marked CTX block into an
existing shell hook. A failing command fails the hook. `remove` deletes only
the marked block, preserving the rest of an existing script. The adapter will
not edit a symlink, a non-shell script, or a hook outside the repository.

Git supports one effective hook directory per repository. If another tool
sets `core.hooksPath`, `status` shows that directory; CTX does not change it.
Installing when a configured hook path is present requires an explicit
`--directory`, so CTX cannot silently edit a tool's generated wrapper.

For example, Husky keeps editable hook scripts in `.husky` and configures Git
to run wrappers from `.husky/_`. Target the editable script explicitly:

```sh
ctx run git hooks install --directory .husky pre-commit -- ./scripts/check.sh
ctx run git hooks remove --directory .husky pre-commit
```

This preserves Husky's wrapper and `core.hooksPath`. Check your project's
hook manager before editing its files; some managers regenerate their source
scripts too. CTX hook management is opt-in and does not affect `ctx computer
hooks`, which configures hooks for supported AI CLIs.
