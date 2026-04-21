# Workflow improvements

- Factor out shared WSL setup and teardown into reusable composite actions.
- Replace repeated per-job boilerplate in `.github/workflows/generic.yaml` with a matrix or scenario-specific reusable actions.
- Move duplicated inline PowerShell helpers, especially WSL cleanup logic, into shared helpers.
- Return the created test user from `setup-distro` instead of hardcoding `user` in downstream jobs.
- Fold the repeated "install snapd from channel or specific revision" branching into a reusable action/helper.
- Add top-level workflow controls such as explicit `permissions`, `concurrency`, and possibly `strategy.fail-fast: false`.
- Simplify the smoke matrix by removing unused keys like `lts` and `codename`.
- Move heavy diagnostics behind `if: failure()` once the current WSL investigation is complete.
- Rewrite `.github/actions/install-wsl` as a small Python CLI with explicit subcommands for version detection, MSI install/skip, and diagnostic listing.
- Rewrite `.github/actions/setup-distro` as a Python CLI to handle import retries, `wsl.conf` editing, systemd probing, and diagnostic collection without PowerShell process-state quirks.
- Keep composite actions as thin wrappers that pass inputs and invoke Python entry points, instead of embedding multi-step shell logic inline.
- Add a small shared Python module for common WSL subprocess handling, expected-error matching, and structured diagnostic output.
- Migrate repeated workflow-side teardown logic into a reusable action or Python helper so `generic.yaml` stops duplicating PowerShell cleanup functions.
- Prefer Python on GitHub-hosted Windows runners over PowerShell for future CI control-plane logic, since it is preinstalled and has clearer subprocess and text-handling semantics.
