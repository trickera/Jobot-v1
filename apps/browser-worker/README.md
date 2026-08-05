# Browser Worker

This Python/Camoufox process is a transport-only worker. It opens URLs and returns HTML over a
newline-delimited JSON protocol; parsing and business rules remain in the Go backend.

Source path: `apps/browser-worker/worker.py`.

The Electron package intentionally installs this directory as
`resources/backend/backend-browser/` to preserve the existing packaged-runtime contract.
