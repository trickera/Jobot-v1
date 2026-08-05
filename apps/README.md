# Applications

Each directory under `apps/` is an independently identifiable runtime boundary:

- `desktop`: npm workspace containing the React renderer and Electron shell.
- `backend-go`: standalone Go module and local HTTP service.
- `browser-worker`: standalone Python/Camoufox transport process.

These applications stay in one repository because their build and release are coordinated, but
they communicate through runtime contracts rather than source-level coupling.
