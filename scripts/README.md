# Scripts

Repository automation is grouped by responsibility:

- `dev/`: local development helpers and Electron bundling/watch scripts.
- `release/`: Python/Camoufox resource preparation and release manifests.
- `qa/`: backend, worker, desktop, installer, and end-to-end smoke tests.

Run the stable npm commands from the repository root. Direct PowerShell invocations may also be
run from any working directory because scripts resolve the repository root from `$PSScriptRoot`.
